# Inventário da barra lateral do OmniRift

Data: 16 de julho de 2026  
Fonte: capturas de tela fornecidas para referência de produto

## Como ler este inventário

Este documento registra os itens visíveis nas capturas da barra lateral do OmniRift. Ele não afirma
que todos os itens existentes no produto foram capturados. Quando uma descrição apareceu cortada,
o nome principal foi mantido e a observação de truncamento foi preservada.

O objetivo é permitir uma discussão posterior sobre quais capacidades fazem sentido para o Agent
Infinite, sem tratar a interface do concorrente como especificação obrigatória.

## Cabeçalho e projeto

- Marca **OmniRift**.
- Subtítulo **Canvas infinito · OmniForge**.
- Ações visuais de layout e recolhimento da barra.
- Seção **Projeto**:
  - Selecionar pasta…
- Seção **Workspace**:
  - Campo com o nome do workspace.
  - Salvar.
  - Abrir.

## Paralelos

- Seção **Paralelos**.
- Indicador `git-native`.
- Ações de criar/configurar paralelo.
- Paralelo **Principal**.
- Contador de itens/agentes do paralelo.

## Novo agente

Modelos e provedores visíveis na captura:

- **OmniAgent** — agente estruturado via ACP; descrição truncada.
- **OmniAgent · Codex** — OmniAgent via Codex; descrição truncada.
- **OmniAgent · Hermes** — OmniAgent via Hermes; descrição truncada.
- **Orquestrador** — Claude Code; descrição truncada.
- **Shell** — terminal puro do sistema.
- **Claude Code** — Anthropic Claude Code; descrição truncada.
- **Codex** — OpenAI Codex CLI.
- **OpenCode** — OpenCode, `sst.dev`.
- **Antigravity** — Google Antigravity; descrição truncada.
- **Gemini CLI** — Google Gemini CLI; descrição truncada.
- **Aider** — pair programmer open-source; descrição truncada.
- **Kilo Code** — CLI do Kilo Code, fork do Roo; descrição truncada.

## Roles

A seção possui ações no cabeçalho para administrar os papéis. Os papéis visíveis são:

- **Orquestrador**, com marcador de provider **Claude**.
- **DevOps**.
- **Frontend**.
- **Backend**.
- **DBA**.
- **Code Reviewer**.
- **QA / Tester**.
- **Arquiteto**, com marcador de provider **Codex**.
- **Security**.
- **Debugger**.

## Ferramentas — Orquestrar

- **Arquiteto de Pipeline**.
- **Kanban do projeto**.
- **Terminal-Bench**.
- **Routines**.
- **TURBO mode**.

## Ferramentas — Agentes

- **CLIs de IA**.
- **Compressores de token**.
- **Skills dos agentes**.
- **Conexões de memória**.
- **MCP Servers**.
- **Memória dos agentes**.

## Projeto & Arquivos

- **Central de copia-cola**.
- **Histórico de sessões**.
- **Hooks do paralelo**.
- **Lembretes**.
- **OmniFS — Pasta de agentes**.
- **Repositórios Git**.
- **Snapshots do canvas**.

## App & Sistema

- **Configurações**.
- **Ajuda / Manual**.
- **Aparência**.
- **Dispositivos móveis**.
- **Feature flags**.
- **Novidades**.
- **Uso de Tokens**.

## MCP Agents

- Área para registrar agentes por terminais.
- Ação para adicionar agentes.
- Limite configurável de agentes em reação/registro; o valor exibido na captura é `5`.
- Texto de apoio: adicionar terminais para registrar agentes.

## Specs

- Área para listar as specs de um projeto.
- Ações no cabeçalho para criar, importar ou abrir uma spec.
- Estado vazio com a orientação: abrir um projeto para listar specs.

## Rodapé

- Consumo do dia, exibido como **Hoje: $28.55**.
- Identificação do filesystem/projeto: **OmniFS**.
- Versão e build: **v0.1.131 · build local**.
- Buscar atualização.
- Licença.
- Seja beta.
- Feedback.
- Grupo WhatsApp.
- Enviar diagnóstico.

## Candidatos para discussão no Agent Infinite

Estes itens são apenas candidatos derivados do inventário. Nenhum deles está aprovado para entrar
no MVP automaticamente:

- Papéis reutilizáveis para criar agentes com role, provider e instruções iniciais.
- Catálogo de templates de agentes e providers.
- Histórico navegável de sessões e dispatches.
- Snapshots/versionamento do canvas.
- Área de MCP Servers configuráveis.
- Skills associadas a cada agente ou role.
- Conexões de memória e memória por agente/time.
- Kanban ou painel de tarefas ligado aos dispatches.
- Rotinas agendadas ou fluxos repetíveis.
- Registro explícito de terminais/agentes externos.
- Uso de tokens e custo por workspace/time.
- Diagnóstico exportável para suporte.
- Central de arquivos, clipboard e artefatos do workspace.
- Redesign da barra lateral com cabeçalho, área de Workspace e seção **Git Worktrees**.
- Clarificação visual da relação entre Agent Teams e Git Worktrees, evitando que a criação de um
  agente pareça criar um worktree novo.
- Área própria para gerenciamento de Agent Teams e criação de Git Worktrees vinculados a um team.

## Perguntas para a próxima discussão

1. Quais itens aumentam diretamente o valor do canvas e da delegação?
2. Quais itens devem ser recursos do workspace e quais devem ser recursos do agente?
3. Memória, skills e hooks devem continuar por sessão, por agente ou por projeto?
4. O catálogo de providers deve ser fixo, detectado localmente ou extensível por configuração?
5. Histórico, snapshots e custo devem entrar antes de novas integrações de providers?
