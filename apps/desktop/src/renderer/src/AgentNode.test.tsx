import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AgentNode, type AgentNodeData } from './AgentNode';

const terminalRender = vi.fn();

vi.mock('@xyflow/react', () => ({
  Handle: () => null,
  Position: { Left: 'left', Right: 'right' },
}));

vi.mock('./TerminalPanel', () => ({
  TerminalPanel: (props: { sessionId: string }) => {
    terminalRender(props.sessionId);
    return <div data-testid="interactive-terminal" />;
  },
}));

const baseData: AgentNodeData = {
  label: 'Backend',
  role: 'Implement APIs',
  kind: 'agent',
  provider: 'codex',
  teamName: 'Platform',
  teamColor: '#b7f34a',
  status: 'Working',
  preview: 'provider output',
};

function renderNode(data: AgentNodeData): void {
  render(
    <AgentNode
      id="node-1"
      data={data}
      selected={false}
      type="agent"
      dragging={false}
      zIndex={0}
      selectable
      deletable
      draggable
      isConnectable
      positionAbsoluteX={0}
      positionAbsoluteY={0}
    />,
  );
}

describe('AgentNode terminal activity', () => {
  beforeEach(() => terminalRender.mockClear());

  it('keeps a running inactive node on the cheap text preview', () => {
    renderNode({ ...baseData, sessionId: 'session-1', terminalActive: false });
    expect(screen.getByLabelText('Preview do terminal de Backend')).toHaveTextContent(
      'provider output',
    );
    expect(screen.queryByTestId('interactive-terminal')).not.toBeInTheDocument();
    expect(terminalRender).not.toHaveBeenCalled();
  });

  it('mounts xterm only when the running node is active', () => {
    renderNode({
      ...baseData,
      sessionId: 'session-1',
      terminalActive: true,
      connection: { baseUrl: 'http://127.0.0.1', token: 'token', version: 'test' },
    });
    expect(screen.getByTestId('interactive-terminal')).toBeInTheDocument();
    expect(terminalRender).toHaveBeenCalledOnce();
    expect(screen.queryByLabelText('Preview do terminal de Backend')).not.toBeInTheDocument();
  });

  it('unmounts the compact xterm while the same terminal is fullscreen', () => {
    renderNode({
      ...baseData,
      sessionId: 'session-1',
      terminalActive: true,
      terminalFullscreen: true,
      connection: { baseUrl: 'http://127.0.0.1', token: 'token', version: 'test' },
    });
    expect(screen.getByText('Terminal expandido')).toBeInTheDocument();
    expect(screen.queryByTestId('interactive-terminal')).not.toBeInTheDocument();
  });

  it('starts an idle agent from its native titlebar control', () => {
    const onStart = vi.fn();
    renderNode({ ...baseData, status: 'Idle', preview: '', onStart });
    fireEvent.click(screen.getByRole('button', { name: 'Iniciar Backend' }));
    expect(onStart).toHaveBeenCalledWith('node-1');
  });
});
