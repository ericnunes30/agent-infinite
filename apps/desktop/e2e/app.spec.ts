import { _electron as electron, expect, test, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
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
  const providerHome = join(root, 'provider-home');
  mkdirSync(repository);
  mkdirSync(localAppData);
  mkdirSync(providerHome);
  const claudeConfigPath = join(providerHome, '.claude.json');
  const claudeConfig =
    '{"mcpServers":{"external-docs":{"url":"https://example.test/mcp","headers":{"Authorization":"Bearer e2e-secret"}}}}';
  writeFileSync(claudeConfigPath, claudeConfig);
  const accessSkill =
    '---\nname: access\ndescription: Manage Discord channel access\n---\nManage access safely.\n';
  for (const marketplace of ['official', 'official.staging']) {
    const skillDirectory = join(
      providerHome,
      '.claude',
      'plugins',
      'marketplaces',
      marketplace,
      'discord',
      'skills',
      'access',
    );
    mkdirSync(skillDirectory, { recursive: true });
    writeFileSync(join(skillDirectory, 'SKILL.md'), accessSkill);
  }
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
      AGENT_INFINITE_PROVIDER_HOME: providerHome,
      ELECTRON_DISABLE_SECURITY_WARNINGS: 'true',
    },
  });
  try {
    const page = await application.firstWindow();
    const pageErrors: string[] = [];
    page.on('pageerror', (error) => pageErrors.push(error.stack ?? error.message));
    await page.evaluate(() => localStorage.removeItem('agent-infinite:last-workspace'));
    await page.reload();
    await page.waitForLoadState('domcontentloaded');
    await page.evaluate(() => localStorage.setItem('agent-infinite:theme:v1', 'dark'));
    await page.reload();
    await page.waitForLoadState('domcontentloaded');
    await expect(page.getByText(/runtime 0\.\d+\.\d+/)).toBeVisible();
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

    const rolesToggle = page.getByRole('button', { name: 'Expandir Roles' });
    await expect(rolesToggle).toHaveAttribute('aria-expanded', 'false');
    await rolesToggle.click();
    await expect(page.getByRole('button', { name: 'DevOps' })).toBeVisible();
    await page.getByRole('button', { name: 'Recolher Roles' }).click();
    await expect(page.getByRole('button', { name: 'DevOps' })).toHaveCount(0);

    const connection = await backendConnection(page);
    const scan = await api<{ items: { id: string; name: string; policy: string }[] }>(
      connection,
      'POST',
      '/api/tools/scan',
    );
    expect(JSON.stringify(scan)).not.toContain('e2e-secret');
    const externalMcp = scan.items.find((item) => item.name === 'external-docs');
    expect(externalMcp?.policy).toBe('provider_default');
    await api(connection, 'PATCH', `/api/tools/mcp-servers/${externalMcp?.id ?? ''}/policy`, {
      policy: 'blocked',
    });
    const managedSkill = await api<{ id: string }>(connection, 'POST', '/api/tools/skills', {
      name: 'portable-review',
      description: 'Portable review instructions',
      provider: 'all',
      markdown: 'Review only relevant changes.',
    });
    const toolsToggle = page.getByRole('button', { name: 'Expandir Ferramentas' });
    if (await toolsToggle.isVisible()) await toolsToggle.click();
    await page.getByRole('button', { name: 'MCPs & Skills' }).click();
    const governance = page.locator('.capability-console');
    await governance.getByRole('button', { name: 'Skills' }).click();
    const managedSkillRow = governance
      .locator('.capability-row')
      .filter({ hasText: 'portable-review' });
    for (const [bulkPolicy, expectedPolicy] of [
      ['blocked', 'blocked'],
      ['curated', 'curated'],
    ] as const) {
      await governance.getByLabel('Política em lote').selectOption(bulkPolicy);
      page.once('dialog', (dialog) => dialog.accept());
      await governance.getByRole('button', { name: /^Aplicar a / }).click();
      await expect(managedSkillRow.locator('select')).toHaveValue(expectedPolicy);
    }
    await governance.getByRole('button', { name: 'Fechar' }).click();
    const crossProviderMcp = await api<{ id: string }>(
      connection,
      'POST',
      '/api/tools/mcp-servers',
      {
        name: 'portable-cross-provider',
        description: 'Portable MCP discovered for another provider',
        provider: 'claude',
        spec: { type: 'http', url: 'https://example.test/portable-mcp' },
      },
    );
    const roleProfile = await api<{ id: string }>(connection, 'POST', '/api/role-profiles', {
      name: 'Curated Reviewer',
      defaultProvider: 'codex',
      mcpIds: [crossProviderMcp.id],
      skillIds: [managedSkill.id],
    });
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
    const uxTeam = await api<TeamResponse>(connection, 'POST', '/api/teams', {
      name: 'UX Team',
      color: '#e7a84b',
      orchestratorProvider: 'mock',
    });
    const reviewWorktree = await api<WorktreeResponse>(connection, 'POST', '/api/worktrees', {
      teamId: sourceTeam.team.id,
      name: 'Mock Control Review',
    });
    const deliveryWorktree = await api<WorktreeResponse>(connection, 'POST', '/api/worktrees', {
      teamId: targetTeam.team.id,
      name: 'Mock Delivery',
    });
    await api(connection, 'PATCH', `/api/nodes/${sourceTeam.orchestrator.id}`, {
      worktreeId: reviewWorktree.id,
    });
    await api(connection, 'PATCH', `/api/nodes/${targetTeam.orchestrator.id}`, {
      worktreeId: deliveryWorktree.id,
    });
    expect(reviewWorktree.teamId).toBe(sourceTeam.team.id);
    const reviewAgent = await api<{ id: string }>(connection, 'POST', '/api/nodes', {
      teamId: sourceTeam.team.id,
      worktreeId: reviewWorktree.id,
      label: 'Review Worker',
      role: 'Runs on the linked review branch',
      provider: 'mock',
      roleProfileId: roleProfile.id,
      skillIds: [managedSkill.id],
      autoStart: false,
    });
    const target = await api<{ id: string }>(connection, 'POST', '/api/nodes', {
      teamId: targetTeam.team.id,
      worktreeId: deliveryWorktree.id,
      label: 'Deterministic Worker',
      role: 'Returns fixture output',
      provider: 'mock',
      autoStart: true,
    });
    await Promise.all(
      Array.from({ length: 47 }, async (_, index) =>
        api(connection, 'POST', '/api/nodes', {
          teamId: targetTeam.team.id,
          worktreeId: deliveryWorktree.id,
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
    await expect(
      worktreeRail
        .locator('.rail-item-with-delete > button:first-child')
        .filter({ hasText: 'Mock Control Review' }),
    ).toBeVisible();
    await expect(
      worktreeRail
        .locator('.rail-item-with-delete > button:first-child')
        .filter({ hasText: 'Mock Delivery' }),
    ).toBeVisible();
    await expect(page.locator('section[aria-labelledby="workspace-title"]')).toBeVisible();
    await expect(
      page.locator('.canvas-toolbar').getByText('2 NODES', { exact: true }),
    ).toBeVisible();
    await expect(
      worktreeRail.locator('.rail-item-with-delete > button:first-child').first(),
    ).toHaveAttribute('aria-pressed', 'true');
    await expect(page.locator('.react-flow__edge')).toHaveCount(0);

    await worktreeRail
      .locator('.rail-item-with-delete > button:first-child')
      .filter({ hasText: 'Mock Delivery' })
      .click();
    await expect(page.getByRole('heading', { name: 'Deterministic Worker' })).toBeVisible();
    await expect(
      page.locator('.canvas-toolbar').getByText('49 NODES', { exact: true }),
    ).toBeVisible();
    await expect(worktreeRail.getByText(/TEAM Mock Delivery/)).toBeVisible();
    await expect(
      worktreeRail
        .locator('.rail-item-with-delete > button:first-child')
        .filter({ hasText: 'Mock Control Review' }),
    ).toBeVisible();
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
    await expect(
      page.locator('.canvas-toolbar').getByText('2 NODES', { exact: true }),
    ).toBeVisible();

    await page.locator('.surface-toggle').click();
    await expect(page.getByRole('heading', { name: 'Mock Control' })).toBeVisible();

    await page.getByRole('button', { name: 'Create Team', exact: true }).click();
    const teamDialog = page.locator('.editor-dialog');
    await teamDialog.getByLabel('Team name').fill('Regression Team');
    await teamDialog.getByRole('button', { name: 'CREATE TEAM' }).click();
    await expect(page.getByRole('heading', { name: 'Regression Team', exact: true })).toBeVisible();
    const quickAgent = page.getByRole('button', { name: /Adicionar ao Team Regression Team/ });
    await expect(quickAgent).toBeEnabled();
    await quickAgent.click();
    const regressionAgentDialog = page.locator('.editor-dialog');
    await regressionAgentDialog.getByLabel('Label').fill('Regression Worker');
    await regressionAgentDialog.getByRole('button', { name: 'CREATE AGENT' }).click();
    await expect(page.getByRole('heading', { name: 'Regression Worker' })).toBeVisible();
    await page.locator('.agent-node').filter({ hasText: 'Regression Worker' }).dblclick();
    const teamAgentEditor = page.locator('.agent-capability-editor');
    await expect(teamAgentEditor.getByRole('heading', { name: 'Editar agente' })).toBeVisible();
    await teamAgentEditor.getByLabel('Nome').fill('Regression Worker Edited');
    await teamAgentEditor.getByRole('button', { name: 'Salvar agente' }).click();
    await expect(
      page.locator('.agent-node').getByRole('heading', { name: 'Regression Worker Edited' }),
    ).toBeVisible();
    const regressionNode = page
      .locator('.agent-node')
      .filter({ hasText: 'Regression Worker Edited' });
    await regressionNode.click();
    await expect(page.getByRole('button', { name: 'Excluir agente' })).toBeVisible();
    page.once('dialog', async (dialog) => {
      expect(dialog.message()).toContain('Regression Worker Edited');
      await dialog.accept();
    });
    await page.getByRole('button', { name: 'Excluir agente' }).click();
    await expect(regressionNode).toHaveCount(0);

    await page.getByRole('button', { name: 'Create Team Template' }).click();
    const createTemplateDialog = page.locator('.editor-dialog');
    await createTemplateDialog.getByLabel('Template name').fill('Direct Template');
    await createTemplateDialog.getByRole('button', { name: 'CREATE TEMPLATE' }).click();
    await expect(page.getByLabel('Template name')).toHaveValue('Direct Template');
    await page.getByRole('button', { name: /Adicionar ao template Direct Template/ }).click();
    const templateAgentDialog = page.locator('.editor-dialog');
    await templateAgentDialog.getByLabel('Label').fill('Template Worker');
    await templateAgentDialog.getByLabel('Role predefinida').selectOption('Backend');
    await expect(
      templateAgentDialog.getByRole('textbox', { name: 'Role', exact: true }),
    ).toHaveValue('Backend');
    await templateAgentDialog.getByRole('button', { name: 'ADD AGENT' }).click();
    await expect(page.getByRole('heading', { name: 'Template Worker' })).toBeVisible();
    await page
      .locator('.template-library-page .agent-node')
      .filter({ hasText: 'Template Worker' })
      .dblclick();
    const templateAgentEditor = page.locator('.agent-capability-editor');
    await templateAgentEditor.getByLabel('Nome').fill('Template Worker Edited');
    await templateAgentEditor.getByRole('button', { name: 'Salvar agente' }).click();
    await expect(
      page
        .locator('.template-library-page .agent-node')
        .getByRole('heading', { name: 'Template Worker Edited' }),
    ).toBeVisible();
    const rolesRail = page.locator('section[aria-labelledby="roles-title"]');
    await rolesRail.getByRole('button', { name: 'Expandir Roles' }).click();
    await rolesRail.getByRole('button', { name: 'Frontend', exact: true }).click();
    const presetAgentDialog = page.locator('.editor-dialog');
    await expect(presetAgentDialog.getByLabel('Role predefinida')).toHaveValue('Frontend');
    await expect(presetAgentDialog.getByRole('textbox', { name: 'Role', exact: true })).toHaveValue(
      'Frontend',
    );
    await presetAgentDialog.getByRole('button', { name: 'Close' }).click();
    await rolesRail.getByRole('button', { name: 'Curated Reviewer', exact: true }).click();
    const profileAgentDialog = page.locator('.editor-dialog');
    await expect(profileAgentDialog.getByLabel('Role com capacidades')).toHaveValue(roleProfile.id);
    await expect(profileAgentDialog.getByText('portable-review')).toBeVisible();
    await expect(profileAgentDialog.getByText('portable-cross-provider')).toBeVisible();
    await expect(profileAgentDialog.getByText('access', { exact: true })).toHaveCount(1);
    await expect(profileAgentDialog.getByText('Manage Discord channel access')).toBeVisible();
    const curatedMcpRow = profileAgentDialog
      .locator('.capability-picker')
      .first()
      .locator('label')
      .filter({ hasText: 'portable-cross-provider' });
    const curatedMcpLayout = await curatedMcpRow.evaluate((row) => {
      const checkbox = row.querySelector('input');
      const content = row.querySelector('span');
      if (!checkbox || !content) throw new Error('Capability row is incomplete.');
      const checkboxBox = checkbox.getBoundingClientRect();
      const contentBox = content.getBoundingClientRect();
      const style = getComputedStyle(row);
      return {
        checkboxWidth: checkboxBox.width,
        contentGap: contentBox.left - checkboxBox.right,
        marginTop: style.marginTop,
        columns: style.gridTemplateColumns,
      };
    });
    expect(curatedMcpLayout.checkboxWidth).toBe(14);
    expect(curatedMcpLayout.contentGap).toBe(10);
    expect(curatedMcpLayout.marginTop).toBe('0px');
    expect(curatedMcpLayout.columns).toMatch(/^14px /);
    await profileAgentDialog.getByLabel('Label').fill('Profile Template Worker');
    await profileAgentDialog.getByRole('button', { name: 'ADD AGENT' }).click();
    await page
      .locator('.template-library-page .agent-node')
      .filter({ hasText: 'Profile Template Worker' })
      .dblclick();
    const profileAgentEditor = page.locator('.agent-capability-editor');
    await expect(profileAgentEditor.getByLabel('Role preset')).toHaveValue(roleProfile.id);
    await expect(profileAgentEditor.getByText('portable-review')).toBeVisible();
    await expect(profileAgentEditor.getByText('portable-cross-provider')).toBeVisible();
    await profileAgentEditor.getByRole('button', { name: 'Cancelar' }).click();
    await page.getByLabel('Template name').fill('Direct Template Renamed');
    await expect
      .poll(async () =>
        (
          await api<{ templates: { name: string; nodes: unknown[] }[] }>(
            connection,
            'GET',
            '/api/team-templates',
          )
        ).templates.some(
          (template) =>
            template.name === 'Direct Template Renamed' &&
            template.nodes.length === 3 &&
            (template.nodes as { role?: string; label?: string }[]).some(
              (node) => node.role === 'Backend' && node.label === 'Template Worker Edited',
            ),
        ),
      )
      .toBe(true);
    await page.getByRole('button', { name: 'CRIAR TEAM A PARTIR DESTE TEMPLATE' }).click();
    await expect(
      page.getByRole('heading', { name: 'Direct Template Renamed', exact: true }),
    ).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Template Worker Edited' })).toBeVisible();

    await page
      .locator('section[aria-labelledby="teams-title"]')
      .locator('.rail-item-with-delete > button:first-child')
      .filter({ hasText: 'Mock Delivery' })
      .click();
    await expect(page.getByRole('heading', { name: 'Mock Delivery' })).toBeVisible();
    await page
      .locator('section[aria-labelledby="teams-title"]')
      .locator('.rail-item-with-delete > button:first-child')
      .filter({ hasText: 'UX Team' })
      .click();
    await expect(page.getByRole('heading', { name: 'UX Team', exact: true })).toBeVisible();
    await page.getByRole('button', { name: 'ADICIONAR AGENTE' }).click();
    const agentDialog = page.locator('.editor-dialog');
    await agentDialog.getByLabel('Label').fill('UX Worker');
    await agentDialog
      .getByRole('textbox', { name: 'Role', exact: true })
      .fill('Valida o fluxo independente');
    await agentDialog.getByRole('button', { name: 'CREATE AGENT', exact: true }).click();
    await expect(page.getByRole('heading', { name: 'UX Worker' })).toBeVisible();
    await page.getByRole('button', { name: 'SAVE AS TEMPLATE' }).click();
    await expect
      .poll(async () =>
        (
          await api<{ templates: { name: string }[] }>(connection, 'GET', '/api/team-templates')
        ).templates.some((template) => template.name === 'UX Team'),
      )
      .toBe(true);
    const teamCountBeforeTemplateOpen = (
      await api<{ teams: unknown[] }>(connection, 'GET', '/api/snapshot')
    ).teams.length;
    const uxTemplateButton = page
      .locator('section[aria-labelledby="templates-title"]')
      .locator('.template-item > button:first-child')
      .filter({ hasText: 'UX Team' });
    await uxTemplateButton.click();
    await page.waitForTimeout(250);
    expect(pageErrors).toEqual([]);
    await expect(uxTemplateButton).toHaveClass(/active/);
    await expect(page.getByText(/v0\.\d+\.\d+ \/ TEAM TEMPLATES/)).toBeVisible();
    await expect(
      page.locator('.template-library-page').getByRole('heading', { name: 'UX Team' }),
    ).toBeVisible();
    expect(
      (await api<{ teams: unknown[] }>(connection, 'GET', '/api/snapshot')).teams,
    ).toHaveLength(teamCountBeforeTemplateOpen);
    await page.getByRole('button', { name: 'Create Git worktree' }).click();
    await page.getByLabel('Team link (optional)').selectOption('');
    await page.getByLabel('Worktree name').fill('Independent UX');
    await expect(page.getByLabel('Create from branch')).toHaveCount(0);
    await page.getByLabel('New branch name').fill('feature/independent-ux');
    await page.getByRole('button', { name: 'CREATE WORKTREE' }).click();
    const independentWorktreeButton = worktreeRail
      .locator('.rail-item-with-delete > button:first-child')
      .filter({ hasText: 'Independent UX' });
    await expect(independentWorktreeButton).toBeVisible();
    await independentWorktreeButton.click();
    await expect(
      page.getByRole('heading', { name: 'Este worktree ainda está vazio' }),
    ).toBeVisible();
    await page.getByRole('button', { name: 'IMPORTAR TEMPLATE' }).click();
    const templateImportDialog = page.locator('.editor-dialog');
    await templateImportDialog
      .getByRole('combobox', { name: /Composição de Team/ })
      .selectOption({ label: 'AGENT TEAM / UX Team / 2 nodes' });
    await templateImportDialog.getByRole('button', { name: 'IMPORT TEMPLATE' }).click();
    await expect(
      page.locator('.canvas-toolbar').getByText('2 NODES', { exact: true }),
    ).toBeVisible();
    await expect(page.getByRole('heading', { name: 'UX Worker' })).toBeVisible();
    const importedSnapshot = await api<{
      worktrees: { id: string; name: string; teamId?: string; branch: string }[];
      nodes: { worktreeId?: string }[];
    }>(connection, 'GET', '/api/snapshot');
    const independentWorktree = importedSnapshot.worktrees.find(
      (worktree) => worktree.name === 'Independent UX',
    );
    expect(independentWorktree?.teamId).toBe(uxTeam.team.id);
    expect(independentWorktree?.branch).toBe('feature/independent-ux');
    expect(
      importedSnapshot.nodes.filter((node) => node.worktreeId === independentWorktree?.id),
    ).toHaveLength(2);
    await page.getByRole('button', { name: 'Create Git worktree' }).click();
    await page.getByLabel('Team link (optional)').selectOption('');
    await page.getByLabel('Worktree name').fill('Independent Direct');
    await page.getByLabel('New branch name').fill('feature/independent-direct');
    await page.getByRole('button', { name: 'CREATE WORKTREE' }).click();
    const directWorktreeButton = worktreeRail
      .locator('.rail-item-with-delete > button:first-child')
      .filter({ hasText: 'Independent Direct' });
    await directWorktreeButton.click();
    await page.getByRole('button', { name: /Criar no worktree independente/ }).click();
    const directAgentDialog = page.locator('.editor-dialog');
    await directAgentDialog.getByLabel('Label').fill('Direct Worktree Agent');
    await directAgentDialog.getByRole('button', { name: 'CREATE AGENT' }).click();
    await expect(page.getByRole('heading', { name: 'Direct Worktree Agent' })).toBeVisible();
    const directSnapshot = await api<{
      worktrees: { id: string; name: string; teamId?: string }[];
      nodes: { label: string; teamId: string; worktreeId?: string }[];
    }>(connection, 'GET', '/api/snapshot');
    const directWorktree = directSnapshot.worktrees.find(
      (worktree) => worktree.name === 'Independent Direct',
    );
    const directAgent = directSnapshot.nodes.find((node) => node.label === 'Direct Worktree Agent');
    expect(directWorktree?.teamId).toBeFalsy();
    expect(directAgent?.teamId).toBeFalsy();
    expect(directAgent?.worktreeId).toBe(directWorktree?.id);
    await worktreeRail
      .locator('.rail-item-with-delete > button:first-child')
      .filter({ hasText: 'Mock Control Review' })
      .click();
    await expect(
      page.locator('.canvas-toolbar').getByText('2 NODES', { exact: true }),
    ).toBeVisible();
    await page.locator('.agent-node').filter({ hasText: 'Review Worker' }).dblclick();
    const canvasAgentEditor = page.locator('.agent-capability-editor');
    await canvasAgentEditor.getByLabel('Nome').fill('Review Worker Edited');
    await canvasAgentEditor.getByRole('button', { name: 'Salvar agente' }).click();
    await expect(
      page.locator('.agent-node').getByRole('heading', { name: 'Review Worker Edited' }),
    ).toBeVisible();

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
    for (let reconnect = 0; reconnect < 5; reconnect += 1) {
      await sourceNode.click();
      await expect(sourceNode).toHaveClass(/terminal-active/);
      await expect(
        sourceNode.getByLabel('Terminal de Mock Control Lead', { exact: true }),
      ).toBeVisible();
      if (reconnect === 0 && process.env.AGENT_INFINITE_VISUAL_CAPTURE === '1') {
        await page.screenshot({
          path: resolve('test-results/native-terminal.png'),
          fullPage: true,
        });
      }
      await sourceNode
        .getByRole('button', { name: 'Recolher terminal de Mock Control Lead' })
        .click();
      await expect(sourceNode).not.toHaveClass(/terminal-active/);
    }
    expect(pageErrors).toEqual([]);
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
      recoveredWorktreeRail
        .locator('.rail-item-with-delete > button:first-child')
        .filter({ hasText: 'Mock Delivery' }),
    ).toBeVisible();
    await recoveredWorktreeRail
      .locator('.rail-item-with-delete > button:first-child')
      .filter({ hasText: 'Mock Delivery' })
      .click();
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
    expect(readFileSync(claudeConfigPath, 'utf8')).toBe(claudeConfig);
  } finally {
    await application.close();
    try {
      rmSync(root, { recursive: true, force: true, maxRetries: 3, retryDelay: 200 });
    } catch {
      // A child process can briefly retain the Windows temp directory after Electron closes.
    }
  }
});

test('canvas promotes exactly one running node to an interactive native terminal', async () => {
  const root = mkdtempSync(join(tmpdir(), 'agent-infinite-terminal-e2e-'));
  const repository = join(root, 'repo');
  const localAppData = join(root, 'local-app-data');
  mkdirSync(repository);
  mkdirSync(localAppData);
  git(repository, 'init', '-b', 'main');
  git(repository, 'config', 'user.email', 'e2e@agent-infinite.local');
  git(repository, 'config', 'user.name', 'Agent Infinite E2E');
  writeFileSync(join(repository, 'README.md'), '# terminal e2e\n');
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
    const pageErrors: string[] = [];
    page.on('pageerror', (error) => pageErrors.push(error.stack ?? error.message));
    await page.evaluate(
      ({ path }) => {
        localStorage.setItem('agent-infinite:last-workspace', path);
        localStorage.setItem('agent-infinite:theme:v1', 'dark');
      },
      { path: repository },
    );
    await page.reload();
    await expect(page.locator('.canvas-stage')).toBeVisible();
    const connection = await backendConnection(page);
    const team = await api<TeamResponse>(connection, 'POST', '/api/teams', {
      name: 'Native',
      color: '#b7f34a',
      orchestratorProvider: 'mock',
    });
    const worktree = await api<WorktreeResponse>(connection, 'POST', '/api/worktrees', {
      teamId: team.team.id,
      name: 'Native Terminal',
    });
    await api(connection, 'PATCH', `/api/nodes/${team.orchestrator.id}`, {
      worktreeId: worktree.id,
    });
    await api<{ id: string }>(connection, 'POST', '/api/nodes', {
      teamId: team.team.id,
      worktreeId: worktree.id,
      label: 'Native Worker',
      role: 'Preview-only while inactive',
      provider: 'mock',
    });
    await page.reload();
    await expect(page.locator('.canvas-stage')).toBeVisible();

    const lead = page.locator('.agent-node').filter({ hasText: 'Native Lead' });
    const workerNode = page.locator('.agent-node').filter({ hasText: 'Native Worker' });
    const startAll = page.getByRole('button', {
      name: 'Iniciar todos os agentes offline deste worktree',
    });
    await expect(startAll).toBeEnabled();
    await expect(page.locator('.terminal-surface')).toHaveCount(0);
    await startAll.click();
    await expect(lead.getByRole('button', { name: 'Parar Native Lead' })).toBeVisible();
    await expect(workerNode.getByRole('button', { name: 'Parar Native Worker' })).toBeVisible();
    await expect(startAll).toBeDisabled();
    await expect(startAll).toContainText('TERMINAIS ATIVOS');
    await expect(lead.getByText(/PREVIEW.*BAIXO CONSUMO/)).toBeVisible();
    await expect(workerNode.getByText(/PREVIEW.*BAIXO CONSUMO/)).toBeVisible();
    await expect(page.locator('.terminal-surface')).toHaveCount(0);

    await lead.click();
    await expect(lead).toHaveClass(/terminal-active/);
    await expect(page.locator('.terminal-surface')).toHaveCount(1);
    const activeBox = await lead.boundingBox();
    expect(activeBox?.width).toBeGreaterThanOrEqual(620);
    expect(activeBox?.height).toBeGreaterThanOrEqual(380);

    await lead.getByRole('button', { name: 'Recolher terminal de Native Lead' }).click();
    await expect(page.locator('.terminal-surface')).toHaveCount(0);
    await workerNode.click();
    await expect(workerNode).toHaveClass(/terminal-active/);
    await expect(lead).not.toHaveClass(/terminal-active/);
    await expect(page.locator('.terminal-surface')).toHaveCount(1);
    await expect(lead.getByText('PREVIEW · BAIXO CONSUMO')).toBeVisible();

    if (process.env.AGENT_INFINITE_VISUAL_CAPTURE === '1') {
      await page.screenshot({ path: resolve('test-results/native-terminal.png'), fullPage: true });
    }
    await workerNode.getByRole('button', { name: 'Recolher terminal de Native Worker' }).click();
    await expect(page.locator('.terminal-surface')).toHaveCount(0);
    expect(pageErrors).toEqual([]);
  } finally {
    await application.close();
    try {
      rmSync(root, { recursive: true, force: true, maxRetries: 3, retryDelay: 200 });
    } catch {
      // A child process can briefly retain the Windows temp directory after Electron closes.
    }
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
