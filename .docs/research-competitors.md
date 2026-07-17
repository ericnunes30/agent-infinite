# Pesquisa: Competidores — OmniRift & Maestri

> Pesquisa realizada em 2026-07-15 via GitHub API + raw source code (READMEs, Rust/Swift source).
> Foco: COMO funcionam tecnicamente — terminal comm, completion detection, dispatch, arquitetura, canvas.

---

## 1. OmniRift

### O que é
App desktop **open-source** (MIT) — **Tauri 2 + Rust + React** — que reúne num **canvas infinito** agentes de IA (Claude Code, Codex, Hermes, shell), **terminais PTY reais**, worktrees git, notas, sketches e navegadores embutidos. **100% local, sem conta.** Linux + Windows (+ macOS beta). Por OmniForge (brasileiro — site `omnirift.omniforge.com.br`).

- **Repo:** `github.com/jessefreitas/OmniRift` · TypeScript/Rust · 19 stars · 10 forks · criado 2026-06-18, atualizado hoje (2026-07-15).
- **Tagline:** "Você orquestra; os agentes trabalham."

### Stack
| Camada | Tecnologia |
|---|---|
| Desktop/Core | **Tauri 2** (WebKitGTK Linux, WebView2 Windows) |
| Backend nativo | **Rust** (PTY, agentes, scheduler, SQLite) |
| Frontend | React 19 + TypeScript + Vite |
| Canvas | **`@xyflow/react` (React Flow) + Pixi.js** |
| Terminal (front) | **`@xterm/xterm`** (xterm.js) |
| Terminal (back) | **`portable-pty`** (Rust) |
| VT100 parse | crate `vt100::Parser` (para ler tela limpa) |
| Persistência | SQLite (auto-save/restore) |
| Monorepo | `apps/desktop` + `packages/{canvas-engine,terminal-node,ui,shared-types}` |

### Modelo de comunicação com terminal — **PTY REAL (híbrido)**
OmniRift roda agentes em **PTYs reais** via `portable-pty` (backend-owned). O output do PTY é parseado por um **emulador VT100 (`vt100::Parser`)** para obter texto limpo da tela (essencial para detecção de estado). O output cru vai ao frontend xterm.js; o texto parseado alimenta o detector de estado.

Adicionalmente, há um **segundo modelo estruturado — ACP (Agent Client Protocol)** para o "OmniAgent". Spawna o adapter como **subprocesso stdio** (NÃO PTY) e fala **JSON-RPC newline-delimited**:
- Claude: `npx -y @agentclientprotocol/claude-agent-acp`
- Codex: `npx -y @agentclientprotocol/codex-acp`
- Hermes: `uvx --from hermes-agent[acp] hermes-acp`

Eventos: `acp://ready`, `acp://update` (tool_call / message_chunk / plan), `acp://permission`, **`acp://turn-done` (fim do turno = completion real)**, `acp://exit`.

### Detecção de completion — **HÍBRIDA (insight central)**

OmniRift tem DOIS mecanismos, um por tipo de agente:

#### A) Terminais PTY — `StateDetector` (`pty/detector.rs`)
Máquina de estados que roda **uma task tokio por sessão**, polling a cada **300ms**. Combina 3 sinais:

1. **Process group detection** (saber se há subprocesso em foreground):
   - Unix: `master.process_group_leader()` (grupo de processo POSIX)
   - Windows: enumera filhos do root via `sysinfo` (ConPTY não tem grupo POSIX) — ≥1 filho vivo = subprocesso
   - `FgClass::Subprocess` → sempre `Working` (algo está rodando)
2. **Quiescence**: sem output no PTY por **400ms** (`QUIET`)
3. **VT100 "bottom"** matched contra **regex profiles por agente** (`pty/profile.rs`):
   - `ready` (prompt pronto): ex. claude/codex/opencode/grok → `(?m)^\s*[❯>›]\s*$`
   - `blocked` (esperando confirmação): "Do you want", `\(y/n\)`, "Press Enter", "Allow this"
   - shell → `(?m)[\$#❯]\s*$`

