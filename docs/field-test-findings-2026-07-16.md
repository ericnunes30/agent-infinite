# Relatório de teste de campo — Agent Infinite 0.1.0

Data do teste: 16 de julho de 2026  
Status: correções de UI entregues na 0.1.1; arquitetura de comunicação definida e pendente de implementação  
Público: engenharia, produto e futuras sessões de manutenção

## 1. Objetivo

Este documento registra os resultados do primeiro teste manual do instalador Windows do Agent
Infinite. Ele separa fatos observados de hipóteses, define critérios de aceite para as correções de
UI e preserva as questões ainda abertas sobre roteamento de tarefas entre agentes.

Depois da leitura, uma pessoa que não participou do teste deve conseguir:

1. reproduzir os três problemas de UI;
2. implementar e verificar as correções sem alterar a comunicação MCP;
3. retomar a discussão sobre comunicação sabendo o que já foi comprovado;
4. distinguir um subagente nativo do provider de um agente conectado no canvas.

## 2. Escopo do teste

O teste foi executado com o instalador Windows x64 da versão 0.1.0. O workspace usado foi um
repositório Git real com dois times:

- **Teste**, com um orquestrador Codex e um agente Codex;
- **teste 2**, com um orquestrador Claude.

O canvas continha uma edge `delegates_to` entre o orquestrador e o agente do time Teste. Os
terminais ConPTY abriram corretamente e aceitaram interação direta com os providers.

## 3. Resultado resumido

| Área                            | Resultado    | Prioridade | Decisão                        |
| ------------------------------- | ------------ | ---------- | ------------------------------ |
| Instalação e inicialização      | Aprovado     | —          | Preservar                      |
| Tema escuro                     | Aprovado     | —          | Preservar como padrão          |
| Tema claro                      | Ausente      | Média      | Implementar agora              |
| Terminal Claude/Codex           | Aprovado     | —          | Não alterar nesta etapa        |
| Filtro visual por time          | Falhou       | Alta       | Corrigir agora                 |
| Alinhamento dos conectores      | Falhou       | Média      | Corrigir agora                 |
| Delegação por linguagem natural | Inconclusiva | Alta       | Adiar para debate arquitetural |

## 4. Comportamento aprovado

### 4.1 Instalação

O aplicativo instalado abriu corretamente, iniciou o backend empacotado e reabriu o workspace
persistido. Não houve falha visível de preload, WebSocket ou lifecycle nessa execução.

### 4.2 Terminal

O painel de terminal apresentou a TUI completa do Codex e permitiu conversar normalmente. O mesmo
resultado foi relatado para os dois agentes testados. Resize, renderização e entrada do terminal não
fazem parte das correções atuais.

### 4.3 Direção visual

O tema escuro industrial foi aprovado. A implementação do tema claro deve ser uma tradução da mesma
linguagem visual, e não um redesign independente. Devem permanecer:

- grid técnico;
- tipografia compacta;
- acentos de telemetria;
- hierarquia entre canvas, rail, inspector e terminal;
- densidade adequada para operação prolongada.

## 5. Incidente UI-01 — edges fantasmas ao filtrar times

### Sintoma

Ao selecionar o segundo time, os nodes do primeiro time desaparecem corretamente, mas a linha que
liga o orquestrador ao agente do primeiro time continua visível no canvas.

### Reprodução

1. Criar um time com orquestrador e agente.
2. Criar uma edge entre os dois.
3. Criar um segundo time.
4. Selecionar o segundo time no rail.
5. Observar uma edge sem endpoints visíveis.

### Causa confirmada

O filtro de time marca nodes de outros times como ocultos, mas entrega a coleção completa de edges
ao React Flow. Como a edge continua no conjunto renderizado, seu path permanece mesmo quando os
dois endpoints estão ocultos.

### Correção definida

Derivar a apresentação a partir de um conjunto de IDs visíveis:

- sem time selecionado: renderizar todos os nodes e todas as edges;
- com time selecionado: renderizar somente edges cujos dois endpoints pertencem ao conjunto de
  nodes visíveis;
