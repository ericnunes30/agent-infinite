import {
  addEdge,
  applyEdgeChanges,
  applyNodeChanges,
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
  Plus,
  Save,
  Users,
  Wrench,
  X,
} from 'lucide-react';
import { useEffect, useMemo, useRef, useState } from 'react';
import type { BackendConnection, ColorTheme } from '../../shared/ipc';
import { AgentNode, type AgentNodeData } from './AgentNode';
import { LocalApi } from './api';
import type { CanvasEdge, CanvasNode, Dispatch, Provider, Snapshot } from './domain';
import { edgesBetweenVisibleNodes, visibleNodeIds, wouldCreateCycle } from './domain';
import { PixiGrid } from './PixiGrid';
import { TerminalPanel } from './TerminalPanel';

interface CanvasWorkspaceProps {
  readonly connection: BackendConnection;
  readonly initial: Snapshot;
  readonly theme: ColorTheme;
  readonly onError: (message: string) => void;
  readonly onOpenWorkspace: () => void;
  readonly surface: 'canvas' | 'teams';
  readonly onSurfaceChange: (surface: 'canvas' | 'teams') => void;
}

const nodeTypes = { agent: AgentNode };

interface RuntimeSession {
  readonly sessionId: string;
  readonly status: string;
  readonly integrationMode: string;
  readonly hookSessionId?: string;
}

interface NativeSubagentActivity {
  readonly id: string;
  readonly nodeId: string;
  readonly provider: string;
  readonly status: 'running' | 'stopped';
  readonly at: string;
}

