import type { BackendConnection } from '../../shared/ipc';
import type { Dispatch, Provider, Snapshot, Worktree } from './domain';

export class LocalApi {
  public constructor(private readonly connection: BackendConnection) {}

  public async openWorkspace(path: string): Promise<Snapshot> {
    return this.request('/api/workspaces/open', { method: 'POST', body: JSON.stringify({ path }) });
  }

  public async snapshot(): Promise<Snapshot> {
    return this.request('/api/snapshot');
  }

  public async createTeam(input: {
    name: string;
    color: string;
    orchestratorProvider: Provider;
    createInitialWorktree?: boolean;
  }): Promise<void> {
    await this.request('/api/teams', { method: 'POST', body: JSON.stringify(input) });
  }

  public async createNode(input: {
    teamId: string;
    worktreeId?: string;
    label: string;
    role: string;
    provider: Provider;
  }): Promise<void> {
    await this.request('/api/nodes', { method: 'POST', body: JSON.stringify(input) });
  }

  public async createWorktree(input: { teamId: string; name: string }): Promise<Worktree> {
    return this.request('/api/worktrees', { method: 'POST', body: JSON.stringify(input) });
  }

  public async deleteWorktree(worktreeId: string): Promise<void> {
    await this.request(`/api/worktrees/${encodeURIComponent(worktreeId)}`, { method: 'DELETE' });
  }

  public async saveLayout(snapshot: Snapshot): Promise<Snapshot> {
    return this.request('/api/canvas/layout', {
      method: 'PUT',
      body: JSON.stringify({
        nodes: snapshot.nodes,
        edges: snapshot.edges,
        viewport: snapshot.viewport,
      }),
    });
  }

  public async setHookPolicy(hooks: 'auto' | 'off' | 'required'): Promise<Snapshot> {
    return this.request('/api/workspaces/integration', {
      method: 'PATCH',
      body: JSON.stringify({ hooks }),
    });
  }

  public async startNode(nodeId: string): Promise<{
    sessionId: string;
    status: string;
    integrationMode: string;
    hookSessionId?: string;
  }> {
    return this.request(`/api/nodes/${encodeURIComponent(nodeId)}/start`, { method: 'POST' });
  }

  public async runtime(): Promise<{
    nodes: {
      nodeId: string;
      sessionId: string;
      status: string;
      integrationMode: string;
      hookSessionId?: string;
    }[];
  }> {
    return this.request('/api/runtime');
  }

  public async dispatches(): Promise<{ dispatches: Dispatch[] }> {
    return this.request('/api/dispatches');
  }

  public async stopNode(nodeId: string): Promise<void> {
    await this.request(`/api/nodes/${encodeURIComponent(nodeId)}/stop`, { method: 'POST' });
  }

  private async request<T>(path: string, init?: RequestInit): Promise<T> {
    const headers = new Headers(init?.headers);
    headers.set('Authorization', `Bearer ${this.connection.token}`);
    if (init?.body) headers.set('Content-Type', 'application/json');
    const response = await fetch(`${this.connection.baseUrl}${path}`, {
      ...init,
      headers,
    });
    const body =
      response.status === 204 ? undefined : ((await response.json()) as T | { message?: string });
    if (!response.ok)
      throw new Error(
        (body as { message?: string } | undefined)?.message ??
          `Request failed (${response.status.toString()}).`,
      );
    return body as T;
  }
}