- manter a coleção canônica completa em estado, para que filtrar a visualização nunca apague edges
  persistidas;
- permitir desmarcar o time ativo clicando nele novamente;
- mostrar no toolbar a contagem visível no contexto selecionado.

### Critérios de aceite

- Nenhuma edge pode aparecer sem os dois endpoints visíveis.
- Alternar entre times não pode alterar o JSON persistido.
- Voltar à visão geral deve restaurar imediatamente todas as edges.
- Dispatch ativo em um time oculto não deve reaparecer como linha fantasma.

## 6. Incidente UI-02 — handle do orquestrador deslocado

### Sintoma

O círculo de saída à direita do node orquestrador aparece aproximadamente vinte pixels à frente da
borda visual do card.

### Causa confirmada

O domínio reserva 320 px de largura para o orquestrador, enquanto o elemento visual interno tem
largura fixa de 300 px. O React Flow posiciona o handle na borda do wrapper de 320 px; o usuário
enxerga a borda do card de 300 px. Essa divergência cria o espaço vazio entre card e conector.

### Correção definida

O card visual deve preencher integralmente o wrapper medido pelo React Flow:

- largura e altura em 100%;
- `box-sizing: border-box`;
- handles centralizados na borda real do card;
- área de interação preservada sem sobrepor o conteúdo;
- nenhuma mudança nas dimensões persistidas do canvas.

### Critérios de aceite

- O centro do handle deve coincidir visualmente com a borda do card.
- A edge deve começar no círculo, sem segmento horizontal vazio.
- Orquestradores e agentes devem continuar respeitando seus tamanhos persistidos.
- Zoom entre 25% e 180% não pode reintroduzir o deslocamento.

## 7. Melhoria UI-03 — tema claro

### Estado atual

O aplicativo define apenas esquema escuro e usa várias cores literais. Não existe estado de tema,
controle na interface, persistência ou sincronização com a barra nativa do Electron.

### Direção definida

O tema claro será uma “mesa técnica diurna”:

- fundo mineral claro, sem branco puro dominante;
- grid discreto em verde/cinza;
- cards claros com texto grafite e profundidade por sombras suaves;
- lime usado como sinal operacional, não como preenchimento decorativo;
- terminal permanece escuro para preservar legibilidade e fidelidade às TUIs dos providers;
- contraste e estados continuam identificáveis sem depender apenas de cor.

### Comportamento

- Adicionar botão claro/escuro na barra superior.
- Manter ambos os ícones no DOM e fazer cross-fade curto e interrompível.
- Persistir a preferência localmente com uma chave versionada.
- Manter dark como padrão para usuários existentes.
- Atualizar também cor, símbolos e contraste da title bar nativa.
- Aplicar o tema antes da primeira pintura sempre que possível, evitando flash escuro/claro.

### Critérios de aceite

- A preferência sobrevive a reload e reinício do aplicativo.
- Não há flash perceptível do tema oposto.
- Launchpad, rail, canvas, nodes, inspector, diálogos e toasts são legíveis nos dois temas.
- O terminal continua escuro nos dois temas.
- O controle tem nome acessível e área de clique mínima de 40 × 40 px.

## 8. Incidente ORCH-01 — pedido criou subagente nativo

### Resultado esperado

Ao pedir ao orquestrador para chamar “o subagente aqui do app Agent Infinite”, o provider deveria
usar `dispatch_task` para o node conectado pela edge do canvas, aguardar a conclusão e recuperar a
saída com `get_agent_output`.

### Resultado observado

O Codex iniciou um subagente interno com nome canônico semelhante a `/root/teste_agent_infinite`.
Ele aguardou esse subagente e relatou uma resposta, mas não houve evidência de que o agente conectado
no canvas recebeu a tarefa.

### Fatos confirmados

1. O processo do Codex foi iniciado com o endpoint MCP correto e bearer token por variável de
   ambiente.
