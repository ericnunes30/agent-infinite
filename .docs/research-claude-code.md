# Pesquisa: Claude Code CLI — Interação Programática

> Pesquisa realizada em 2026-07-15 com Claude Code v2.1.210 (instalação local, comandos testados e verificados)

---

## 1. Suporte MCP — YES, first-class

Claude Code pode atuar como **MCP client** (conectar aos seus servers) e **MCP server** (expor-se para outros apps).

### 3 formas de configurar

#### A. Settings file (`~/.claude/settings.json`) — escopo global/usuário

```json
{
  "mcpServers": {
    "agent-infinite": {
      "type": "stdio",
      "command": "node",
      "args": ["C:/path/to/agent-infinite-mcp.js"],
      "env": { "API_KEY": "xxx" }
    }
  }
}
```

**HTTP/SSE transport:**
```json
{
  "mcpServers": {
    "agent-infinite-remote": {
      "type": "http",
      "url": "http://localhost:3000/mcp",
      "headers": { "Authorization": "Bearer token123" }
    }
  }
}
```

#### B. CLI flags — por invocação (crítico para o Agent Infinite)

```bash
# De um arquivo JSON
claude -p --mcp-config ./my-mcp-config.json --strict-mcp-config "your prompt"

# JSON inline
claude -p --mcp-config '{"mcpServers":{"agent-infinite":{"type":"stdio","command":"node","args":["server.js"]}}}' --strict-mcp-config "your prompt"
```

`--strict-mcp-config` faz Claude Code usar **apenas** os servers do `--mcp-config`, ignorando outros servers configurados.

#### C. Project-level `.mcp.json` (na raiz do projeto)

Mesmo formato. Mostra como "Pending approval" até o usuário aprovar.

### Commands de gerenciamento

```bash
claude mcp list                                    # lista servers + health
claude mcp add my-server -- node server.js         # add stdio server
claude mcp add --transport http name https://url   # add HTTP server
claude mcp add-json name '{"command":"node","args":["s.js"]}'  # add from JSON
claude mcp get my-server                           # detalhes
claude mcp remove my-server                        # remover
```

### Tools expostas

Uma vez conectado, Claude Code expõe tools com prefixo `mcp__<server-name>__<tool-name>`:
- `mcp__agent_infinite__dispatch_task`
- `mcp__agent_infinite__get_agent_output`

O modelo chama essas tools autonomamente durante a sessão. Resultados voltam como `tool_result` events no stream-json.

### Claude Code COMO MCP server

```bash
claude mcp serve
```

Inicia Claude Code como MCP server (stdio), expondo suas built-in tools (Bash, Edit, Read, etc.) para outros MCP clients.

---

## 2. Modo Non-Interactive — `--print` / `-p`

```bash
# Básico: single prompt, text output, exit
claude -p "explain this codebase"

# Pipe via stdin
echo "what is 2+2?" | claude -p

# Com JSON output
claude -p --output-format json "list all files"
```

**Comportamentos do `-p`:**
- Roda um prompt (ou stream de prompts) e sai — sem REPL interativo
- Skips workspace trust dialog
- Suporta `--output-format text|json|stream-json`
- Suporta `--input-format text|stream-json`
- Funciona com `--max-budget-usd`

---

## 3. Detecção de Prompt — NÃO fazer, usar stream-json

### O prompt interativo

O TUI do Claude Code usa Ink (React for CLIs) com bordas decorativas, animações, status lines, color codes. **Não é** um simples `❯` ou `>` que você pode grep de forma confiável. Detectar "prompt retornou" scrapeando terminal é **frágil e não recomendado**.

### Alternativas (todas verificadas)

| Método | Como detectar "done" | Confiabilidade |
|---|---|---|
| `stream-json` output | Event `{"type":"result","terminal_reason":"completed"}` | **Excelente** — estruturado |
| `json` output | Processo sai; parse do JSON array, último elemento é `result` | **Excelente** |
| `-p` text mode | Processo sai com code 0; stdout tem a resposta | Bom |

### Event `result` (verificado)

```json
{
  "type": "result",
  "subtype": "success",
  "is_error": false,
  "result": "4.",
  "stop_reason": "end_turn",
  "terminal_reason": "completed",
  "num_turns": 1,
  "duration_ms": 1591,
  "total_cost_usd": 0.0618,
  "session_id": "53751379-..."
}
```

