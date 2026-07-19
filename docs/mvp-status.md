# Status do MVP — Agent Infinite

Data de referência: 19 de julho de 2026
Versão de referência: `0.15.5`

Este documento separa o escopo original do MVP, o que já foi comprovado e o que ainda impede o
fechamento operacional da próxima entrega.

## Escopo do MVP

O MVP é composto por oito capacidades: abrir um workspace Git; criar o estado persistido do
workspace; exibir nodes de orquestrador e agente no canvas; criar e vincular worktrees a times; abrir o
terminal na worktree; delegar uma tarefa; devolver o resultado ao orquestrador; e persistir o canvas
em JSON.

## Aprovado ou implementado

- Workspace Git, estado `.agent-infinite` e persistência do canvas.
- Times, worktrees, nodes, edges e filtro visual por time/worktree.
- Terminal PowerShell/ConPTY para Claude Code, Codex, Pi e OpenCode.
- Hooks efêmeros por sessão, detector de terminal e recuperação após reinício.
- Delegação Codex → Codex com retorno associado ao dispatch e sem polling repetitivo, aprovada em
  campo na versão `0.2.3`.
- Tema escuro como padrão e tema claro validados, com persistência local e terminal preservado em
  esquema escuro.
- Seleção inicial do primeiro time/worktree implementada como estado padrão, sem clique simulado.
- Barra lateral 0.3.0 entregue com cabeçalho, Workspace e seção **Git Worktrees**.
- Teams e Git Worktrees agora são entidades separadas: um team pode possuir vários worktrees.
- Criação de worktree, vínculo ao team e escolha do worktree pelo agente estão disponíveis na API e na UI.
- Team Templates reutilizáveis, canvas de definição independente e execução contra um worktree escolhido.
- Governança global de MCPs e skills para Claude, Codex, Pi e OpenCode, com políticas herdada,
  curada e bloqueada aplicadas somente às sessões iniciadas pelo aplicativo.
- Catálogo e seleção persistente de modelos em Roles, agentes, orquestradores e templates.
- Ativação em lote dos terminais offline, previews de baixo consumo e expansão individual em tela cheia.
- Exclusão segura de worktree com encerramento e remoção automática dos nodes runtime, preservando a
  definição do Team e recusando checkouts com alterações não commitadas.
- Prontidão do Pi orientada por lifecycle hooks, sem depender do desenho visual do compositor para
  entregar tarefas delegadas.

## Pendências para fechar o MVP

1. **Validar o instalador 0.15.5 no ambiente de campo**, incluindo delegação Codex → Pi após um
   restart completo do aplicativo e dos terminais.
2. **Publicar a release no repositório remoto**, anexando o instalador e seu checksum.
3. **Configurar assinatura de código e atualização automática**, que continuam fora do MVP atual.

## Entrega 0.3.0 (histórico)

Esta atualização modifica a barra lateral do Agent Infinite inspirando-se no inventário do OmniRift,
sem copiar sua identidade visual. O escopo entregue é:

- adicionar um cabeçalho de aplicação com identidade e ações principais;
- incluir uma área de **Workspace** para o contexto do repositório aberto e suas ações principais;
- incluir a área de worktrees e renomear sua apresentação de **Paralelos** para **Git Worktrees**;
- manter a seleção de times e o canvas sincronizados com o contexto ativo;
- preservar o modelo de dados, os IDs e o contrato de comunicação existentes até que uma mudança de
  domínio seja decidida separadamente.

- a seleção inicial aponta para o primeiro Git Worktree disponível;
- os IDs, o modelo de dados e o contrato de comunicação permaneceram inalterados;
- os testes unitários, lint, build e E2E cobrem a nova apresentação e a seleção inicial.

## Artefato 0.3.0 (histórico)

- Instalador: `Agent-Infinite-Setup-0.3.0-x64.exe`.
- Tamanho: 123.928.045 bytes.
- SHA-256: `A351B575A45824DAE38E1209EE960221A325E8452B9A08687EBDC9F14243EC46`.

## Registro histórico — esclarecimento Agent Teams e Git Worktrees

As dúvidas e propostas abaixo foram resolvidas na entrega 0.4.0 descrita ao final deste documento.

### Dúvida registrada

Na sidebar, os itens **Teste** e **teste 2** aparecem dentro de **Git Worktrees**, mas o produto
também usa o conceito de time de agentes. Isso pode fazer parecer que criar um Agent Team e criar
um Git Worktree são sempre ações diferentes.

### Regra do modelo anterior (0.3.0)

No MVP, um time é a unidade de isolamento do código: cada time possui um Git Worktree, uma branch e
um orquestrador. Portanto:

- criar um **novo time** cria um novo Git Worktree;
- criar um **agente** dentro de um time reutiliza o Git Worktree desse time;
- dois agentes do mesmo time não devem criar worktrees separados;
- o item mostrado em **Git Worktrees** representa o time e seu workspace físico.

### Ajuste que originou a release 0.4.0

Deixar essa relação explícita na interface, sem alterar o domínio imediatamente:

- o botão da seção deve dizer **Create Git Worktree** ou **Novo time/worktree**;
- o botão de agente deve dizer **Add Agent** e ficar associado ao Git Worktree ativo;
- cada item deve mostrar claramente nome do time, branch e caminho do worktree;
- a documentação deve explicar que o Agent Team é o agrupamento lógico que compartilha aquele
  worktree;