2. O fluxo MCP bidirecional funcionou anteriormente quando a solicitação citava explicitamente
   `dispatch_task` e o ID do alvo.
3. As instruções atuais do MCP listam apenas IDs permitidos e não distinguem “agente do Agent
   Infinite” de “subagente nativo do provider”.
4. A documentação do Codex recomenda usar as instruções do servidor MCP para workflows entre tools
   e colocar a orientação essencial nos primeiros 512 caracteres.
5. No momento posterior da inspeção, somente o processo do orquestrador estava ativo. O agente alvo
   não estava rodando; nesse estado, um dispatch correto seria rejeitado como alvo offline.

### O que ainda não foi determinado

- Se o agente alvo estava ativo no instante exato da solicitação original.
- Se Claude e outros providers fazem o mesmo fallback para mecanismos nativos.
- Qual precedência deve existir entre agentes do canvas e subagentes internos.
- Se o backend deve iniciar automaticamente um alvo offline.
- Se a palavra “subagente” deve sempre significar node do canvas dentro do aplicativo.
- Como apresentar ao usuário a identidade escolhida antes de executar a delegação.

### Hipóteses para o debate

1. **Instrução insuficiente:** o provider não recebeu regra forte para preferir `dispatch_task`.
2. **Ambiguidade de identidade:** IDs sem labels dificultam mapear linguagem natural ao node certo.
3. **Alvo offline:** mesmo escolhendo MCP, o provider não conseguiria despachar e pode ter optado
   por um fallback nativo.
4. **Conflito de capacidades:** providers com multiagente próprio podem preferir suas tools internas
   quando o usuário usa termos genéricos.

### Decisão atual

Nenhuma mudança de comunicação será implementada na etapa de UI. O tema exige uma decisão conjunta
e deve considerar Codex, Claude e futuros providers como um problema único de roteamento e
identidade.

### Perguntas que precisam de decisão

1. Dentro do app, “agente” e “subagente” devem sempre significar node do canvas?
2. O provider pode usar subagentes nativos se não houver edge adequada?
3. Um alvo offline deve ser iniciado automaticamente, gerar confirmação ou causar erro?
4. O orquestrador deve mostrar previamente “vou delegar para X” antes da tool call?
5. Labels podem ser usadas como identidade estável ou somente IDs devem autorizar dispatch?
6. O mesmo contrato deve ser obrigatório para Claude, Codex e providers futuros?

### Resolução posterior

As decisões de identidade, precedência, lifecycle, target offline e hooks efêmeros foram fechadas em
16 de julho de 2026. A especificação canônica é [Arquitetura de comunicação entre agentes](communication-architecture.md).
Ela substitui a decisão provisória de adiamento desta seção. O incidente foi encerrado na versão
0.2.0 após implementação e validação real Claude → Codex e Codex → Claude por linguagem natural.

## 9. Ordem de implementação

### Release 0.1.1 — UI

1. Adicionar infraestrutura de tema e alternância persistente.
2. Corrigir filtro contextual de edges.
3. Corrigir geometria do card/handle.
4. Adicionar testes unitários dos derivados de visibilidade e tema.
5. Executar teste Electron nos dois temas e com dois times.
6. Gerar e validar novo instalador Windows.

### Release posterior — comunicação

1. Fechar as decisões da seção 8.
2. Definir contrato de roteamento comum aos providers.
3. Adicionar observabilidade de seleção de tool, target e fallback.
4. Implementar fixtures e testes por provider.
5. Repetir aceitação manual com linguagem natural, sem IDs nem nomes de tools.

## 10. Matriz mínima de verificação da UI

| Cenário                          | Resultado esperado                         |
| -------------------------------- | ------------------------------------------ |
| Abrir versão atualizada          | Dark continua padrão                       |
| Alternar para light              | Toda a shell muda; terminal permanece dark |
| Recarregar em light              | Light reaparece antes da interação         |
| Selecionar time sem edges        | Nenhuma edge de outro time aparece         |
| Selecionar time com edge interna | Node e edge aparecem juntos                |
| Voltar à visão geral             | Todas as entidades reaparecem              |
| Mover orquestrador               | Handle permanece aderente à borda          |
| Aplicar zoom mínimo/máximo       | Handle e edge continuam alinhados          |
| Reabrir workspace                | Layout e edges permanecem intactos         |

