import { Handle, Position, type NodeProps } from '@xyflow/react';
import { Bot, Crown, Maximize2, Pencil, Play, Square, TerminalSquare, X } from 'lucide-react';
import { memo } from 'react';
import type { BackendConnection } from '../../shared/ipc';
import type { NodeKind, Provider } from './domain';
import { TerminalPanel } from './TerminalPanel';

export interface AgentNodeData extends Record<string, unknown> {
  readonly label: string;
  readonly role: string;
  readonly kind: NodeKind;
  readonly provider: Provider;
  readonly model?: string;
  readonly teamName: string;
  readonly teamColor: string;
  readonly status: string;
  readonly preview: string;
  readonly sessionId?: string;
  readonly terminalActive?: boolean;
  readonly terminalFullscreen?: boolean;
  readonly connection?: BackendConnection;
  readonly onStart?: (nodeId: string) => void;
  readonly onStop?: (nodeId: string) => void;
  readonly onEdit?: (nodeId: string) => void;
  readonly onCollapseTerminal?: () => void;
  readonly onExpandTerminal?: () => void;
}

function AgentNodeView({ id, data, selected }: NodeProps): React.JSX.Element {
  const agent = data as AgentNodeData;
  const running = Boolean(agent.sessionId);
  const terminalActive = running && Boolean(agent.terminalActive && agent.connection);
  const runAction = (
    event: React.MouseEvent<HTMLButtonElement>,
    action: (() => void) | undefined,
  ): void => {
    event.preventDefault();
    event.stopPropagation();
    action?.();
  };

  return (
    <article
      className={`agent-node agent-terminal-node${selected ? ' selected' : ''}${running ? ' running' : ''}${terminalActive ? ' terminal-active' : ''}`}
      style={{ '--team': agent.teamColor } as React.CSSProperties}
    >
      <Handle type="target" position={Position.Left} />
      <header className="agent-terminal-titlebar">
        <div className="agent-terminal-identity">
          <i className={`state-${agent.status.toLowerCase()}`} aria-hidden="true" />
          <span>{agent.kind === 'orchestrator' ? <Crown size={11} /> : <Bot size={11} />}</span>
          <h3>{agent.label}</h3>
        </div>
        <div className="agent-terminal-badges">
          <span>{agent.provider}</span>
          <span>{agent.model ?? 'default'}</span>
        </div>
        <div className="agent-terminal-controls nodrag">
          {agent.onEdit ? (
            <button
              type="button"
              title="Editar agente"
              aria-label={`Editar ${agent.label}`}
              onClick={(event) => runAction(event, () => agent.onEdit?.(id))}
            >
              <Pencil size={10} />
            </button>
          ) : null}
          {running && agent.onStop ? (
            <button
              type="button"
              title="Parar agente"
              aria-label={`Parar ${agent.label}`}
              onClick={(event) => runAction(event, () => agent.onStop?.(id))}
            >
              <Square size={10} />
            </button>
          ) : agent.onStart ? (
            <button
              type="button"
              title="Iniciar agente"
              aria-label={`Iniciar ${agent.label}`}
              onClick={(event) => runAction(event, () => agent.onStart?.(id))}
            >
              <Play size={10} />
            </button>
          ) : null}
          {terminalActive ? (
            <>
              <button
                type="button"
                title="Expandir terminal"
                aria-label={`Expandir terminal de ${agent.label}`}
                onClick={(event) => runAction(event, agent.onExpandTerminal)}
              >
                <Maximize2 size={11} />
              </button>
              <button
                type="button"
                title="Recolher terminal"
                aria-label={`Recolher terminal de ${agent.label}`}
                onClick={(event) => runAction(event, agent.onCollapseTerminal)}
              >
                <X size={11} />
              </button>
            </>
          ) : null}
        </div>
      </header>
      <div className="agent-terminal-context">
        <span>{agent.role || 'General implementation agent'}</span>
        <code>{agent.teamName}</code>
      </div>
      <div className="agent-terminal-body nodrag nowheel">
        {terminalActive && agent.terminalFullscreen ? (
          <div className="agent-terminal-expanded-placeholder">Terminal expandido</div>
        ) : terminalActive && agent.connection && agent.sessionId ? (
          <TerminalPanel
            connection={agent.connection}
            sessionId={agent.sessionId}
            label={`Terminal de ${agent.label}`}
            compact
            accent={agent.teamColor}
          />
        ) : running ? (
          <pre className="node-preview" aria-label={`Preview do terminal de ${agent.label}`}>
            {agent.preview ? agent.preview : 'Sessão ativa. Aguardando saída do provider…'}
          </pre>
        ) : (
          <div className="agent-terminal-idle">
            <TerminalSquare size={15} />
            <span>Terminal inativo</span>
            <small>Selecione e inicie para abrir a sessão neste node.</small>
          </div>
        )}
      </div>
      <footer className="agent-terminal-statusbar">
        <span>{agent.status}</span>
        <span>
          {terminalActive ? 'INTERATIVO · XTERM' : running ? 'PREVIEW · BAIXO CONSUMO' : 'OFFLINE'}
        </span>
      </footer>
      {agent.kind === 'orchestrator' ? <Handle type="source" position={Position.Right} /> : null}
    </article>
  );
}

export const AgentNode = memo(AgentNodeView, (previous, next) => {
  const before = previous.data as AgentNodeData;
  const after = next.data as AgentNodeData;
  return (
    previous.id === next.id &&
    previous.selected === next.selected &&
    before.label === after.label &&
    before.role === after.role &&
    before.kind === after.kind &&
    before.provider === after.provider &&
    before.model === after.model &&
    before.teamName === after.teamName &&
    before.teamColor === after.teamColor &&
    before.status === after.status &&
    before.sessionId === after.sessionId &&
    before.terminalActive === after.terminalActive &&
    before.terminalFullscreen === after.terminalFullscreen &&
    (after.terminalActive ? true : before.preview === after.preview)
  );
});