- `terminal_reason`: `"completed"`, `"interrupted"`, etc.
- `stop_reason`: `"end_turn"`, `"tool_use"`, `"max_tokens"`, etc.

---

## 4. Interação Programática — descoberta-chave

### Arquitetura: Bidirectional stream-json (RECOMENDADO)

```bash
claude -p \
  --input-format stream-json \
  --output-format stream-json \
  --mcp-config ./agent-infinite-mcp.json \
  --strict-mcp-config \
  --session-id <uuid> \
  --no-session-persistence
```

**Cria um processo long-running onde:**
- Você escreve JSON no **stdin** (uma mensagem por linha)
- Claude Code escreve eventos JSON no **stdout** (uma por linha, realtime)
- Mesma `session_id` persiste entre turns (verificado)

### Input (stdin) — verificado

```json
{"type":"user","message":{"role":"user","content":"Say A"}}
{"type":"user","message":{"role":"user","content":"Say B"}}
```

Cada linha é uma mensagem user separada. Claude processa em ordem, mantém contexto. stdin fica aberto — pode mandar mensagens a qualquer momento.

### Output events (stdout) — verificado

| Event type | Quando emitido | Key fields |
|---|---|---|
| `system` (init) | Início da sessão | `session_id`, `tools[]`, `mcp_servers[]` (com status), `model` |
| `rate_limit_event` | Após cada turn | Rate limit status |
| `assistant` | Resposta do modelo | `message.content[]` (text/tool_use), `message.usage`, `stop_reason` |
| `tool_result` | Após execução de tool | Tool output |
| `result` | **Fim de cada turn** | `result`, `stop_reason`, `terminal_reason`, `num_turns`, `total_cost_usd`, `is_error` |

**`result` event = sinal de "task complete".** Todo turn termina com um. Detectar completion = checar `type: "result"` + `terminal_reason: "completed"`.

### Flags úteis

```bash
--include-partial-messages     # streaming text chunks token-a-token
--include-hook-events          # hook lifecycle events
--replay-user-messages         # echo user messages back
--session-id <uuid>            # pin session ID (para resume)
--no-session-persistence       # não salvar sessão em disco
--max-budget-usd 5.00          # cap de spending
--allowed-tools "Bash Read Edit"          # whitelist tools
--disallowed-tools "Bash(rm *)"           # blacklist tools
--permission-mode bypassPermissions       # auto-approve
--dangerously-skip-permissions            # skip all permission checks
--system-prompt "..."          # custom system prompt
--append-system-prompt "..."   # append ao default
--agents '{"reviewer":{...}}'  # definir sub-agents
--model opus|sonnet|haiku      # seleção de modelo
--effort low|medium|high       # reasoning effort
```

### Session resume

```bash
claude -p -r <session-id> --output-format json "next prompt"
claude -p -c --output-format json "next prompt"   # continuar mais recente
claude -p -r <session-id> --fork-session "next prompt"  # fork
```

### Structured output validation

```bash
claude -p --json-schema '{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}' "extract the name"
```

### Background agents

```bash
claude --background            # inicia como background agent
claude agents                  # gerenciar background agents
```

---

## Arquitetura Recomendada para Agent Infinite

```
Agent Infinite (Go backend)
    │
    ├── spawns: claude -p --input-format stream-json --output-format stream-json
    │            --mcp-config agent-infinite-mcp.json --strict-mcp-config
    │            --session-id <uuid> --dangerously-skip-permissions
    │
    ├── stdin  → envia {"type":"user","message":{"role":"user","content":"task..."}}  por tarefa
    ├── stdout ← lê JSON lines: system/init, assistant, tool_result, result
    │
    └── detecta completion via {"type":"result","terminal_reason":"completed"}
```

**Para MCP integration** (Claude Code chamando de volta o Agent Infinite):
- Rodar MCP server no Agent Infinite expondo `dispatch_task` / `get_agent_output`
- Passar via `--mcp-config` para o modelo poder chamar as tools autonomamente
- Tools aparecem como `mcp__agent_infinite__dispatch_task` etc.

**Para "agente terminou?":** Parse stdout por `{"type":"result",...}` — `terminal_reason: "completed"` = done, `is_error: true` = falha, `result` = texto final.