type RailSection = 'workspace' | 'worktrees' | 'newAgent' | 'roles' | 'tools';

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
        teamName: team?.name ?? 'Unknown team',
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
  const [dialog, setDialog] = useState<'team' | 'worktree' | 'agent' | null>(null);
  const [selectedWorktree, setSelectedWorktree] = useState<string | null>(
    () => initial.worktrees[0]?.id ?? null,
  );
  const [managedTeamId, setManagedTeamId] = useState<string | null>(
    () => initial.teams[0]?.id ?? null,
  );
  const [agentRolePreset, setAgentRolePreset] = useState('');
  const [railSections, setRailSections] = useState<Record<RailSection, boolean>>({
    workspace: true,
    worktrees: true,
    newAgent: true,
    roles: false,
    tools: false,
  });
  const [selectedNode, setSelectedNode] = useState<string | null>(null);
  const [saveState, setSaveState] = useState<'saved' | 'saving' | 'error'>('saved');
  const [sessions, setSessions] = useState<Record<string, RuntimeSession>>({});
  const [dispatches, setDispatches] = useState<Dispatch[]>([]);
  const [nativeSubagents, setNativeSubagents] = useState<NativeSubagentActivity[]>([]);
  const hydrated = useRef(false);
  const selectedWorkspace = useRef(initial.workspaceId);

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

  const visibleIds = useMemo(
    () => visibleNodeIds(snapshot.nodes, activeWorktree?.teamId ?? null, selectedWorktree),
    [activeWorktree?.teamId, selectedWorktree, snapshot.nodes],
  );
  const displayedNodes = useMemo(
    () => nodes.map((node) => ({ ...node, hidden: !visibleIds.has(node.id) })),
    [nodes, visibleIds],
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
    void Promise.all([api.runtime(), api.dispatches()])
      .then(([runtime, activity]) => {
        setSessions(
          Object.fromEntries(
            runtime.nodes.map((node) => [
              node.nodeId,
              {
                sessionId: node.sessionId,
                status: node.status,
                integrationMode: node.integrationMode,
                ...(node.hookSessionId ? { hookSessionId: node.hookSessionId } : {}),
              },
            ]),
          ),
        );
        setDispatches(activity.dispatches);
        setNodes((current) =>
          current.map((node) => {
            const running = runtime.nodes.find((item) => item.nodeId === node.id);
            return running ? { ...node, data: { ...node.data, status: running.status } } : node;
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
            },
          }));
        }
        if (event.type === 'terminal.exited') {
          setSessions((current) =>
            Object.fromEntries(Object.entries(current).filter(([id]) => id !== event.entityId)),
          );
        }
        if (event.type === 'agent.output_preview' && typeof event.payload?.text === 'string') {
          setNodes((current) =>
            current.map((node) =>
              node.id === event.entityId
                ? { ...node, data: { ...node.data, preview: event.payload?.text as string } }
                : node,
            ),
          );
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
      socket?.close();
    };
  }, [connection]);

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
      const next = withLayout(snapshot, nodes, edges, viewport);
      void api
        .saveLayout(next)
        .then(() => setSaveState('saved'))
        .catch((reason: unknown) => {
          setSaveState('error');
          onError(reason instanceof Error ? reason.message : 'Layout save failed.');
        });
    }, 500);
    return () => window.clearTimeout(timer);
  }, [api, edges, nodes, onError, snapshot, viewport]);

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
    setNodes((current) => applyNodeChanges(changes, current));
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
      if (sessions[nodeId]) await api.stopNode(nodeId);
      const runtime = await api.startNode(nodeId);
      setSessions((current) => ({ ...current, [nodeId]: runtime }));
      setNodes((current) =>
        current.map((node) =>
          node.id === nodeId ? { ...node, data: { ...node.data, status: runtime.status } } : node,
        ),
      );
    } catch (reason) {
      onError(reason instanceof Error ? reason.message : 'Agent start failed.');
    }
  }

  async function stopNode(nodeId: string): Promise<void> {
    try {
      await api.stopNode(nodeId);
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
    if (
      selectedNode &&
      snapshot.nodes.find((node) => node.id === selectedNode)?.worktreeId !== worktreeId
    ) {
      setSelectedNode(null);
    }
  }

  function openAgent(role = ''): void {
    if (!activeWorktree) {
      onError('Create or select a Git worktree before creating an agent.');
      return;
    }
    setAgentRolePreset(role);
    setDialog('agent');
  }

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
                onClick={() => setDialog('worktree')}
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
                    <button
                      type="button"
                      key={worktree.id}
                      className={selectedWorktree === worktree.id ? 'active' : ''}
                      aria-pressed={selectedWorktree === worktree.id}
                      onClick={() => selectWorktree(worktree.id)}
                    >
                      <i style={{ background: team?.color ?? '#78817c' }} />
                      <span className="worktree-name">
                        <strong>{worktree.name}</strong>
                        <small>
                          TEAM: {team?.name ?? 'UNLINKED'} ·{' '}
                          {worktree.branch.length > 0 ? worktree.branch : 'branch pending'}
                        </small>
                      </span>
                      <small className="worktree-count">
                        {snapshot.nodes.filter((node) => node.worktreeId === worktree.id).length}
                      </small>
                    </button>
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
                disabled={!activeWorktree}
                onClick={() => openAgent()}
              >
                <Plus size={14} />
              </button>
            </div>
          </div>
          {railSections.newAgent && (
            <button
              type="button"
              className="quick-agent-button"
              disabled={!activeWorktree}
              onClick={() => openAgent()}
            >
              <Bot size={14} />
              <span>
                <strong>{activeWorktree ? 'Usar worktree ativo' : 'Selecione um worktree'}</strong>
                <small>
                  {activeWorktree
                    ? `TEAM: ${activeTeam?.name ?? 'UNLINKED'}`
                    : 'O canvas é definido por worktree'}
                </small>
              </span>
            </button>
          )}
        </section>

        <section className="rail-section rail-roles" aria-labelledby="roles-title">
          <div className="rail-section-heading">
            <span id="roles-title">ROLES</span>
            <RailCollapseToggle
              label="Roles"
              expanded={railSections.roles}
              onToggle={() => toggleRailSection('roles')}
            />
          </div>
          {railSections.roles && (
            <div className="role-list">
              {['DevOps', 'Frontend', 'Backend', 'QA / Tester', 'Code Reviewer'].map((role) => (
                <button
                  type="button"
                  key={role}
                  disabled={!activeWorktree}
                  onClick={() => openAgent(role)}
                >
                  <Bot size={12} /> {role}
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
              <button type="button" onClick={() => onSurfaceChange('teams')}>
                <Wrench size={12} /> Gerenciar Agent Teams
              </button>
            </>
          )}
        </section>
      </aside>

      <section className="canvas-stage">
        {surface === 'teams' ? (
          <TeamManagementPage
            snapshot={snapshot}
            managedTeamId={managedTeamId}
            onManagedTeamChange={setManagedTeamId}
            onCreateTeam={() => setDialog('team')}
            onBackToCanvas={() => onSurfaceChange('canvas')}
          />
        ) : (
          <>
            <PixiGrid viewport={viewport} theme={theme} />
            <ReactFlow
              nodes={displayedNodes}
              edges={displayedEdges}
              nodeTypes={nodeTypes}
              onNodesChange={nodeChanges}
              onEdgesChange={edgeChanges}
              onConnect={connectNodes}
              onNodeClick={(_event, node) => setSelectedNode(node.id)}
              onNodeDoubleClick={(_event, node) => void startNode(node.id)}
              onPaneClick={() => setSelectedNode(null)}
              onMoveEnd={(_event, nextViewport) => setViewport(nextViewport)}
              defaultViewport={initial.viewport}
              minZoom={0.25}
              maxZoom={1.8}
              onlyRenderVisibleElements
              fitView={initial.nodes.length === 0}
              proOptions={{ hideAttribution: true }}
            />
            <div className="canvas-toolbar">
              <span>{visibleIds.size} NODES</span>
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
                    <dt>Status</dt>
                    <dd>{runtime?.status ?? 'Idle'}</dd>
                  </div>
                  <div>
                    <dt>Auto start</dt>
                    <dd>{node.autoStart ? 'YES' : 'NO'}</dd>
                  </div>
                  <div>
                    <dt>Integration</dt>
                    <dd>{runtime?.integrationMode ?? 'OFFLINE'}</dd>
                  </div>
                </dl>
                <div className="inspector-actions">
                  <button type="button" onClick={() => void startNode(node.id)}>
                    {runtime ? 'Restart' : 'Start'}
                  </button>
                  {runtime && (
                    <button type="button" onClick={() => void stopNode(node.id)}>
                      Stop
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

      {selectedNode && sessions[selectedNode] && (
        <section className="node-terminal">
          <div className="terminal-header">
            <span>{snapshot.nodes.find((node) => node.id === selectedNode)?.label} / CONPTY</span>
            <button type="button" onClick={() => setSelectedNode(null)}>
              <X size={13} />
            </button>
          </div>
          <TerminalPanel connection={connection} sessionId={sessions[selectedNode].sessionId} />
        </section>
      )}

      {dialog && (
        <EditorDialog
          kind={dialog}
          snapshot={snapshot}
          activeWorktree={activeWorktree}
          activeTeam={activeTeam}
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
                    createInitialWorktree?: boolean;
                  },
                );
              } else if (dialog === 'worktree') {
                const created = await api.createWorktree(
                  payload as { teamId: string; name: string },
                );
                const next = await refresh();
                setSelectedWorktree(
                  next.worktrees.find((item) => item.id === created.id)?.id ?? created.id,
                );
                setDialog(null);
                return;
              } else {
                if (!activeWorktree)
                  throw new Error('Select a Git worktree before creating an agent.');
                await api.createNode(
                  payload as {
                    label: string;
                    role: string;
                    provider: Provider;
                  } & { teamId: string; worktreeId: string },
                );
              }
              const next = await refresh();
              if (dialog === 'team') {
                const createdTeam = next.teams.at(-1);
                setManagedTeamId(createdTeam?.id ?? managedTeamId);
              }
              setDialog(null);
            } catch (reason) {
              onError(reason instanceof Error ? reason.message : 'Operation failed.');
            }
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

function TeamManagementPage({
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

interface EditorDialogProps {
  readonly kind: 'team' | 'worktree' | 'agent';
  readonly snapshot: Snapshot;
  readonly activeWorktree: Snapshot['worktrees'][number] | null;
  readonly activeTeam: Snapshot['teams'][number] | null;
  readonly agentRolePreset: string;
  readonly onClose: () => void;
  readonly onSubmit: (payload: Record<string, unknown>) => Promise<void>;
}

function EditorDialog({
  kind,
  snapshot,
  activeWorktree,
  activeTeam,
  agentRolePreset,
  onClose,
  onSubmit,
}: EditorDialogProps): React.JSX.Element {
  const [busy, setBusy] = useState(false);
  const [teamId, setTeamId] = useState(activeTeam?.id ?? snapshot.teams[0]?.id ?? '');
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
          if (kind === 'team') data.createInitialWorktree = data.createInitialWorktree === 'on';
          void onSubmit(data).finally(() => setBusy(false));
        }}
      >
        <header>
          <span>
            {kind === 'team' ? 'NEW TEAM' : kind === 'worktree' ? 'NEW GIT WORKTREE' : 'NEW AGENT'}
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
              <select name="orchestratorProvider">
                <option value="claude">Claude Code</option>
                <option value="codex">Codex</option>
              </select>
            </label>
            <label className="dialog-checkbox">
              <input name="createInitialWorktree" type="checkbox" defaultChecked />
              Create the initial Git worktree
            </label>
          </>
        ) : kind === 'worktree' ? (
          <>
            <label>
              Team
              <select
                name="teamId"
                required
                value={teamId}
                onChange={(event) => setTeamId(event.target.value)}
              >
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
          </>
        ) : (
          <>
            <div className="agent-worktree-context">
              <span>ACTIVE GIT WORKTREE</span>
              <strong>{activeWorktree?.name ?? 'NONE'}</strong>
              <small>TEAM: {activeTeam?.name ?? 'UNLINKED'}</small>
              <input name="teamId" type="hidden" value={activeWorktree?.teamId ?? ''} />
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
              <select name="provider">
                <option value="codex">Codex</option>
                <option value="claude">Claude Code</option>
              </select>
            </label>
          </>
        )}
        <button className="dialog-submit" type="submit" disabled={busy}>
          {busy ? 'CREATING…' : `CREATE ${kind.toUpperCase()}`}
        </button>
      </form>
    </div>
  );
}
