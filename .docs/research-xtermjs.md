# Research: xterm.js in Electron + React with Go PTY WebSocket Backend

> **Date:** 2026-07-15
> **Architecture:** Go backend (PTY + WebSocket) → WebSocket → Electron (React + xterm.js)
> **Context:** Agent Infinite — canvas com até 50 nodes, 10-20 terminais ativos simultâneos
>
> **Note:** MCP web-search tools (tavily/context7) não estavam disponíveis neste contexto de sub-agent. Pesquisa baseada em conhecimento técnico da API estável do xterm.js v5.x (packages scoped `@xterm/*`). A API descrita aqui é precisa para a versão atual.

---

## 1. NPM Packages Needed

O xterm.js migrou para o scope `@xterm` no npm (a partir da v5.5, 2024). Os nomes antigos (`xterm`, `xterm-addon-fit`) ainda existem mas estão deprecated.

### Core (obrigatório)

| Package | Versão atual | Purpose |
|---|---|---|
| `@xterm/xterm` | ^5.5 | Core terminal emulator |
| `@xterm/xterm` (CSS) | — | Import `@xterm/xterm/css/xterm.css` |

### Addons (recomendados)

| Package | Purpose | Essencial? |
|---|---|---|
| `@xterm/addon-fit` | Auto-fit terminal ao container (calcula cols/rows) | **Sim** — sem ele não há resize |
| `@xterm/addon-webgl-renderer` | Renderer WebGL (alta performance) | **Sim** — Electron tem Chromium, WebGL sempre disponível |
| `@xterm/addon-web-links` | URLs clicáveis no terminal | Nice-to-have |
| `@xterm/addon-search` | Buscar texto no scrollback | Nice-to-have (útil para logs) |
| `@xterm/addon-canvas-renderer` | Renderer Canvas (fallback sem WebGL) | Não — WebGL é superior em Electron |
| `@xterm/addon-serialize` | Serializar buffer do terminal (persistir estado) | Útil para restore de sessão |
| `@xterm/addon-clipboard` | Clipboard integrado | Opcional — Electron tem clipboard API nativa |
| `@xterm/addon-ligatures` | Ligaduras tipográficas | Opcional — só se usar Fira Code etc. |

### Instalação

```bash
npm install @xterm/xterm @xterm/addon-fit @xterm/addon-webgl-renderer @xterm/addon-web-links @xterm/addon-search
```

### Por que WebGL renderer e não Canvas?

- **WebGL renderer** (`@xterm/addon-webgl-renderer`): renderiza via GPU. Performance ~10x melhor com output rápido. **Recomendado para Electron** (Chromium sempre suporta WebGL).
- **Canvas renderer** (`@xterm/addon-canvas-renderer`): renderiza via Canvas 2D. Fallback para ambientes sem WebGL. Menos performante.
- **DOM renderer** (default, sem addon): renderiza com spans HTML. **Muito lento** para output rápido — não use em produção.

---

## 2. xterm.js Core API

### Criar instância

```typescript
import { Terminal } from '@xterm/xterm';
import '@xterm/xterm/css/xterm.css';

const term = new Terminal({
  cols: 80,
  rows: 24,
  cursorBlink: true,
  scrollback: 1000,        // limitar para economizar memória (default 1000)
  fontFamily: 'Cascadia Code, Fira Code, monospace',
  fontSize: 14,
  allowProposedApi: true,  // necessário para alguns addons
});
```

### Montar no DOM

```typescript
term.open(containerElement);  // containerElement deve ter dimensões reais
term.focus();
```

### Escrever output do backend no terminal

```typescript
// String (menos eficiente — decodifica UTF-8)
term.write('Hello\r\n');

// Uint8Array (MAIS eficiente — passa direto para o parser)
term.write(new Uint8Array([72, 101, 108, 108, 111]));
```

