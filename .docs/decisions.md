# Agent Infinite — Decisões e Estado do Projeto

> Documento vivo. Consolidado a partir da sessão de grill (2026-07-15) + fase de pesquisa técnica.
> Fontes: `segunda versao do plan.html`, research reports em `.docs/research-*.md`

---

## ✅ Decisões fechadas

### Produto e Arquitetura

| # | Decisão | Detalhe |
|---|---------|---------|
| 1 | **Worktree por time** | Time inteiro compartilha 1 worktree. Times diferentes = worktrees diferentes. Paralelismo entre times. |
| 2 | **Orquestrador = agente líder** | É uma instância de CLI agent (ex: Claude Code) que delega aos filhos do seu time. |
| 3 | **Arquitetura: 2 runtimes** | Electron + Go. Elixir **descartado** no início. Desktop local only, sem máquinas remotas. |
| 4 | **Go = core + worker** | Go faz tudo: state, events, orquestração, PTY, Git, processos. 1 binário. |
| 5 | **Electron + React + TS** | UI não negociável. |
| 6 | **Go over Rust** | Pragmatismo — mais simples e rápido de desenvolver. |
| 7 | **Worktrees no MVP** | Paralelismo é o core differentiator. Inegociável. |
| 8 | **Segurança: zero no MVP** | Sem sandbox, sem guardrails. "Problema de cada um." |
| 9 | **Auth: externa** | Usuário configura CLI agents na própria máquina. App não gerencia. |
| 10 | **Persistência: JSON-only no MVP** | SQLite deferido pra v1.1. |
| 11 | **Terminal: PowerShell only no MVP** | WSL e Git Bash = v1.1. |
| 12 | **QA roda testes, não edita código** | Playwright e similares. Conflito de escrita entre agentes do time é raro por design (papéis especializados). |
| 13 | **MVP scope: 8 itens** | Workspace → `.agent-infinite/` → canvas (Orchestrator + Agent) → worktree por time → terminal na worktree → dispatch → output retorna → persistir JSON. |

### Pesquisa Técnica (resolvido pela fase de pesquisa)

| # | Decisão | Detalhe | Fonte |
|---|---------|---------|-------|
| 14 | **TUI real (PTY interativo)** | Agentes rodam em PTY interativo. Usuário vê o TUI completo do Claude Code/Codex no canvas. Não usa stream-json. | Usuário |
| 15 | **MCP para dispatch** | Agent Infinite expõe MCP server com tools `dispatch_task` e `get_agent_output`. Passa via `--mcp-config` ao agente. Tools viram `mcp__agent_infinite__dispatch_task`. | `research-claude-code.md` |
| 16 | **Dispatch = escreve `text+\r` no PTY** | MCP tool escreve `text+\r` no stdin do PTY do agente alvo. Igual OmniRift e Maestri. | `research-competitors.md` |
| 17 | **Results assíncronos** | Dispatch retorna imediatamente ("dispatched"). Results chegam via event stream. Blocking wait é difícil (OmniRift sequer implementou). Projetar async desde o início. | `research-competitors.md` |
| 18 | **Completion detection: heurística OmniRift-style** | Máquina de estados: process group detection + quiescence (400ms sem output) + regex por perfil de agente. Estados: Working → Blocked → Done → Idle → Dead. | `research-competitors.md` |
| 19 | **Canvas: React Flow + Pixi.js** | Confirmado pelo OmniRift (concorrente direto usa exatamente essa stack). | `research-competitors.md` |
| 20 | **Go PTY: UserExistsError/conpty** | ConPTY nativo, sem DLLs, API completa (Start, Read, Write, Wait, Close, Resize). Requer Windows 10 1809+. | `research-go-pty-windows.md` |
| 21 | **xterm.js: @xterm/xterm + addons** | `@xterm/xterm` + `@xterm/addon-fit` + `@xterm/addon-webgl-renderer`. Binary WS frames para dados, JSON text frames para controle. WebGL renderer essencial (10x mais rápido). | `research-xtermjs.md` |
| 22 | **Limite WebGL contexts** | Chromium ~16 WebGL contexts. Para 20+ terminais: lazy mount/unmount + canvas renderer fallback. | `research-xtermjs.md` |

---

## ❓ Questões em aberto

