import { ShieldCheck, X } from 'lucide-react';
import { useMemo, useState } from 'react';
import { CapabilityPicker } from './AgentCapabilityEditor';
import type { LocalApi } from './api';
import { isCapabilityAvailable, isCapabilityCompatible } from './domain';
import type { CapabilityItem, ModelInventory, Provider, RoleProfile } from './domain';
import { ModelSelector } from './ModelSelector';

interface Props {
  readonly api: LocalApi;
  readonly role: RoleProfile | null;
  readonly capabilities: readonly CapabilityItem[];
  readonly models: ModelInventory;
  readonly onClose: () => void;
  readonly onSaved: () => Promise<void>;
  readonly onError: (message: string) => void;
}

export function RoleProfileEditor({
  api,
  role,
  capabilities,
  models,
  onClose,
  onSaved,
  onError,
}: Props): React.JSX.Element {
  const [provider, setProvider] = useState<Provider>(role?.defaultProvider ?? 'codex');
  const [model, setModel] = useState(role?.model ?? '');
  const initiallyCompatible = (id: string, kind: CapabilityItem['kind']): boolean =>
    capabilities.some(
      (item) =>
        item.id === id &&
        item.kind === kind &&
        item.policy === 'curated' &&
        isCapabilityAvailable(item) &&
        isCapabilityCompatible(item, role?.defaultProvider ?? 'codex'),
    );
  const [mcpIds, setMcpIds] = useState<string[]>(
    [...(role?.mcpIds ?? [])].filter((id) => initiallyCompatible(id, 'mcp')),
  );
  const [skillIds, setSkillIds] = useState<string[]>(
    [...(role?.skillIds ?? [])].filter((id) => initiallyCompatible(id, 'skill')),
  );
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
  const toggle = (ids: readonly string[], kind: CapabilityItem['kind']): void => {
    const setter = kind === 'mcp' ? setMcpIds : setSkillIds;
    setter((current) => {
      const selected = ids.some((id) => current.includes(id));
      const representative = ids[0];
      if (selected) return current.filter((value) => !ids.includes(value));
      return representative ? [...current, representative] : current;
    });
  };
  return (
    <div className="capability-backdrop" role="presentation" onMouseDown={onClose}>
      <form
        className="agent-capability-editor role-profile-editor"
        onMouseDown={(event) => event.stopPropagation()}
        onSubmit={(event) => {
          event.preventDefault();
          setBusy(true);
          const data = new FormData(event.currentTarget);
          const name = data.get('name');
          void api
            .saveRoleProfile({
              ...(role ? { id: role.id, builtin: role.builtin ?? false } : {}),
              name: typeof name === 'string' ? name : '',
              defaultProvider: provider,
              model,
              mcpIds,
              skillIds,
            })
            .then(onSaved)
            .catch((reason: unknown) =>
              onError(reason instanceof Error ? reason.message : 'Falha ao salvar role.'),
            )
            .finally(() => setBusy(false));
        }}
      >
        <header>
          <div>
            <span>ROLE PRESET</span>
            <h2>
              <ShieldCheck size={18} /> {role ? 'Editar role' : 'Nova role'}
            </h2>
          </div>
          <button type="button" onClick={onClose}>
            <X size={16} />
          </button>
        </header>
        <div className="agent-editor-grid">
          <label>
            Nome
            <input name="name" required maxLength={80} defaultValue={role?.name} autoFocus />
          </label>
          <label>
            Provider padrão
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
          <ModelSelector
            provider={provider}
            value={model}
            inventory={models}
            onChange={setModel}
            label="Modelo padrão"
          />
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
        <footer>
          {role && !role.builtin ? (
            <button
              type="button"
              onClick={() => {
                setBusy(true);
                void api
                  .deleteRoleProfile(role.id)
                  .then(onSaved)
                  .catch((reason: unknown) =>
                    onError(reason instanceof Error ? reason.message : 'Falha ao excluir role.'),
                  )
                  .finally(() => setBusy(false));
              }}
            >
              Excluir role
            </button>
          ) : null}
          <button type="button" onClick={onClose}>
            Cancelar
          </button>
          <button type="submit" disabled={busy}>
            Salvar role
          </button>
        </footer>
      </form>
    </div>
  );
}
