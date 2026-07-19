import { AlertTriangle, Bot, RotateCcw, X } from 'lucide-react';
import { useMemo, useState } from 'react';
import type { LocalApi } from './api';
import { groupCapabilityItems, isCapabilityAvailable, isCapabilityCompatible } from './domain';
import type { CapabilityItem, CanvasNode, Provider, RoleProfile } from './domain';
import type { ModelInventory } from './domain';
import { ModelSelector } from './ModelSelector';

interface Props {
  readonly api: LocalApi;
  readonly node: CanvasNode | null;
  readonly roles: readonly RoleProfile[];
  readonly capabilities: readonly CapabilityItem[];
  readonly models: ModelInventory;
  readonly running: boolean;
  readonly onClose: () => void;
  readonly onSaved: () => Promise<void>;
  readonly onSave?: (input: AgentEditorUpdate) => Promise<void>;
  readonly onError: (message: string) => void;
}

export interface AgentEditorUpdate {
  readonly label: string;
  readonly role: string;
  readonly provider: Provider;
  readonly model: string;
  readonly autoStart: boolean;
  readonly roleProfileId: string;
  readonly mcpIds: string[];
  readonly skillIds: string[];
}

export function AgentCapabilityEditor({
  api,
  node,
  roles,
  capabilities,
  models,
  running,
  onClose,
  onSaved,
  onSave,
  onError,
}: Props): React.JSX.Element | null {
  const inferredRole =
    roles.find((candidate) => candidate.id === node?.roleProfileId) ??
    (!node?.roleProfileId
      ? roles.find(
          (candidate) => candidate.name.trim().toLowerCase() === node?.role.trim().toLowerCase(),
        )
      : undefined);
  const [provider, setProvider] = useState<Provider>(node?.provider ?? 'codex');
  const [model, setModel] = useState(node?.model ?? inferredRole?.model ?? '');
  const [roleId, setRoleId] = useState(node?.roleProfileId ?? inferredRole?.id ?? '');
  const [mcpIds, setMcpIds] = useState<string[]>(() => [
    ...(node?.mcpIds?.length ? node.mcpIds : (inferredRole?.mcpIds ?? [])),
  ]);
  const [skillIds, setSkillIds] = useState<string[]>(() => [
    ...(node?.skillIds?.length ? node.skillIds : (inferredRole?.skillIds ?? [])),
  ]);
  const [busy, setBusy] = useState(false);
  const selectable = useMemo(
    () =>
      capabilities.filter(
        (item) =>
          isCapabilityAvailable(item) &&
          item.policy === 'curated' &&
          isCapabilityCompatible(item, provider),
      ),
    [capabilities, provider],
  );
  const inherited = groupCapabilityItems(
    capabilities.filter(
      (item) =>
        isCapabilityAvailable(item) &&
        item.policy === 'provider_default' &&
        (item.provider === 'all' || item.provider === provider),
    ),
  ).map(({ item }) => item);
  const selectedItems = groupCapabilityItems(selectable, [...mcpIds, ...skillIds])
    .filter(({ ids, item }) =>
      ids.some((id) => (item.kind === 'mcp' ? mcpIds : skillIds).includes(id)),
    )
    .map(({ item }) => item);
  if (!node) return null;
  const toggle = (ids: readonly string[], kind: CapabilityItem['kind']): void => {
    const setter = kind === 'mcp' ? setMcpIds : setSkillIds;
    setter((current) => {
      const selected = ids.some((id) => current.includes(id));
      const representative = ids[0];
      if (selected) return current.filter((value) => !ids.includes(value));
      return representative ? [...current, representative] : current;
    });
  };
  const applyRole = (nextRoleId: string): void => {
    setRoleId(nextRoleId);
    const role = roles.find((candidate) => candidate.id === nextRoleId);
    if (!role) return;
    if (role.defaultProvider) setProvider(role.defaultProvider);
    setModel(role.model ?? '');
    setMcpIds([...role.mcpIds]);
    setSkillIds([...role.skillIds]);
  };
  return (
    <div className="capability-backdrop" role="presentation" onMouseDown={onClose}>
      <form
        className="agent-capability-editor"
        onMouseDown={(event) => event.stopPropagation()}
        onSubmit={(event) => {
          event.preventDefault();
          setBusy(true);
          const data = new FormData(event.currentTarget);
          const update: AgentEditorUpdate = {
            label: formString(data, 'label'),
            role: formString(data, 'role'),
            provider,
            model,
            autoStart: data.get('autoStart') === 'on',
            roleProfileId: roleId,
            mcpIds,
            skillIds,
          };
          const save = onSave
            ? onSave(update)
            : api.updateNode(node.id, update).then(() => undefined);
          void save
            .then(onSaved)
            .catch((reason: unknown) =>
              onError(reason instanceof Error ? reason.message : 'Falha ao editar agente.'),
            )
            .finally(() => setBusy(false));
        }}
      >
        <header>
          <div>
            <span>NODE GOVERNANCE</span>
            <h2>
              <Bot size={18} /> Editar agente
            </h2>
          </div>
          <button type="button" onClick={onClose}>
            <X size={16} />
          </button>
        </header>
        {running ? (
          <div className="restart-required">
            <AlertTriangle size={14} />
            <span>Configurações serão aplicadas no próximo restart deste agente.</span>
          </div>
        ) : null}
        {(node.mcpIds?.length ?? 0) + (node.skillIds?.length ?? 0) >
        mcpIds.length + skillIds.length ? (
          <div className="restart-required">
            <AlertTriangle size={14} />
            <span>
              Referências bloqueadas, ausentes ou incompatíveis serão removidas ao salvar.
            </span>
          </div>
        ) : null}
        <div className="capability-cost-summary">
          <span>
            Herdado: ≈ {inherited.reduce((sum, item) => sum + (item.estimatedTokens ?? 0), 0)}{' '}
            tokens
          </span>
          <span>
            Selecionado: ≈{' '}
            {selectedItems.reduce((sum, item) => sum + (item.estimatedTokens ?? 0), 0)} tokens
          </span>
          <span>
            Skills sob demanda: ≈{' '}
            {selectedItems.reduce((sum, item) => sum + (item.contentTokens ?? 0), 0)} tokens
          </span>
        </div>
        <div className="agent-editor-grid">
          <label>
            Nome
            <input name="label" required maxLength={80} defaultValue={node.label} />
          </label>
          <label>
            Provider
            <select
              value={provider}
              onChange={(event) => {
                setProvider(event.target.value as Provider);
                setModel('');
                setMcpIds([]);
                setSkillIds([]);
              }}
            >
              {['claude', 'codex', 'pi', 'opencode'].map((value) => (
                <option key={value}>{value}</option>
              ))}
            </select>
          </label>
          <ModelSelector provider={provider} value={model} inventory={models} onChange={setModel} />
          <label>
            Role preset
            <select value={roleId} onChange={(event) => applyRole(event.target.value)}>
              <option value="">Role personalizada</option>
              {roles.map((role) => (
                <option key={role.id} value={role.id}>
                  {role.name}
                </option>
              ))}
            </select>
          </label>
          <label>
            Role / função
            <input name="role" maxLength={240} defaultValue={node.role} />
          </label>
        </div>
        <CapabilityPicker
          items={selectable.filter((item) => item.kind === 'mcp')}
          selected={mcpIds}
          onToggle={toggle}
          title="MCP SERVERS CURADOS"
        />
        <CapabilityPicker
          items={selectable.filter((item) => item.kind === 'skill')}
          selected={skillIds}
          onToggle={toggle}
          title="SKILLS CURADAS"
        />
        <label className="dialog-checkbox-row">
          <input name="autoStart" type="checkbox" defaultChecked={node.autoStart} />
          Iniciar automaticamente
        </label>
        <footer>
          <button type="button" onClick={onClose}>
            Cancelar
          </button>
          <button type="submit" disabled={busy}>
            {running ? (
              <>
                <RotateCcw size={13} /> Salvar para restart
              </>
            ) : (
              'Salvar agente'
            )}
          </button>
        </footer>
      </form>
    </div>
  );
}

