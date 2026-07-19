/* eslint-disable @typescript-eslint/no-misused-promises, @typescript-eslint/no-base-to-string, @typescript-eslint/no-unsafe-assignment, @typescript-eslint/no-unsafe-argument, @typescript-eslint/restrict-template-expressions, @typescript-eslint/prefer-nullish-coalescing */
import {
  AlertTriangle,
  Ban,
  DatabaseZap,
  Cpu,
  Plus,
  RefreshCw,
  ShieldCheck,
  Sparkles,
  X,
} from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import type { CapabilityItem, CapabilityPolicy, ModelInventory, Provider } from './domain';
import type { LocalApi } from './api';

const PROVIDERS: (Provider | 'all')[] = ['all', 'claude', 'codex', 'pi', 'opencode'];
const POLICY_LABEL: Record<CapabilityPolicy, string> = {
  provider_default: 'Preservar provider',
  curated: 'Curado por agente',
  blocked: 'Bloqueado no app',
};
const BULK_POLICY_LABEL: Record<CapabilityPolicy, string> = {
  provider_default: 'Preservar todos nos providers',
  curated: 'Curar todos por agente',
  blocked: 'Bloquear todos no Agent Infinite',
};

interface Props {
  readonly api: LocalApi;
  readonly onClose: () => void;
  readonly onError: (message: string) => void;
}