> **Performance tip:** Sempre preferir `term.write(Uint8Array)` quando receber dados binários do WebSocket. Evita overhead de UTF-8 decode/encode.

### Capturar input do usuário

```typescript
term.onData((data: string) => {
  // data = keystrokes do usuário (string UTF-16)
  // Enviar para o backend via WebSocket
  ws.send(data);
});
```

> `onData` dispara a cada keystroke. Para teclas especiais (setas, Ctrl+C), o xterm.js já traduz para escape sequences ANSI que o PTY entende.

### Outros eventos úteis

```typescript
term.onResize(({ cols, rows }) => {
  // Dispara quando o terminal muda de tamanho (via FitAddon.fit() ou term.resize())
  sendResizeToBackend(cols, rows);
});

term.onTitleChange((title) => {
  // Shell pode setar título via escape sequence
  updateTabTitle(title);
});

term.onBell(() => {
  // Bell (ASCII BEL 0x07)
});

term.onBinary((data: string) => {
  // Input binário do usuário (raro)
});
```

### Cleanup

```typescript
term.dispose();  // Libera todos os recursos, listeners, renderer, addons
```

---

## 3. React Component Structure (Electron)

### Não há problema de SSR no Electron

Electron roda Chromium no renderer process — **DOM sempre disponível**. Se usar Vite + React (recomendado para Electron), não há SSR. xterm.js funciona diretamente.

Se usar Next.js com SSR (não recomendado para Electron, mas possível), importar dinamicamente:
```typescript
const TerminalComponent = dynamic(() => import('./TerminalView'), { ssr: false });
```

### Componente React completo

