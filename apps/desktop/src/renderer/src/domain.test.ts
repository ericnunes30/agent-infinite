import { describe, expect, it } from 'vitest';
import {
  edgesBetweenVisibleNodes,
  groupCapabilityItems,
  initialTeamId,
  initialWorktreeId,
  isCapabilityAvailable,
  isCapabilityCompatible,
  visibleNodeIds,
  wouldCreateCycle,
  type CanvasEdge,
  type CanvasNode,
} from './domain';

describe('capability compatibility', () => {
  const base = {
    id: 'portable',
    kind: 'mcp' as const,
    name: 'Portable',
    origin: 'external' as const,
    provider: 'claude' as const,
    scope: 'user' as const,
    fingerprint: 'fingerprint',
    status: 'unchanged' as const,
    policy: 'curated' as const,
    enforceable: true,
    firstSeenAt: '',
    lastSeenAt: '',
  };

  it('allows curated MCPs and skills to be selected by another compatible provider', () => {
    expect(isCapabilityCompatible({ ...base, spec: { command: 'server' } }, 'codex')).toBe(true);
    expect(
      isCapabilityCompatible({ ...base, kind: 'skill', skillPath: 'SKILL.md' }, 'opencode'),
    ).toBe(true);
  });

  it('keeps local MCPs unavailable for Pi', () => {
    expect(isCapabilityCompatible({ ...base, spec: { command: 'server' } }, 'pi')).toBe(false);
    expect(
      isCapabilityCompatible({ ...base, spec: { type: 'http', url: 'https://mcp.test' } }, 'pi'),
    ).toBe(true);
  });

  it('groups identical fingerprints and prefers an already selected source', () => {
    const peer = { ...base, id: 'peer', groupId: 'mcp-same' };
    const selected = { ...base, id: 'selected', groupId: 'mcp-same' };
    const groups = groupCapabilityItems([peer, selected], ['selected']);
    expect(groups).toHaveLength(1);
    expect(groups[0]?.item.id).toBe('selected');
    expect(groups[0]?.ids).toEqual(['peer', 'selected']);
  });

  it('excludes missing and scan-error capabilities from selection', () => {
    expect(isCapabilityAvailable(base)).toBe(true);
    expect(isCapabilityAvailable({ ...base, status: 'missing' })).toBe(false);
    expect(isCapabilityAvailable({ ...base, status: 'scan_error' })).toBe(false);
  });
});

describe('initial team selection', () => {
  it('uses the first team when the workspace has teams', () =>
    expect(
      initialTeamId([
        { id: 'first', name: '', color: '', branch: '', baseRef: '', createdAt: '' },
        { id: 'second', name: '', color: '', branch: '', baseRef: '', createdAt: '' },
      ]),
    ).toBe('first'));

  it('keeps an empty workspace without a selected team', () =>
    expect(initialTeamId([])).toBeNull());
});

describe('initial worktree selection', () => {
  const worktrees = [
    { id: 'first-worktree', teamId: 'first', name: '', branch: '', baseRef: '', createdAt: '' },
    { id: 'second-worktree', teamId: 'second', name: '', branch: '', baseRef: '', createdAt: '' },
  ];

  it('uses the first worktree linked to the selected team', () =>
    expect(initialWorktreeId(worktrees, 'first')).toBe('first-worktree'));

  it('returns null when a team has no worktree', () =>
    expect(initialWorktreeId(worktrees, 'missing')).toBeNull());
});

describe('wouldCreateCycle', () => {
  const edges: CanvasEdge[] = [
    { id: 'a', source: 'one', target: 'two', type: 'delegates_to' },
    { id: 'b', source: 'two', target: 'three', type: 'delegates_to' },
  ];

  it('rejects a path back to the source', () =>
    expect(wouldCreateCycle(edges, 'three', 'one')).toBe(true));
  it('allows an independent delegation', () =>
    expect(wouldCreateCycle(edges, 'one', 'four')).toBe(false));
});

describe('team visibility', () => {
  const nodes: CanvasNode[] = [
    {
      id: 'team-one-lead',
      kind: 'orchestrator',
      provider: 'codex',
      teamId: 'team-one',
      worktreeId: 'worktree-one',
      label: 'Lead one',
      role: '',
      autoStart: false,
      position: { x: 0, y: 0 },
      size: { width: 320, height: 220 },
    },
    {
      id: 'team-one-agent',
      kind: 'agent',
      provider: 'codex',
      teamId: 'team-one',
      worktreeId: 'worktree-one',
      label: 'Agent one',
      role: '',
      autoStart: false,
      position: { x: 400, y: 0 },
      size: { width: 300, height: 210 },
    },
    {
      id: 'team-two-lead',
      kind: 'orchestrator',
      provider: 'claude',
      teamId: 'team-two',
      worktreeId: 'worktree-two',
      label: 'Lead two',
      role: '',
      autoStart: false,
      position: { x: 0, y: 300 },
      size: { width: 320, height: 220 },
    },
  ];
  const edges: CanvasEdge[] = [
    {
      id: 'team-one-edge',
      source: 'team-one-lead',
      target: 'team-one-agent',
      type: 'delegates_to',
    },
  ];

  it('hides an edge when either endpoint is outside the selected team', () => {
    const visibleIds = visibleNodeIds(nodes, 'team-two');
    expect(edgesBetweenVisibleNodes(edges, visibleIds)).toEqual([]);
    expect(edges).toHaveLength(1);
  });

  it('keeps the edge when both endpoints are visible', () => {
    const visibleIds = visibleNodeIds(nodes, 'team-one');
    expect(edgesBetweenVisibleNodes(edges, visibleIds)).toEqual(edges);
  });

  it('filters nodes to the selected worktree inside a team', () => {
    expect([...visibleNodeIds(nodes, 'team-one', 'worktree-one')]).toEqual([
      'team-one-lead',
      'team-one-agent',
    ]);
    expect([...visibleNodeIds(nodes, 'team-one', 'worktree-two')]).toEqual([]);
  });
});
