import { FitAddon } from '@xterm/addon-fit';
import { SearchAddon } from '@xterm/addon-search';
import { WebLinksAddon } from '@xterm/addon-web-links';
import { WebglAddon } from '@xterm/addon-webgl';
import { Terminal } from '@xterm/xterm';
import '@xterm/xterm/css/xterm.css';
import { useEffect, useRef } from 'react';
import type { BackendConnection } from '../../shared/ipc';

interface TerminalPanelProps {
  readonly connection: BackendConnection;
  readonly sessionId: string;
}

export function TerminalPanel({ connection, sessionId }: TerminalPanelProps): React.JSX.Element {
  const containerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return undefined;
    const terminal = new Terminal({
      cursorBlink: true,
      cursorStyle: 'bar',
      fontFamily: "'DM Mono', Consolas, monospace",
      fontSize: 12,
      lineHeight: 1.15,
      scrollback: 1000,
      theme: {
        background: '#080a0b',
        foreground: '#d8dfda',
        cursor: '#b7f34a',
        selectionBackground: '#b7f34a33',
      },
    });
    const fit = new FitAddon();
    terminal.loadAddon(fit);
    terminal.loadAddon(new SearchAddon());
    terminal.loadAddon(new WebLinksAddon());
    terminal.open(container);
    try {
      terminal.loadAddon(new WebglAddon());
    } catch {
      // The DOM renderer remains a safe fallback when a GPU context is unavailable.
    }

    let disposed = false;
    let socket: WebSocket | null = null;
    let reconnectTimer: number | undefined;
    let resizeTimer: number | undefined;
    let animationFrame: number | undefined;
    let pending: Uint8Array[] = [];
    const encoder = new TextEncoder();
    const endpoint = `${connection.baseUrl.replace(/^http/, 'ws')}/ws/terminals/${sessionId}?token=${encodeURIComponent(connection.token)}`;

    const flush = (): void => {
      animationFrame = undefined;
      for (const chunk of pending) terminal.write(chunk);
      pending = [];
    };

    const connect = (): void => {
      if (disposed) return;
      const next = new WebSocket(endpoint);
      next.binaryType = 'arraybuffer';
      socket = next;
      next.addEventListener('open', () => {
        terminal.reset();
        fit.fit();
        next.send(JSON.stringify({ type: 'resize', cols: terminal.cols, rows: terminal.rows }));
      });
      next.addEventListener('message', (event: MessageEvent<unknown>) => {
        if (!(event.data instanceof ArrayBuffer)) return;
        pending.push(new Uint8Array(event.data));
        animationFrame ??= requestAnimationFrame(flush);
      });
      next.addEventListener('close', () => {
        if (!disposed) reconnectTimer = window.setTimeout(connect, 800);
      });
    };

    const input = terminal.onData((data) => {
      if (socket?.readyState === WebSocket.OPEN) socket.send(encoder.encode(data));
    });
    const resized = terminal.onResize(({ cols, rows }) => {
      if (socket?.readyState === WebSocket.OPEN) {
        socket.send(JSON.stringify({ type: 'resize', cols, rows }));
      }
    });
    const observer = new ResizeObserver(() => {
      if (resizeTimer) window.clearTimeout(resizeTimer);
      resizeTimer = window.setTimeout(() => fit.fit(), 50);
    });
    observer.observe(container);
    connect();

    return () => {
      disposed = true;
      if (reconnectTimer) window.clearTimeout(reconnectTimer);
      if (resizeTimer) window.clearTimeout(resizeTimer);
      if (animationFrame) cancelAnimationFrame(animationFrame);
      observer.disconnect();
      input.dispose();
      resized.dispose();
      socket?.close();
      terminal.dispose();
    };
  }, [connection, sessionId]);

  return <div ref={containerRef} className="terminal-surface" aria-label="PowerShell terminal" />;
}
