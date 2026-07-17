import { Handle, Position, type NodeProps } from '@xyflow/react';
import { Bot, Crown } from 'lucide-react';
import type { NodeKind, Provider } from './domain';

export interface AgentNodeData extends Record<string, unknown> {
  readonly label: string;
  readonly role: string;
  readonly kind: NodeKind;
  readonly provider: Provider;
  readonly teamName: string;
  readonly teamColor: string;
  readonly status: string;
  readonly preview: string;
}

export function AgentNode({ data, selected }: NodeProps): React.JSX.Element {
  const agent = data as AgentNodeData;
  return (
    <article
      className={`agent-node ${selected ? 'selected' : ''}`}
      style={{ '--team': agent.teamColor } as React.CSSProperties}
    >
      <Handle type="target" position={Position.Left} />
      <header>
        <span className="node-kind">
          {agent.kind === 'orchestrator' ? <Crown size={13} /> : <Bot size={13} />}
          {agent.kind}
        </span>
        <i className={`state-${agent.status.toLowerCase()}`} />
      </header>
      <h3>{agent.label}</h3>
      <p>{agent.role || 'General implementation agent'}</p>
      {agent.preview && <pre className="node-preview">{agent.preview}</pre>}
      <footer>
        <span>{agent.provider}</span>
        <span>{agent.teamName}</span>
      </footer>
      {agent.kind === 'orchestrator' && <Handle type="source" position={Position.Right} />}
    </article>
  );
}
