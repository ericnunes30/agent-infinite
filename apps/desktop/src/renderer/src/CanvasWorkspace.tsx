import {
  addEdge,
  applyEdgeChanges,
  applyNodeChanges,
  Background,
  MarkerType,
  ReactFlow,
  type Connection,
  type Edge,
  type EdgeChange,
  type Node,
  type NodeChange,
  type Viewport,
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import {
  Bot,
  ChevronDown,
  FolderOpen,
  GitBranch,
  LayoutTemplate,
  Play,
  Plus,
  Save,
  ShieldCheck,
  Users,
  Wrench,
  X,
} from 'lucide-react';
import { useEffect, useMemo, useRef, useState } from 'react';
import type { BackendConnection, ColorTheme } from '../../shared/ipc';
import { AgentNode, type AgentNodeData } from './AgentNode';
import { LocalApi } from './api';
import type {
  CapabilityItem,
  CanvasEdge,
  CanvasNode,
  Dispatch,
  ModelInventory,
  Provider,
  Snapshot,
  TeamTemplate,
} from './domain';
import {
  edgesBetweenVisibleNodes,
  groupCapabilityItems,
  isCapabilityAvailable,
  isCapabilityCompatible,
  visibleNodeIds,
  wouldCreateCycle,
} from './domain';
import { PixiGrid } from './PixiGrid';
import { CapabilityGovernance } from './CapabilityGovernance';
import {
  AgentCapabilityEditor,
  CapabilityPicker,
  type AgentEditorUpdate,
} from './AgentCapabilityEditor';
import { RoleProfileEditor } from './RoleProfileEditor';
import { ModelSelector } from './ModelSelector';
import { TerminalPanel } from './TerminalPanel';
import {
  BULK_START_CONCURRENCY,
  canvasLayoutSignature,
  TERMINAL_PREVIEW_BATCH_MS,
} from './canvasPerformance';

interface CanvasWorkspaceProps {
  readonly connection: BackendConnection;
  readonly initial: Snapshot;
  readonly theme: ColorTheme;
  readonly onError: (message: string) => void;
  readonly onOpenWorkspace: () => void;
  readonly surface: 'canvas' | 'teams' | 'templates';
  readonly onSurfaceChange: (surface: 'canvas' | 'teams' | 'templates') => void;
}

const nodeTypes = { agent: AgentNode };

const DEFAULT_AGENT_ROLES = [
  'DevOps',
  'Frontend',
  'Backend',
  'QA / Tester',
  'Code Reviewer',
] as const;

interface RuntimeSession {
  readonly sessionId: string;
  readonly status: string;
  readonly integrationMode: string;
  readonly hookSessionId?: string;
  readonly mcpConnected: boolean;
}

interface NativeSubagentActivity {
  readonly id: string;
  readonly nodeId: string;
  readonly provider: string;
  readonly status: 'running' | 'stopped';
  readonly at: string;
}

type RailSection =
  'workspace' | 'teams' | 'worktrees' | 'templates' | 'newAgent' | 'roles' | 'tools';

interface RailCollapseToggleProps {
  readonly label: string;
  readonly expanded: boolean;
  readonly onToggle: () => void;
}

function RailCollapseToggle({
  label,
  expanded,
  onToggle,
}: RailCollapseToggleProps): React.JSX.Element {
  return (
    <button
      type="button"
      className="rail-collapse-toggle"
      aria-label={`${expanded ? 'Recolher' : 'Expandir'} ${label}`}
      aria-expanded={expanded}
      onClick={onToggle}
    >
      <ChevronDown size={13} aria-hidden="true" />
    </button>
  );
}

function toFlowNodes(snapshot: Snapshot): Node<AgentNodeData>[] {
  return snapshot.nodes.map((node) => {
    const team = snapshot.teams.find((candidate) => candidate.id === node.teamId);
    const worktree = snapshot.worktrees.find((candidate) => candidate.id === node.worktreeId);
    return {
      id: node.id,
      type: 'agent',
      position: node.position,
      width: node.size.width,
      height: node.size.height,
      data: {
        label: node.label,
        role: node.role,
        kind: node.kind,
        provider: node.provider,
        ...(node.model ? { model: node.model } : {}),
        teamName: team?.name ?? worktree?.name ?? 'Standalone',
        teamColor: team?.color ?? '#78817c',
        status: 'Idle',
        preview: '',
      },
    };
  });
}

function toFlowEdges(edges: readonly CanvasEdge[]): Edge[] {
  return edges.map((edge) => ({
    ...edge,
    type: 'smoothstep',
    markerEnd: { type: MarkerType.ArrowClosed, color: 'var(--lime)' },
    style: { stroke: 'var(--edge)', strokeWidth: 1.4 },
  }));
}

function withLayout(
  snapshot: Snapshot,
  nodes: readonly Node<AgentNodeData>[],
  edges: readonly Edge[],
  viewport: Viewport,
): Snapshot {
  const domainNodes: CanvasNode[] = snapshot.nodes.map((node) => {
    const flow = nodes.find((candidate) => candidate.id === node.id);
    return {
      ...node,
      position: flow?.position ?? node.position,
      size: {
        width: flow?.measured?.width ?? flow?.width ?? node.size.width,
        height: flow?.measured?.height ?? flow?.height ?? node.size.height,
      },
    };
  });
  const domainEdges: CanvasEdge[] = edges.map((edge) => ({
    id: edge.id,
    source: edge.source,
    target: edge.target,
    type: 'delegates_to',
  }));
  return { ...snapshot, nodes: domainNodes, edges: domainEdges, viewport };
}

export function CanvasWorkspace({
  connection,
  initial,
  theme,
  onError,
  onOpenWorkspace,
  surface,
  onSurfaceChange,
}: CanvasWorkspaceProps): React.JSX.Element {
  const api = useMemo(() => new LocalApi(connection), [connection]);
  const [snapshot, setSnapshot] = useState(initial);
  const [nodes, setNodes] = useState<Node<AgentNodeData>[]>(() => toFlowNodes(initial));
  const [edges, setEdges] = useState<Edge[]>(() => toFlowEdges(initial.edges));
  const [viewport, setViewport] = useState<Viewport>(initial.viewport);
  const [dialog, setDialog] = useState<
    | 'team'
    | 'worktree'
    | 'agent'
    | 'role'
    | 'importAgent'
    | 'importTemplate'
    | 'template'
    | 'templateAgent'
    | 'saveTemplate'
    | null
  >(null);
  const [runTeamId, setRunTeamId] = useState<string | null>(null);
  const [worktreeTeamId, setWorktreeTeamId] = useState<string | null>(null);
  const [templates, setTemplates] = useState<TeamTemplate[]>([]);
  const [selectedTemplateId, setSelectedTemplateId] = useState<string | null>(null);
  const [gitBranches, setGitBranches] = useState<{ all: string[]; available: string[] }>({
    all: [],
    available: [],
  });
  const [selectedWorktree, setSelectedWorktree] = useState<string | null>(
    () => initial.worktrees[0]?.id ?? null,
  );
  const [managedTeamId, setManagedTeamId] = useState<string | null>(
    () => initial.teams[0]?.id ?? null,
  );
  const [agentRolePreset, setAgentRolePreset] = useState('');
  const [railSections, setRailSections] = useState<Record<RailSection, boolean>>({
    workspace: true,
    teams: true,
    worktrees: true,
    templates: true,
    newAgent: true,
    roles: false,
    tools: false,
  });
  const [selectedNode, setSelectedNode] = useState<string | null>(null);
  const [fullscreenTerminalNodeId, setFullscreenTerminalNodeId] = useState<string | null>(null);
  const [saveState, setSaveState] = useState<'saved' | 'saving' | 'error'>('saved');
  const [sessions, setSessions] = useState<Record<string, RuntimeSession>>({});
  const [bulkStartProgress, setBulkStartProgress] = useState<{
    readonly completed: number;
    readonly total: number;
  } | null>(null);
  const [dispatches, setDispatches] = useState<Dispatch[]>([]);
  const [nativeSubagents, setNativeSubagents] = useState<NativeSubagentActivity[]>([]);
  const [capabilities, setCapabilities] = useState<CapabilityItem[]>([]);
  const [modelInventory, setModelInventory] = useState<ModelInventory>({ providers: [] });
  const [governanceOpen, setGovernanceOpen] = useState(false);
  const [editingAgentId, setEditingAgentId] = useState<string | null>(null);
  const [editingTemplateAgentId, setEditingTemplateAgentId] = useState<string | null>(null);
  const [editingRoleId, setEditingRoleId] = useState<string | null>(null);
  const hydrated = useRef(false);
  const selectedWorkspace = useRef(initial.workspaceId);
  const pendingPreviews = useRef(new Map<string, string>());
  const previewTimer = useRef<number | undefined>(undefined);
  const latestCanvasState = useRef({ snapshot, nodes, edges, viewport });
  latestCanvasState.current = { snapshot, nodes, edges, viewport };
  // Runtime output/status are intentionally excluded so terminal traffic never schedules disk saves.
  const layoutSignature = canvasLayoutSignature(nodes, edges, viewport);

  useEffect(() => {
    if (selectedWorkspace.current === snapshot.workspaceId) return;
    selectedWorkspace.current = snapshot.workspaceId;
    setSelectedWorktree(snapshot.worktrees[0]?.id ?? null);
    setManagedTeamId(snapshot.teams[0]?.id ?? null);
    setSelectedNode(null);
  }, [snapshot.workspaceId, snapshot.teams, snapshot.worktrees]);

  const activeWorktree = useMemo(
    () => snapshot.worktrees.find((worktree) => worktree.id === selectedWorktree) ?? null,
    [selectedWorktree, snapshot.worktrees],
  );
  const activeTeam = useMemo(
    () => snapshot.teams.find((team) => team.id === activeWorktree?.teamId) ?? null,
    [activeWorktree?.teamId, snapshot.teams],
  );
  const activeWorktreeTeamId = activeWorktree?.teamId;
  const managedTeam = useMemo(
    () => snapshot.teams.find((team) => team.id === managedTeamId) ?? null,
    [managedTeamId, snapshot.teams],
  );
  const selectedTemplate = useMemo(
    () => templates.find((template) => template.id === selectedTemplateId) ?? templates[0] ?? null,
    [selectedTemplateId, templates],
  );
  const availableAgentRoles = useMemo(
    () => [...new Set([...DEFAULT_AGENT_ROLES, ...snapshot.customRoles])],
    [snapshot.customRoles],
  );
  const agentContextTeam = surface === 'teams' ? managedTeam : activeTeam;
  const canCreateAgent =
    surface === 'teams' ? managedTeam !== null : surface === 'canvas' && activeWorktree !== null;
  const canCreateTemplateAgent = surface === 'templates' && selectedTemplate !== null;

  const visibleIds = useMemo(
    () => visibleNodeIds(snapshot.nodes, activeWorktree?.teamId ?? null, selectedWorktree),
    [activeWorktree?.teamId, selectedWorktree, snapshot.nodes],
  );
  const offlineVisibleNodeIds = useMemo(
    () => [...visibleIds].filter((nodeId) => sessions[nodeId] === undefined),
    [sessions, visibleIds],
  );
  const displayedNodes = useMemo(
    () =>
      nodes.map((node) => {
        const runtime = sessions[node.id];
        const terminalActive = selectedNode === node.id && Boolean(runtime);
        const terminalFullscreen = fullscreenTerminalNodeId === node.id && terminalActive;
        const terminalWidth = Math.max(node.width ?? node.measured?.width ?? 0, 620);
        const terminalHeight = Math.max(node.height ?? node.measured?.height ?? 0, 380);
        return {
          ...node,
          hidden: !visibleIds.has(node.id),
          ...(terminalActive
            ? {
                width: terminalWidth,
                height: terminalHeight,
                measured: { width: terminalWidth, height: terminalHeight },
                style: { ...node.style, width: terminalWidth, height: terminalHeight },
              }
            : {}),
          ...(terminalActive ? { zIndex: 20 } : {}),
          data: {
            ...node.data,
            ...(runtime ? { sessionId: runtime.sessionId } : {}),
            terminalActive,
            terminalFullscreen,
            connection,
            onStart: (nodeId: string) => {
              setSelectedNode(nodeId);
              void startNode(nodeId);
            },
            onStop: (nodeId: string) => void stopNode(nodeId),
            onEdit: (nodeId: string) => openAgentEditor(nodeId),
            onCollapseTerminal: () => {
              setFullscreenTerminalNodeId(null);
              setSelectedNode(null);
            },
            onExpandTerminal: () => setFullscreenTerminalNodeId(node.id),
          },
        };
      }),
    [connection, fullscreenTerminalNodeId, nodes, selectedNode, sessions, visibleIds],
  );
  const displayedEdges = useMemo(() => {
    if (selectedWorktree === null) return edges;
    const visibleDomainEdges = edgesBetweenVisibleNodes(
      edges.map((edge) => ({
        id: edge.id,
        source: edge.source,
        target: edge.target,
        type: 'delegates_to' as const,
      })),
      visibleIds,
    );
    const visibleEdgeIds = new Set(visibleDomainEdges.map((edge) => edge.id));
    return edges.filter((edge) => visibleEdgeIds.has(edge.id));
  }, [edges, selectedWorktree, visibleIds]);

  useEffect(() => {
    void Promise.all([
      api.runtime(),
      api.dispatches(),
      api.capabilityInventory(),
      api.modelInventory(),
    ])
      .then(([runtime, activity, inventory, models]) => {
        setCapabilities(inventory.items);
        setModelInventory(models);
        setSessions(
          Object.fromEntries(
            runtime.nodes.map((node) => [
              node.nodeId,
              {
                sessionId: node.sessionId,
                status: node.status,
                integrationMode: node.integrationMode,
                mcpConnected: node.mcpConnected,
                ...(node.hookSessionId ? { hookSessionId: node.hookSessionId } : {}),
              },
            ]),
          ),
        );
        setDispatches(activity.dispatches);
        setNodes((current) =>
          current.map((node) => {
            const running = runtime.nodes.find((item) => item.nodeId === node.id);
            return running
              ? {
                  ...node,
                  data: {
                    ...node.data,
                    status: running.status,
                    preview: running.preview ?? node.data.preview,
                  },
                }
              : node;
          }),
        );
      })
      .catch((reason: unknown) =>
        onError(reason instanceof Error ? reason.message : 'Runtime recovery failed.'),
      );
  }, [api, onError]);

  useEffect(() => {
    let disposed = false;
    let socket: WebSocket | null = null;
    let reconnect: number | undefined;
    const connect = (): void => {
      if (disposed) return;
      const endpoint = `${connection.baseUrl.replace(/^http/, 'ws')}/ws/events?token=${encodeURIComponent(connection.token)}`;
      socket = new WebSocket(endpoint);
      socket.addEventListener('message', (message: MessageEvent<unknown>) => {
        if (typeof message.data !== 'string') return;
        let event: {
          type: string;
          entityId: string;
          at?: string;
          payload?: Record<string, unknown>;
        };
        try {
          event = JSON.parse(message.data) as typeof event;
        } catch {
          return;
        }
        if (event.type === 'terminal.started' && typeof event.payload?.sessionId === 'string') {
          setSessions((current) => ({
            ...current,
            [event.entityId]: {
              sessionId: event.payload?.sessionId as string,
              status: 'Starting',
              integrationMode: 'hooks-pending',
              mcpConnected: false,
            },
          }));
        }
        if (event.type === 'integration.mcp_connected') {
          setSessions((current) => {
            const existing = current[event.entityId];
            return existing
              ? { ...current, [event.entityId]: { ...existing, mcpConnected: true } }
              : current;
          });
        }
        if (event.type === 'terminal.exited') {
          setSessions((current) =>
            Object.fromEntries(Object.entries(current).filter(([id]) => id !== event.entityId)),
          );
        }
        if (event.type === 'agent.output_preview' && typeof event.payload?.text === 'string') {
          pendingPreviews.current.set(event.entityId, event.payload.text);
          previewTimer.current ??= window.setTimeout(() => {
            previewTimer.current = undefined;
            const updates = new Map(pendingPreviews.current);
            pendingPreviews.current.clear();
            setNodes((current) =>
              current.map((node) => {
                const preview = updates.get(node.id);
                return preview === undefined ? node : { ...node, data: { ...node.data, preview } };
              }),
            );
          }, TERMINAL_PREVIEW_BATCH_MS);
        }
        if (event.type === 'agent.status_changed' && typeof event.payload?.status === 'string') {
          const status = event.payload.status;
          setNodes((current) =>
            current.map((node) =>
              node.id === event.entityId ? { ...node, data: { ...node.data, status } } : node,
            ),
          );
          setSessions((current) => {
            const existing = current[event.entityId];
            return existing ? { ...current, [event.entityId]: { ...existing, status } } : current;
          });
        }
        if (event.type.startsWith('dispatch.') && event.payload?.dispatch_id) {
          const dispatch = event.payload as unknown as Dispatch;
          setDispatches((current) => [
            dispatch,
            ...current.filter((item) => item.dispatch_id !== dispatch.dispatch_id),
          ]);
          const source = dispatch.source_node_id;
          const target = dispatch.target_node_id;
          const active = !['done', 'blocked', 'failed', 'canceled'].includes(dispatch.status);
          setEdges((current) =>
            current.map((edge) =>
              edge.source === source && edge.target === target
                ? {
                    ...edge,
                    animated: active,
                    label: dispatch.status.toUpperCase(),
                    labelStyle: { fill: 'var(--text-soft)', fontSize: 8 },
                    labelBgStyle: { fill: 'var(--panel)', fillOpacity: 0.92 },
                  }
                : edge,
            ),
          );
        }
        if (
          (event.type === 'integration.hook_event' || event.type === 'integration.degraded') &&
          typeof event.payload?.mode === 'string'
        ) {
          const mode = event.payload.mode;
          setSessions((current) => {
            const existing = current[event.entityId];
            return existing
              ? { ...current, [event.entityId]: { ...existing, integrationMode: mode } }
              : current;
          });
        }
        if (event.type === 'integration.required_failed') {
          setSessions((current) => {
            const existing = current[event.entityId];
            return existing
              ? { ...current, [event.entityId]: { ...existing, integrationMode: 'error' } }
              : current;
          });
        }
        if (
          event.type === 'integration.native_subagent_started' ||
          event.type === 'integration.native_subagent_stopped'
        ) {
          const details =
            typeof event.payload?.details === 'object' && event.payload.details
              ? (event.payload.details as Record<string, unknown>)
              : {};
          const idValue = details.agent_id ?? details.agentId ?? event.payload?.hookSessionId;
          const activityId =
            typeof idValue === 'string' || typeof idValue === 'number'
              ? idValue.toString()
              : crypto.randomUUID();
          const providerValue = event.payload?.provider;
          const activity: NativeSubagentActivity = {
            id: activityId,
            nodeId: event.entityId,
            provider: typeof providerValue === 'string' ? providerValue : 'unknown',
            status: event.type === 'integration.native_subagent_started' ? 'running' : 'stopped',
            at: event.at ?? new Date().toISOString(),
          };
          setNativeSubagents((current) => [
            activity,
            ...current.filter((item) => item.id !== activity.id),
          ]);
        }
      });
      socket.addEventListener('close', () => {
        if (!disposed) reconnect = window.setTimeout(connect, 1000);
      });
    };
    connect();
    return () => {
      disposed = true;
      if (reconnect) window.clearTimeout(reconnect);
      if (previewTimer.current) window.clearTimeout(previewTimer.current);
      previewTimer.current = undefined;
      pendingPreviews.current.clear();
      socket?.close();
    };
  }, [connection]);

  useEffect(() => {
    void api
      .teamTemplates()
      .then((result) => setTemplates(result.templates))
      .catch(() => undefined);
  }, [api]);

  useEffect(() => {
    if (dialog !== 'worktree') return;
    void api
      .gitBranches()
      .then(setGitBranches)
      .catch((reason: unknown) =>
        onError(reason instanceof Error ? reason.message : 'Could not load Git branches.'),
      );
  }, [api, dialog, onError]);

  async function refresh(): Promise<Snapshot> {
    const next = await api.snapshot();
    setSnapshot(next);
    setNodes(toFlowNodes(next));
    setEdges(toFlowEdges(next.edges));
    return next;
  }

  useEffect(() => {
    if (!hydrated.current) {
      hydrated.current = true;
      return undefined;
    }
    const timer = window.setTimeout(() => {
      const current = latestCanvasState.current;
      const next = withLayout(current.snapshot, current.nodes, current.edges, current.viewport);
      void api
        .saveLayout(next)
        .then(() => setSaveState('saved'))
        .catch((reason: unknown) => {
          setSaveState('error');
          onError(reason instanceof Error ? reason.message : 'Layout save failed.');
        });
    }, 500);
    return () => window.clearTimeout(timer);
  }, [api, layoutSignature, onError]);

  async function saveWorkspace(): Promise<void> {
    setSaveState('saving');
    try {
      await api.saveLayout(withLayout(snapshot, nodes, edges, viewport));
      setSaveState('saved');
    } catch (reason) {
      setSaveState('error');
      onError(reason instanceof Error ? reason.message : 'Workspace save failed.');
    }
  }

  const nodeChanges = (changes: NodeChange<Node<AgentNodeData>>[]): void =>
    setNodes((current) =>
      applyNodeChanges(
        changes.filter(
          (change) =>
            !(
              change.type === 'dimensions' &&
              change.id === selectedNode &&
              Boolean(sessions[change.id])
            ),
        ),
        current,
      ),
    );
  const edgeChanges = (changes: EdgeChange<Edge>[]): void =>
    setEdges((current) => applyEdgeChanges(changes, current));

  function connectNodes(connectionRequest: Connection): void {
    const { source, target } = connectionRequest;
    if (!source || !target) return;
    const sourceNode = snapshot.nodes.find((node) => node.id === source);
    const domainEdges: CanvasEdge[] = edges.map((edge) => ({
      id: edge.id,
      source: edge.source,
      target: edge.target,
      type: 'delegates_to',
    }));
    if (
      sourceNode?.kind !== 'orchestrator' ||
      domainEdges.some((edge) => edge.source === source && edge.target === target) ||
      wouldCreateCycle(domainEdges, source, target)
    ) {
      onError('Only orchestrators can create non-cyclic delegation paths.');
      return;
    }
    setEdges((current) =>
      addEdge(
        {
          ...connectionRequest,
          id: crypto.randomUUID(),
          type: 'smoothstep',
          markerEnd: { type: MarkerType.ArrowClosed, color: 'var(--lime)' },
          style: { stroke: 'var(--edge)', strokeWidth: 1.4 },
        },
        current,
      ),
    );
  }

  async function startNode(nodeId: string): Promise<void> {
    try {
      if (fullscreenTerminalNodeId !== nodeId) setFullscreenTerminalNodeId(null);
      if (sessions[nodeId]) await api.stopNode(nodeId);
      const runtime = await api.startNode(nodeId);
      registerRuntime(nodeId, runtime);
    } catch (reason) {
      onError(reason instanceof Error ? reason.message : 'Agent start failed.');
    }
  }

  function registerRuntime(nodeId: string, runtime: RuntimeSession): void {
    setSessions((current) => ({ ...current, [nodeId]: runtime }));
    setNodes((current) =>
      current.map((node) =>
        node.id === nodeId ? { ...node, data: { ...node.data, status: runtime.status } } : node,
      ),
    );
  }

  async function startAllOfflineNodes(): Promise<void> {
    if (!activeWorktree || bulkStartProgress || offlineVisibleNodeIds.length === 0) return;
    const nodeIds = [...offlineVisibleNodeIds];
    const failures: string[] = [];
    // Bulk activation should never mount an interactive Xterm. Every newly started
    // session stays on the lightweight preview until the user selects one node.
    setSelectedNode(null);
    setFullscreenTerminalNodeId(null);
    setBulkStartProgress({ completed: 0, total: nodeIds.length });
    try {
      // Starting every provider at once causes a sharp CPU and disk spike. Two at a time
      // keeps startup responsive while all running nodes still use preview-only rendering.
      for (let offset = 0; offset < nodeIds.length; offset += BULK_START_CONCURRENCY) {
        const batch = nodeIds.slice(offset, offset + BULK_START_CONCURRENCY);
        await Promise.all(
          batch.map(async (nodeId) => {
            try {
              const runtime = await api.startNode(nodeId);
              registerRuntime(nodeId, runtime);
            } catch (reason) {
              const label = snapshot.nodes.find((node) => node.id === nodeId)?.label ?? nodeId;
              const message = reason instanceof Error ? reason.message : 'falha desconhecida';
              failures.push(`${label}: ${message}`);
            } finally {
              setBulkStartProgress((current) =>
                current ? { ...current, completed: current.completed + 1 } : current,
              );
            }
          }),
        );
      }
    } finally {
      setBulkStartProgress(null);
    }
    if (failures.length > 0) {
      onError(
        `${String(failures.length)} agente(s) não puderam ser iniciados. ${failures.join(' | ')}`,
      );
    }
  }

  async function stopNode(nodeId: string): Promise<void> {
    try {
      await api.stopNode(nodeId);
      if (fullscreenTerminalNodeId === nodeId) setFullscreenTerminalNodeId(null);
      setSessions((current) =>
        Object.fromEntries(Object.entries(current).filter(([id]) => id !== nodeId)),
      );
      setNodes((current) =>
        current.map((node) =>
          node.id === nodeId ? { ...node, data: { ...node.data, status: 'Dead' } } : node,
        ),
      );
    } catch (reason) {
      onError(reason instanceof Error ? reason.message : 'Agent stop failed.');
    }
  }

  function selectWorktree(worktreeId: string): void {
    setSelectedWorktree(worktreeId);
    onSurfaceChange('canvas');
    if (
      selectedNode &&
      snapshot.nodes.find((node) => node.id === selectedNode)?.worktreeId !== worktreeId
    ) {
      setSelectedNode(null);
    }
  }

  function selectTeam(teamId: string): void {
    setManagedTeamId(teamId);
    setSelectedNode(null);
    onSurfaceChange('teams');
  }

  async function refreshTemplates(): Promise<void> {
    const result = await api.teamTemplates();
    setTemplates(result.templates);
  }

  function openAgent(role = ''): void {
    if (!canCreateAgent) {
      onError('Select a Team canvas or a Git worktree before creating an agent.');
      return;
    }
    setAgentRolePreset(role);
    setDialog('agent');
  }

  function openTemplateAgent(role = ''): void {
    if (!canCreateTemplateAgent) {
      onError('Select a Team Template before adding an agent.');
      return;
    }
    setAgentRolePreset(role);
    setDialog('templateAgent');
  }

  function openNewAgent(role = ''): void {
    if (surface === 'templates') {
      openTemplateAgent(role);
      return;
    }
    if (canCreateAgent) {
      openAgent(role);
      return;
    }
    onError('Selecione um Team, Git worktree ou Team Template antes de adicionar um agente.');
  }

  function openAgentEditor(nodeId: string): void {
    void api
      .capabilityInventory()
      .then((inventory) => {
        setCapabilities(inventory.items);
        setEditingAgentId(nodeId);
      })
      .catch((reason: unknown) =>
        onError(
          reason instanceof Error ? reason.message : 'Não foi possível carregar MCPs e skills.',
        ),
      );
  }

  function openTemplateAgentEditor(nodeId: string): void {
    void api
      .capabilityInventory()
      .then((inventory) => {
        setCapabilities(inventory.items);
        setEditingTemplateAgentId(nodeId);
      })
      .catch((reason: unknown) =>
        onError(
          reason instanceof Error ? reason.message : 'Não foi possível carregar MCPs e skills.',
        ),
      );
  }

  function deleteAgent(node: Snapshot['nodes'][number]): void {
    if (node.kind === 'orchestrator') return;
    if (
      !window.confirm(
        `Excluir o agente “${node.label}”? As delegações ligadas a ele também serão removidas.`,
      )
    )
      return;
    void (async () => {
      const runtime = sessions[node.id];
      if (runtime) await api.stopNode(node.id);
      await api.deleteNode(node.id);
      setSelectedNode(null);
      setEditingAgentId(null);
      await refresh();
    })().catch((reason: unknown) =>
      onError(reason instanceof Error ? reason.message : 'Não foi possível excluir o agente.'),
    );
  }

  const fullscreenTerminal =
    fullscreenTerminalNodeId !== null && selectedNode === fullscreenTerminalNodeId
      ? (() => {
          const node = snapshot.nodes.find((item) => item.id === fullscreenTerminalNodeId);
          const runtime = sessions[fullscreenTerminalNodeId];
          return node && runtime ? { node, runtime } : null;
        })()
      : null;

  useEffect(() => {
    if (!fullscreenTerminal) return undefined;
    const closeOnEscape = (event: KeyboardEvent): void => {
      if (event.key === 'Escape') setFullscreenTerminalNodeId(null);
    };
    window.addEventListener('keydown', closeOnEscape);
    return () => window.removeEventListener('keydown', closeOnEscape);
  }, [fullscreenTerminal]);

  function toggleRailSection(section: RailSection): void {
    setRailSections((current) => ({ ...current, [section]: !current[section] }));
  }

  return (
    <div className="workspace-layout">
      <aside className="team-rail">
        <section className="rail-section workspace-context" aria-labelledby="workspace-title">
          <div className="rail-section-heading">
            <span id="workspace-title">WORKSPACE</span>
            <div className="rail-heading-actions">
              <span className="workspace-status">ATTACHED</span>
              <RailCollapseToggle
                label="Workspace"
                expanded={railSections.workspace}
                onToggle={() => toggleRailSection('workspace')}
              />
            </div>
          </div>
          {railSections.workspace && (
            <>
              <div className="workspace-context-card">
                <strong>
                  {snapshot.workspacePath.split(/[\\/]/).at(-1) ?? snapshot.workspacePath}
                </strong>
                <span title={snapshot.workspacePath}>{snapshot.workspacePath}</span>
              </div>
              <div className="workspace-actions">
                <button
                  type="button"
                  onClick={() => void saveWorkspace()}
                  disabled={saveState === 'saving'}
                >
                  <Save size={13} />
                  {saveState === 'saving'
                    ? 'Salvando'
                    : saveState === 'error'
                      ? 'Tentar salvar'
                      : 'Salvar'}
                </button>
                <button type="button" onClick={onOpenWorkspace}>
                  <FolderOpen size={13} /> Abrir
                </button>
              </div>
              <span className={`save-indicator save-${saveState}`}>
                {saveState === 'saving'
                  ? 'SALVANDO ALTERAÇÕES'
                  : saveState === 'error'
                    ? 'FALHA AO SALVAR'
                    : 'ALTERAÇÕES SALVAS'}
              </span>
            </>
          )}
        </section>

        <section className="rail-section" aria-labelledby="teams-title">
          <div className="rail-section-heading rail-heading">
            <span id="teams-title">AGENT TEAMS</span>
            <div className="rail-heading-actions">
              <RailCollapseToggle
                label="Agent Teams"
                expanded={railSections.teams}
                onToggle={() => toggleRailSection('teams')}
              />
              <button type="button" aria-label="Create Team" onClick={() => setDialog('team')}>
                <Plus size={14} />
              </button>
            </div>
          </div>
          {railSections.teams && (
            <div className="worktree-list">
              {snapshot.teams.map((team) => (
                <div key={team.id} className="rail-item-with-delete">
                  <button
                    type="button"
                    className={surface === 'teams' && managedTeamId === team.id ? 'active' : ''}
                    aria-pressed={surface === 'teams' && managedTeamId === team.id}
                    onClick={() => selectTeam(team.id)}
                  >
                    <i style={{ background: team.color }} />
                    <span className="worktree-name">
                      <strong>{team.name}</strong>
                      <span className="rail-item-meta">
                        <em>DEFINIÇÃO</em>
                        <code>
                          {
                            snapshot.nodes.filter(
                              (node) => node.teamId === team.id && !node.worktreeId,
                            ).length
                          }{' '}
                          AGENTES
                        </code>
                      </span>
                    </span>
                    <small className="worktree-count">
                      {
                        snapshot.nodes.filter((node) => node.teamId === team.id && !node.worktreeId)
                          .length
                      }
                    </small>
                  </button>
                  <button
                    type="button"
                    className="rail-item-delete"
                    aria-label={`Excluir Team ${team.name}`}
                    onClick={() => {
                      if (
                        !window.confirm(
                          `Excluir o workflow do Team “${team.name}”? Os Git worktrees e seus agentes serão preservados como canvases independentes.`,
                        )
                      )
                        return;
                      void api
                        .deleteTeam(team.id)
                        .then(async () => {
                          const next = await refresh();
                          setManagedTeamId(next.teams[0]?.id ?? null);
                          if (managedTeamId === team.id && activeWorktree)
                            onSurfaceChange('canvas');
                        })
                        .catch((reason: unknown) =>
                          onError(
                            reason instanceof Error ? reason.message : 'Could not delete Team.',
                          ),
                        );
                    }}
                  >
                    <X size={12} />
                  </button>
                </div>
              ))}
            </div>
          )}
        </section>

        <section
          className="rail-section worktree-section git-worktree-section"
          aria-labelledby="worktrees-title"
        >
          <div className="rail-section-heading rail-heading">
            <span id="worktrees-title">GIT WORKTREES</span>
            <div className="rail-heading-actions">
              <RailCollapseToggle
                label="Git Worktrees"
                expanded={railSections.worktrees}
                onToggle={() => toggleRailSection('worktrees')}
              />
              <button
                type="button"
                aria-label="Create Git worktree"
                onClick={() => {
                  setWorktreeTeamId(null);
                  setDialog('worktree');
                }}
              >
                <Plus size={14} />
              </button>
            </div>
          </div>
          {railSections.worktrees && (
            <>
              <div className="worktree-summary">
                <GitBranch size={13} aria-hidden="true" />
                <span>{snapshot.worktrees.length} worktrees</span>
                <small>CANVAS SELECTION</small>
              </div>
              <div className="worktree-list">
                {snapshot.worktrees.map((worktree) => {
                  const team = snapshot.teams.find((item) => item.id === worktree.teamId);
                  return (
                    <div key={worktree.id} className="rail-item-with-delete">
                      <button
                        type="button"
                        className={
                          surface === 'canvas' && selectedWorktree === worktree.id ? 'active' : ''
                        }
                        aria-pressed={surface === 'canvas' && selectedWorktree === worktree.id}
                        onClick={() => selectWorktree(worktree.id)}
                      >
                        <i style={{ background: team?.color ?? '#78817c' }} />
                        <span className="worktree-name">
                          <strong>{worktree.name}</strong>
                          <small>
                            <span className="rail-item-meta">
                              <em>{team ? `TEAM ${team.name}` : 'INDEPENDENTE'}</em>
                              <code title={worktree.branch}>
                                {worktree.branch.length > 0 ? worktree.branch : 'branch pending'}
                              </code>
                            </span>
                          </small>
                        </span>
                        <small className="worktree-count">
                          {snapshot.nodes.filter((node) => node.worktreeId === worktree.id).length}
                        </small>
                      </button>
                      <button
                        type="button"
                        className="rail-item-delete"
                        aria-label={`Excluir Git Worktree ${worktree.name}`}
                        onClick={() => {
                          if (!window.confirm(`Excluir o Git Worktree “${worktree.name}”?`)) return;
                          void api
                            .deleteWorktree(worktree.id)
                            .then(async () => {
                              const next = await refresh();
                              if (selectedWorktree === worktree.id)
                                setSelectedWorktree(next.worktrees[0]?.id ?? null);
                            })
                            .catch((reason: unknown) =>
                              onError(
                                reason instanceof Error
                                  ? reason.message
                                  : 'Could not delete Git Worktree.',
                              ),
                            );
                        }}
                      >
                        <X size={12} />
                      </button>
                    </div>
                  );
                })}
                {snapshot.worktrees.length === 0 && (
                  <p className="worktree-empty">
                    Create a Git worktree and link it to an Agent Team.
                  </p>
                )}
              </div>
            </>
          )}
        </section>

        <section className="rail-section" aria-labelledby="templates-title">
          <div className="rail-section-heading rail-heading">
            <span id="templates-title">TEAM TEMPLATES</span>
            <div className="rail-heading-actions">
              <RailCollapseToggle
                label="Team Templates"
                expanded={railSections.templates}
                onToggle={() => toggleRailSection('templates')}
              />
              <button
                type="button"
                aria-label="Create Team Template"
                onClick={() => setDialog('template')}
              >
                <Plus size={14} />
              </button>
            </div>
          </div>
          {railSections.templates && (
            <div className="worktree-list">
              {templates.map((template) => (
                <div key={template.id} className="template-item">
                  <button
                    type="button"
                    className={
                      surface === 'templates' && selectedTemplateId === template.id ? 'active' : ''
                    }
                    onClick={() => {
                      setSelectedTemplateId(template.id);
                      onSurfaceChange('templates');
                    }}
                  >
                    <i style={{ background: template.color }} />
                    <span className="worktree-name">
                      <strong>{template.name}</strong>
                      <span className="rail-item-meta">
                        <em>BIBLIOTECA</em>
                        <code>{template.nodes.length} NODES</code>
                      </span>
                    </span>
                  </button>
                  <button
                    type="button"
                    aria-label={`Delete template ${template.name}`}
                    onClick={() =>
                      void api
                        .deleteTeamTemplate(template.id)
                        .then(refreshTemplates)
                        .catch((reason: unknown) =>
                          onError(
                            reason instanceof Error
                              ? reason.message
                              : 'Could not delete Team template.',
                          ),
                        )
                    }
                  >
                    <X size={12} />
                  </button>
                </div>
              ))}
              {templates.length === 0 && (
                <p className="worktree-empty">Save a Team workflow as a template.</p>
              )}
            </div>
          )}
        </section>

        <section className="rail-section rail-quick-agent" aria-labelledby="new-agent-title">
          <div className="rail-section-heading rail-heading">
            <span id="new-agent-title">NOVO AGENTE</span>
            <div className="rail-heading-actions">
              <RailCollapseToggle
                label="Novo Agente"
                expanded={railSections.newAgent}
                onToggle={() => toggleRailSection('newAgent')}
              />
              <button
                type="button"
                aria-label="Create agent"
                disabled={!canCreateAgent && !canCreateTemplateAgent}
                onClick={() => openNewAgent()}
              >
                <Plus size={14} />
              </button>
            </div>
          </div>
          {railSections.newAgent && (
            <button
              type="button"
              className="quick-agent-button"
              disabled={!canCreateAgent && !canCreateTemplateAgent}
              onClick={() => openNewAgent()}
            >
              <Bot size={14} />
              <span>
                <strong>
                  {surface === 'teams'
                    ? `Adicionar ao Team ${managedTeam?.name ?? ''}`
                    : surface === 'templates'
                      ? `Adicionar ao template ${selectedTemplate?.name ?? ''}`
                      : activeWorktree?.teamId
                        ? 'Criar no worktree ativo'
                        : activeWorktree
                          ? 'Criar no worktree independente'
                          : 'Selecione um Team, worktree ou template'}
                </strong>
                <small>
                  {surface === 'teams'
                    ? 'WORKFLOW DE DEFINIÇÃO / SEM WORKTREE'
                    : surface === 'templates'
                      ? 'PRESET REUTILIZÁVEL'
                      : activeWorktree
                        ? `BRANCH: ${activeWorktree.branch}`
                        : 'ESCOLHA O CONTEXTO DE DESTINO'}
                </small>
              </span>
            </button>
          )}
        </section>

        <section className="rail-section rail-roles" aria-labelledby="roles-title">
          <div className="rail-section-heading rail-heading">
            <span id="roles-title">ROLES</span>
            <div className="rail-heading-actions">
              <RailCollapseToggle
                label="Roles"
                expanded={railSections.roles}
                onToggle={() => toggleRailSection('roles')}
              />
              <button
                type="button"
                aria-label="Adicionar role personalizada"
                onClick={() => setEditingRoleId('new')}
              >
                <Plus size={14} />
              </button>
            </div>
          </div>
          {railSections.roles && (
            <div className="role-list">
              {availableAgentRoles.map((role) => (
                <button
                  type="button"
                  key={role}
                  disabled={!canCreateAgent && !canCreateTemplateAgent}
                  onClick={() => openNewAgent(role)}
                >
                  <Bot size={12} /> {role}
                </button>
              ))}
              {snapshot.roleProfiles.map((role) => (
                <div key={`profile-${role.id}`} className="role-profile-actions">
                  {!availableAgentRoles.some(
                    (name) => name.trim().toLowerCase() === role.name.trim().toLowerCase(),
                  ) && (
                    <button
                      type="button"
                      disabled={!canCreateAgent && !canCreateTemplateAgent}
                      onClick={() => openNewAgent(role.name)}
                    >
                      <Bot size={12} /> {role.name}
                    </button>
                  )}
                  <button type="button" onClick={() => setEditingRoleId(role.id)}>
                    <ShieldCheck size={12} /> Editar {role.name}
                  </button>
                </div>
              ))}
              {snapshot.customRoles.map((role) => (
                <button
                  type="button"
                  key={`delete-${role}`}
                  className="custom-role-delete"
                  aria-label={`Excluir role ${role}`}
                  onClick={() =>
                    void api
                      .deleteCustomRole(role)
                      .then(refresh)
                      .catch((reason: unknown) =>
                        onError(
                          reason instanceof Error
                            ? reason.message
                            : 'Could not delete custom role.',
                        ),
                      )
                  }
                >
                  <X size={11} /> Excluir {role}
                </button>
              ))}
            </div>
          )}
        </section>

        <section className="rail-section rail-tools" aria-labelledby="tools-title">
          <div className="rail-section-heading">
            <span id="tools-title">FERRAMENTAS</span>
            <RailCollapseToggle
              label="Ferramentas"
              expanded={railSections.tools}
              onToggle={() => toggleRailSection('tools')}
            />
          </div>
          {railSections.tools && (
            <>
              <button type="button" onClick={() => void saveWorkspace()}>
                <Save size={12} /> Salvar canvas
              </button>
              <button type="button" onClick={onOpenWorkspace}>
                <FolderOpen size={12} /> Abrir workspace
              </button>
              {activeWorktreeTeamId ? (
                <button
                  type="button"
                  onClick={() =>
                    void api
                      .extractTeamWorkflow(activeWorktreeTeamId, activeWorktree.id)
                      .then(async () => {
                        await refresh();
                        selectTeam(activeWorktreeTeamId);
                      })
                      .catch((reason: unknown) =>
                        onError(
                          reason instanceof Error
                            ? reason.message
                            : 'Could not extract the Team workflow.',
                        ),
                      )
                  }
                >
                  <Wrench size={12} /> Extrair workflow para Team
                </button>
              ) : null}
              <button type="button" onClick={() => onSurfaceChange('teams')}>
                <Wrench size={12} /> Gerenciar Agent Teams
              </button>
              <button type="button" onClick={() => setGovernanceOpen(true)}>
                <ShieldCheck size={12} /> MCPs &amp; Skills
              </button>
            </>
          )}
        </section>
      </aside>

      <section className="canvas-stage">
        {surface === 'teams' ? (
          <TeamDefinitionCanvas
            snapshot={snapshot}
            teamId={managedTeamId}
            nodes={nodes}
            edges={edges}
            onNodesChange={nodeChanges}
            onEdgesChange={edgeChanges}
            onConnect={connectNodes}
            onCreateTeam={() => setDialog('team')}
            onCreateAgent={() => openAgent()}
            onSaveTemplate={(teamId) =>
              void api
                .saveTeamTemplate({ teamId })
                .then(refreshTemplates)
                .catch((reason: unknown) =>
                  onError(
                    reason instanceof Error ? reason.message : 'Could not save Team template.',
                  ),
                )
            }
            onRunTeam={(teamId) => setRunTeamId(teamId)}
            onSelectAgent={(nodeId) => setSelectedNode(nodeId)}
            onEditAgent={openAgentEditor}
          />
        ) : surface === 'templates' ? (
          <TemplateLibraryPage
            template={selectedTemplate}
            onCreateTemplate={() => setDialog('template')}
            onCreateAgent={() => openTemplateAgent()}
            onUpdate={async (draft) => {
              const updated = await api.updateTeamTemplate(draft);
              setTemplates((current) =>
                current.map((template) => (template.id === updated.id ? updated : template)),
              );
              return updated;
            }}
            onApply={(templateId) => {
              void api
                .applyTeamTemplate(templateId)
                .then(async ({ team }) => {
                  await refresh();
                  setManagedTeamId(team.id);
                  onSurfaceChange('teams');
                })
                .catch((reason: unknown) =>
                  onError(
                    reason instanceof Error ? reason.message : 'Could not apply Team template.',
                  ),
                );
            }}
            onDelete={(templateId) => {
              void api
                .deleteTeamTemplate(templateId)
                .then(async () => {
                  await refreshTemplates();
                  setSelectedTemplateId(null);
                })
                .catch((reason: unknown) =>
                  onError(
                    reason instanceof Error ? reason.message : 'Could not delete Team template.',
                  ),
                );
            }}
            onEditAgent={openTemplateAgentEditor}
          />
        ) : (
          <>
            <PixiGrid viewport={viewport} theme={theme} />
            {activeWorktree && visibleIds.size === 0 ? (
              <div className="empty-worktree-canvas">
                <div className="empty-worktree-icon">
                  <LayoutTemplate size={21} />
                </div>
                <span>GIT WORKTREE / {activeWorktree.branch}</span>
                <h2>Este worktree ainda está vazio</h2>
                <p>
                  Importe uma composição completa de Team Template ou adicione um agente individual
                  a este canvas.
                </p>
                <div>
                  <button
                    type="button"
                    className="primary"
                    onClick={() => setDialog('importTemplate')}
                  >
                    <LayoutTemplate size={14} /> IMPORTAR TEMPLATE
                  </button>
                  <button
                    type="button"
                    onClick={() => {
                      openNewAgent();
                    }}
                  >
                    <Bot size={14} /> ADICIONAR AGENTE
                  </button>
                </div>
              </div>
            ) : null}
            <ReactFlow
              nodes={displayedNodes}
              edges={displayedEdges}
              nodeTypes={nodeTypes}
              onNodesChange={nodeChanges}
              onEdgesChange={edgeChanges}
              onConnect={connectNodes}
              onNodeClick={(_event, node) => setSelectedNode(node.id)}
              onNodeDoubleClick={(_event, node) => openAgentEditor(node.id)}
              onPaneClick={() => setSelectedNode(null)}
              onMoveEnd={(_event, nextViewport) => setViewport(nextViewport)}
              defaultViewport={initial.viewport}
              minZoom={0.25}
              maxZoom={1.8}
              onlyRenderVisibleElements
              fitView={initial.nodes.length === 0}
              proOptions={{ hideAttribution: true }}
            />
            {fullscreenTerminal ? (
              <section
                className="terminal-fullscreen-overlay"
                aria-label={`Terminal expandido de ${fullscreenTerminal.node.label}`}
              >
                <header>
                  <div>
                    <span>TERMINAL / TELA CHEIA</span>
                    <strong>{fullscreenTerminal.node.label}</strong>
                  </div>
                  <button
                    type="button"
                    aria-label="Sair da tela cheia do terminal"
                    title="Sair da tela cheia (Esc)"
                    onClick={() => setFullscreenTerminalNodeId(null)}
                  >
                    <X size={16} />
                  </button>
                </header>
                <div className="terminal-fullscreen-body">
                  <TerminalPanel
                    connection={connection}
                    sessionId={fullscreenTerminal.runtime.sessionId}
                    label={`Terminal de ${fullscreenTerminal.node.label}`}
                    accent={
                      snapshot.teams.find((team) => team.id === fullscreenTerminal.node.teamId)
                        ?.color ?? '#b7f34a'
                    }
                  />
                </div>
              </section>
            ) : null}
            <div className="canvas-toolbar">
              <span>{visibleIds.size} NODES</span>
              <button
                type="button"
                className="canvas-start-all"
                disabled={
                  !activeWorktree ||
                  offlineVisibleNodeIds.length === 0 ||
                  bulkStartProgress !== null
                }
                aria-label="Iniciar todos os agentes offline deste worktree"
                title="Ativa os terminais offline em lotes e os mantém em preview de baixo consumo"
                onClick={() => void startAllOfflineNodes()}
              >
                <Play size={13} />
                {bulkStartProgress
                  ? `INICIANDO ${String(bulkStartProgress.completed)}/${String(bulkStartProgress.total)}`
                  : offlineVisibleNodeIds.length === 0 && visibleIds.size > 0
                    ? 'TERMINAIS ATIVOS'
                    : 'ATIVAR TERMINAIS'}
              </button>
              <button type="button" disabled={!activeWorktree} onClick={() => openAgent()}>
                <Plus size={14} /> Agent
              </button>
            </div>
          </>
        )}
      </section>

      <aside className="inspector-panel">
        {selectedNode ? (
          (() => {
            const node = snapshot.nodes.find((item) => item.id === selectedNode);
            if (!node) return null;
            const runtime = sessions[node.id];
            return (
              <>
                <span className="inspector-kicker">{node.kind.toUpperCase()}</span>
                <h2>{node.label}</h2>
                <p>{node.role || 'General implementation agent'}</p>
                <dl>
                  <div>
                    <dt>Provider</dt>
                    <dd>{node.provider}</dd>
                  </div>
                  <div>
                    <dt>Modelo</dt>
                    <dd>
                      {node.model ??
                        `Padrão — ${modelInventory.providers.find((item) => item.provider === node.provider)?.defaultModel ?? 'automático'}`}
                    </dd>
                  </div>
                  <div>
                    <dt>Status</dt>
                    <dd>{runtime?.status ?? 'Idle'}</dd>
                  </div>
                  <div>
                    <dt>Auto start</dt>
                    <dd>{node.autoStart ? 'YES' : 'NO'}</dd>
                  </div>
                  <div>
                    <dt>MCP Agent Infinite</dt>
                    <dd>
                      {runtime ? (runtime.mcpConnected ? 'CONECTADO' : 'AGUARDANDO') : 'OFFLINE'}
                    </dd>
                  </div>
                  <div>
                    <dt>Provider hooks</dt>
                    <dd>
                      {!runtime
                        ? 'OFFLINE'
                        : runtime.integrationMode === 'hooks'
                          ? 'ATIVOS'
                          : runtime.integrationMode === 'hooks-pending'
                            ? 'AGUARDANDO'
                            : runtime.integrationMode === 'error'
                              ? 'ERRO'
                              : 'INATIVOS'}
                    </dd>
                  </div>
                  <div>
                    <dt>Fallback detector</dt>
                    <dd>{runtime?.integrationMode === 'detector' ? 'ATIVO' : 'INATIVO'}</dd>
                  </div>
                </dl>
                <div className="inspector-actions">
                  <button type="button" onClick={() => openAgentEditor(node.id)}>
                    Edit
                  </button>
                  <button type="button" onClick={() => void startNode(node.id)}>
                    {runtime ? 'Restart' : 'Start'}
                  </button>
                  {runtime && (
                    <button type="button" onClick={() => void stopNode(node.id)}>
                      Stop
                    </button>
                  )}
                  {node.kind !== 'orchestrator' && (
                    <button type="button" className="danger" onClick={() => deleteAgent(node)}>
                      Excluir agente
                    </button>
                  )}
                </div>
              </>
            );
          })()
        ) : (
          <>
            <span className="inspector-kicker">WORKSPACE</span>
            <h2>{snapshot.workspacePath.split(/[\\/]/).at(-1)}</h2>
            <p>{snapshot.workspacePath}</p>
            <dl>
              <div>
                <dt>Teams</dt>
                <dd>{snapshot.teams.length}</dd>
              </div>
              <div>
                <dt>Worktrees</dt>
                <dd>{snapshot.worktrees.length}</dd>
              </div>
              <div>
                <dt>Agents</dt>
                <dd>{snapshot.nodes.length}</dd>
              </div>
              <div>
                <dt>Delegations</dt>
                <dd>{snapshot.edges.length}</dd>
              </div>
              <div className="integration-policy-row">
                <dt>Provider hooks</dt>
                <dd>
                  <select
                    aria-label="Provider hook policy"
                    value={snapshot.integration.hooks}
                    onChange={(event) => {
                      const hooks = event.target.value as 'auto' | 'off' | 'required';
                      void api
                        .setHookPolicy(hooks)
                        .then(setSnapshot)
                        .catch((reason: unknown) =>
                          onError(
                            reason instanceof Error ? reason.message : 'Hook policy update failed.',
                          ),
                        );
                    }}
                  >
                    <option value="auto">AUTO</option>
                    <option value="off">OFF</option>
                    <option value="required">REQUIRED</option>
                  </select>
                </dd>
              </div>
            </dl>
          </>
        )}
        <section className="dispatch-activity" aria-label="Dispatch activity">
          <header>
            <span>DISPATCH ACTIVITY</span>
            <small>{dispatches.length}</small>
          </header>
          {dispatches
            .filter(
              (dispatch) =>
                !selectedNode ||
                dispatch.source_node_id === selectedNode ||
                dispatch.target_node_id === selectedNode,
            )
            .slice(0, 6)
            .map((dispatch) => (
              <article key={dispatch.dispatch_id}>
                <div>
                  <strong>{dispatch.source_label}</strong>
                  <span aria-hidden="true">→</span>
                  <strong>{dispatch.target_label}</strong>
                </div>
                <p>{dispatch.task}</p>
                <footer>
                  <code>{dispatch.dispatch_id.slice(0, 8)}</code>
                  <span data-state={dispatch.status}>{dispatch.status.toUpperCase()}</span>
                  <time>
                    {dispatch.delivery_confirmed_by?.toUpperCase() ?? 'PENDING'} ·{' '}
                    {new Date(dispatch.updated_at).toLocaleTimeString()}
                  </time>
                </footer>
                {dispatch.result.error && <em>{dispatch.result.error}</em>}
              </article>
            ))}
          {dispatches.length === 0 && <p className="dispatch-empty">No dispatches yet.</p>}
        </section>
        {nativeSubagents.length > 0 && (
          <section className="native-subagent-activity" aria-label="Observed native subagents">
            <header>NATIVE SUBAGENTS OBSERVED</header>
            {nativeSubagents
              .filter((activity) => !selectedNode || activity.nodeId === selectedNode)
              .slice(0, 4)
              .map((activity) => (
                <article key={activity.id}>
                  <span>{activity.provider}</span>
                  <code>{activity.id.slice(0, 10)}</code>
                  <strong data-state={activity.status}>{activity.status.toUpperCase()}</strong>
                </article>
              ))}
          </section>
        )}
      </aside>

      {dialog && (
        <EditorDialog
          kind={dialog}
          snapshot={snapshot}
          activeWorktree={surface === 'teams' ? null : activeWorktree}
          activeTeam={agentContextTeam}
          gitBranches={gitBranches}
          templates={templates}
          roleOptions={availableAgentRoles}
          capabilities={capabilities}
          models={modelInventory}
          roleProfiles={snapshot.roleProfiles}
          agentRolePreset={agentRolePreset}
          onClose={() => setDialog(null)}
          onSubmit={async (payload) => {
            try {
              if (dialog === 'team') {
                await api.createTeam(
                  payload as {
                    name: string;
                    color: string;
                    orchestratorProvider: Provider;
                    orchestratorModel?: string;
                  },
                );
              } else if (dialog === 'template') {
                const created = await api.createTeamTemplate(
                  payload as {
                    name: string;
                    description?: string;
                    color: string;
                    orchestratorProvider: Provider;
                    orchestratorModel?: string;
                  },
                );
                await refreshTemplates();
                setSelectedTemplateId(created.id);
                onSurfaceChange('templates');
                setDialog(null);
                return;
              } else if (dialog === 'templateAgent') {
                if (!selectedTemplate) throw new Error('Select a Team Template.');
                if (
                  typeof payload.label !== 'string' ||
                  typeof payload.role !== 'string' ||
                  typeof payload.provider !== 'string'
                ) {
                  throw new Error('Agent fields are invalid.');
                }
                const node: CanvasNode = {
                  id: crypto.randomUUID().replaceAll('-', ''),
                  kind: 'agent',
                  provider: payload.provider as Provider,
                  ...(typeof payload.model === 'string' && payload.model
                    ? { model: payload.model }
                    : {}),
                  teamId: '',
                  label: payload.label,
                  role: payload.role,
                  ...(typeof payload.roleProfileId === 'string' && payload.roleProfileId
                    ? { roleProfileId: payload.roleProfileId }
                    : {}),
                  mcpIds: Array.isArray(payload.mcpIds) ? (payload.mcpIds as string[]) : [],
                  skillIds: Array.isArray(payload.skillIds) ? (payload.skillIds as string[]) : [],
                  autoStart: payload.autoStart === 'on',
                  position: {
                    x: 500 + selectedTemplate.nodes.length * 36,
                    y: 150 + selectedTemplate.nodes.length * 24,
                  },
                  size: { width: 300, height: 210 },
                };
                const updated = await api.updateTeamTemplate({
                  ...selectedTemplate,
                  nodes: [...selectedTemplate.nodes, node],
                });
                setTemplates((current) =>
                  current.map((template) => (template.id === updated.id ? updated : template)),
                );
                setDialog(null);
                return;
              } else if (dialog === 'saveTemplate') {
                if (typeof payload.teamId !== 'string') throw new Error('Select a Team.');
                const saved = await api.saveTeamTemplate({
                  teamId: payload.teamId,
                  ...(typeof payload.name === 'string' && payload.name.length > 0
                    ? { name: payload.name }
                    : {}),
                  ...(typeof payload.description === 'string' && payload.description.length > 0
                    ? { description: payload.description }
                    : {}),
                });
                await refreshTemplates();
                setSelectedTemplateId(saved.id);
                onSurfaceChange('templates');
                setDialog(null);
                return;
              } else if (dialog === 'role') {
                if (typeof payload.name !== 'string') throw new Error('Role name is required.');
                await api.createCustomRole(payload.name);
              } else if (dialog === 'worktree') {
                const created = await api.createWorktree(
                  payload as {
                    teamId?: string;
                    name: string;
                    baseRef?: string;
                    newBranch?: string;
                    existingBranch?: string;
                  },
                );
                const next = await refresh();
                setSelectedWorktree(
                  next.worktrees.find((item) => item.id === created.id)?.id ?? created.id,
                );
                if (worktreeTeamId) {
                  setRunTeamId(worktreeTeamId);
                  setWorktreeTeamId(null);
                }
                setDialog(null);
                return;
              } else if (dialog === 'importAgent') {
                if (!activeWorktree) throw new Error('Select a Git worktree first.');
                if (typeof payload.nodeId !== 'string') throw new Error('Select an agent.');
                await api.importNodeToWorktree(activeWorktree.id, payload.nodeId);
              } else if (dialog === 'importTemplate') {
                if (!activeWorktree) throw new Error('Select a Git worktree first.');
                if (typeof payload.templateId !== 'string') throw new Error('Select a template.');
                const [sourceKind, sourceId] = payload.templateId.split(':', 2);
                if (!sourceId) throw new Error('Select a Team or Team Template.');
                if (sourceKind === 'team') {
                  await api.importTeamToWorktree(activeWorktree.id, sourceId);
                } else if (sourceKind === 'template') {
                  await api.importTemplateToWorktree(activeWorktree.id, sourceId);
                } else {
                  throw new Error('Unknown Team composition source.');
                }
              } else {
                await api.createNode(
                  payload as {
                    label: string;
                    role: string;
                    provider: Provider;
                    model?: string;
                    roleProfileId?: string;
                    mcpIds?: string[];
                    skillIds?: string[];
                  } & { teamId: string; worktreeId: string },
                );
              }
              const next = await refresh();
              if (dialog === 'team') {
                const createdTeam = next.teams.at(-1);
                setManagedTeamId(createdTeam?.id ?? managedTeamId);
                onSurfaceChange('teams');
              }
              setDialog(null);
            } catch (reason) {
              onError(reason instanceof Error ? reason.message : 'Operation failed.');
            }
          }}
        />
      )}
      {governanceOpen ? (
        <CapabilityGovernance
          api={api}
          onError={onError}
          onClose={() => {
            setGovernanceOpen(false);
            void Promise.all([api.capabilityInventory(), api.modelInventory()]).then(
              ([result, models]) => {
                setCapabilities(result.items);
                setModelInventory(models);
              },
            );
          }}
        />
      ) : null}
      {editingAgentId ? (
        <AgentCapabilityEditor
          api={api}
          node={snapshot.nodes.find((node) => node.id === editingAgentId) ?? null}
          roles={snapshot.roleProfiles}
          capabilities={capabilities}
          models={modelInventory}
          running={Boolean(sessions[editingAgentId])}
          onClose={() => setEditingAgentId(null)}
          onSaved={async () => {
            await refresh();
            setEditingAgentId(null);
          }}
          onError={onError}
        />
      ) : null}
      {editingTemplateAgentId && selectedTemplate ? (
        <AgentCapabilityEditor
          api={api}
          node={selectedTemplate.nodes.find((node) => node.id === editingTemplateAgentId) ?? null}
          roles={snapshot.roleProfiles}
          capabilities={capabilities}
          models={modelInventory}
          running={false}
          onClose={() => setEditingTemplateAgentId(null)}
          onSave={async (update: AgentEditorUpdate) => {
            const updated = await api.updateTeamTemplate({
              ...selectedTemplate,
              nodes: selectedTemplate.nodes.map((node) =>
                node.id === editingTemplateAgentId ? { ...node, ...update } : node,
              ),
            });
            setTemplates((current) =>
              current.map((template) => (template.id === updated.id ? updated : template)),
            );
          }}
          onSaved={() => {
            setEditingTemplateAgentId(null);
            return Promise.resolve();
          }}
          onError={onError}
        />
      ) : null}
      {editingRoleId ? (
        <RoleProfileEditor
          api={api}
          role={
            editingRoleId === 'new'
              ? null
              : (snapshot.roleProfiles.find((role) => role.id === editingRoleId) ?? null)
          }
          capabilities={capabilities}
          models={modelInventory}
          onClose={() => setEditingRoleId(null)}
          onSaved={async () => {
            await refresh();
            setEditingRoleId(null);
          }}
          onError={onError}
        />
      ) : null}
      {runTeamId && (
        <TeamRunDialog
          team={snapshot.teams.find((team) => team.id === runTeamId) ?? null}
          worktrees={snapshot.worktrees}
          onClose={() => setRunTeamId(null)}
          onCreateWorktree={() => {
            setRunTeamId(null);
            setWorktreeTeamId(runTeamId);
            setDialog('worktree');
          }}
          onRun={(worktreeId) => {
            void api
              .runTeam(runTeamId, worktreeId)
              .then(() => {
                setRunTeamId(null);
                return refresh();
              })
              .catch((reason: unknown) =>
                onError(reason instanceof Error ? reason.message : 'Could not execute Team.'),
              );
          }}
        />
      )}
    </div>
  );
}

interface TeamManagementPageProps {
  readonly snapshot: Snapshot;
  readonly managedTeamId: string | null;
  readonly onManagedTeamChange: (teamId: string) => void;
  readonly onCreateTeam: () => void;
  readonly onBackToCanvas: () => void;
}

export function TeamManagementPage({
  snapshot,
  managedTeamId,
  onManagedTeamChange,
  onCreateTeam,
  onBackToCanvas,
}: TeamManagementPageProps): React.JSX.Element {
  return (
    <div className="teams-page" aria-labelledby="agent-teams-page-title">
      <header className="teams-page-header">
        <div>
          <span>AGENT TEAMS / MANAGEMENT</span>
          <h1 id="agent-teams-page-title">Agent Teams</h1>
          <p>
            Teams define people and roles. Git Worktrees link to a team, while only a Git Worktree
            changes the canvas selection.
          </p>
        </div>
        <div>
          <button type="button" className="teams-back" onClick={onBackToCanvas}>
            Back to canvas
          </button>
          <button type="button" className="teams-create" onClick={onCreateTeam}>
            <Plus size={14} /> New team
          </button>
        </div>
      </header>
      <div className="teams-page-grid">
        {snapshot.teams.map((team) => {
          const worktrees = snapshot.worktrees.filter((worktree) => worktree.teamId === team.id);
          const agentCount = snapshot.nodes.filter((node) => node.teamId === team.id).length;
          return (
            <button
              type="button"
              key={team.id}
              className={
                managedTeamId === team.id ? 'team-management-card active' : 'team-management-card'
              }
              aria-pressed={managedTeamId === team.id}
              onClick={() => onManagedTeamChange(team.id)}
            >
              <span className="team-card-signal" style={{ background: team.color }} />
              <span className="team-card-kicker">TEAM</span>
              <strong>{team.name}</strong>
              <small>
                {agentCount} agents · {worktrees.length} worktrees linked
              </small>
              <span className="team-card-worktrees">
                {worktrees.length > 0
                  ? worktrees.map((worktree) => worktree.name).join(' · ')
                  : 'No linked Git worktrees'}
              </span>
            </button>
          );
        })}
        {snapshot.teams.length === 0 && (
          <div className="teams-page-empty">
            <Users size={20} />
            <strong>No Agent Teams yet</strong>
            <span>Create a team, then link it when creating a Git worktree.</span>
          </div>
        )}
      </div>
    </div>
  );
}

interface TemplateLibraryPageProps {
  readonly template: TeamTemplate | null;
  readonly onCreateTemplate: () => void;
  readonly onCreateAgent: () => void;
  readonly onUpdate: (template: TeamTemplate) => Promise<TeamTemplate>;
  readonly onApply: (templateId: string) => void;
  readonly onDelete: (templateId: string) => void;
  readonly onEditAgent: (nodeId: string) => void;
}

function TemplateLibraryPage({
  template,
  onCreateTemplate,
  onCreateAgent,
  onUpdate,
  onApply,
  onDelete,
  onEditAgent,
}: TemplateLibraryPageProps): React.JSX.Element {
  if (!template) {
    return (
      <div className="team-definition-empty">
        <LayoutTemplate size={22} />
        <strong>Nenhum template salvo</strong>
        <span>Crie um canvas reutilizável de agentes, roles e conexões.</span>
        <button type="button" onClick={onCreateTemplate}>
          <Plus size={14} /> NOVO TEAM TEMPLATE
        </button>
      </div>
    );
  }
  return (
    <TeamTemplateEditor
      key={template.id}
      template={template}
      onCreateAgent={onCreateAgent}
      onUpdate={onUpdate}
      onApply={onApply}
      onDelete={onDelete}
      onEditAgent={onEditAgent}
    />
  );
}

interface TeamTemplateEditorProps {
  readonly template: TeamTemplate;
  readonly onCreateAgent: () => void;
  readonly onUpdate: (template: TeamTemplate) => Promise<TeamTemplate>;
  readonly onApply: (templateId: string) => void;
  readonly onDelete: (templateId: string) => void;
  readonly onEditAgent: (nodeId: string) => void;
}

function TeamTemplateEditor({
  template,
  onCreateAgent,
  onUpdate,
  onApply,
  onDelete,
  onEditAgent,
}: TeamTemplateEditorProps): React.JSX.Element {
  const [draft, setDraft] = useState(template);
  const [dirty, setDirty] = useState(false);
  const [saveState, setSaveState] = useState<'saved' | 'saving' | 'error'>('saved');
  const flowNodes = useMemo<Node<AgentNodeData>[]>(
    () =>
      draft.nodes.map((node) => ({
        id: node.id,
        type: 'agent',
        position: node.position,
        width: node.size.width,
        height: node.size.height,
        data: {
          label: node.label,
          role: node.role,
          kind: node.kind,
          provider: node.provider,
          ...(node.model ? { model: node.model } : {}),
          teamName: draft.name,
          teamColor: draft.color,
          status: 'Template',
          preview: '',
          onEdit: onEditAgent,
        },
      })),
    [draft.color, draft.name, draft.nodes, onEditAgent],
  );
  const flowEdges = useMemo(() => toFlowEdges(draft.edges), [draft.edges]);

  useEffect(() => {
    if (!dirty) setDraft(template);
  }, [dirty, template]);

  useEffect(() => {
    if (!dirty) return;
    setSaveState('saving');
    const timer = window.setTimeout(() => {
      void onUpdate(draft)
        .then((updated) => {
          setDraft(updated);
          setDirty(false);
          setSaveState('saved');
        })
        .catch(() => setSaveState('error'));
    }, 450);
    return () => window.clearTimeout(timer);
  }, [dirty, draft, onUpdate]);

  const changeDraft = (update: (current: TeamTemplate) => TeamTemplate): void => {
    setDraft(update);
    setDirty(true);
  };

  const handleNodesChange = (changes: NodeChange<Node<AgentNodeData>>[]): void => {
    const persistentChanges = changes.filter(
      (change) => change.type === 'position' && change.position !== undefined,
    );
    if (persistentChanges.length === 0) return;
    const nextFlow = applyNodeChanges(persistentChanges, flowNodes);
    const byID = new Map(nextFlow.map((node) => [node.id, node]));
    changeDraft((current) => ({
      ...current,
      nodes: current.nodes.map((node) => {
        const flow = byID.get(node.id);
        return flow
          ? {
              ...node,
              position: flow.position,
              size: node.size,
            }
          : node;
      }),
    }));
  };

  return (
    <section className="template-library-page">
      <header className="template-library-hero">
        <div className="template-library-signal" style={{ background: draft.color }} />
        <div>
          <span>TEAM TEMPLATE / AUTOSAVE · {saveState.toUpperCase()}</span>
          <input
            className="template-title-input"
            aria-label="Template name"
            value={draft.name}
            maxLength={80}
            onChange={(event) =>
              changeDraft((current) => ({ ...current, name: event.target.value }))
            }
          />
          <input
            className="template-description-input"
            aria-label="Template description"
            value={draft.description ?? ''}
            maxLength={240}
            placeholder="Descreva quando reutilizar esta composição"
            onChange={(event) =>
              changeDraft((current) => ({ ...current, description: event.target.value }))
            }
          />
        </div>
        <div className="template-library-actions">
          <button
            type="button"
            onClick={() => {
              if (!dirty) {
                onCreateAgent();
                return;
              }
              setSaveState('saving');
              void onUpdate(draft).then((updated) => {
                setDraft(updated);
                setDirty(false);
                setSaveState('saved');
                onCreateAgent();
              });
            }}
          >
            <Plus size={13} /> ADICIONAR AGENTE
          </button>
          <button type="button" onClick={() => onDelete(draft.id)}>
            EXCLUIR
          </button>
          <button type="button" className="primary" onClick={() => onApply(draft.id)}>
            CRIAR TEAM A PARTIR DESTE TEMPLATE
          </button>
        </div>
      </header>
      <div className="template-editor-flow">
        <ReactFlow
          nodes={flowNodes}
          edges={flowEdges}
          nodeTypes={nodeTypes}
          onNodesChange={handleNodesChange}
          onEdgesChange={(changes) => {
            const next = applyEdgeChanges(changes, flowEdges);
            changeDraft((current) => ({
              ...current,
              edges: next.map((edge) => ({
                id: edge.id,
                source: edge.source,
                target: edge.target,
                type: 'delegates_to',
              })),
            }));
          }}
          onConnect={(connection) => {
            if (!connection.source || !connection.target) return;
            const source = draft.nodes.find((node) => node.id === connection.source);
            if (source?.kind !== 'orchestrator' || connection.source === connection.target) return;
            changeDraft((current) => ({
              ...current,
              edges: [
                ...current.edges,
                {
                  id: crypto.randomUUID().replaceAll('-', ''),
                  source: connection.source,
                  target: connection.target,
                  type: 'delegates_to',
                },
              ],
            }));
          }}
          onNodeDoubleClick={(_event, node) => onEditAgent(node.id)}
          fitView
          minZoom={0.2}
          maxZoom={1.5}
          proOptions={{ hideAttribution: true }}
        >
          <Background gap={32} size={1} color="rgba(159, 216, 53, 0.09)" />
        </ReactFlow>
      </div>
    </section>
  );
}

interface TeamDefinitionCanvasProps {
  readonly snapshot: Snapshot;
  readonly teamId: string | null;
  readonly nodes: readonly Node<AgentNodeData>[];
  readonly edges: readonly Edge[];
  readonly onNodesChange: (changes: NodeChange<Node<AgentNodeData>>[]) => void;
  readonly onEdgesChange: (changes: EdgeChange<Edge>[]) => void;
  readonly onConnect: (connection: Connection) => void;
  readonly onCreateTeam: () => void;
  readonly onCreateAgent: () => void;
  readonly onSaveTemplate: (teamId: string) => void;
  readonly onRunTeam: (teamId: string) => void;
  readonly onSelectAgent: (nodeId: string) => void;
  readonly onEditAgent: (nodeId: string) => void;
}

function TeamDefinitionCanvas({
  snapshot,
  teamId,
  nodes,
  edges,
  onNodesChange,
  onEdgesChange,
  onConnect,
  onCreateTeam,
  onCreateAgent,
  onSaveTemplate,
  onRunTeam,
  onSelectAgent,
  onEditAgent,
}: TeamDefinitionCanvasProps): React.JSX.Element {
  const team = snapshot.teams.find((candidate) => candidate.id === teamId) ?? null;
  const nodeIds = useMemo(
    () =>
      new Set(
        snapshot.nodes
          .filter((node) => node.teamId === teamId && !node.worktreeId)
          .map((node) => node.id),
      ),
    [snapshot.nodes, teamId],
  );
  const teamNodes = useMemo(() => nodes.filter((node) => nodeIds.has(node.id)), [nodeIds, nodes]);
  const teamEdges = useMemo(
    () => edges.filter((edge) => nodeIds.has(edge.source) && nodeIds.has(edge.target)),
    [edges, nodeIds],
  );

  if (!team) {
    return (
      <div className="team-definition-empty">
        <Users size={22} />
        <strong>No Agent Team selected</strong>
        <span>Create a Team or choose one in the sidebar to edit its independent workflow.</span>
        <button type="button" onClick={onCreateTeam}>
          <Plus size={14} /> NEW TEAM
        </button>
      </div>
    );
  }

  return (
    <section className="team-definition-canvas" aria-labelledby="team-definition-title">
      <header className="team-definition-header">
        <div>
          <span>AGENT TEAM / WORKFLOW DEFINITION</span>
          <h1 id="team-definition-title">{team.name}</h1>
          <p>Independent canvas. A Git Worktree is selected only when this Team is executed.</p>
        </div>
        <div>
          <button type="button" onClick={onCreateAgent}>
            <Plus size={14} /> ADICIONAR AGENTE
          </button>
          <button type="button" onClick={() => onSaveTemplate(team.id)}>
            <Save size={14} /> SAVE AS TEMPLATE
          </button>
          <button type="button" className="teams-create" onClick={() => onRunTeam(team.id)}>
            RUN TEAM
          </button>
        </div>
      </header>
      <div className="team-definition-flow">
        <ReactFlow
          nodes={teamNodes}
          edges={teamEdges}
          nodeTypes={nodeTypes}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          onConnect={onConnect}
          onNodeClick={(_event, node) => onSelectAgent(node.id)}
          onNodeDoubleClick={(_event, node) => onEditAgent(node.id)}
          fitView
          minZoom={0.2}
          maxZoom={1.4}
          proOptions={{ hideAttribution: true }}
        >
          <Background gap={32} size={1} color="rgba(159, 216, 53, 0.09)" />
        </ReactFlow>
      </div>
    </section>
  );
}

interface TeamRunDialogProps {
  readonly team: Snapshot['teams'][number] | null;
  readonly worktrees: readonly Snapshot['worktrees'][number][];
  readonly onClose: () => void;
  readonly onCreateWorktree: () => void;
  readonly onRun: (worktreeId: string) => void;
}

function TeamRunDialog({
  team,
  worktrees,
  onClose,
  onCreateWorktree,
  onRun,
}: TeamRunDialogProps): React.JSX.Element {
  const [worktreeId, setWorktreeId] = useState(worktrees[0]?.id ?? '');
  return (
    <div className="dialog-backdrop" role="presentation">
      <section className="editor-dialog team-run-dialog" role="dialog" aria-modal="true">
        <header>
          <span>EXECUTE TEAM</span>
          <button type="button" aria-label="Close" onClick={onClose}>
            <X size={15} />
          </button>
        </header>
        <p>
          {team?.name ?? 'This Team'} keeps its workflow independent. Choose the Git Worktree only
          for this runtime session.
        </p>
        {worktrees.length === 0 ? (
          <>
            <p className="dialog-note">This Team has no Git Worktrees yet.</p>
            <button className="dialog-submit" type="button" onClick={onCreateWorktree}>
              CREATE GIT WORKTREE
            </button>
          </>
        ) : (
          <>
            <label>
              Git Worktree
              <select value={worktreeId} onChange={(event) => setWorktreeId(event.target.value)}>
                {worktrees.map((worktree) => (
                  <option key={worktree.id} value={worktree.id}>
                    {worktree.name} / {worktree.branch || 'branch pending'}
                  </option>
                ))}
              </select>
            </label>
            <button className="dialog-submit" type="button" onClick={() => onRun(worktreeId)}>
              EXECUTE TEAM
            </button>
          </>
        )}
      </section>
    </div>
  );
}

interface EditorDialogProps {
  readonly kind:
    | 'team'
    | 'template'
    | 'templateAgent'
    | 'worktree'
    | 'agent'
    | 'role'
    | 'importAgent'
    | 'importTemplate'
    | 'saveTemplate';
  readonly snapshot: Snapshot;
  readonly activeWorktree: Snapshot['worktrees'][number] | null;
  readonly activeTeam: Snapshot['teams'][number] | null;
  readonly gitBranches: { readonly all: readonly string[]; readonly available: readonly string[] };
  readonly templates: readonly TeamTemplate[];
  readonly roleOptions: readonly string[];
  readonly capabilities: readonly CapabilityItem[];
  readonly models: ModelInventory;
  readonly roleProfiles: Snapshot['roleProfiles'];
  readonly agentRolePreset: string;
  readonly onClose: () => void;
  readonly onSubmit: (payload: Record<string, unknown>) => Promise<void>;
}

function EditorDialog({
  kind,
  snapshot,
  activeWorktree,
  activeTeam,
  gitBranches,
  templates,
  roleOptions,
  capabilities,
  models,
  roleProfiles,
  agentRolePreset,
  onClose,
  onSubmit,
}: EditorDialogProps): React.JSX.Element {
  const teamImportSources = snapshot.teams
    .map((team) => {
      const nodes = snapshot.nodes.filter((node) => node.teamId === team.id && !node.worktreeId);
      const nodeIds = new Set(nodes.map((node) => node.id));
      const edges = snapshot.edges.filter(
        (edge) => nodeIds.has(edge.source) && nodeIds.has(edge.target),
      );
      const provider = nodes.find((node) => node.kind === 'orchestrator')?.provider;
      return provider ? { team, nodes, edges, provider } : null;
    })
    .filter((source) => source !== null);
  const [busy, setBusy] = useState(false);
  const [teamId, setTeamId] = useState(activeTeam?.id ?? '');
  const [branchMode, setBranchMode] = useState<'new' | 'existing'>('new');
  const [templateId, setTemplateId] = useState(
    kind === 'importTemplate'
      ? templates[0]
        ? `template:${templates[0].id}`
        : teamImportSources[0]
          ? `team:${teamImportSources[0].team.id}`
          : ''
      : (templates[0]?.id ?? ''),
  );
  const [templateTeamId, setTemplateTeamId] = useState(
    activeTeam?.id ?? snapshot.teams[0]?.id ?? '',
  );
  const matchingRoleProfile = roleProfiles.find(
    (role) => role.name.trim().toLowerCase() === agentRolePreset.trim().toLowerCase(),
  );
  const [selectedRolePreset, setSelectedRolePreset] = useState(agentRolePreset);
  const [agentRole, setAgentRole] = useState(agentRolePreset);
  const [agentProvider, setAgentProvider] = useState<Provider>(
    matchingRoleProfile?.defaultProvider ?? 'codex',
  );
  const [agentModel, setAgentModel] = useState(matchingRoleProfile?.model ?? '');
  const [orchestratorProvider, setOrchestratorProvider] = useState<Provider>(
    kind === 'team' ? 'claude' : 'codex',
  );
  const [orchestratorModel, setOrchestratorModel] = useState('');
  const [roleProfileId, setRoleProfileId] = useState(matchingRoleProfile?.id ?? '');
  const [mcpIds, setMcpIds] = useState<string[]>(() => [...(matchingRoleProfile?.mcpIds ?? [])]);
  const [skillIds, setSkillIds] = useState<string[]>(() => [
    ...(matchingRoleProfile?.skillIds ?? []),
  ]);
  const curatedCapabilities = capabilities.filter(
    (item) =>
      isCapabilityAvailable(item) &&
      item.policy === 'curated' &&
      isCapabilityCompatible(item, agentProvider),
  );
  const inheritedCount = groupCapabilityItems(
    capabilities.filter(
      (item) =>
        isCapabilityAvailable(item) &&
        item.policy === 'provider_default' &&
        (item.provider === 'all' || item.provider === agentProvider),
    ),
  ).length;
  const toggleCapability = (
    ids: readonly string[],
    capabilityKind: CapabilityItem['kind'],
  ): void => {
    const setter = capabilityKind === 'mcp' ? setMcpIds : setSkillIds;
    setter((current) => {
      const selected = ids.some((id) => current.includes(id));
      const representative = ids[0];
      if (selected) return current.filter((value) => !ids.includes(value));
      return representative ? [...current, representative] : current;
    });
  };
  const applyRoleProfile = (id: string): void => {
    setRoleProfileId(id);
    const profile = roleProfiles.find((candidate) => candidate.id === id);
    if (!profile) return;
    setAgentRole(profile.name);
    if (profile.defaultProvider) setAgentProvider(profile.defaultProvider);
    setAgentModel(profile.model ?? '');
    setMcpIds([...profile.mcpIds]);
    setSkillIds([...profile.skillIds]);
  };
  const selectedTemplate = templates.find((template) => template.id === templateId) ?? null;
  const importSources = [
    ...templates.map((template) => ({
      id: `template:${template.id}`,
      name: template.name,
      color: template.color,
      provider: template.orchestratorProvider,
      nodes: template.nodes,
      edges: template.edges,
      source: 'BIBLIOTECA',
    })),
    ...teamImportSources.map(({ team, nodes, edges, provider }) => ({
      id: `team:${team.id}`,
      name: team.name,
      color: team.color,
      provider,
      nodes,
      edges,
      source: 'AGENT TEAM',
    })),
  ];
  const selectedImportSource = importSources.find((source) => source.id === templateId) ?? null;
  return (
    <div className="dialog-backdrop" role="presentation">
      <form
        className="editor-dialog"
        onSubmit={(event) => {
          event.preventDefault();
          setBusy(true);
          const data = Object.fromEntries(new FormData(event.currentTarget).entries()) as Record<
            string,
            unknown
          >;
          if (kind === 'agent' || kind === 'templateAgent') {
            data.provider = agentProvider;
            data.model = agentModel;
            data.roleProfileId = roleProfileId;
            data.mcpIds = mcpIds;
            data.skillIds = skillIds;
          }
          if (kind === 'team' || kind === 'template') {
            data.orchestratorProvider = orchestratorProvider;
            data.orchestratorModel = orchestratorModel;
          }
          void onSubmit(data).finally(() => setBusy(false));
        }}
      >
        <header>
          <span>
            {kind === 'team'
              ? 'NEW TEAM'
              : kind === 'template'
                ? 'NEW TEAM TEMPLATE'
                : kind === 'templateAgent'
                  ? 'ADD AGENT TO TEMPLATE'
                  : kind === 'saveTemplate'
                    ? 'SAVE TEAM AS TEMPLATE'
                    : kind === 'worktree'
                      ? 'NEW GIT WORKTREE'
                      : kind === 'role'
                        ? 'NEW CUSTOM ROLE'
                        : kind === 'importAgent'
                          ? 'IMPORT AGENT TO WORKTREE'
                          : kind === 'importTemplate'
                            ? 'IMPORT TEAM TEMPLATE'
                            : 'NEW AGENT'}
          </span>
          <button type="button" aria-label="Close" onClick={onClose}>
            <X size={15} />
          </button>
        </header>
        {kind === 'team' ? (
          <>
            <label>
              Team name
              <input name="name" required maxLength={80} autoFocus />
            </label>
            <label>
              Signal color
              <input name="color" type="color" defaultValue="#b7f34a" />
            </label>
            <label>
              Orchestrator
              <select
                name="orchestratorProvider"
                value={orchestratorProvider}
                onChange={(event) => {
                  setOrchestratorProvider(event.target.value as Provider);
                  setOrchestratorModel('');
                }}
              >
                <option value="claude">Claude Code</option>
                <option value="codex">Codex</option>
                <option value="pi">Pi</option>
                <option value="opencode">OpenCode</option>
              </select>
            </label>
            <ModelSelector
              provider={orchestratorProvider}
              value={orchestratorModel}
              inventory={models}
              onChange={setOrchestratorModel}
              label="Modelo do orquestrador"
            />
          </>
        ) : kind === 'template' ? (
          <>
            <label>
              Template name
              <input name="name" required maxLength={80} autoFocus />
            </label>
            <label>
              Description (optional)
              <textarea name="description" maxLength={240} />
            </label>
            <label>
              Signal color
              <input name="color" type="color" defaultValue="#b7f34a" />
            </label>
            <label>
              Orchestrator
              <select
                name="orchestratorProvider"
                value={orchestratorProvider}
                onChange={(event) => {
                  setOrchestratorProvider(event.target.value as Provider);
                  setOrchestratorModel('');
                }}
              >
                <option value="codex">Codex</option>
                <option value="claude">Claude Code</option>
                <option value="pi">Pi</option>
                <option value="opencode">OpenCode</option>
              </select>
            </label>
            <ModelSelector
              provider={orchestratorProvider}
              value={orchestratorModel}
              inventory={models}
              onChange={setOrchestratorModel}
              label="Modelo do orquestrador"
            />
            <p className="dialog-note">
              Este template será salvo imediatamente e terá um canvas próprio, sem Team ou Worktree.
            </p>
          </>
        ) : kind === 'saveTemplate' ? (
          <>
            <label>
              Source Team
              <select
                name="teamId"
                required
                value={templateTeamId}
                onChange={(event) => setTemplateTeamId(event.target.value)}
              >
                {snapshot.teams.map((team) => (
                  <option key={team.id} value={team.id}>
                    {team.name} /{' '}
                    {
                      snapshot.nodes.filter((node) => node.teamId === team.id && !node.worktreeId)
                        .length
                    }{' '}
                    nodes
                  </option>
                ))}
              </select>
            </label>
            <label>
              Template name (optional)
              <input name="name" maxLength={80} placeholder="Uses the Team name by default" />
            </label>
            <label>
              Description (optional)
              <textarea
                name="description"
                maxLength={240}
                placeholder="When should this composition be reused?"
              />
            </label>
            <p className="dialog-note">
              Worktrees, branches, terminals, sessions and credentials are never included.
            </p>
          </>
        ) : kind === 'role' ? (
          <label>
            Role name
            <input name="name" required maxLength={80} autoFocus />
          </label>
        ) : kind === 'worktree' ? (
          <>
            <label>
              Team link (optional)
              <select
                name="teamId"
                value={teamId}
                onChange={(event) => setTeamId(event.target.value)}
              >
                <option value="">Independent worktree</option>
                {snapshot.teams.map((team) => (
                  <option key={team.id} value={team.id}>
                    {team.name}
                  </option>
                ))}
              </select>
            </label>
            <label>
              Worktree name
              <input name="name" required maxLength={80} autoFocus />
            </label>
            <div className="branch-mode-switch" role="group" aria-label="Git branch mode">
              <button
                type="button"
                className={branchMode === 'new' ? 'active' : ''}
                onClick={() => setBranchMode('new')}
              >
                NEW BRANCH
              </button>
              <button
                type="button"
                className={branchMode === 'existing' ? 'active' : ''}
                onClick={() => setBranchMode('existing')}
              >
                EXISTING BRANCH
              </button>
            </div>
            {branchMode === 'new' ? (
              <label>
                New branch name
                <input
                  name="newBranch"
                  required
                  maxLength={240}
                  placeholder="feature/my-change"
                  autoComplete="off"
                />
              </label>
            ) : (
              <label>
                Existing available branch
                <select
                  name="existingBranch"
                  required
                  defaultValue={gitBranches.available[0] ?? ''}
                >
                  {gitBranches.available.length === 0 && (
                    <option value="">No available branches</option>
                  )}
                  {gitBranches.available.map((branch) => (
                    <option key={branch} value={branch}>
                      {branch}
                    </option>
                  ))}
                </select>
              </label>
            )}
          </>
        ) : kind === 'importAgent' ? (
          <>
            <div className="agent-worktree-context">
              <span>DESTINATION WORKTREE</span>
              <strong>{activeWorktree?.name ?? 'NONE'}</strong>
              <small>{activeWorktree?.branch ?? ''}</small>
            </div>
            <label>
              Agent definition
              <select name="nodeId" required>
                {snapshot.nodes
                  .filter((node) => node.kind === 'agent' && !node.worktreeId)
                  .map((node) => (
                    <option key={node.id} value={node.id}>
                      {node.label} / {node.role || node.provider}
                    </option>
                  ))}
              </select>
            </label>
          </>
        ) : kind === 'importTemplate' ? (
          <>
            <div className="agent-worktree-context template-destination">
              <span>DESTINATION WORKTREE</span>
              <strong>{activeWorktree?.name ?? 'NONE'}</strong>
              <small>BRANCH: {activeWorktree?.branch ?? ''}</small>
            </div>
            {importSources.length > 0 ? (
              <>
                <label>
                  Composição de Team
                  <select
                    name="templateId"
                    required
                    value={templateId}
                    onChange={(event) => setTemplateId(event.target.value)}
                  >
                    {importSources.map((source) => (
                      <option key={source.id} value={source.id}>
                        {source.source} / {source.name} / {source.nodes.length} nodes
                      </option>
                    ))}
                  </select>
                </label>
                {selectedImportSource ? (
                  <div className="template-import-preview">
                    <i style={{ background: selectedImportSource.color }} />
                    <div>
                      <strong>{selectedImportSource.name}</strong>
                      <span>
                        {selectedImportSource.nodes.length} NODES ·{' '}
                        {selectedImportSource.edges.length} CONEXÕES
                      </span>
                    </div>
                    <code>{selectedImportSource.provider.toUpperCase()}</code>
                  </div>
                ) : null}
                <p className="dialog-note">
                  {activeWorktree?.teamId
                    ? 'A composição será instanciada no Team já associado a este worktree.'
                    : 'O worktree será associado ao Team escolhido; templates criam uma nova definição de Team.'}
                </p>
              </>
            ) : (
              <p className="dialog-note">
                Nenhum Team ou Team Template com workflow está disponível.
              </p>
            )}
          </>
        ) : kind === 'templateAgent' ? (
          <>
            <div className="agent-worktree-context">
              <span>TEAM TEMPLATE</span>
              <strong>{selectedTemplate?.name ?? 'NONE'}</strong>
              <small>AUTOSAVE / SEM WORKTREE</small>
            </div>
            <label>
              Label
              <input name="label" required maxLength={80} autoFocus />
            </label>
            <label htmlFor="template-agent-role-preset">
              Role predefinida
              <select
                id="template-agent-role-preset"
                aria-label="Role predefinida"
                value={selectedRolePreset}
                onChange={(event) => {
                  const role = event.target.value;
                  setSelectedRolePreset(role);
                  if (role) setAgentRole(role);
                }}
              >
                <option value="">Personalizada</option>
                {roleOptions.map((role) => (
                  <option key={role} value={role}>
                    {role}
                  </option>
                ))}
              </select>
            </label>
            <label htmlFor="template-agent-role">
              Role
              <textarea
                id="template-agent-role"
                name="role"
                maxLength={240}
                value={agentRole}
                onChange={(event) => {
                  const role = event.target.value;
                  setAgentRole(role);
                  if (role !== selectedRolePreset) setSelectedRolePreset('');
                }}
              />
            </label>
            <label>
              Provider
              <select
                name="provider"
                value={agentProvider}
                onChange={(event) => {
                  setAgentProvider(event.target.value as Provider);
                  setAgentModel('');
                  setMcpIds([]);
                  setSkillIds([]);
                }}
              >
                <option value="codex">Codex</option>
                <option value="claude">Claude Code</option>
                <option value="pi">Pi</option>
                <option value="opencode">OpenCode</option>
              </select>
            </label>
            <ModelSelector
              provider={agentProvider}
              value={agentModel}
              inventory={models}
              onChange={setAgentModel}
            />
            <label>
              Role com capacidades
              <select
                value={roleProfileId}
                onChange={(event) => applyRoleProfile(event.target.value)}
              >
                <option value="">Sem preset de capacidades</option>
                {roleProfiles.map((role) => (
                  <option key={role.id} value={role.id}>
                    {role.name}
                  </option>
                ))}
              </select>
            </label>
            <p className="dialog-note">
              {inheritedCount} capacidade(s) do provider serão herdadas e ficam bloqueadas para
              edição até virarem curadas.
            </p>
            <CapabilityPicker
              items={curatedCapabilities.filter((item) => item.kind === 'mcp')}
              selected={mcpIds}
              onToggle={toggleCapability}
              title="MCP SERVERS CURADOS"
            />
            <CapabilityPicker
              items={curatedCapabilities.filter((item) => item.kind === 'skill')}
              selected={skillIds}
              onToggle={toggleCapability}
              title="SKILLS CURADAS"
            />
            <label className="dialog-checkbox-row">
              <input name="autoStart" type="checkbox" />
              Iniciar automaticamente quando o Team for executado
            </label>
          </>
        ) : (
          <>
            <div className="agent-worktree-context">
              <span>ACTIVE GIT WORKTREE</span>
              <strong>{activeWorktree?.name ?? 'NONE'}</strong>
              <small>TEAM: {activeTeam?.name ?? 'UNLINKED'}</small>
              <input name="teamId" type="hidden" value={activeTeam?.id ?? ''} />
              <input name="worktreeId" type="hidden" value={activeWorktree?.id ?? ''} />
            </div>
            <label>
              Label
              <input name="label" required maxLength={80} autoFocus />
            </label>
            <label>
              Role
              <textarea name="role" maxLength={240} defaultValue={agentRolePreset} />
            </label>
            <label>
              Provider
              <select
                name="provider"
                value={agentProvider}
                onChange={(event) => {
                  setAgentProvider(event.target.value as Provider);
                  setAgentModel('');
                  setMcpIds([]);
                  setSkillIds([]);
                }}
              >
                <option value="codex">Codex</option>
                <option value="claude">Claude Code</option>
                <option value="pi">Pi</option>
                <option value="opencode">OpenCode</option>
              </select>
            </label>
            <ModelSelector
              provider={agentProvider}
              value={agentModel}
              inventory={models}
              onChange={setAgentModel}
            />
            <label>
              Role com capacidades
              <select
                value={roleProfileId}
                onChange={(event) => applyRoleProfile(event.target.value)}
              >
                <option value="">Sem preset de capacidades</option>
                {roleProfiles.map((role) => (
                  <option key={role.id} value={role.id}>
                    {role.name}
                  </option>
                ))}
              </select>
            </label>
            <p className="dialog-note">
              {inheritedCount} capacidade(s) herdadas do provider; selecione abaixo apenas itens
              curados.
            </p>
            <CapabilityPicker
              items={curatedCapabilities.filter((item) => item.kind === 'mcp')}
              selected={mcpIds}
              onToggle={toggleCapability}
              title="MCP SERVERS CURADOS"
            />
            <CapabilityPicker
              items={curatedCapabilities.filter((item) => item.kind === 'skill')}
              selected={skillIds}
              onToggle={toggleCapability}
              title="SKILLS CURADAS"
            />
          </>
        )}
        <button
          className="dialog-submit"
          type="submit"
          disabled={
            busy ||
            (kind === 'importTemplate' && importSources.length === 0) ||
            (kind === 'saveTemplate' && snapshot.teams.length === 0)
          }
        >
          {busy
            ? 'PROCESSING…'
            : kind === 'template'
              ? 'CREATE TEMPLATE'
              : kind === 'templateAgent'
                ? 'ADD AGENT'
                : kind === 'saveTemplate'
                  ? 'SAVE TEMPLATE'
                  : kind === 'importAgent'
                    ? 'IMPORT AGENT'
                    : kind === 'importTemplate'
                      ? 'IMPORT TEMPLATE'
                      : kind === 'role'
                        ? 'SAVE ROLE'
                        : `CREATE ${kind.toUpperCase()}`}
        </button>
      </form>
    </div>
  );
}
