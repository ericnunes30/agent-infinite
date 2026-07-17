import type { AgentInfiniteDesktopApi } from '../../shared/ipc';

declare global {
  interface Window {
    readonly agentInfinite: AgentInfiniteDesktopApi;
  }
}

export {};