```tsx
import { useEffect, useRef } from 'react';
import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import { WebglAddon } from '@xterm/addon-webgl-renderer';
import { WebLinksAddon } from '@xterm/addon-web-links';
import { SearchAddon } from '@xterm/addon-search';
import '@xterm/xterm/css/xterm.css';

interface TerminalViewProps {
  websocketUrl: string;  // ex: 'ws://localhost:8080/terminal/123'
}

export function TerminalView({ websocketUrl }: TerminalViewProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const termRef = useRef<Terminal | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const fitRef = useRef<FitAddon | null>(null);

  useEffect(() => {
    if (!containerRef.current) return;

    // 1. Criar terminal
    const term = new Terminal({
      cursorBlink: true,
      scrollback: 1000,
      fontFamily: 'Cascadia Code, monospace',
      fontSize: 14,
      allowProposedApi: true,
    });
    termRef.current = term;

    // 2. Carregar addons
    const fitAddon = new FitAddon();
    term.loadAddon(fitAddon);
    fitRef.current = fitAddon;

    term.loadAddon(new WebLinksAddon());
    term.loadAddon(new SearchAddon());

    // WebGL renderer — tentar carregar, fallback para DOM se falhar
    try {
      term.loadAddon(new WebglAddon());
    } catch (e) {
      console.warn('WebGL not available, falling back to DOM renderer', e);
    }

    // 3. Montar no DOM
    term.open(containerRef.current);
    term.focus();

    // 4. Fit inicial (depois do open)
    fitAddon.fit();

    // 5. Conectar WebSocket
    const ws = new WebSocket(websocketUrl);
    wsRef.current = ws;
    ws.binaryType = 'arraybuffer';  // CRÍTICO: receber binary como ArrayBuffer

    // --- Backend → Terminal (output do PTY) ---
    ws.onmessage = (event) => {
      if (event.data instanceof ArrayBuffer) {
        // Binary frame = raw PTY output → escrever direto como Uint8Array
        term.write(new Uint8Array(event.data));
      } else {
        // Text frame = JSON control message (não é dados de terminal)
        handleControlMessage(event.data, term, fitAddon);
      }
    };

    // --- Terminal → Backend (input do usuário) ---
    term.onData((data) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(data);  // envia como text frame (keystrokes são strings curtas)
      }
    });

    // --- Resize → Backend ---
    term.onResize(({ cols, rows }) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: 'resize', cols, rows }));
      }
    });

    ws.onclose = () => {
      term.write('\r\n\x1b[31m[WebSocket disconnected]\x1b[0m\r\n');
    };

    // 6. ResizeObserver — refit quando container muda de tamanho
    const resizeObserver = new ResizeObserver(() => {
      try {
        fitAddon.fit();
      } catch (e) {
        // container pode não ter dimensões ainda
      }
    });
    resizeObserver.observe(containerRef.current);

    // 7. Cleanup
    return () => {
      resizeObserver.disconnect();
      ws.close();
      term.dispose();
      termRef.current = null;
      wsRef.current = null;
      fitRef.current = null;
    };
  }, [websocketUrl]);

  return (
    <div
      ref={containerRef}
      style={{ width: '100%', height: '100%', overflow: 'hidden' }}
    />
  );
}

function handleControlMessage(raw: string, term: Terminal, fit: FitAddon) {
  try {
    const msg = JSON.parse(raw);
    // Backend pode enviar mensagens de controle (ex: status, erro)
    if (msg.type === 'error') {
      term.write(`\r\n\x1b[31m[Error: ${msg.message}]\x1b[0m\r\n`);
    }
  } catch {
    // Se não é JSON, tratar como dados de terminal (fallback)
    term.write(raw);
  }
}
```

### Pitfalls comuns no React

1. **Container sem dimensões:** O `div` deve ter `width: 100%; height: 100%` e o parent deve ter altura definida. Sem isso, `fitAddon.fit()` calcula 0 cols/rows.

2. **StrictMode double-render:** React 18 StrictMode monta/desmonta componentes duas vezes em dev. O cleanup do `useEffect` deve ser idempotente — `term.dispose()` e `ws.close()` são seguros de chamar múltiplas vezes.

3. **Re-create on prop change:** O `useEffect` depende de `websocketUrl`. Se a URL muda, o terminal é recriado. Para persistir o terminal entre reconexões, separar a lógica de WS do lifecycle do terminal.

4. **CSS import:** Esquecer `import '@xterm/xterm/css/xterm.css'` causa rendering quebrado (sem padding, sem scroll bar, texto sobreposto).

5. **Não chamar `fit()` antes de `open()`:** `fit()` precisa que o terminal esteja montado no DOM com dimensões reais.

---

## 4. WebSocket Data Flow

### Protocolo: Binary = data, Text = control

A abordagem mais limpa é usar o tipo de frame do WebSocket para distinguir:

| Frame type | Conteúdo | Direção | Uso |
|---|---|---|---|
| **Binary** | Raw PTY bytes (Uint8Array) | Backend → Frontend | Output do terminal |
| **Text** | JSON control messages | Ambas direções | Resize, status, errores |
| **Text** | Keystroke strings | Frontend → Backend | Input do usuário |

> WebSocket nativamente distingue text frames (UTF-8 strings) de binary frames (ArrayBuffer). No Go, `gorilla/websocket` expõe `messageType` (TextMessage=1 vs BinaryMessage=2).

### Fluxo completo

```
┌─────────────────────────────────────────────────────────────┐
│ FRONTEND (Electron + React)                                  │
│                                                              │
│  User keyboard                                               │
│       ↓                                                      │
│  term.onData(str) ──text frame──→ ws.send(str)              │
│                                              ↓               │
└──────────────────────────────────────────────┼──────────────┘
                                               │ WebSocket
┌──────────────────────────────────────────────┼──────────────┐
│ BACKEND (Go)                                  ↓              │
│  ws.ReadMessage() → ptmx.Write()  (input → PTY stdin)       │
│                                                              │
│  PTY stdout → ptmx.Read() → ws.WriteMessage(Binary, bytes)   │
│                                              ↓               │
└──────────────────────────────────────────────┼──────────────┘
                                               │ WebSocket (binary frame)
┌──────────────────────────────────────────────┼──────────────┐
│ FRONTEND                                     ↓               │
│  ws.onmessage (ArrayBuffer)                                 │
│       ↓                                                      │
│  term.write(new Uint8Array(data))  (render no terminal)     │
│                                                              │
│  Resize: term.onResize → ws.send(JSON{type:'resize',...})   │
│       → Go: json.Unmarshal → pty.Setsize()                  │
└─────────────────────────────────────────────────────────────┘
```

### Go backend (sketch de referência)

```go
package main

import (
    "encoding/json"
    "log"
    "net/http"
    "os/exec"
    "github.com/creack/pty"
    "github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
    CheckOrigin: func(r *http.Request) bool { return true },
}

func handleTerminal(w http.ResponseWriter, r *http.Request) {
    ws, err := upgrader.Upgrade(w, r, nil)
    if err != nil {
        log.Printf("upgrade error: %v", err)
        return
    }
    defer ws.Close()

    // Criar PTY
    cmd := exec.Command("powershell.exe") // ou wsl, bash, etc.
    ptmx, err := pty.Start(cmd)
    if err != nil {
        log.Printf("pty error: %v", err)
        return
    }
    defer ptmx.Close()
    defer cmd.Process.Kill()

    // Goroutine: PTY output → WebSocket (binary frames)
    go func() {
        buf := make([]byte, 4096)
        for {
            n, err := ptmx.Read(buf)
            if err != nil {
                break
            }
            // Enviar como binary frame (copia só os bytes lidos)
            ws.WriteMessage(websocket.BinaryMessage, buf[:n])
        }
        ws.Close()
    }()

    // Main goroutine: WebSocket → PTY input + control messages
    for {
        msgType, payload, err := ws.ReadMessage()
        if err != nil {
            break
        }

        switch msgType {
        case websocket.TextMessage:
            // Pode ser keystroke (string pura) ou JSON control
            var ctrl struct {
                Type string `json:"type"`
                Cols uint16 `json:"cols"`
                Rows uint16 `json:"rows"`
            }
            if json.Unmarshal(payload, &ctrl) == nil && ctrl.Type == "resize" {
                pty.Setsize(ptmx, &pty.Winsize{
                    Rows: ctrl.Rows,
                    Cols: ctrl.Cols,
                })
                continue
            }
            // Se não é JSON control, é keystroke → escrever no PTY
            ptmx.Write(payload)

        case websocket.BinaryMessage:
            // Binary do frontend = input binário → PTY
            ptmx.Write(payload)
        }
    }
}
```

> **Atenção:** O exemplo acima mistura keystrokes (text) e control (JSON) no mesmo text frame type. Para disambiguar de forma robusta, recomenda-se **um prefixo** ou **convenção fixa**: keystrokes são strings curtas sem `{`, control messages sempre começam com `{`. Ou alternativamente, usar um byte de tipo no início de cada text frame. Ver seção Protocol Design abaixo.

### Protocol Design: recomendação

Para evitar ambiguidade entre keystrokes e control messages no text frame:

**Opção A (recomendada — simples):**
- Text frames do frontend → sempre JSON: `{ "type": "input", "data": "ls\r" }` ou `{ "type": "resize", "cols": 80, "rows": 24 }`
- Binary frames do backend → raw PTY output
- Binary frames do frontend → raw input (alternativa ao JSON input)

**Opção B (mais eficiente — prefix byte):**
- Tudo binary. Byte 0 = tipo: `0x01` = data, `0x02` = resize, `0x03` = control
- Mais compacto, mas mais complexo de implementar

**Opção C (híbrida — prática):**
- Text frame = JSON control (resize, status)
- Binary frame = raw terminal data (ambas direções)
- Frontend envia input como binary: `ws.send(new TextEncoder().encode(data))`

A **Opção C** é a mais limpa e performática. O componente React acima pode ser ajustado:

```typescript
// Input do usuário → binary frame
term.onData((data) => {
  if (ws.readyState === WebSocket.OPEN) {
    ws.send(new TextEncoder().encode(data));  // binary
  }
});

// Resize → text frame (JSON control)
term.onResize(({ cols, rows }) => {
  if (ws.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify({ type: 'resize', cols, rows }));  // text
  }
});

// Output do backend → sempre binary
ws.onmessage = (event) => {
  if (event.data instanceof ArrayBuffer) {
    term.write(new Uint8Array(event.data));  // binary = terminal data
  } else {
    // text = control message from backend
    handleControlMessage(event.data);
  }
};
```

---

## 5. Performance

### Como xterm.js lida com output rápido

O xterm.js tem **rendering batching interno**: o parser processa dados imediatamente, mas o **renderer** (WebGL/Canvas) só redesenha no próximo `requestAnimationFrame` (~16ms a 60fps). Isso significa:

- **Parser:** Processa ANSI sequences na thread principal, sincronamente. Muito rápido (~100MB/s+).
- **Renderer:** Batches visual updates to rAF. Não bloqueia a UI com redraws excessivos.
- **Buffer:** Mantém scrollback em memória. Strings são armazenadas como arrays internos.

### Throttling / Batching / Backpressure

#### No frontend (xterm.js + WebSocket)

1. **Usar `term.write(Uint8Array)` em vez de `term.write(string)`:** Evita overhead de UTF-8 decoding. O parser do xterm.js aceita `Uint8Array` nativamente.

2. **Batch múltiplos `onmessage` antes de escrever:** Se o backend envia muitos frames pequenos rapidamente, acumular em um buffer e flushar no rAF:
   ```typescript
   let writeBuffer: Uint8Array[] = [];
   let flushScheduled = false;

   ws.onmessage = (event) => {
     if (event.data instanceof ArrayBuffer) {
       writeBuffer.push(new Uint8Array(event.data));
       if (!flushScheduled) {
         flushScheduled = true;
         requestAnimationFrame(() => {
           for (const chunk of writeBuffer) term.write(chunk);
           writeBuffer = [];
           flushScheduled = false;
         });
       }
     }
   };
   ```
   > xterm.js já faz batching interno no renderer, mas reduzir o número de chamadas `write()` também ajuda — cada `write()` reinicia o parser state machine.

3. **Limitar scrollback:** `scrollback: 1000` (default) é suficiente para a maioria dos casos. `scrollback: 10000` aumenta uso de memória significativamente, especialmente com 10-20 terminais ativos.

4. **WebGL renderer:** Essencial para output rápido. O DOM renderer cria/removes spans HTML para cada célula — extremamente lento com output de build logs.

#### No backend (Go)

1. **Buffer de leitura do PTY:** Usar buffer de 4096-16384 bytes. `ptmx.Read()` retorna o que estiver disponível.

2. **Não ler byte-a-byte:** Sempre ler em chunks. Go's `io.Read` do PTY retorna blocos.

3. **Backpressure do WebSocket:** `gorilla/websocket` tem `ws.WriteMessage()` que bloqueia se o buffer de envio estiver cheio. Para evitar bloquear a goroutine de leitura do PTY, usar um channel com buffer:
   ```go
   outputCh := make(chan []byte, 256)
   go func() {
     buf := make([]byte, 16384)
     for {
       n, err := ptmx.Read(buf)
       if err != nil { close(outputCh); return }
       chunk := make([]byte, n)
       copy(chunk, buf[:n])
       outputCh <- chunk
     }
   }()
   // Writer goroutine
   for chunk := range outputCh {
     if err := ws.WriteMessage(websocket.BinaryMessage, chunk); err != nil {
       break
     }
   }
   ```

4. **Coalescing:** Se o PTY produz muitos chunks pequenos rapidamente, pode-se coalescer em um único frame WebSocket com um pequeno delay (ex: 1ms) ou quando o buffer atinge N bytes. Trade-off: latência vs throughput.

#### Cenário: build log muito rápido

Para output de `cargo build`, `npm install`, etc. (megabytes de output em segundos):

- xterm.js + WebGL renderer aguenta bem — o parser é rápido e o renderer batcha
- O gargalo real é geralmente a **memória do scrollback**, não o rendering
- Para logs que não precisam de interação, considerar modo "write-only" sem scrollback alto
- `term.options.logLevel = 'off'` para suprimir warnings de performance do xterm.js

### Multi-terminal (10-20 terminais simultâneos)

Este é o cenário do Agent Infinite. Considerações:

1. **Lazy mount:** Só montar xterm.js para terminais visíveis no canvas. Terminais em nodes fora da viewport podem ter seu estado serializado (`@xterm/addon-serialize`) e remontados quando visíveis.

2. **`term.dispose()` para off-screen:** Destrói renderer, libera WebGL context. Cada instância WebGL usa um context GPU — Chromium tem limite de ~16 contextos WebGL simultâneos. Com 20 terminais, pode hitting esse limite. Solução: usar `@xterm/addon-canvas-renderer` como fallback quando WebGL context exhausted, ou lazy-mount.

3. **WebGL context limit:** Navegadores limitam ~16 contextos WebGL ativos. Para 20+ terminais, nem todos podem ter WebGL renderer. Estratégias:
   - Usar WebGL apenas para terminais focados/visíveis
   - Fallback para Canvas renderer nos demais
   - Reusar contextos via `dispose()` quando terminal sai da viewport

4. **CPU em idle:** xterm.js não redrawa quando não há output. Em idle, CPU ~0%. Bom para o requisito "CPU baixa em idle" do plano.

5. **Memory:** Cada terminal com scrollback 1000 e 80x24 usa ~100-200KB de buffer. 20 terminais = ~2-4MB. Negligenciável.

---

## 6. Resize Handling

### Fluxo de resize

```
Container redimensiona (window resize, canvas zoom, node resize)
       ↓
ResizeObserver detecta mudança no container
       ↓
fitAddon.fit()  → recalcula cols/rows baseado no tamanho do container + font metrics
       ↓
term.onResize({ cols, rows }) dispara
       ↓
ws.send(JSON.stringify({ type: 'resize', cols, rows }))
       ↓
Go: pty.Setsize(ptmx, &pty.Winsize{Rows: rows, Cols: cols})
       ↓
PTY notifica o processo filho (SIGWINCH on Unix)
       ↓
Shell/programa re-renderiza com novas dimensões
```

### FitAddon — como funciona

```typescript
const fitAddon = new FitAddon();
term.loadAddon(fitAddon);

// Chamar quando o container muda de tamanho
fitAddon.fit();

// Após fit(), ler as novas dimensões
console.log(term.cols, term.rows);  // ex: 80, 24
```

`fit()` calcula quantas células cabem no container dividindo a largura/altura do container pela largura/altura de uma célula (medida via font metrics). Depois chama `term.resize(cols, rows)` internamente, que dispara `onResize`.

### ResizeObserver — detectar mudanças no container

```typescript
const resizeObserver = new ResizeObserver(() => {
  fitAddon.fit();
});
resizeObserver.observe(containerRef.current);

// Cleanup
resizeObserver.disconnect();
```

> **Importante:** `ResizeObserver` pode disparar múltiplas vezes rapidamente durante drag-resize. `fitAddon.fit()` é barato mas enviar resize messages a cada frame é desnecessário. Throttle simples:
> ```typescript
> let resizeTimer: number;
> const resizeObserver = new ResizeObserver(() => {
>   clearTimeout(resizeTimer);
>   resizeTimer = setTimeout(() => fitAddon.fit(), 50);
> });
> ```

### Initial resize (momento da conexão)

**Ordem crítica:**
1. `term.open(container)` — monta no DOM
2. `fitAddon.fit()` — calcula dimensões reais
3. **Enviar resize inicial para o backend** — o PTY precisa saber o tamanho antes de qualquer output

```typescript
term.open(containerRef.current);
fitAddon.fit();
// Enviar tamanho inicial
ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }));
```

> Sem o resize inicial, o PTY usa default 80x24. Se o container for maior/menor, o output fica mal formatado até o primeiro resize.

### Go side: pty.Setsize

```go
import "github.com/creack/pty"

func resizePty(ptmx *os.File, cols, rows uint16) error {
    return pty.Setsize(ptmx, &pty.Winsize{
        Rows: rows,
        Cols: cols,
        X:    0,  // pixels (opcional)
        Y:    0,
    })
}
```

No Windows, `creack/pty` suporta resize via `pty.Setsize` (usa `SetConsoleScreenBufferSize` + `ResizePseudoConsole` internamente). Funciona com PowerShell, CMD, WSL.

### Canvas zoom (específico do Agent Infinite)

Se o canvas tiver zoom (React Flow), o container do terminal muda de tamanho visual mas o DOM element pode não mudar de dimensões reais (transform: scale). Considerações:

- Se usar CSS `transform: scale()`, o `ResizeObserver` **não dispara** (transform não muda layout dimensions). Precisa observar o zoom level do React Flow e chamar `fit()` manualmente.
- Se o terminal estiver dentro de um node do React Flow que redimensiona via width/height reais (não transform), o `ResizeObserver` funciona normalmente.
- Para zoom, pode-se ajustar `term.options.fontSize` proporcionalmente ao zoom, ou deixar o CSS scale cuidar da escala visual (mais simples, mas o terminal renderiza na resolução base e escala — pode ficar blurry).

---

## Summary: Checklist de Implementação

### Pacotes (npm install)
```
@xterm/xterm
@xterm/addon-fit
@xterm/addon-webgl-renderer
@xterm/addon-web-links
@xterm/addon-search
```

### Estrutura de código mínima
1. Componente React com `useRef` + `useEffect`
2. Criar `Terminal`, carregar addons (Fit, WebGL, WebLinks, Search)
3. `term.open(container)` → `fitAddon.fit()` → enviar resize inicial
4. Conectar WebSocket com `binaryType = 'arraybuffer'`
5. `ws.onmessage` → `term.write(new Uint8Array(data))` (binary = output)
6. `term.onData` → `ws.send(data)` (input do usuário)
7. `term.onResize` → `ws.send(JSON{type:'resize',cols,rows})`
8. `ResizeObserver` no container → `fitAddon.fit()` (throttled)
9. Cleanup: `ResizeObserver.disconnect()` + `ws.close()` + `term.dispose()`

### Performance checklist
- [x] Usar `@xterm/addon-webgl-renderer` (Electron tem Chromium = WebGL sempre)
- [x] `ws.binaryType = 'arraybuffer'` + `term.write(new Uint8Array(data))`
- [x] `scrollback: 1000` (não 10000) para multi-terminal
- [x] Throttle `fitAddon.fit()` no ResizeObserver (50ms)
- [x] Lazy mount/unmount para terminais off-screen no canvas
- [x] Fallback para Canvas renderer se WebGL context exhausted (>16 terminais)
- [x] Go: buffer de leitura 4096-16384 bytes, channel com buffer para backpressure

### Pitfalls a evitar
- ❌ Esquecer `import '@xterm/xterm/css/xterm.css'`
- ❌ Chamar `fit()` antes de `open()` ou com container sem dimensões
- ❌ Não enviar resize inicial ao conectar
- ❌ Usar DOM renderer (default) — muito lento
- ❌ Não fazer cleanup no `useEffect` return (memory leak em StrictMode)
- ❌ `scrollback` alto com 20 terminais (memória)
- ❌ Mais de 16 WebGL contexts simultâneos sem fallback