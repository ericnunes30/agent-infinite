export type Provider = 'claude' | 'codex' | 'pi' | 'opencode';
export type NodeKind = 'orchestrator' | 'agent';

export interface Team {
  readonly id: string;
  readonly name: string;
  readonly color: string;
  readonly branch: string;
  readonly baseRef: string;
  readonly createdAt: string;
}

export interface Worktree {
  readonly id: string;
  readonly teamId?: string;
  readonly name: string;
  readonly branch: string;
  readonly baseRef: string;
  readonly createdAt: string;
}

export interface CanvasNode {
  readonly id: string;
  readonly kind: NodeKind;
  readonly provider: Provider;
  readonly model?: string;
  readonly teamId: string;
  readonly worktreeId?: string;
  readonly label: string;
  readonly role: string;
  readonly roleProfileId?: string;
  readonly mcpIds?: readonly string[];
  readonly skillIds?: readonly string[];
  readonly autoStart: boolean;
  readonly position: { readonly x: number; readonly y: number };
  readonly size: { readonly width: number; readonly height: number };
}

export interface RoleProfile {
  readonly id: string;
  readonly name: string;
  readonly defaultProvider?: Provider;
  readonly model?: string;
  readonly mcpIds: readonly string[];
  readonly skillIds: readonly string[];
  readonly builtin?: boolean;
}

export type CapabilityKind = 'mcp' | 'skill';
export type CapabilityPolicy = 'provider_default' | 'curated' | 'blocked';
export type CapabilityStatus = 'new' | 'unchanged' | 'changed' | 'missing' | 'scan_error';

export interface CapabilityItem {
  readonly id: string;
  readonly kind: CapabilityKind;
  readonly name: string;
  readonly description?: string;
  readonly origin: 'managed' | 'external' | 'internal';
  readonly provider: Provider | 'all';
  readonly scope: 'user' | 'project' | 'plugin' | 'app' | 'session';
  readonly sourcePath?: string;
  readonly nativeKey?: string;
  readonly fingerprint: string;
  readonly groupId?: string;
  readonly status: CapabilityStatus;
  readonly policy: CapabilityPolicy;
  readonly enforceable: boolean;
  readonly spec?: Readonly<Record<string, unknown>>;
  readonly secretNames?: readonly string[];
  readonly skillPath?: string;
  readonly estimatedTokens?: number;
  readonly metadataTokens?: number;
  readonly contentTokens?: number;
  readonly toolCount?: number;
  readonly firstSeenAt: string;
  readonly lastSeenAt: string;
  readonly archived?: boolean;
  readonly changes?: readonly string[];
}

export function isCapabilityCompatible(item: CapabilityItem, provider: Provider): boolean {
  if (item.provider === 'all' || item.provider === provider) return true;
  if (item.kind === 'skill') return true;
  if (provider !== 'pi') return true;
  const type = typeof item.spec?.type === 'string' ? item.spec.type : '';
  return typeof item.spec?.url === 'string' || type === 'remote' || type === 'http';
}

export function isCapabilityAvailable(item: CapabilityItem): boolean {
  return !item.archived && item.status !== 'missing' && item.status !== 'scan_error';
}

export interface CapabilityGroup {
  readonly item: CapabilityItem;
  readonly ids: readonly string[];
}

export function groupCapabilityItems(
  items: readonly CapabilityItem[],
  preferredIds: readonly string[] = [],
): CapabilityGroup[] {
  const preferred = new Set(preferredIds);
  const groups = new Map<string, { item: CapabilityItem; ids: string[] }>();
  for (const item of items) {
    const key = item.groupId?.trim() ? item.groupId : item.id;
    const existing = groups.get(key);
    if (!existing) {
      groups.set(key, { item, ids: [item.id] });
      continue;
    }
    existing.ids.push(item.id);
    if (preferred.has(item.id) && !preferred.has(existing.item.id)) existing.item = item;
  }
  return [...groups.values()];
}

export interface CanvasEdge {
  readonly id: string;
  readonly source: string;
  readonly target: string;
  readonly type: 'delegates_to';
}

