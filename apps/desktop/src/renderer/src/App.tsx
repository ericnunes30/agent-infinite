import {
  AlertTriangle,
  Box,
  FolderGit2,
  Moon,
  RadioTower,
  RotateCw,
  Sun,
  Users,
} from 'lucide-react';
import { useEffect, useState } from 'react';
import type { BackendState, ColorTheme } from '../../shared/ipc';
import { CanvasWorkspace } from './CanvasWorkspace';
import { LocalApi } from './api';
import type { Snapshot } from './domain';

const initialBackend: BackendState = { status: 'starting' };
const themeStorageKey = 'agent-infinite:theme:v1';

function initialTheme(): ColorTheme {
  const saved = window.localStorage.getItem(themeStorageKey);
  return saved === 'light' ? 'light' : 'dark';
}

export function App(): React.JSX.Element {
  const [backend, setBackend] = useState<BackendState>(initialBackend);
  const [workspace, setWorkspace] = useState<Snapshot | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [opening, setOpening] = useState(false);
  const [attachedBackend, setAttachedBackend] = useState<string | null>(null);
  const [theme, setTheme] = useState<ColorTheme>(initialTheme);
  const [surface, setSurface] = useState<'canvas' | 'teams'>('canvas');

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    document.documentElement.style.colorScheme = theme;
    window.localStorage.setItem(themeStorageKey, theme);
    document
      .querySelector('meta[name="theme-color"]')
      ?.setAttribute('content', theme === 'dark' ? '#090b0d' : '#eef1eb');
    void window.agentInfinite.setTheme(theme);
  }, [theme]);

  useEffect(() => {
    void window.agentInfinite.getBackendState().then(setBackend);
    return window.agentInfinite.onBackendState(setBackend);
  }, []);

  useEffect(() => {
    const connection = backend.connection;
    const previous = window.localStorage.getItem('agent-infinite:last-workspace');
    if (!connection || !previous || attachedBackend === connection.baseUrl) return;
    setAttachedBackend(connection.baseUrl);
    void new LocalApi(connection)
      .openWorkspace(previous)
      .then(setWorkspace)
      .catch((reason: unknown) => {
        setWorkspace(null);
        setError(reason instanceof Error ? reason.message : 'Workspace recovery failed.');
      });
  }, [attachedBackend, backend.connection]);

  async function openRepository(): Promise<void> {
    const connection = backend.connection;
    if (!connection) return;
    const path = await window.agentInfinite.selectWorkspace();
    if (!path) return;
    setOpening(true);
    setError(null);
    try {
      const snapshot = await new LocalApi(connection).openWorkspace(path);
      window.localStorage.setItem('agent-infinite:last-workspace', path);
      setAttachedBackend(connection.baseUrl);
      setWorkspace(snapshot);
      setSurface('canvas');
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'The workspace could not be opened.');
    } finally {
      setOpening(false);
    }
  }

  const runtimeLabel =
    backend.status === 'ready' ? `runtime ${backend.connection?.version ?? ''}` : backend.status;

  return (
    <main className="shell">
      <header className="titlebar">
        <div className="brand-mark" aria-hidden="true">
          <span />
          <span />
          <span />
        </div>
        <p>AGENT INFINITE</p>
        <div className="titlebar-tools">
          {workspace && (
            <button
              type="button"
              className={`surface-toggle ${surface === 'teams' ? 'active' : ''}`}
              aria-label="Gerenciar Agent Teams"
              title="Gerenciar Agent Teams"
              aria-pressed={surface === 'teams'}
              onClick={() => setSurface((current) => (current === 'canvas' ? 'teams' : 'canvas'))}
            >
              <Users size={15} aria-hidden="true" />
            </button>
          )}
          <button
            type="button"
            className="theme-toggle"
            aria-label={theme === 'dark' ? 'Ativar tema claro' : 'Ativar tema escuro'}
            title={theme === 'dark' ? 'Ativar tema claro' : 'Ativar tema escuro'}
            aria-pressed={theme === 'light'}
            onClick={() => setTheme((current) => (current === 'dark' ? 'light' : 'dark'))}
          >
            <Sun className="theme-icon theme-icon-light" size={16} aria-hidden="true" />
            <Moon className="theme-icon theme-icon-dark" size={16} aria-hidden="true" />
          </button>
          <div className={`titlebar-status status-${backend.status}`}>
            <i /> {runtimeLabel}
          </div>
        </div>
      </header>

      {!workspace && (
        <>
          <section className="launchpad">
            <div className="eyebrow">
              <RadioTower size={14} /> CONTROL PLANE / LOCAL
            </div>
            <h1>
              Direct the work.
              <br />
              <em>Keep every thread visible.</em>
            </h1>
            <p className="lede">
              Open a Git repository to assemble isolated agent teams, live terminals, and explicit
              delegation paths on one operational canvas.
            </p>
            {backend.status === 'ready' ? (
              <div className="launch-actions">
                <button type="button" disabled={opening} onClick={() => void openRepository()}>
                  <FolderGit2 size={18} /> {opening ? 'Validating repository…' : 'Open repository'}
                </button>
              </div>
            ) : (
              <button type="button" onClick={() => void window.agentInfinite.restartBackend()}>
                <RotateCw size={18} />{' '}
                {backend.status === 'starting' ? 'Backend starting…' : 'Restart backend'}
              </button>
            )}
            {(error ?? backend.message) && (
              <div className="inline-error" role="alert">
                <AlertTriangle size={15} /> {error ?? backend.message}
              </div>
            )}
          </section>
          <aside className="system-card" aria-label="Runtime status">
            <div className="card-heading">
              <Box size={15} /> SYSTEM LINK
            </div>
            <dl>
              <div>
                <dt>Desktop</dt>
                <dd>Electron</dd>
              </div>
              <div>
                <dt>Core</dt>
                <dd>Go</dd>
              </div>
              <div>
                <dt>Runtime</dt>
                <dd>{backend.status.toUpperCase()}</dd>
              </div>
              <div>
                <dt>Workspace</dt>
                <dd>NONE</dd>
              </div>
            </dl>
            <div className="signal">
              <span />
              <span />
              <span />
              <span />
              <span />
            </div>
          </aside>
        </>
      )}

      {workspace && backend.connection && (
        <CanvasWorkspace
          key={workspace.workspaceId}
          connection={backend.connection}
          initial={workspace}
          theme={theme}
          onError={setError}
          onOpenWorkspace={() => void openRepository()}
          surface={surface}
          onSurfaceChange={setSurface}
        />
      )}
      {workspace && error && (
        <div className="toast-error" role="alert">
          {error}
        </div>
      )}
      <footer>
        <span>v0.5.0 / {surface === 'teams' ? 'AGENT TEAMS' : 'CANVAS'}</span>
        <span>{workspace ? 'WORKSPACE ATTACHED' : 'NO WORKSPACE ATTACHED'}</span>
      </footer>
    </main>
  );
}
