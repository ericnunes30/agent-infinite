import { app, BrowserWindow, dialog, ipcMain, nativeTheme, shell } from 'electron';
import { join } from 'node:path';
import type { ColorTheme } from '../shared/ipc';
import { BackendSupervisor, type BackendState } from './backend';

let mainWindow: BrowserWindow | null = null;
let backend: BackendSupervisor | null = null;
let colorTheme: ColorTheme = 'dark';

const themeChrome: Record<ColorTheme, { readonly background: string; readonly symbols: string }> = {
  dark: { background: '#090b0d', symbols: '#dfe7e2' },
  light: { background: '#eef1eb', symbols: '#253129' },
};

function applyNativeTheme(theme: ColorTheme): void {
  colorTheme = theme;
  nativeTheme.themeSource = theme;
  const chrome = themeChrome[theme];
  for (const window of BrowserWindow.getAllWindows()) {
    window.setBackgroundColor(chrome.background);
    window.setTitleBarOverlay({
      color: chrome.background,
      symbolColor: chrome.symbols,
      height: 42,
    });
  }
}

function createWindow(): void {
  mainWindow = new BrowserWindow({
    width: 1440,
    height: 900,
    minWidth: 1060,
    minHeight: 680,
    backgroundColor: themeChrome[colorTheme].background,
    show: false,
    titleBarStyle: 'hidden',
    titleBarOverlay: {
      color: themeChrome[colorTheme].background,
      symbolColor: themeChrome[colorTheme].symbols,
      height: 42,
    },
    webPreferences: {
      preload: join(__dirname, '../preload/index.js'),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
    },
  });

  mainWindow.once('ready-to-show', () => mainWindow?.show());
  mainWindow.webContents.setWindowOpenHandler(({ url }) => {
    void shell.openExternal(url);
    return { action: 'deny' };
  });

  if (process.env.ELECTRON_RENDERER_URL) {
    void mainWindow.loadURL(process.env.ELECTRON_RENDERER_URL);
  } else {
    void mainWindow.loadFile(join(__dirname, '../renderer/index.html'));
  }
}

void app.whenReady().then(() => {
  const binaryPath = app.isPackaged
    ? join(process.resourcesPath, 'agent-infinite-backend.exe')
    : join(app.getAppPath(), 'resources', 'agent-infinite-backend.exe');
  backend = new BackendSupervisor(binaryPath, (state: BackendState) => {
    mainWindow?.webContents.send('backend:state', state);
  });
  ipcMain.handle(
    'backend:get-state',
    () => backend?.state ?? { status: 'error', message: 'Backend unavailable.' },
  );
  ipcMain.handle('backend:restart', () => backend?.restart());
  ipcMain.handle('theme:set', (_event, theme: unknown) => {
    if (theme !== 'dark' && theme !== 'light') throw new Error('Invalid color theme.');
    applyNativeTheme(theme);
  });
  ipcMain.handle('workspace:select', async () => {
    const options: Electron.OpenDialogOptions = {
      title: 'Open a Git repository',
      properties: ['openDirectory'],
    };
    const result = mainWindow
      ? await dialog.showOpenDialog(mainWindow, options)
      : await dialog.showOpenDialog(options);
    return result.canceled ? null : (result.filePaths[0] ?? null);
  });
  createWindow();
  backend.start();
  app.on('activate', () => {
    if (BrowserWindow.getAllWindows().length === 0) createWindow();
  });
});

app.on('before-quit', () => backend?.stop());

app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') app.quit();
});
