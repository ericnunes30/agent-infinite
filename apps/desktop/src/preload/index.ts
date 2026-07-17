import { contextBridge, ipcRenderer } from 'electron';
import type { AgentInfiniteDesktopApi, BackendState, ColorTheme } from '../shared/ipc';

const api: AgentInfiniteDesktopApi = Object.freeze({
  platform: process.platform,
  versions: Object.freeze({
    electron: process.versions.electron,
    chrome: process.versions.chrome,
    node: process.versions.node,
  }),
  getBackendState: () => ipcRenderer.invoke('backend:get-state') as Promise<BackendState>,
  restartBackend: () => ipcRenderer.invoke('backend:restart') as Promise<void>,
  setTheme: (theme: ColorTheme) => ipcRenderer.invoke('theme:set', theme) as Promise<void>,
  selectWorkspace: () => ipcRenderer.invoke('workspace:select') as Promise<string | null>,
  onBackendState: (listener: (state: BackendState) => void) => {
    const handler = (_event: Electron.IpcRendererEvent, state: BackendState): void =>
      listener(state);
    ipcRenderer.on('backend:state', handler);
    return () => ipcRenderer.removeListener('backend:state', handler);
  },
});

contextBridge.exposeInMainWorld('agentInfinite', api);
