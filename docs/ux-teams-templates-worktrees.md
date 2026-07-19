# UX de Teams, Templates e Git Worktrees (0.11.0)

## Team Templates independentes

O botão `+` de **TEAM TEMPLATES** cria uma composição independente, sem exigir um Team existente.
O template abre em seu próprio canvas e salva automaticamente nome, descrição, agentes, roles,
providers, auto-start, posições e conexões. Nenhum Worktree, branch, terminal ou dispatch faz parte
do template.

**Salvar como template** no canvas de um Team permanece como atalho secundário para copiar uma
composição já existente. **Criar Team a partir deste template** instancia novos IDs sem alterar o
template original.

## Importar um template no Worktree

Um Git Worktree vazio oferece a ação **Importar Template** no próprio canvas. A janela mostra o
destino, a branch ativa e a composição do template antes da confirmação.

- Em um Worktree associado, a composição é instanciada no Team existente.
- Em um Worktree independente, o Worktree continua independente e uma definição de Team é criada
  para preservar a identidade dos nodes e das conexões.
- A importação copia os nodes e as conexões com novos IDs; sessões e dispatches nunca são copiados.

## Navegação

- Selecionar um **Team** abre o canvas de definição e permite adicionar agentes sem criar ou escolher
  um worktree.
- Selecionar um **Team Template** abre sua página de detalhes. Nada é criado até o usuário confirmar
  **Criar Team a partir deste template**.
- Selecionar um **Git Worktree** abre seu canvas operacional, independentemente de ele possuir um
  Team vinculado.

## Git Worktrees

Ao criar um worktree, o vínculo com Team é opcional. O usuário pode:

- criar uma branch nova a partir de qualquer branch local;
- montar o worktree usando uma branch local existente que ainda não esteja em outro checkout;
- criar um worktree independente e importar uma definição de agente existente para ele.

O vínculo temporário usado ao executar um Team continua sem alterar o canvas de definição ou a
seleção global.

## Acabamento da lateral

Os itens usam uma hierarquia consistente: nome, badge de contexto, referência curta e contagem. As
ações de exclusão têm área de clique própria, feedback de hover e confirmação, sem interferir na ação
principal do item.