export function CapabilityGovernance({ api, onClose, onError }: Props): React.JSX.Element {
  const [items, setItems] = useState<CapabilityItem[]>([]);
  const [models, setModels] = useState<ModelInventory>({ providers: [] });
  const [tab, setTab] = useState<'mcp' | 'skill' | 'model'>('mcp');
  const [provider, setProvider] = useState<Provider | 'all'>('all');
  const [origin, setOrigin] = useState('all');
  const [scope, setScope] = useState('all');
  const [policy, setPolicy] = useState('all');
  const [status, setStatus] = useState('all');
  const [bulkPolicy, setBulkPolicy] = useState<CapabilityPolicy | ''>('');
  const [busy, setBusy] = useState(false);
  const [editingItem, setEditingItem] = useState<CapabilityItem | 'new' | null>(null);
  const [scanErrors, setScanErrors] = useState<Record<string, string>>({});

  const load = async (): Promise<void> => {
    const [result, modelResult] = await Promise.all([
      api.capabilityInventory(),
      api.modelInventory(),
    ]);
    setItems(result.items);
    setModels(modelResult);
  };
  useEffect(() => {
    void load().catch((reason: unknown) =>
      onError(reason instanceof Error ? reason.message : 'Falha ao carregar capacidades.'),
    );
  }, [api]);

  const visible = useMemo(
    () =>
      items.filter(
        (item) =>
          tab !== 'model' &&
          !item.archived &&
          item.kind === tab &&
          (provider === 'all' || item.provider === provider || item.provider === 'all') &&
          (origin === 'all' || item.origin === origin) &&
          (scope === 'all' || item.scope === scope) &&
          (policy === 'all' || item.policy === policy) &&
          (status === 'all' || item.status === status),
      ),
    [items, origin, policy, provider, scope, status, tab],
  );
  const tokenTotal = visible.reduce((sum, item) => sum + (item.estimatedTokens ?? 0), 0);
  const bulkEligible = visible.filter((item) => item.origin !== 'internal');

  const scan = async (): Promise<void> => {
    setBusy(true);
    try {
      if (tab === 'model') {
        setModels(await api.scanModels(provider === 'all' ? undefined : provider));
      } else {
        const result = await api.scanCapabilities();
        setItems(result.items);
        setScanErrors(result.scanErrors);
      }
    } catch (reason) {
      onError(reason instanceof Error ? reason.message : 'Falha ao escanear providers.');
    } finally {
      setBusy(false);
    }
  };

  const updatePolicy = async (item: CapabilityItem, policy: CapabilityPolicy): Promise<void> => {
    try {
      const updated = await api.setCapabilityPolicy(item.id, item.kind, policy);
      setItems((current) =>
        current.map((candidate) => (candidate.id === updated.id ? updated : candidate)),
      );
    } catch (reason) {
      onError(reason instanceof Error ? reason.message : 'Falha ao alterar política.');
    }
  };

  const updateVisiblePolicies = async (): Promise<void> => {
    if (!bulkPolicy || bulkEligible.length === 0) return;
    const capabilityType = tab === 'mcp' ? 'MCPs' : 'skills';
    if (
      !window.confirm(
        `${BULK_POLICY_LABEL[bulkPolicy]} para ${bulkEligible.length} ${capabilityType} exibidos?`,
      )
    )
      return;
    setBusy(true);
    try {
      const result = await api.setCapabilityPolicies(
        bulkEligible.map((item) => item.id),
        bulkPolicy,
      );
      const updated = new Map(result.items.map((item) => [item.id, item]));
      setItems((current) => current.map((item) => updated.get(item.id) ?? item));
      setBulkPolicy('');
    } catch (reason) {
      onError(reason instanceof Error ? reason.message : 'Falha ao alterar políticas em lote.');
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="capability-backdrop" role="presentation" onMouseDown={onClose}>
      <section
        className="capability-console"
        role="dialog"
        aria-modal="true"
        aria-labelledby="capability-title"
        onMouseDown={(event) => event.stopPropagation()}
      >
        <header className="capability-header">
          <div>
            <span>GLOBAL GOVERNANCE / 0.13</span>
            <h2 id="capability-title">MCPs, Skills & Models</h2>
            <p>
              Inventário read-only dos providers e políticas aplicadas apenas aos agentes iniciados
              pelo Agent Infinite.
            </p>
          </div>
          <div className="capability-header-actions">
            <button type="button" onClick={() => void scan()} disabled={busy}>
              <RefreshCw size={14} className={busy ? 'spin' : ''} /> Escanear
            </button>
            <button type="button" className="icon-only" onClick={onClose} aria-label="Fechar">
              <X size={17} />
            </button>
          </div>
        </header>
        <div className="capability-policy-note">
          <ShieldCheck size={15} />
          <strong>Seus CLIs permanecem intactos.</strong>
          <span>Bloqueios usam overlays temporários e não editam configurações externas.</span>
        </div>
        <nav className="capability-tabs" aria-label="Tipo de capacidade">
          <button
            type="button"
            className={tab === 'mcp' ? 'active' : ''}
            onClick={() => setTab('mcp')}
          >
            <DatabaseZap size={14} /> MCP Servers
          </button>
          <button
            type="button"
            className={tab === 'skill' ? 'active' : ''}
            onClick={() => setTab('skill')}
          >
            <Sparkles size={14} /> Skills
          </button>
          <button
            type="button"
            className={tab === 'model' ? 'active' : ''}
            onClick={() => setTab('model')}
          >
            <Cpu size={14} /> Models
          </button>
          <span hidden={tab === 'model'}>
            {visible.length} REGISTROS · ≈ {tokenTotal.toLocaleString()} TOKENS
          </span>
          {tab === 'model' ? (
            <span>
              {models.providers.reduce((sum, catalog) => sum + catalog.models.length, 0)} MODELOS
            </span>
          ) : null}
          <button
            type="button"
            hidden={tab === 'model'}
            className="capability-add"
            onClick={() => setEditingItem('new')}
          >
            <Plus size={14} /> Adicionar
          </button>
        </nav>
        <div className="capability-provider-filter">
          {PROVIDERS.map((name) => (
            <button
              type="button"
              key={name}
              className={provider === name ? 'active' : ''}
              onClick={() => setProvider(name)}
            >
              {name}
            </button>
          ))}
        </div>
        <div className="capability-advanced-filters" hidden={tab === 'model'}>
          <label>
            Origem
            <select value={origin} onChange={(event) => setOrigin(event.target.value)}>
              {['all', 'managed', 'external', 'internal'].map((value) => (
                <option key={value}>{value}</option>
              ))}
            </select>
          </label>
          <label>
            Escopo
            <select value={scope} onChange={(event) => setScope(event.target.value)}>
              {['all', 'user', 'project', 'plugin', 'app', 'session'].map((value) => (
                <option key={value}>{value}</option>
              ))}
            </select>
          </label>
          <label>
            Política
            <select value={policy} onChange={(event) => setPolicy(event.target.value)}>
              {['all', 'provider_default', 'curated', 'blocked'].map((value) => (
                <option key={value}>{value}</option>
              ))}
            </select>
          </label>
          <label>
            Status
            <select value={status} onChange={(event) => setStatus(event.target.value)}>
              {['all', 'new', 'unchanged', 'changed', 'missing', 'scan_error'].map((value) => (
                <option key={value}>{value}</option>
              ))}
            </select>
          </label>
        </div>
        <div className="capability-bulk-policy" hidden={tab === 'model'}>
          <div>
            <ShieldCheck size={14} />
            <span>
              <strong>POLÍTICA EM LOTE</strong>
              <small>Aplica somente aos itens exibidos pela aba e filtros atuais.</small>
            </span>
          </div>
          <select
            aria-label="Política em lote"
            value={bulkPolicy}
            onChange={(event) => setBulkPolicy(event.target.value as CapabilityPolicy | '')}
          >
            <option value="">Selecionar ação…</option>
            {Object.entries(BULK_POLICY_LABEL).map(([value, label]) => (
              <option key={value} value={value}>
                {label}
              </option>
            ))}
          </select>
          <button
            type="button"
            disabled={!bulkPolicy || bulkEligible.length === 0 || busy}
            onClick={() => void updateVisiblePolicies()}
          >
            Aplicar a {bulkEligible.length}
          </button>
        </div>
        {Object.keys(scanErrors).length > 0 ? (
          <div className="capability-scan-errors">
            <AlertTriangle size={14} /> {Object.keys(scanErrors).length} origem(ns) não puderam ser
            analisadas.
          </div>
        ) : null}
        {tab === 'model' ? <ModelCatalog inventory={models} provider={provider} /> : null}
        <div className="capability-list" hidden={tab === 'model'}>
          {visible.map((item) => (
            <CapabilityRow
              key={item.id}
              item={item}
              onError={onError}
              peers={items.filter((candidate) => candidate.groupId === item.groupId)}
              onPolicy={updatePolicy}
              onArchive={async () => {
                await api.archiveCapability(item.id, item.kind);
                await load();
              }}
              onPromote={async () => {
                const raw =
                  item.kind === 'mcp'
                    ? window.prompt(
                        'Informe novamente os segredos, um ENV=valor ou HEADER.Nome=valor por linha. Eles serão protegidos pelo Windows e não entrarão no catálogo.',
                        '',
                      )
                    : '';
                if (item.kind === 'mcp' && raw === null) return;
                const secrets = Object.fromEntries(
                  (raw ?? '')
                    .split(/\r?\n/)
                    .map((line) => line.split('=', 2))
                    .filter((pair) => pair.length === 2 && pair[0]),
                );
                await api.promoteCapability(item.id, item.kind, secrets);
                await load();
              }}
              {...(item.origin === 'managed' ? { onEdit: () => setEditingItem(item) } : {})}
              {...(item.kind === 'mcp'
                ? {
                    onTest: async () => {
                      const result = await api.testMCP(item.id);
                      window.alert(
                        `MCP respondeu via ${result.transport}: ${result.toolCount} tool(s)\n${result.tools.join('\n')}`,
                      );
                    },
                  }
                : {})}
            />
          ))}
          {visible.length === 0 ? (
            <div className="capability-empty">Nenhuma capacidade encontrada neste filtro.</div>
          ) : null}
        </div>
        {editingItem && tab !== 'model' ? (
          <CapabilityEditor
            kind={tab}
            api={api}
            item={editingItem === 'new' ? null : editingItem}
            onClose={() => setEditingItem(null)}
            onSaved={async () => {
              setEditingItem(null);
              await load();
            }}
            onError={onError}
          />
        ) : null}
      </section>
    </div>
  );
}

function ModelCatalog({
  inventory,
  provider,
}: {
  readonly inventory: ModelInventory;
  readonly provider: Provider | 'all';
}): React.JSX.Element {
  const catalogs = inventory.providers.filter(
    (catalog) => provider === 'all' || catalog.provider === provider,
  );
  return (
    <div className="model-catalog">
      {catalogs.map((catalog) => (
        <article key={catalog.provider}>
          <header>
            <div>
              <strong>{catalog.provider}</strong>
              <span className={`model-scan-status status-${catalog.status}`}>{catalog.status}</span>
            </div>
            <small>{catalog.cliVersion || 'CLI não detectado'}</small>
          </header>
          <dl>
            <div>
              <dt>Padrão configurado</dt>
              <dd>{catalog.defaultModel || 'Automático do CLI'}</dd>
            </div>
            <div>
              <dt>Origem</dt>
              <dd title={catalog.defaultSource}>{catalog.defaultSource || 'provider'}</dd>
            </div>
            <div>
              <dt>Última verificação</dt>
              <dd>{catalog.scannedAt ? new Date(catalog.scannedAt).toLocaleString() : '—'}</dd>
            </div>
          </dl>
          {catalog.error ? <p className="capability-diff">{catalog.error}</p> : null}
          <div className="model-catalog-list">
            {catalog.models.map((model) => (
              <div key={model.id}>
                <span>
                  <strong>{model.displayName || model.id}</strong>
                  <code>{model.id}</code>
                </span>
                <small data-status={model.status}>{model.status}</small>
              </div>
            ))}
            {catalog.models.length === 0 ? <p>Nenhum modelo listado.</p> : null}
          </div>
        </article>
      ))}
      {catalogs.length === 0 ? (
        <div className="capability-empty">Catálogo ainda não verificado.</div>
      ) : null}
    </div>
  );
}

function CapabilityRow({
  item,
  peers,
  onPolicy,
  onArchive,
  onPromote,
  onTest,
  onEdit,
  onError,
}: {
  readonly item: CapabilityItem;
  readonly peers: readonly CapabilityItem[];
  readonly onPolicy: (item: CapabilityItem, policy: CapabilityPolicy) => Promise<void>;
  readonly onArchive: () => Promise<void>;
  readonly onPromote: () => Promise<void>;
  readonly onTest?: () => Promise<void>;
  readonly onEdit?: () => void;
  readonly onError: (message: string) => void;
}): React.JSX.Element {
  const run = (action: () => Promise<void>): void => {
    void action().catch((reason: unknown) =>
      onError(reason instanceof Error ? reason.message : 'Falha na operação.'),
    );
  };
  return (
    <article className={`capability-row policy-${item.policy}`}>
      <div className="capability-status-rail" />
      <div className="capability-identity">
        <div>
          <strong>{item.name}</strong>
          <span className={`origin-${item.origin}`}>{item.origin}</span>
          <span>{item.provider}</span>
          <span>{item.scope}</span>
        </div>
        <p>{item.description || item.nativeKey || 'Sem descrição'}</p>
        {item.changes?.length ? (
          <p className="capability-diff">Diff: {item.changes.join(', ')}</p>
        ) : null}
        <small title={item.sourcePath}>{item.sourcePath || 'Biblioteca do Agent Infinite'}</small>
        <div className="capability-provider-matrix" aria-label="Detecção por provider">
          {(['claude', 'codex', 'pi', 'opencode'] as const).map((providerName) => (
            <span
              key={providerName}
              data-detected={peers.some(
                (peer) => peer.provider === providerName || peer.provider === 'all',
              )}
            >
              {providerName.slice(0, 2)}
            </span>
          ))}
          <time>{new Date(item.lastSeenAt).toLocaleString()}</time>
        </div>
      </div>
      <div className="capability-metrics">
        <span>{item.status}</span>
        <strong>≈ {(item.estimatedTokens ?? 0).toLocaleString()}</strong>
        <small>tokens estimados</small>
        {item.kind === 'skill' ? (
          <small>
            {item.metadataTokens ?? 0} meta · {item.contentTokens ?? 0} sob demanda
          </small>
        ) : null}
        {item.kind === 'mcp' ? <small>{item.toolCount ?? '—'} tools</small> : null}
      </div>
      <div className="capability-policy">
        <label htmlFor={`policy-${item.id}`}>Política da sessão</label>
        <select
          id={`policy-${item.id}`}
          value={item.policy}
          disabled={item.origin === 'internal'}
          onChange={(event) => void onPolicy(item, event.target.value as CapabilityPolicy)}
        >
          {Object.entries(POLICY_LABEL).map(([value, label]) => (
            <option key={value} value={value}>
              {label}
            </option>
          ))}
        </select>
        {!item.enforceable ? (
          <small>
            <Ban size={11} /> enforcement indisponível
          </small>
        ) : null}
      </div>
      <div className="capability-row-actions">
        {onEdit ? (
          <button type="button" onClick={onEdit}>
            Editar
          </button>
        ) : null}
        {onTest ? (
          <button type="button" onClick={() => run(onTest)}>
            Testar
          </button>
        ) : null}
        {item.origin === 'external' ? (
          <button type="button" onClick={() => run(onPromote)}>
            Promover
          </button>
        ) : null}
        {item.origin === 'managed' ? (
          <button
            type="button"
            className="capability-archive"
            onClick={() => run(onArchive)}
            aria-label={`Arquivar ${item.name}`}
          >
            <X size={13} />
          </button>
        ) : null}
      </div>
    </article>
  );
}

function CapabilityEditor({
  kind,
  api,
  item,
  onClose,
  onSaved,
  onError,
}: {
  readonly kind: 'mcp' | 'skill';
  readonly api: LocalApi;
  readonly item: CapabilityItem | null;
  readonly onClose: () => void;
  readonly onSaved: () => Promise<void>;
  readonly onError: (message: string) => void;
}): React.JSX.Element {
  const [busy, setBusy] = useState(false);
  const [markdown, setMarkdown] = useState('');
  useEffect(() => {
    if (item?.kind === 'skill')
      void api
        .managedSkillContent(item.id)
        .then((result) => setMarkdown(result.markdown))
        .catch((reason: unknown) =>
          onError(reason instanceof Error ? reason.message : 'Falha ao carregar skill.'),
        );
  }, [api, item, onError]);
  return (
    <div className="capability-editor-shade">
      <form
        className="capability-editor"
        onSubmit={async (event) => {
          event.preventDefault();
          setBusy(true);
          const data = new FormData(event.currentTarget);
          try {
            if (kind === 'skill') {
              await api.saveManagedSkill({
                ...(item ? { id: item.id } : {}),
                name: String(data.get('name')),
                description: String(data.get('description')),
                provider: String(data.get('provider')) as Provider | 'all',
                markdown,
              });
            } else {
              const parsed = JSON.parse(String(data.get('spec'))) as Record<string, unknown>;
              const secrets = Object.fromEntries(
                String(data.get('secrets'))
                  .split(/\r?\n/)
                  .map((line) => line.split('=', 2))
                  .filter((pair) => pair.length === 2 && pair[0]),
              );
              await api.saveManagedMCP({
                ...(item ? { id: item.id } : {}),
                name: String(data.get('name')),
                description: String(data.get('description')),
                provider: String(data.get('provider')) as Provider | 'all',
                spec: parsed,
                secrets,
              });
            }
            await onSaved();
          } catch (reason) {
            onError(reason instanceof Error ? reason.message : 'Capacidade inválida.');
          } finally {
            setBusy(false);
          }
        }}
      >
        <header>
          <div>
            <span>MANAGED / {kind.toUpperCase()}</span>
            <h3>{item ? 'Editar capacidade' : 'Nova capacidade'}</h3>
          </div>
          <button type="button" onClick={onClose}>
            <X size={15} />
          </button>
        </header>
        <label>
          Nome
          <input name="name" required maxLength={80} autoFocus defaultValue={item?.name} />
        </label>
        <label>
          Descrição
          <input name="description" maxLength={240} defaultValue={item?.description} />
        </label>
        <label>
          Provider
          <select name="provider" defaultValue={item?.provider ?? 'all'}>
            {PROVIDERS.map((name) => (
              <option key={name} value={name}>
                {name}
              </option>
            ))}
          </select>
        </label>
        {kind === 'skill' ? (
          <label>
            SKILL.md
            <input
              type="file"
              accept=".md,text/markdown"
              onChange={(event) => {
                const file = event.target.files?.[0];
                if (file) void file.text().then(setMarkdown);
              }}
            />
            <textarea
              name="markdown"
              required
              rows={14}
              value={markdown}
              onChange={(event) => setMarkdown(event.target.value)}
              placeholder={
                '---\nname: minha-skill\ndescription: Quando usar esta skill\n---\n\nInstruções…'
              }
            />
          </label>
        ) : (
          <>
            <label>
              Spec canônico (JSON)
              <textarea
                name="spec"
                required
                rows={10}
                defaultValue={
                  item?.spec
                    ? JSON.stringify(item.spec, null, 2)
                    : '{\n  "type": "http",\n  "url": "https://example.com/mcp"\n}'
                }
              />
            </label>
            <label>
              Segredos (ENV=valor ou HEADER.Nome=valor)
              <textarea name="secrets" rows={3} className="secret-input" autoComplete="off" />
            </label>
          </>
        )}
        <footer>
          <button type="button" onClick={onClose}>
            Cancelar
          </button>
          <button type="submit" disabled={busy}>
            {busy ? 'SALVANDO…' : 'SALVAR NO CATÁLOGO'}
          </button>
        </footer>
      </form>
    </div>
  );
}
