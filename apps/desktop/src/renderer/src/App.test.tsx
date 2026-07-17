import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { vi } from 'vitest';
import { App } from './App';

vi.mock('./TerminalPanel', () => ({ TerminalPanel: () => null }));
vi.mock('./CanvasWorkspace', () => ({ CanvasWorkspace: () => null }));
const setTheme = vi.fn(() => Promise.resolve());

describe('App', () => {
  beforeEach(() => {
    window.localStorage.clear();
    document.documentElement.dataset.theme = 'dark';
    setTheme.mockClear();
    Object.defineProperty(window, 'agentInfinite', {
      configurable: true,
      value: {
        platform: 'win32',
        versions: {},
        getBackendState: () => Promise.resolve({ status: 'starting' }),
        restartBackend: () => Promise.resolve(),
        selectWorkspace: () => Promise.resolve(null),
        setTheme,
        onBackendState: () => () => undefined,
      },
    });
  });

  it('renders the backend lifecycle launchpad', () => {
    render(<App />);
    expect(screen.getByText('AGENT INFINITE')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /backend starting/i })).toBeInTheDocument();
    expect(screen.getByText('SYSTEM LINK')).toBeInTheDocument();
  });

  it('switches to the light theme and persists the preference', async () => {
    render(<App />);
    fireEvent.click(screen.getByRole('button', { name: 'Ativar tema claro' }));

    await waitFor(() => expect(document.documentElement.dataset.theme).toBe('light'));
    expect(window.localStorage.getItem('agent-infinite:theme:v1')).toBe('light');
    expect(setTheme).toHaveBeenLastCalledWith('light');
  });
});
