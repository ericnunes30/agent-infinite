export type BackendStatus = 'starting' | 'ready' | 'disconnected' | 'error';
export type ColorTheme = 'dark' | 'light';
export interface BackendConnection {
  readonly baseUrl: string;
  readonly token: string;
  readonly version: string;
}
export interface BackendState {
  readonly status: BackendStatus;
  readonly connection?: BackendConnection;
  readonly message?: string;
}
export interface AgentInfiniteDesktopApi {
  readonly platform: string;
  readonly versions: Readonly<Record<string, string>>;
  getBackendState(): Promise<BackendState>;
  restartBackend(): Promise<void>;
  setTheme(theme: ColorTheme): Promise<void>;
  selectWorkspace(): Promise<string | null>;
  onBackendState(listener: (state: BackendState) => void): () => void;
}
