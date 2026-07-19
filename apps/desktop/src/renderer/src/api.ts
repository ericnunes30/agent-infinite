import type { BackendConnection } from '../../shared/ipc';
import type {
  CanvasNode,
  CapabilityItem,
  Dispatch,
  Provider,
  Snapshot,
  TeamExecution,
  TeamTemplate,
  RoleProfile,
  ModelInventory,
  Worktree,
} from './domain';

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
    orchestratorModel?: string;
  }): Promise<void> {
    await this.request('/api/teams', { method: 'POST', body: JSON.stringify(input) });
  }

  public async teamTemplates(): Promise<{ templates: TeamTemplate[] }> {
    return this.request('/api/team-templates');
  }

  public async saveTeamTemplate(input: {
    teamId: string;
    name?: string;
    description?: string;
  }): Promise<TeamTemplate> {
    return this.request('/api/team-templates', { method: 'POST', body: JSON.stringify(input) });
  }

  public async createTeamTemplate(input: {
    name: string;
    description?: string;
    color: string;
    orchestratorProvider: Provider;
    orchestratorModel?: string;
  }): Promise<TeamTemplate> {
    return this.request('/api/team-templates', { method: 'POST', body: JSON.stringify(input) });
  }

  public async updateTeamTemplate(template: TeamTemplate): Promise<TeamTemplate> {
    return this.request(`/api/team-templates/${encodeURIComponent(template.id)}`, {
      method: 'PATCH',
      body: JSON.stringify({
        name: template.name,
        description: template.description ?? '',
        color: template.color,
        orchestratorProvider: template.orchestratorProvider,
        orchestratorModel: template.orchestratorModel ?? '',
        nodes: template.nodes,
        edges: template.edges,
      }),
    });
  }

  public async deleteTeamTemplate(templateId: string): Promise<void> {
    await this.request(`/api/team-templates/${encodeURIComponent(templateId)}`, {
      method: 'DELETE',
    });
  }

  public async applyTeamTemplate(
    templateId: string,
    name?: string,
  ): Promise<{ team: Snapshot['teams'][number] }> {
    return this.request(`/api/team-templates/${encodeURIComponent(templateId)}/apply`, {
      method: 'POST',
      body: JSON.stringify({ name }),
    });
  }

  public async runTeam(teamId: string, worktreeId: string): Promise<TeamExecution> {
    return this.request(`/api/teams/${encodeURIComponent(teamId)}/run`, {
      method: 'POST',
      body: JSON.stringify({ worktreeId }),
    });
  }

  public async extractTeamWorkflow(teamId: string, worktreeId: string): Promise<void> {
    await this.request(`/api/teams/${encodeURIComponent(teamId)}/extract`, {
      method: 'POST',
      body: JSON.stringify({ worktreeId }),
    });
  }

  public async createNode(input: {
    teamId: string;
    worktreeId?: string;
    label: string;
    role: string;
    provider: Provider;
    model?: string;
    roleProfileId?: string;
    mcpIds?: string[];
    skillIds?: string[];
  }): Promise<void> {
    await this.request('/api/nodes', { method: 'POST', body: JSON.stringify(input) });
  }

  public async updateNode(
    nodeId: string,
    input: Partial<
      Pick<CanvasNode, 'label' | 'role' | 'provider' | 'model' | 'autoStart' | 'roleProfileId'>
    > & {
      mcpIds?: string[];
      skillIds?: string[];
    },
  ): Promise<CanvasNode> {
    return this.request(`/api/nodes/${encodeURIComponent(nodeId)}`, {
      method: 'PATCH',
      body: JSON.stringify(input),
    });
  }

  public async deleteNode(nodeId: string): Promise<void> {
    await this.request(`/api/nodes/${encodeURIComponent(nodeId)}`, { method: 'DELETE' });
  }

  public async capabilityInventory(): Promise<{ items: CapabilityItem[] }> {
    return this.request('/api/tools/inventory');
  }

  public async modelInventory(): Promise<ModelInventory> {
    return this.request('/api/models/inventory');
  }

  public async scanModels(provider?: Provider): Promise<ModelInventory> {
    return this.request('/api/models/scan', {
      method: 'POST',
      body: JSON.stringify(provider ? { provider } : {}),
    });
  }

  public async scanCapabilities(): Promise<{
    items: CapabilityItem[];
    scanErrors: Record<string, string>;
    scannedAt: string;
  }> {
    return this.request('/api/tools/scan', { method: 'POST' });
  }

  public async setCapabilityPolicy(
    id: string,
    kind: CapabilityItem['kind'],
    policy: CapabilityItem['policy'],
  ): Promise<CapabilityItem> {
    return this.request(
      `/api/tools/${kind === 'mcp' ? 'mcp-servers' : 'skills'}/${encodeURIComponent(id)}/policy`,
      {
        method: 'PATCH',
        body: JSON.stringify({ policy }),
      },
    );
  }

  public async setCapabilityPolicies(
    ids: string[],
    policy: CapabilityItem['policy'],
  ): Promise<{ items: CapabilityItem[] }> {
    return this.request('/api/tools/policies', {
      method: 'PATCH',
      body: JSON.stringify({ ids, policy }),
    });
  }

  public async saveManagedMCP(input: {
    id?: string;
    name: string;
    description?: string;
    provider: Provider | 'all';
    spec: Record<string, unknown>;
    secrets?: Record<string, string>;
  }): Promise<CapabilityItem> {
    const path = input.id
      ? `/api/tools/mcp-servers/${encodeURIComponent(input.id)}`
      : '/api/tools/mcp-servers';
    return this.request(path, { method: input.id ? 'PATCH' : 'POST', body: JSON.stringify(input) });
  }

  public async saveManagedSkill(input: {
    id?: string;
    name: string;
    description?: string;
    provider: Provider | 'all';
    markdown: string;
  }): Promise<CapabilityItem> {
    const path = input.id
      ? `/api/tools/skills/${encodeURIComponent(input.id)}`
      : '/api/tools/skills';
    return this.request(path, { method: input.id ? 'PATCH' : 'POST', body: JSON.stringify(input) });
  }

  public async managedSkillContent(id: string): Promise<{ markdown: string }> {
    return this.request(`/api/tools/skills/${encodeURIComponent(id)}`);
  }

  public async archiveCapability(id: string, kind: CapabilityItem['kind']): Promise<void> {
    await this.request(
      `/api/tools/${kind === 'mcp' ? 'mcp-servers' : 'skills'}/${encodeURIComponent(id)}`,
      { method: 'DELETE' },
    );
  }

  public async promoteCapability(
    id: string,
    kind: CapabilityItem['kind'],
    secrets: Record<string, string> = {},
  ): Promise<CapabilityItem> {
    return this.request(
      `/api/tools/${kind === 'mcp' ? 'mcp-servers' : 'skills'}/${encodeURIComponent(id)}/promote`,
      { method: 'POST', body: JSON.stringify({ secrets }) },
    );
  }

  public async testMCP(
    id: string,
  ): Promise<{ ok: boolean; transport: string; toolCount: number; tools: string[] }> {
    return this.request(`/api/tools/mcp-servers/${encodeURIComponent(id)}/test`, {
      method: 'POST',
    });
  }

  public async roleProfiles(): Promise<{ roles: RoleProfile[] }> {
    return this.request('/api/role-profiles');
  }

  public async saveRoleProfile(
    role: Omit<RoleProfile, 'id'> & { id?: string },
  ): Promise<RoleProfile> {
    const path = role.id
      ? `/api/role-profiles/${encodeURIComponent(role.id)}`
      : '/api/role-profiles';
    return this.request(path, { method: role.id ? 'PATCH' : 'POST', body: JSON.stringify(role) });
  }

  public async deleteRoleProfile(id: string): Promise<void> {
    await this.request(`/api/role-profiles/${encodeURIComponent(id)}`, { method: 'DELETE' });
  }

  public async createWorktree(input: {
    teamId?: string;
    name: string;
    baseRef?: string;
    newBranch?: string;
    existingBranch?: string;
  }): Promise<Worktree> {
    return this.request('/api/worktrees', { method: 'POST', body: JSON.stringify(input) });
  }

  public async gitBranches(): Promise<{ all: string[]; available: string[] }> {
    return this.request('/api/git/branches');
  }

  public async importNodeToWorktree(worktreeId: string, nodeId: string): Promise<CanvasNode> {
    return this.request(`/api/worktrees/${encodeURIComponent(worktreeId)}/nodes/import`, {
      method: 'POST',
      body: JSON.stringify({ nodeId }),
    });
  }

  public async importTemplateToWorktree(
    worktreeId: string,
    templateId: string,
  ): Promise<{ teamId: string; worktreeId: string; nodeIds: string[] }> {
    return this.request(
      `/api/worktrees/${encodeURIComponent(worktreeId)}/templates/${encodeURIComponent(templateId)}/import`,
      { method: 'POST' },
    );
  }

  public async importTeamToWorktree(
    worktreeId: string,
    teamId: string,
  ): Promise<{ teamId: string; worktreeId: string; nodeIds: string[] }> {
    return this.request(
      `/api/worktrees/${encodeURIComponent(worktreeId)}/teams/${encodeURIComponent(teamId)}/import`,
      { method: 'POST' },
    );
  }

  public async deleteWorktree(worktreeId: string): Promise<void> {
    await this.request(`/api/worktrees/${encodeURIComponent(worktreeId)}`, { method: 'DELETE' });
  }

  public async deleteTeam(teamId: string): Promise<void> {
    await this.request(`/api/teams/${encodeURIComponent(teamId)}`, { method: 'DELETE' });
  }

  public async createCustomRole(name: string): Promise<{ roles: string[] }> {
    return this.request('/api/roles', { method: 'POST', body: JSON.stringify({ name }) });
  }

  public async deleteCustomRole(name: string): Promise<void> {
    await this.request(`/api/roles/${encodeURIComponent(name)}`, { method: 'DELETE' });
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
    mcpConnected: boolean;
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
      mcpConnected: boolean;
      preview?: string;
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