| # | Questão | Prioridade | Status | Notas |
|---|---------|-----------|--------|-------|
| 1 | **Mecanismo de dispatch** | 🔴 Crítica | ✅ Resolvido | MCP + escreve `text+\r` no PTY (decisão #15, #16) |
| 2 | **Detecção de conclusão** | 🔴 Crítica | ✅ Resolvido | Heurística OmniRift-style (decisão #18) |
| 3 | **Prompts interativos** ("Shall I proceed?") | 🟡 Alta | 🔄 A resolver no spike | Agente pode travar esperando input. No MVP: repassar pro usuário? |
| 4 | **Parsing de output** | 🟡 Alta | ✅ Resolvido | Não parsear no MVP. Orchestrator (LLM) interpreta o texto. |
| 5 | **Canvas engine** | 🟡 Alta | ✅ Resolvido | React Flow + Pixi.js (decisão #19) |
| 6 | **Protocolo Go ↔ UI** | 🟡 Alta | ✅ Resolvido | WebSocket. Binary frames = dados do terminal, text frames = controle (resize, status). |
| 7 | **Startup do Go Worker** | 🟢 Média | 🔄 A resolver no spike | Electron main process inicia/para processo Go. |
| 8 | **Agent crash recovery** | 🟢 Média | 🔄 A resolver no spike | Go detecta processo morto, reporta status "dead". |
| 9 | **Git ops concorrentes** | 🟢 Média | 🔄 A resolver no spike | Serializar git ops por worktree no Go (fila simples). |
| 10 | **Formato da mensagem de dispatch** | 🟡 Alta | 🔄 A definir | O que é uma "task"? Texto livre? Estrutura? |
| 11 | **Packaging** | 🟢 Média | 🔄 A resolver depois | Bundlar Go binário com Electron installer. |
| 12 | **Elixir no futuro** | ⚪ Baixa | 🔄 Longo prazo | Se distribuir agentes em múltiplas máquinas. |
| 13 | **Stack de libs restantes** | 🟡 Alta | 🔄 A definir | State management (Zustand/Jotai), SQLite lib, etc. |
| 14 | **Regex profiles por agente** | 🟡 Alta | 🔄 A definir no spike | Padrões de prompt: Claude Code, Codex, Gemini CLI, OpenCode. |
| 15 | **VT100 parser em Go** | 🟡 Alta | 🔄 A definir no spike | Biblioteca para parsear VT100 e obter texto limpo da tela. |

---

## 📋 MVP scope final (8 itens)

1. Abrir workspace Git (detectar `.git`)
2. Criar `.agent-infinite/` com `canvas.json`
3. Canvas com Orchestrator + Agent nodes
4. Criar worktree por time (Go Worker)
5. Abrir terminal na worktree (PowerShell only)
6. Orchestrator faz dispatch de tarefa para Agent
7. Output do Agent volta para o Orchestrator
8. Persistir canvas em JSON

**Cortado pro v1.1+**: WSL/Git Bash, environment check, branch listing, diff viewer, SQLite, approval gates, sandbox.

---

## 🏗️ Arquitetura técnica (estado atual)

```
┌─────────────────────────────────────────┐
│         Electron + React + TS           │  ← UI / Canvas / xterm.js
│  React Flow + Pixi.js (canvas)          │
│  @xterm/xterm + addon-fit + addon-webgl │
└──────────────┬──────────────────────────┘
               │ WebSocket
               │ (binary = terminal data, text = control)
┌──────────────┴──────────────────────────┐
│              Go Backend                  │  ← 1 binário
│  ┌──────────────────────────────────┐    │
│  │ Core: state, events, canvas mgr  │    │
│  │ MCP Server: dispatch_task,        │    │
│  │   get_agent_output, get_status    │    │
│  └──────────────────────────────────┘    │
│  ┌──────────────────────────────────┐    │
│  │ Worker: ConPTY (UserExistsError) │    │
│  │   PTY manager, process lifecycle  │    │
│  │   Completion detector (heurística)│    │
│  │   Git/worktree operations        │    │
│  └──────────────────────────────────┘    │
└──────────────┬──────────────────────────┘
               │
    ┌──────────┼──────────┐
    ↓          ↓          ↓
 PowerShell  Claude    Codex / Gemini
 (PTY)      Code(PTY)  CLI (PTY)
```

### Completion detection (heurística OmniRift-style)
```
Sinais combinados (polling 300ms):
  1. Process group: há subprocess em foreground? → Working
  2. Quiescence: sem output no PTY por 400ms
  3. Regex perfil: VT100 parse + match do prompt

Estados: Working → Blocked → Done → Idle → Dead
```

### Dispatch flow
```
Orchestrator (Claude Code) chama MCP tool
  → MCP server recebe dispatch_task(agent_id, task)
  → Go escreve task + "\r" no PTY do agente alvo
  → Tool retorna "dispatched" (assíncrono)
  → Agente processa no seu TUI
  → Completion detector detecta "Done"
  → Event stream notifica orchestrator
  → Orchestrator chama get_agent_output (se precisar)
```

---

## 🎯 Próximos passos: Spikes

### Spike A: Terminal real (Go + ConPTY + xterm.js)
- Go: `conpty.Start("powershell.exe")` → WebSocket → xterm.js
- Validar: digito no xterm.js → PowerShell executa → output volta
- Validar: resize sync (xterm.js ↔ ConPTY)

### Spike B: Detecção de completion (heurística)
- Go: lê PTY output, detecta quiescence (400ms sem output)
- Go: regex match do prompt (`(?m)^\s*[❯>›]\s*$`)
- Validar: roda comando → detecta quando terminal volta pro prompt
- Validar: estados Working → Done

### Spike C: Dispatch (MCP + escreve no PTY)
- Go: MCP server com tool `dispatch_task`
- Tool escreve `text+\r` no PTY do agente alvo
- Validar: orchestrator chama tool → texto aparece no terminal do agente
- Validar: tool retorna "dispatched" assíncrono

---

## 📚 Research reports (referência)

| Arquivo | Tópico |
|---------|--------|
| `research-claude-code.md` | Claude Code MCP, stream-json, non-interactive mode, completion detection |
| `research-go-pty-windows.md` | Go PTY libs, ConPTY, UserExistsError/conpty |
| `research-xtermjs.md` | xterm.js, WebSocket, Electron, addons, performance |
| `research-competitors.md` | OmniRift + Maestri — arquitetura, completion detection, dispatch |