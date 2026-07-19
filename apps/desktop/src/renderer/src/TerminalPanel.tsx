import { FitAddon } from '@xterm/addon-fit';
import { SearchAddon } from '@xterm/addon-search';
import { WebLinksAddon } from '@xterm/addon-web-links';
import { Terminal } from '@xterm/xterm';
import '@xterm/xterm/css/xterm.css';
import { useEffect, useRef } from 'react';
import type { BackendConnection } from '../../shared/ipc';

interface TerminalPanelProps {
  readonly connection: BackendConnection;
  readonly sessionId: string;
  readonly label?: string;
  readonly compact?: boolean;
  readonly accent?: string;
}

export function TerminalPanel({
  connection,
  sessionId,
  label = 'Terminal do agente',
  compact = false,
  accent = '#b7f34a',
}: TerminalPanelProps): React.JSX.Element {
  const containerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return undefined;
    const terminal = new Terminal({
      cursorBlink: document.visibilityState === 'visible',
      cursorStyle: 'bar',
      fontFamily: "'DM Mono', Consolas, monospace",
      fontSize: compact ? 10 : 12,
      lineHeight: compact ? 1.2 : 1.15,
      scrollback: 1000,
      theme: {
        background: '#080a0b',
        foreground: '#d8dfda',
        cursor: accent,
        selectionBackground: `${accent}33`,
      },
    });
    const fit = new FitAddon();
    terminal.loadAddon(fit);
    terminal.loadAddon(new SearchAddon());
    terminal.loadAddon(new WebLinksAddon());
    terminal.open(container);
    let disposed = false;
    let socket: WebSocket | null = null;
    let reconnectTimer: number | undefined;
    let resizeTimer: number | undefined;
    let animationFrame: number | undefined;
    let pending: Uint8Array[] = [];
    let reconnectDelay = 250;
    const encoder = new TextEncoder();
    const endpoint = `${connection.baseUrl.replace(/^http/, 'ws')}/ws/terminals/${sessionId}?token=${encodeURIComponent(connection.token)}`;

    void import('@xterm/addon-webgl')
      .then(({ WebglAddon }) => {
        if (disposed) return;
        const webgl = new WebglAddon();
        webgl.onContextLoss(() => webgl.dispose());
        terminal.loadAddon(webgl);
      })
      .catch(() => undefined);

    const flush = (): void => {
      animationFrame = undefined;
      if (disposed) {
        pending = [];
        return;
      }
      for (const chunk of pending) terminal.write(chunk);
      pending = [];
    };

    const connect = (): void => {
      if (disposed) return;
      const next = new WebSocket(endpoint);
      next.binaryType = 'arraybuffer';
      socket = next;
      next.addEventListener('open', () => {
        reconnectDelay = 250;
        terminal.reset();
        if (container.clientWidth > 0 && container.clientHeight > 0) fit.fit();
        next.send(JSON.stringify({ type: 'resize', cols: terminal.cols, rows: terminal.rows }));
        terminal.focus();
      });
      next.addEventListener('message', (event: MessageEvent<unknown>) => {
        if (!(event.data instanceof ArrayBuffer)) return;
        pending.push(new Uint8Array(event.data));
        animationFrame ??= requestAnimationFrame(flush);
      });
      next.addEventListener('error', () => next.close());
      next.addEventListener('close', () => {
        if (socket === next) socket = null;
        if (!disposed) {
          reconnectTimer = window.setTimeout(connect, reconnectDelay);
          reconnectDelay = Math.min(reconnectDelay * 2, 4000);
        }
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
      resizeTimer = window.setTimeout(() => {
        if (!disposed && container.clientWidth > 0 && container.clientHeight > 0) fit.fit();
      }, 50);
    });
    observer.observe(container);
    const updateCursor = (): void => {
      terminal.options.cursorBlink = document.visibilityState === 'visible' && document.hasFocus();
    };
    document.addEventListener('visibilitychange', updateCursor);
    window.addEventListener('focus', updateCursor);
    window.addEventListener('blur', updateCursor);
    connect();

    return () => {
      disposed = true;
      if (reconnectTimer) window.clearTimeout(reconnectTimer);
      if (resizeTimer) window.clearTimeout(resizeTimer);
      if (animationFrame) cancelAnimationFrame(animationFrame);
      pending = [];
      observer.disconnect();
      document.removeEventListener('visibilitychange', updateCursor);
      window.removeEventListener('focus', updateCursor);
      window.removeEventListener('blur', updateCursor);
      input.dispose();
      resized.dispose();
      socket?.close();
      terminal.dispose();
    };
  }, [accent, compact, connection, sessionId]);

  return (
    <div
      ref={containerRef}
      className={`terminal-surface${compact ? ' terminal-surface-compact' : ''}`}
      aria-label={label}
      onMouseDown={(event) => event.stopPropagation()}
      onDoubleClick={(event) => event.stopPropagation()}
    />
  );
}
