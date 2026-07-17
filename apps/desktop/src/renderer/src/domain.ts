export type Provider = 'claude' | 'codex';
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
  readonly teamId: string;
  readonly name: string;
  readonly branch: string;
  readonly baseRef: string;
  readonly createdAt: string;
}

export interface CanvasNode {
  readonly id: string;
  readonly kind: NodeKind;
  readonly provider: Provider;
  readonly teamId: string;
  readonly worktreeId?: string;
  readonly label: string;
  readonly role: string;
  readonly autoStart: boolean;
  readonly position: { readonly x: number; readonly y: number };
  readonly size: { readonly width: number; readonly height: number };
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
  readonly viewport: { readonly x: number; readonly y: number; readonly zoom: number };
  readonly integration: { readonly hooks: 'auto' | 'off' | 'required' };
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