export function CapabilityPicker({
  items,
  selected,
  onToggle,
  title,
}: {
  readonly items: readonly CapabilityItem[];
  readonly selected: readonly string[];
  readonly onToggle: (ids: readonly string[], kind: CapabilityItem['kind']) => void;
  readonly title: string;
}): React.JSX.Element {
  const groups = groupCapabilityItems(items, selected);
  return (
    <fieldset className="capability-picker">
      <legend>{title}</legend>
      {groups.length === 0 ? (
        <p>Nenhum item curado compatível.</p>
      ) : (
        <div>
          {groups.map(({ item, ids }) => (
            <label key={item.id}>
              <input
                type="checkbox"
                checked={ids.some((id) => selected.includes(id))}
                onChange={() => onToggle(ids, item.kind)}
              />
              <span>
                <strong>{item.name}</strong>
                {item.description ? (
                  <small className="capability-picker-description" title={item.description}>
                    {item.description}
                  </small>
                ) : null}
                <small>
                  {item.origin} · {item.provider} · ≈ {item.estimatedTokens ?? 0} tokens
                  {ids.length > 1 ? ` · ${ids.length.toString()} fontes idênticas` : ''}
                </small>
              </span>
            </label>
          ))}
        </div>
      )}
    </fieldset>
  );
}

function formString(data: FormData, key: string): string {
  const value = data.get(key);
  return typeof value === 'string' ? value : '';
}