- separar Agent Teams e Git Worktrees como entidades independentes era a decisão pendente; ela foi
  aprovada e implementada na release 0.4.0 com migração aditiva.

Essa mudança é de clareza de produto e UX. Se implementada como funcionalidade de UI, deve entrar na
versão funcional `0.4.0`, não como correção `0.3.1`.

### Direção de produto proposta

O próximo modelo deve oferecer uma área própria para gerenciar **Agent Teams** e permitir criar
**Git Worktrees** separadamente, vinculando cada worktree a um team. A relação desejada é:

```text
Workspace
├── Agent Team
│   ├── Git Worktree / branch A
│   └── Git Worktree / branch B
└── Agent Team
    └── Git Worktree / branch C
```

Isso permite que um mesmo team opere em mais de uma branch sem duplicar o team lógico. A criação de
um agente escolhe o team e, quando necessário, o worktree em que ele trabalhará. O canvas mostra o
worktree ativo por padrão; selecionar o team remove o filtro de worktree e mostra todos os seus nodes.

## Fora do MVP

Atualização automática, assinatura de código, WSL/Git Bash, SQLite, máquinas remotas, diff viewer,
approval gates e sandbox continuam fora do MVP.

## Referências

- O incidente UI-04 está detalhado no relatório de teste de campo.
- O contrato de comunicação e sua aprovação estão documentados na arquitetura de comunicação entre
  agentes.

## Entrega 0.4.0 — Agent Teams e Git Worktrees

Esta release transforma a relação entre Agent Teams e Git Worktrees em um modelo explícito e
aditivo. Canvases existentes são migrados ao serem abertos: o worktree antigo preserva seu ID,
branch e diretório físico, e os nodes passam a apontar para ele.

- a seção **Agent Teams** gerencia os times lógicos;
- a seção **Git Worktrees** cria e seleciona branches vinculadas a um team;
- um team pode possuir vários worktrees;
- a criação de agente permite escolher team e worktree;
- `POST /api/worktrees` e `DELETE /api/worktrees/{id}` administram worktrees;
- clientes antigos continuam compatíveis: criar um team cria o worktree inicial por padrão;
- a seleção inicial aponta para o primeiro team e o primeiro worktree desse team.

O instalador e a validação de campo da versão 0.4.0 permanecem como o próximo aceite operacional.

## Artefato 0.4.0

- Instalador: `Agent-Infinite-Setup-0.4.0-x64.exe`.
- Tamanho: 123.941.890 bytes.
- SHA-256: `A0C3F16468EB2649424E5331E38E1CB36CABADCAD21ACAD1D32083B568D086C7`.

## Correção 0.4.1 — seleção Team → Worktree

Esta correção remove a ambiguidade visual entre **Agent Teams** e **Git Worktrees**:

- selecionar um Agent Team passa a selecionar automaticamente o primeiro worktree vinculado a ele;
- a seção Git Worktrees mostra somente os worktrees do Team ativo;
- criar um worktree ou agente começa no Team/Worktree atualmente selecionado;
- a marca do produto permanece apenas no cabeçalho global, sem repetição na barra lateral.

## Artefato 0.4.1

- Instalador: `Agent-Infinite-Setup-0.4.1-x64.exe`.
- Tamanho: 123.942.405 bytes.
- SHA-256: `8A65D7A10A60BAD7F51C541C33539BE1931C63840F8F08F45470011D565401C2`.

## Entrega 0.5.0 — seleção de canvas por Git Worktree

Esta entrega remove o vínculo incorreto de navegação entre Agent Teams e Git Worktrees.

- somente **Git Worktrees** selecionam e filtram o canvas;
- a lista de worktrees mostra todos os worktrees do workspace e o Team vinculado em cada item;
- **Agent Teams** agora abre uma página própria de gestão, sem alterar o worktree ou o canvas ativo;
- o Team é escolhido no formulário de criação do Git Worktree;
- um novo agente herda obrigatoriamente o Git Worktree ativo e, por consequência, seu Team;
- a rail lateral continua abaixo de Git Worktrees com **Novo Agente**, **Roles** e **Ferramentas**.

Essa separação permite que um mesmo Agent Team tenha diversos worktrees/branches sem transformar
Team em um filtro implícito do canvas.

## Artefato 0.5.0

- Instalador: `Agent-Infinite-Setup-0.5.0-x64.exe`.
- Tamanho: 123.943.321 bytes.
- SHA-256: `032E454E8ABEA741B9ED4DBFFB5F2E48928DE680D228EFD4A5C1E1D25B6BCCD3`.

## Entrega 0.6.0 — providers Pi e OpenCode

- Pi é iniciado no terminal normal com uma extensão temporária que expõe as ferramentas de
  comunicação do Agent Infinite e reporta o ciclo de vida da sessão.
- OpenCode usa MCP remoto e plugin temporário por nó; a configuração e o token vivem somente no
  runtime do processo.
- O papel de orquestrador é independente do provider: Pi e OpenCode podem originar e receber
  delegações através das conexões existentes do canvas.
- OpenCode é atualizado fora do app com `npm install -g opencode-ai@latest`; Pi permanece na
  versão instalada.