## 11. Fora do escopo desta etapa

- Alterar prompts ou instruções MCP.
- Iniciar agentes automaticamente durante dispatch.
- Desabilitar subagentes nativos do Codex ou Claude.
- Mudar detector de estado, ConPTY ou terminal.
- Alterar schema do canvas ou worktrees.

## 12. Resultado da correção UI 0.1.1

A rodada de UI foi concluída sem alterar prompts, instruções MCP, política de auto-start ou escolha
de subagentes. A versão 0.1.1 contém:

- tema claro com botão acessível, aplicação antes da primeira pintura, persistência local e
  sincronização da title bar nativa;
- terminal preservado em tema escuro;
- projeção de edges baseada nos endpoints visíveis, sem modificar a coleção persistida;
- seleção de time reversível ao clicar novamente no item ativo;
- contagem contextual de nodes no toolbar;
- card preenchendo o wrapper do React Flow, eliminando o deslocamento do handle.

### Evidência automatizada

- testes unitários cobrem persistência do tema e visibilidade das edges;
- teste Electron alterna o tema, recarrega o app, seleciona e desmarca um time e verifica a
  remoção/restauração da edge;
- o mesmo teste mede o centro do handle contra a borda direita do card com tolerância inferior a
  2 px;
- lint, verificação de formatação, build de produção e fluxo E2E de dispatch permanecem aprovados.

### Artefato

- Arquivo: `Agent-Infinite-Setup-0.1.1-x64.exe`
- Tamanho: 123.542.235 bytes (117,82 MiB)
- SHA-256: `240597DD180EA57D98861DC5671915B24B862951AE3B00149948BB44273C8FAB`

O próximo trabalho deve começar pela pendência UI-04 e pelos testes de fechamento do MVP. A
comunicação está coberta pela especificação canônica e aprovada em campo na versão 0.2.3.

## 13. Pendência UI-04 — primeiro time como estado inicial

### Sintoma

Na primeira abertura do workspace, nenhum time fica ativo e o canvas apresenta os nodes de todos
os times.

### Comportamento esperado

Depois que o snapshot do workspace for carregado, o primeiro time da lista deve ser o estado
selecionado por padrão. Isso é uma inicialização de estado, não um clique simulado.

Enquanto esse estado existir, o canvas deve mostrar somente os nodes e as edges desse time, e o item
correspondente deve aparecer ativo no rail. Se o workspace não tiver times, o estado continua sem
seleção.

### Critérios de aceite

- Um workspace com times abre com o primeiro time ativo.
- Nenhum node ou edge de outro time aparece nessa primeira renderização.
- A seleção manual de outro time continua funcionando.
- A coleção persistida do canvas não é alterada pela seleção inicial.
- A ausência de times não causa erro nem seleção inválida.

## 14. Entrega UI 0.3.0 — barra lateral e seleção inicial

A pendência UI-04 foi implementada junto com a atualização da barra lateral. O rail agora apresenta
um cabeçalho próprio, contexto do Workspace, ações de salvar/abrir e a seção **Git Worktrees** no
lugar da apresentação anterior de times/paralelos. O primeiro Git Worktree é inicializado como
seleção padrão quando o workspace possui times.

O domínio persistido continua usando os mesmos IDs, equipes, worktrees e contrato de comunicação.
O teste Electron confirma a seleção inicial, a filtragem de nodes/edges e a troca entre worktrees.

O instalador da atualização foi gerado como `Agent-Infinite-Setup-0.3.0-x64.exe` (123.928.045 bytes;
SHA-256 `A351B575A45824DAE38E1209EE960221A325E8452B9A08687EBDC9F14243EC46`).