**Estados:** `Working → Blocked → Done → Idle → Dead` (Dead = canal fechou).
- Para **agentes** (`is_agent=true`): chegar em `ready` vindo de Working/Blocked/Done → **`Done`**.
- Para **shell**: chegar em `ready` → `Idle` (semântica de prompt, não de task).
- Grace de startup de **1.5s** (evita falsos prontos na inicialização).
- Eventos emitidos ao frontend via `agent://status` (Tauri event) + `state_map` (DashMap) + `state_tx` (broadcast).

> **Resumo:** OmniRift ESCREVE no PTY mas faz detecção sofisticada — emula VT100 para texto limpo + detecta processo em foreground + timer de quiescence + regex por perfil de agente. É heurística robusta, não protocolo estruturado.

#### B) Agentes ACP (estruturados) — completion REAL
O `acp://turn-done` é o sinal estruturado de fim de turno. Não há scraping. O adapter fala JSON-RPC; o OmniRift é **proxy transparente** (faz handshake initialize → session/new, repassa `session/update` e requests).

### Dispatch / orquestração
O **"Conductor"** é um agente orquestrador (Claude Code/Codex/Hermes/LLM) que recebe input da barra, decide, e despacha via **MCP tools `orchestrator_*`**.

- `dispatch_task(targets, task, context, priority)` (`orchestrator/mod.rs`):
  - Resolve targets: `@nome`, `@all`, `@idle`, `@role:x`, `@worktree:floor` → session_ids (`mcp::resolve_group`)
  - **Injeta escrevendo `text + "\r"` no stdin do PTY** (`dispatch_to_session`) — funciona pra TODO tipo de agente, inclusive ACP (o adapter roda dentro do PTY; o PTY é o canal físico compartilhado).
  - `priority`: `"blocking"` (espera, timeout 5min) vs `"async"` (retorna imediatamente).
    - ⚠️ **Comentário no código:** "Blocking — espera real (ACP condvar) é Fase 2. Por enquanto o comportamento é idêntico ao async: retorna imediatamente após o despacho. O resultado chega via event stream (`orchestrator://log`)." Ou seja, **espera síncrona ainda NÃO implementada** — results chegam assíncronos por eventos.
  - Loga tudo em SQLite (`orchestration_log`: id, timestamp, source, target, payload, status, stage, parent_id).

### MCP — **SIM, first-class**
OmniRift roda **MCP servers** e os injeta nos agentes. O OmniAgent (ACP) recebe tools do OmniRift: `terminal_*`, `claim_*`, `memory_*`, `workspace_*`. Bridge MCP via `npx -y mcp-remote <url>` para o server `omnirift-agents`. Há checagem de `npx` no PATH no spawn (falha muda → aviso visível `acp://mcp-warning`).

### Canvas
React Flow + Pixi.js. **Conexões semânticas**: a linha entre agentes carrega **payload tipado** (ex. diff), com nós de **Review (gate)** — segura o diff, mostra renderizado, você **Aprova/Rejeita** (motivo volta pro autor). **Validador IA** liga um agente revisor que valida sozinho (APPROVE/REJECT). **Filtro** roteia por conteúdo (tipo/regex/caminho). **Floors** = worktrees git reais (vários projetos lado a lado + "Land" com gate de review).

### Recursos extras
- Routines (agendadas + gatilhos de lifecycle de floor, systemd/schtasks)
- OmniPartner (chat BYOK: OpenAI/Anthropic/Ollama)
- Memória plugável (Local SQLite / OmniMemory HTTP+MCP / Obsidian)
- `omnirift-cli` (controle via terminal), Mobile (parear por QR, steering opt-in E2EE LAN)
- Monitor de recursos (CPU/GPU/mem), Saúde do Projeto (IA), Editor Monaco + análise de complexidade

