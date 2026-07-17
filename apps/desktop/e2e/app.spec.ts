import { _electron as electron, expect, test, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

interface Connection {
  baseUrl: string;
  token: string;
}

interface TeamResponse {
  team: { id: string };
  orchestrator: { id: string };
}

interface WorktreeResponse {
  id: string;
  teamId: string;
  name: string;
  branch: string;
}

interface Snapshot {
  nodes: unknown[];
  viewport: unknown;
}

interface RuntimeEvent {
  type: string;
  entityId: string;
  payload?: { status?: string };
}

test('mock agents dispatch, transition Working to Done, and persist across reload', async () => {
  const root = mkdtempSync(join(tmpdir(), 'agent-infinite-e2e-'));
  const repository = join(root, 'repo');
  const localAppData = join(root, 'local-app-data');
  mkdirSync(repository);
  mkdirSync(localAppData);
  git(repository, 'init', '-b', 'main');
  git(repository, 'config', 'user.email', 'e2e@agent-infinite.local');
  git(repository, 'config', 'user.name', 'Agent Infinite E2E');
  writeFileSync(join(repository, 'README.md'), '# e2e\n');
  git(repository, 'add', 'README.md');
  git(repository, 'commit', '-m', 'initial');

  const launchEnvironment = { ...process.env };
  delete launchEnvironment.ELECTRON_RUN_AS_NODE;
  const application = await electron.launch({
    args: [resolve('.')],
    cwd: resolve('.'),
    env: {
      ...launchEnvironment,
      AGENT_INFINITE_TEST_MODE: '1',
      LOCALAPPDATA: localAppData,
      ELECTRON_DISABLE_SECURITY_WARNINGS: 'true',
    },
  });
  try {
    const page = await application.firstWindow();
    await page.evaluate(() => localStorage.removeItem('agent-infinite:last-workspace'));
    await page.reload();
    await page.waitForLoadState('domcontentloaded');
    await page.evaluate(() => localStorage.setItem('agent-infinite:theme:v1', 'dark'));
    await page.reload();
    await page.waitForLoadState('domcontentloaded');
    await expect(page.getByText(/runtime 0\.5\.0/)).toBeVisible();
    await page.getByRole('button', { name: 'Ativar tema claro' }).click();
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
    await page.reload();
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
    await page.evaluate(
      (path) => localStorage.setItem('agent-infinite:last-workspace', path),
      repository,
    );
    await page.reload();
    await expect(page.locator('.canvas-stage')).toBeVisible();

    const connection = await backendConnection(page);
    const sourceTeam = await api<TeamResponse>(connection, 'POST', '/api/teams', {
      name: 'Mock Control',
      color: '#b7f34a',
      orchestratorProvider: 'mock',
    });
    const targetTeam = await api<TeamResponse>(connection, 'POST', '/api/teams', {
      name: 'Mock Delivery',
      color: '#64d8ff',
      orchestratorProvider: 'mock',
    });
    const reviewWorktree = await api<WorktreeResponse>(connection, 'POST', '/api/worktrees', {
      teamId: sourceTeam.team.id,
      name: 'Mock Control Review',
    });
    expect(reviewWorktree.teamId).toBe(sourceTeam.team.id);
    const reviewAgent = await api<{ id: string }>(connection, 'POST', '/api/nodes', {
      teamId: sourceTeam.team.id,
      worktreeId: reviewWorktree.id,
      label: 'Review Worker',
      role: 'Runs on the linked review branch',
      provider: 'mock',
      autoStart: false,
    });
    const target = await api<{ id: string }>(connection, 'POST', '/api/nodes', {
      teamId: targetTeam.team.id,
      label: 'Deterministic Worker',
      role: 'Returns fixture output',
      provider: 'mock',
      autoStart: true,
    });
    await Promise.all(
      Array.from({ length: 47 }, async (_, index) =>
        api(connection, 'POST', '/api/nodes', {
          teamId: targetTeam.team.id,
          label: `Fixture Agent ${String(index + 1).padStart(2, '0')}`,
          role: 'Canvas load fixture',
          provider: 'mock',
          autoStart: false,
        }),
      ),
    );
    const snapshot = await api<Snapshot>(connection, 'GET', '/api/snapshot');
    await api(connection, 'PUT', '/api/canvas/layout', {
      nodes: snapshot.nodes,
      edges: [
        {
          id: 'e2e-delegation',
          source: sourceTeam.orchestrator.id,
          target: target.id,
          type: 'delegates_to',
        },
      ],
      viewport: snapshot.viewport,
    });

    await page.reload();
    const worktreeRail = page.locator('section[aria-labelledby="worktrees-title"]');
    await expect(page.getByText('GIT WORKTREES')).toBeVisible();
    await expect(worktreeRail.getByRole('button', { name: /Mock Control Review/ })).toBeVisible();
    await expect(worktreeRail.getByRole('button', { name: /Mock Delivery/ })).toBeVisible();
    await expect(page.getByLabel('WORKSPACE')).toBeVisible();
    await expect(page.getByText('1 NODES', { exact: true })).toBeVisible();
    await expect(page.locator('.worktree-list button').first()).toHaveAttribute(
      'aria-pressed',
      'true',
    );
    await expect(page.locator('.react-flow__edge')).toHaveCount(0);

    await worktreeRail.getByRole('button', { name: /Mock Delivery/ }).click();
    await expect(page.getByRole('heading', { name: 'Deterministic Worker' })).toBeVisible();
    await expect(page.getByText('49 NODES', { exact: true })).toBeVisible();
    await expect(worktreeRail.getByText(/TEAM: Mock Delivery/)).toBeVisible();
    await expect(worktreeRail.getByRole('button', { name: /Mock Control Review/ })).toBeVisible();
    await expect(page.locator('.react-flow__edge')).toHaveCount(0);
    const linkedSnapshot = await api<{ nodes: { id: string; worktreeId?: string }[] }>(
      connection,
      'GET',
      '/api/snapshot',
    );
    expect(linkedSnapshot.nodes.find((node) => node.id === reviewAgent.id)?.worktreeId).toBe(
      reviewWorktree.id,
    );

    await worktreeRail.locator('button').filter({ hasText: 'Mock Control' }).first().click();
    await expect(page.getByText('1 NODES', { exact: true })).toBeVisible();

    await page.locator('.surface-toggle').click();
    await expect(page.getByRole('heading', { name: 'Agent Teams' })).toBeVisible();
    const teamsPage = page.locator('.teams-page');
    await teamsPage.getByRole('button', { name: /Mock Delivery/ }).click();
    await teamsPage.getByRole('button', { name: 'Back to canvas' }).click();
    await expect(page.getByText('1 NODES', { exact: true })).toBeVisible();

    const sourceNode = page.locator('.agent-node').filter({ hasText: 'Mock Control Lead' });
    const sourceHandle = sourceNode.locator('.react-flow__handle-right');
    const [nodeBox, handleBox] = await Promise.all([
      sourceNode.boundingBox(),
      sourceHandle.boundingBox(),
    ]);
    expect(nodeBox).not.toBeNull();
    expect(handleBox).not.toBeNull();
    if (nodeBox && handleBox) {
      expect(
        Math.abs(nodeBox.x + nodeBox.width - (handleBox.x + handleBox.width / 2)),
      ).toBeLessThan(2);
    }

    await collectEvents(page, connection);
    await api(connection, 'POST', `/api/nodes/${sourceTeam.orchestrator.id}/start`);
    // Reopening the workspace on reload auto-started the target.
    await expect
      .poll(async () =>
        (await api<{ nodes: { nodeId: string }[] }>(connection, 'GET', '/api/runtime')).nodes.some(
          (item) => item.nodeId === target.id,
        ),
      )
      .toBe(true);
    await expect
      .poll(async () => {
        const runtime = await api<{
          nodes: { nodeId: string; integrationMode: string }[];
        }>(connection, 'GET', '/api/runtime');
        return runtime.nodes.find((item) => item.nodeId === target.id)?.integrationMode;
      })
      .toBe('hooks');

    // The canvas autosave may have persisted its pre-fixture edge state while the UI was reloading.
    // Reassert the server-side delegation fixture immediately before exercising MCP routing.
    const dispatchSnapshot = await api<Snapshot & { edges: unknown[] }>(
      connection,
      'GET',
      '/api/snapshot',
    );
    await api(connection, 'PUT', '/api/canvas/layout', {
      nodes: dispatchSnapshot.nodes,
      edges: [
        {
          id: 'e2e-delegation',
          source: sourceTeam.orchestrator.id,
          target: target.id,
          type: 'delegates_to',
        },
      ],
      viewport: dispatchSnapshot.viewport,
    });

    const mcpDispatch = await dispatchThroughMCP(
      connection,
      sourceTeam.orchestrator.id,
      'Deterministic Worker',
      'E2E_DISPATCH',
    );
    await page.waitForFunction((targetID) => {
      const events = (window as unknown as { __agentInfiniteEvents: RuntimeEvent[] })
        .__agentInfiniteEvents;
      return (
        events.some(
          (event) => event.entityId === targetID && event.payload?.status === 'Working',
        ) && events.some((event) => event.entityId === targetID && event.payload?.status === 'Done')
      );
    }, target.id);
    const dispatchResult = await getDispatchResultThroughMCP(mcpDispatch);
    expect(dispatchResult).toContain('E2E_DISPATCH');
    await expect(page.getByLabel('Dispatch activity').getByText('DONE')).toBeVisible();

    await page.reload();
    const recoveredWorktreeRail = page.locator('section[aria-labelledby="worktrees-title"]');
    await expect(
      recoveredWorktreeRail.locator('button').filter({ hasText: 'Mock Control' }).first(),
    ).toBeVisible();
    await expect(
      recoveredWorktreeRail.getByRole('button', { name: /Mock Delivery/ }),
    ).toBeVisible();
    await recoveredWorktreeRail.getByRole('button', { name: /Mock Delivery/ }).click();
    await expect(page.getByRole('heading', { name: 'Deterministic Worker' })).toBeVisible();

    await api(connection, 'PATCH', '/api/workspaces/integration', { hooks: 'off' });

    await page.evaluate(() => window.agentInfinite.restartBackend());
    await expect
      .poll(async () => {
        const next = await backendConnection(page).catch(() => null);
        return next?.baseUrl === connection.baseUrl ? null : next;
      })
      .not.toBeNull();
    const recoveredConnection = await backendConnection(page);
    await expect(page.locator('.canvas-stage')).toBeVisible();
    await expect
      .poll(async () =>
        (
          await api<{ nodes: { nodeId: string }[] }>(recoveredConnection, 'GET', '/api/runtime')
        ).nodes.some((item) => item.nodeId === target.id),
      )
      .toBe(true);
    await expect
      .poll(async () => {
        const runtime = await api<{
          nodes: { nodeId: string; integrationMode: string }[];
        }>(recoveredConnection, 'GET', '/api/runtime');
        return runtime.nodes.find((item) => item.nodeId === target.id)?.integrationMode;
      })
      .toBe('detector');
    await expect
      .poll(async () => {
        const activity = await api<{
          dispatches: { dispatch_id: string; status: string }[];
        }>(recoveredConnection, 'GET', '/api/dispatches');
        return activity.dispatches.find(
          (dispatch) => dispatch.dispatch_id === mcpDispatch.dispatchID,
        )?.status;
      })
      .toBe('done');
  } finally {
    await application.close();
    rmSync(root, { recursive: true, force: true });
  }
});

function git(repository: string, ...args: string[]): void {
  execFileSync('git', ['-C', repository, ...args], { stdio: 'pipe' });
}

async function backendConnection(page: Page): Promise<Connection> {
  return page.evaluate(async () => {
    const state = await window.agentInfinite.getBackendState();
    if (!state.connection) throw new Error('backend connection is unavailable');
    return state.connection;
  });
}

async function api<T = unknown>(
  connection: Connection,
  method: string,
  path: string,
  body?: unknown,
): Promise<T> {
  const init: RequestInit = {
    method,
    headers: {
      Authorization: `Bearer ${connection.token}`,
      ...(body === undefined ? {} : { 'Content-Type': 'application/json' }),
    },
  };
  if (body !== undefined) init.body = JSON.stringify(body);
  const response = await fetch(connection.baseUrl + path, init);
  const text = await response.text();
  if (!response.ok) throw new Error(`${method} ${path}: ${response.status.toString()} ${text}`);
  return (text ? JSON.parse(text) : undefined) as T;
}

async function collectEvents(page: Page, connection: Connection): Promise<void> {
  await page.evaluate(({ baseUrl, token }) => {
    const scope = window as unknown as { __agentInfiniteEvents: RuntimeEvent[] };
    scope.__agentInfiniteEvents = [];
    const socket = new WebSocket(
      `${baseUrl.replace(/^http/, 'ws')}/ws/events?token=${encodeURIComponent(token)}`,
    );
    socket.addEventListener('message', (event) => {
      scope.__agentInfiniteEvents.push(JSON.parse(String(event.data)) as RuntimeEvent);
    });
  }, connection);
}

async function dispatchThroughMCP(
  connection: Connection,
  sourceID: string,
  target: string,
  task: string,
): Promise<{ endpoint: string; headers: Record<string, string>; dispatchID: string }> {
  const endpoint = `${connection.baseUrl}/mcp/${sourceID}`;
  const headers = {
    Authorization: `Bearer ${connection.token}`,
    Accept: 'application/json, text/event-stream',
    'Content-Type': 'application/json',
  };
  const initialized = await fetch(endpoint, {
    method: 'POST',
    headers,
    body: JSON.stringify({
      jsonrpc: '2.0',
      id: 1,
      method: 'initialize',
      params: {
        protocolVersion: '2025-06-18',
        capabilities: {},
        clientInfo: { name: 'agent-infinite-e2e', version: '0.1.0' },
      },
    }),
  });
  if (!initialized.ok) throw new Error(`MCP initialize failed: ${await initialized.text()}`);
  const sessionID = initialized.headers.get('mcp-session-id');
  if (!sessionID) throw new Error('MCP server returned no session id');
  const sessionHeaders = { ...headers, 'Mcp-Session-Id': sessionID };
  await fetch(endpoint, {
    method: 'POST',
    headers: sessionHeaders,
    body: JSON.stringify({ jsonrpc: '2.0', method: 'notifications/initialized' }),
  });
  const dispatched = await fetch(endpoint, {
    method: 'POST',
    headers: sessionHeaders,
    body: JSON.stringify({
      jsonrpc: '2.0',
      id: 2,
      method: 'tools/call',
      params: { name: 'delegate_task', arguments: { target, task } },
    }),
  });
  const result = await dispatched.text();
  if (!dispatched.ok || result.includes('isError'))
    throw new Error(`MCP dispatch failed: ${result}`);
  const parsed = JSON.parse(result) as {
    result?: { structuredContent?: { dispatch_id?: string }; content?: { text?: string }[] };
  };
  const dispatchID =
    parsed.result?.structuredContent?.dispatch_id ??
    /"dispatch_id"\s*:\s*"([^"]+)"/.exec(parsed.result?.content?.[0]?.text ?? '')?.[1];
  if (!dispatchID) throw new Error(`MCP dispatch returned no dispatch_id: ${result}`);
  return { endpoint, headers: sessionHeaders, dispatchID };
}

async function getDispatchResultThroughMCP(input: {
  endpoint: string;
  headers: Record<string, string>;
  dispatchID: string;
}): Promise<string> {
  const response = await fetch(input.endpoint, {
    method: 'POST',
    headers: input.headers,
    body: JSON.stringify({
      jsonrpc: '2.0',
      id: 3,
      method: 'tools/call',
      params: {
        name: 'get_dispatch_result',
        arguments: { dispatch_id: input.dispatchID, max_lines: 500 },
      },
    }),
  });
  const text = await response.text();
  if (!response.ok || text.includes('isError')) throw new Error(`MCP result read failed: ${text}`);
  return text;
}