export interface Snapshot {
  readonly schemaVersion: 1;
  readonly workspaceId: string;
  readonly workspacePath: string;
  readonly teams: readonly Team[];
  readonly worktrees: readonly Worktree[];
  readonly nodes: readonly CanvasNode[];
  readonly edges: readonly CanvasEdge[];
  readonly customRoles: readonly string[];
  readonly roleProfiles: readonly RoleProfile[];
  readonly viewport: { readonly x: number; readonly y: number; readonly zoom: number };
  readonly integration: { readonly hooks: 'auto' | 'off' | 'required' };
}

export interface TeamTemplate {
  readonly id: string;
  readonly name: string;
  readonly description?: string;
  readonly color: string;
  readonly orchestratorProvider: Provider;
  readonly orchestratorModel?: string;
  readonly nodes: readonly CanvasNode[];
  readonly edges: readonly CanvasEdge[];
  readonly createdAt: string;
}

export type ModelAvailability = 'available' | 'missing' | 'unverified';
export type ModelScanStatus = 'ok' | 'stale' | 'scan_error';

export interface ProviderModel {
  readonly id: string;
  readonly displayName?: string;
  readonly source: string;
  readonly status: ModelAvailability;
  readonly isDefault?: boolean;
}

export interface ProviderModelCatalog {
  readonly provider: Provider;
  readonly cliVersion?: string;
  readonly defaultModel?: string;
  readonly defaultSource?: string;
  readonly status: ModelScanStatus;
  readonly error?: string;
  readonly scannedAt?: string;
  readonly models: readonly ProviderModel[];
}

export interface ModelInventory {
  readonly providers: readonly ProviderModelCatalog[];
  readonly scannedAt?: string;
}

export interface TeamExecution {
  readonly teamId: string;
  readonly worktreeId: string;
  readonly startedAt: string;
  readonly startedNodeIds: readonly string[];
}

export function initialTeamId(teams: readonly Team[]): string | null {
  return teams[0]?.id ?? null;
}

export function initialWorktreeId(
  worktrees: readonly Worktree[],
  teamId: string | null,
): string | null {
  return worktrees.find((worktree) => worktree.teamId === teamId)?.id ?? null;
}

export type DispatchStatus =
  'created' | 'queued' | 'delivered' | 'running' | 'done' | 'blocked' | 'failed' | 'canceled';

export interface Dispatch {
  readonly dispatch_id: string;
  readonly source_node_id: string;
  readonly source_label: string;
  readonly target_node_id: string;
  readonly target_label: string;
  readonly task: string;
  readonly status: DispatchStatus;
  readonly created_at: string;
  readonly updated_at: string;
  readonly completed_at?: string;
  readonly result: { readonly status: DispatchStatus; readonly error?: string };
  readonly delivery_confirmed_by?: 'hook' | 'detector';
}

export function wouldCreateCycle(
  edges: readonly CanvasEdge[],
  source: string,
  target: string,
): boolean {
  if (source === target) return true;
  const adjacency = new Map<string, string[]>();
  for (const edge of edges) {
    const targets = adjacency.get(edge.source) ?? [];
    targets.push(edge.target);
    adjacency.set(edge.source, targets);
  }
  const stack = [target];
  const seen = new Set<string>();
  while (stack.length > 0) {
    const node = stack.pop();
    if (!node || seen.has(node)) continue;
    if (node === source) return true;
    seen.add(node);
    stack.push(...(adjacency.get(node) ?? []));
  }
  return false;
}

export function visibleNodeIds(
  nodes: readonly CanvasNode[],
  selectedTeam: string | null,
  selectedWorktree: string | null = null,
): ReadonlySet<string> {
  return new Set(
    nodes
      .filter(
        (node) =>
          (selectedTeam === null || node.teamId === selectedTeam) &&
          (selectedWorktree === null || node.worktreeId === selectedWorktree),
      )
      .map((node) => node.id),
  );
}

export function edgesBetweenVisibleNodes(
  edges: readonly CanvasEdge[],
  nodeIds: ReadonlySet<string>,
): CanvasEdge[] {
  return edges.filter((edge) => nodeIds.has(edge.source) && nodeIds.has(edge.target));
}