---

## 2. Maestri

### O que é
App **proprietário macOS** (distribuído via **SetApp**), "Manage AI agents like a team, not a terminal." Canvas espacial infinito com terminais, agentes de IA, notas, file browsers e navegadores embutidos. Requer **macOS 26.2+**. Site `maestriapp.com`.

- **Clone open-source:** `github.com/zlh-428/open-maestri` · **Swift** · GPL v3 · 17 stars · compatível com formato `workspace.json` da Maestri v0.25.4 (CLI `omaestri` = `maestri`).
- Toda a análise técnica abaixo vem do **open-maestri** (espelha a arquitetura da Maestri proprietária; README afirma compatibilidade total de formato e CLI).

### Stack (open-maestri → espelha Maestri)
| Camada | Tecnologia |
|---|---|
| Plataforma | **Native macOS** — **SwiftUI + AppKit** (NÃO Electron/Tauri) |
| Canvas | **NSView customizado** (5 camadas de subviews, viewport culling, z-index double-buffering, hit-test caching 2px) |
| Terminal | **[SwiftTerm](https://github.com/migueldeicaza/SwiftTerm)** (PTY, VT100/xterm-256color) |
| Browser embutido | **WKWebView** (AppKit) |
| Persistência | Arquivos atômicos (`FileManager.replaceItem`), `workspace.json` (schemaVersion 2) |
| IPC | HTTP `POST /cli` em `127.0.0.1` (TCP porta dinâmica) + **Unix socket** `~/.open-maestri/run/agent.sock` |
| CLI | binário Swift separado (`omaestri`) |
| Concurrency | `@Observable` (Swift 5.9), snapshot pattern p/ I/O em background |

### Modelo de comunicação com terminal — **PTY REAL**
Terminais via **SwiftTerm** (PTY nativo). TUI interativa totalmente visível. **Não usa stream-json nem protocolo estruturado.** O `omaestri` CLI é **auto-injetado** no ambiente de cada terminal (`MAESTRI_SERVER_PORT` / `MAESTRI_SOCKET` env var).

### Detecção de completion — **TIMER DE SILÊNCIO (insight central)**

Maestri NÃO usa protocolo estruturado. Para agentes de IA, detecta "terminou" puramente por **silêncio de output**:

`TerminalActivityMonitor` (`Sources/Terminal/TerminalActivityMonitor.swift`):
- `recordOutput()` chamado a **cada byte de output do PTY** → atualiza `lastOutputTime`, marca `isRunning = true`.
- **`GlobalActivityClock`**: UM timer background compartilhado (`DispatchSourceTimer`, 1s, queue utility) — evita N timers na main thread. A cada tick chama `checkActivity()` de cada terminal (dispatchado pra main thread).
- `checkActivity()`: se `isRunning` **E** `elapsed >= Constants.agentIdleTimeout` (segundos sem output) → `isRunning = false`, dispara `onStatusChanged(false)` → posta notificação **`.terminalBecameIdle`**.

`omaestri ask "Name" "prompt"` (`AskHandler.swift`):
- Injeta o prompt no PTY alvo (`tm.write(text + "\r")`), marca `markActiveTask()`.
- **Para agentes de IA:** `waitForIdleNotification` — escuta `.terminalBecameIdle` (com **grace inicial de 500ms** + **timeout de 30s**), depois retorna um **snapshot de texto do buffer do SwiftTerm**.
- **Para `generic_shell`:** `waitForPromptEvent` — assina o callback de output do PTY e checa se a **última linha termina com char de prompt** (`%`, `$`, `>`, `❯`) — **prompt scraping**. Compara contra baseline de linhas pré-injeção.
- Timeout de 30s em ambos; retorna o snapshot do buffer (texto puro) em qualquer caso.

`omaestri check "Name"` (`CheckHandler.swift`): apenas lê as últimas N linhas do output do terminal alvo, **strips ANSI** (CSI/OSC/ESC) — **sem esperar completion**. É leitura "agora".

> **Resumo:** Maestri detecta "agente terminou" por **timer de silêncio** (sem output no PTY por N segundos = idle/done). Simples e frágil: um agente pensando muito tempo sem emitir output gera **falso "done"**; um agente verboso nunca fica "done". Para shells, adicionalmente faz **scraping de char de prompt**.

### Dispatch / coordenação multi-agente
Via **CLI `omaestri`** → **HTTP `POST /cli`** ao `InterAgentServer` (localhost only — TCP `127.0.0.1` + Unix socket). Body JSON `{ "args": [...] }`, header `X-Terminal-ID: <UUID>` (escopo de permissão, persiste entre workspaces). Resposta `text/plain`.

Comandos:
```
omaestri list                                   # lista agentes/notas/portais conectados
omaestri ask "Name" "prompt"                    # envia e ESPERA resposta (idle)
omaestri check "Name" [lines]                    # lê output recente (sem esperar)
omaestri note read "Name" [--offset N] [--limit N]
omaestri note write "Name" "content"
omaestri recruit "Name" --preset claude-code --role coder   # Maestro mode
omaestri dismiss "Name"                                     # Maestro mode
omaestri connect "From" "To"                                 # Maestro mode
omaestri portal navigate "Name" "url"
omaestri portal snapshot "Name"               # accessibility tree do WKWebView
omaestri portal click "Name" @ref
omaestri portal fill "Name" @ref "value"
```

**Maestro Mode:** um agente atua como **lead** e recruta/conecta/demite outros agentes programaticamente (`recruit`/`connect`/`dismiss`). Conexões têm status visual (`communicating`/`idle`) via `ConnectionManager`.

### MCP — **NÃO (no clone)**
open-maestri não menciona MCP. A comunicação inter-agente é pelo CLI `omaestri` sobre HTTP/Unix socket local, não MCP. (Maestri proprietária pode diferir, mas o clone não usa MCP.)

### Canvas
NSView custom. **Conexões com animação de física** (corda catenária, 21 pontos de controle — `RopeSimulation`). Minimap. Node types: Terminal, Note, FileTree, Portal (WKWebView), Text, Freehand, Shape, Stroke. Persistência: auto-save 30s (background thread, snapshot pattern), recuperação de crash via flag `cleanShutdown`.

### Recursos extras
- Skills (instaladas em `~/.claude/skills/`), Roles (personas reutilizáveis)
- Portal browser automation (snapshot = accessibility tree, click/fill por `@ref`)
- Note chains (nota-conectada-a-nota, agentes atravessam a cadeia)
- Scrollback persistido entre restarts

---

## Comparativo direto — as 5 perguntas-chave

| Pergunta | OmniRift | Maestri (via open-maestri) |
|---|---|---|
| **1. Terminal comm** | **PTY real** (`portable-pty`) + **ACP estruturado** (JSON-RPC stdio) híbrido. VT100 parser p/ texto limpo. | **PTY real** (SwiftTerm). Só PTY — sem protocolo estruturado. |
| **2. Completion detection** | **Híbrido:** (a) PTY = máquina de estados (process-group + quiescence 400ms + regex por agente na tela VT100) → `Done`; (b) ACP = `turn-done` (estruturado, real). | **Timer de silêncio:** sem output no PTY por N seg → `.terminalBecameIdle` → done. Shell: +scraping de char de prompt. 30s timeout. |
| **3. Dispatch** | Conductor (agente) chama MCP tools `orchestrator_*` → escreve `text+\r` no PTY do alvo. `@all/@idle/@role/@worktree`. Async vs blocking (blocking NÃO implementado — results via eventos). | Agente chama CLI `omaestri ask "Name" "prompt"` → HTTP POST /cli → injeta `text+\r` no PTY alvo e espera idle. Maestro mode: `recruit/connect/dismiss`. |
| **4. Arquitetura** | **Tauri 2 + Rust + React 19**. Monorepo. SQLite. Cross-platform (Linux/Win/macOS). | **Native macOS SwiftUI+AppKit**. Swift. Arquivos atômicos. macOS-only (26.2+). |
| **5. Canvas** | **React Flow + Pixi.js**. Conexões semânticas (diff tipado), Review gates, validador IA, floors (worktrees). | **NSView custom**. Cordas com física (catenária), minimap, note chains, portal automation. |

---

## Insights acionáveis para o Agent Infinite

### Sobre completion detection (A pergunta-chave)
Existem **3 abordagens** no mercado, em ordem de robustez:

1. **Protocolo estruturado (stream-json / ACP)** — `{"type":"result","terminal_reason":"completed"}`. **MAIS robusto.** Exige rodar o agente em modo non-interactive (`claude -p --output-format stream-json`). Perde a TUI visual interativa. É o que o Agent Infinite já planeja (ver `research-claude-code.md`).

2. **Heurística de tela PTY (OmniRift)** — emular VT100 p/ texto limpo + detectar processo em foreground (process group / filhos) + timer de quiescence + regex por agente. Mantém TUI visual. **Razoavelmente robusta**, muito trabalho pra calibrar regex por CLI e por versão.

3. **Timer de silêncio (Maestri)** — sem output por N seg = done. **Simples, frágil.** Falsos done em agentes que pensam muito; nunca-done em agentes verbosos.

**Recomendação:** O plano atual do Agent Infinite (stream-json bidirecional, `result` event) é a abordagem **mais robusta que ambos os competidores**. Trade-off: perde a TUI visual do Claude Code (modo `-p` não é interativo). Se a TUI visual for requisito absoluto, a heurística de tela do OmniRift (VT100 + process-group + quiescence + regex) é o estado-da-arte a estudar — mas é muita engenharia frágil.

### Híbrido é o caminho (OmniRift acerta aqui)
OmniRift oferece **dois modos** no mesmo app: terminais PTY (TUI visível, detecção heurística) E agentes ACP estruturados (sem TUI, completion real). O Agent Infinite poderia fazer o mesmo: **modo "terminal visível" (PTY + heurística) para interação humana**, e **modo "agente orquestrado" (stream-json) para dispatch automático** — o melhor dos dois mundos.

### Dispatch = escrever no PTY
**Ambos despacham escrevendo `text + \r` no stdin do PTY** do agente alvo. Mesmo o OmniRift, com ACP estruturado, injeta o dispatch pelo PTY que o adapter habita. Isso é simples e funciona com qualquer CLI. O Agent Infinite pode fazer igual para o modo PTY.

### Inter-agente: CLI vs MCP
- **Maestri:** CLI `omaestri` injetado + HTTP local (simples, acoplado ao app).
- **OmniRift:** MCP tools (`orchestrator_*`) expostas ao Conductor (padrão, interoperável).
- O plano do Agent Infinite (MCP server expondo `dispatch_task`/`get_agent_output`) **alinha com OmniRift** — mais interoperável e padrão.

### Canvas
- React Flow + Pixi.js (OmniRift) é a stack **mais próxima do que o Agent Infinite já planeja** (Electron + React). Maestri usa NSView nativo (inviável fora do macOS).
- **Conexões semânticas** do OmniRift (diff tipado, Review gates, validador IA) são um diferencial de UX forte a considerar — transformam linhas em "gates de quality" reais, não só cabos de texto.

### Atenção: blocking wait não está pronto no OmniRift
O `dispatch_task` "blocking" do OmniRift **ainda retorna imediatamente** (results via event stream; espera síncrona é "Fase 2"). Isso confirma que **esperar um agente terminar síncrono é difícil** — o Agent Infinite deve projetar desde o início para **results assíncronos por eventos/stream** (como já faz o stream-json com `result` events), não bloquear a chamada de dispatch.