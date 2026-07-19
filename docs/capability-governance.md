# Governança de MCPs e skills (0.12.0)

O Agent Infinite mantém um inventário global em `%LOCALAPPDATA%\AgentInfinite\capabilities.json`. O scanner lê configurações e diretórios de Claude Code, Codex, Pi e OpenCode, mas nunca corrige, remove ou regrava esses arquivos. Capacidades desaparecidas permanecem no catálogo com status `missing` para preservar referências históricas.

Cada registro externo começa em `provider_default`: sessões iniciadas pelo aplicativo preservam a configuração atual do provider. Em `curated`, o item só entra na sessão quando estiver selecionado no agente (diretamente ou por uma role usada como preset). Em `blocked`, ele é excluído de toda sessão iniciada pelo Agent Infinite. Essas políticas não afetam CLIs abertos fora do aplicativo.

## Segredos

Specs externas são armazenadas mascaradas. Antes do spawn, o backend relê a configuração original somente em memória. Ao promover ou criar um MCP gerenciado, os valores secretos são protegidos com DPAPI no cofre local do Windows; catálogo, API e logs expõem apenas os nomes. A promoção nunca reutiliza automaticamente uma credencial externa.

## Isolamento por provider

- Claude recebe uma configuração MCP estrita e plugins temporários contendo apenas as skills efetivas.
- Codex recebe overrides de sessão para MCPs e `skills.config`.
- Pi usa `--no-skills`, caminhos explícitos e uma extensão temporária para MCPs HTTP autorizados.
- OpenCode recebe `OPENCODE_CONFIG_DIR` e `OPENCODE_CONFIG_CONTENT` temporários.

Os overlays ficam no diretório de runtime e são removidos ao encerrar a sessão ou reconciliar o backend. Se a versão instalada não suportar o enforcement solicitado, o spawn falha explicitamente; o aplicativo não informa um bloqueio que não conseguiu aplicar.

O teste de compatibilidade com os quatro CLIs instalados é opt-in para não consumir sessões reais em CI: execute `AGENT_INFINITE_REAL_PROVIDERS=1 go test ./internal/agent ./internal/app` no PowerShell usando `$env:AGENT_INFINITE_REAL_PROVIDERS='1'`.

## Persistência do workspace

`canvas.json` guarda `roleProfiles` e, em cada node, `roleProfileId`, `mcpIds` e `skillIds`. Uma role é um preset: editar a role afeta novos agentes; agentes existentes mantêm sua seleção. Alterações em um agente em execução entram em vigor no próximo restart explícito.
