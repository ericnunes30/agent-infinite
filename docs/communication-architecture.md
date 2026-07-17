# Arquitetura de comunicação entre agentes

Status: implementada na versão 0.2.0; espera e recuperação de sessão corrigidas na 0.2.1; reconhecimento dos composers atuais do Codex corrigido na 0.2.2; conclusão e captura do resultado corrigidas na 0.2.3  
Público: engenharia, produto e futuras sessões de manutenção  
Escopo: Codex, Claude Code e futuros providers executados pelo Agent Infinite

## 1. Objetivo

Este documento define como agentes do canvas descobrem, acionam e recebem respostas uns dos outros.
Depois da leitura, uma pessoa nova no projeto deve conseguir implementar um provider ou alterar o
fluxo de dispatch sem reintroduzir fallback silencioso, dependência global de hooks ou resultados
associados ao agente errado.

O Agent Infinite é a camada de orquestração ao redor dos providers. O backend é a fonte de verdade
para identidade, permissões, sessões, dispatches e resultados. Nenhum provider decide sozinho quais
nodes pertencem ao time ou quais conexões autorizam comunicação.

## 2. Invariantes

1. Uma edge do canvas concede permissão de comunicação do orquestrador para o target.
2. Agentes conectados no canvas têm precedência sobre subagentes nativos do provider.
3. Não existe fallback silencioso de um agente do canvas para um subagente nativo.
4. IDs são a identidade autorizadora; labels e roles são identidades humanas resolvidas pelo
   backend.
5. Todo resultado pertence a um `dispatch_id`, nunca apenas a um `agent_id`.
6. MCP é o contrato de comandos; hooks sinalizam lifecycle; PTY preserva a sessão interativa.
7. Hooks melhoram a confiabilidade, mas a comunicação continua funcional com detector de terminal
   quando um provider não os suporta.
8. O Agent Infinite não modifica configuração global nem arquivos versionados do projeto para
   habilitar sua integração.
9. Uma tarefa permanece em `queued` até o provider estar realmente pronto para receber texto.

## 3. Vocabulário e precedência

- **Agente do canvas** ou **agente conectado**: processo representado por um node do Agent Infinite.
- **Orquestrador**: node autorizado a delegar através de edges de saída.
- **Target**: agente conectado que recebe uma tarefa.
- **Subagente nativo**: agente criado internamente pelo Codex, Claude ou outro provider, sem node
  correspondente no canvas.
- **Dispatch**: uma solicitação rastreável entre um orquestrador e um target.

Quando o usuário disser “agente”, “agente do app”, “agente conectado”, ou citar uma label/role
visível, o provider deve usar o Agent Infinite. Um subagente nativo somente pode ser usado quando o
usuário o pedir explicitamente. Ausência de target, ambiguidade ou target offline nunca autorizam
fallback automático para mecanismos nativos.

## 4. Planos de integração

### 4.1 MCP: plano de comando

O servidor MCP expõe uma interface pequena e orientada a dispatch:

- `list_connected_agents`: retorna a identidade do caller e targets permitidos com ID, label, role,
  provider e status;
- `delegate_task`: recebe target e tarefa, cria o dispatch e retorna imediatamente seu ID e estado;
- `get_dispatch_result`: recuperação bloqueante para um resultado quando a notificação automática
  foi interrompida; a espera acontece no backend e não deve ser consultada em loop.

As instruções do servidor devem começar com a regra de precedência do Agent Infinite, explicar que
subagentes nativos não são fallback e fornecer o fluxo completo entre as tools. Labels podem ser
aceitas quando identificam exatamente um target; IDs permanecem obrigatórios internamente. Em caso
de ambiguidade, a chamada retorna candidatos em vez de escolher silenciosamente.

### 4.2 Hooks: plano de lifecycle

Cada provider possui um adaptador que normaliza seus eventos para o backend:

- `SessionStart` registra a sessão e injeta identidade, papel, time e targets conectados;
- `UserPromptSubmit` confirma que um envelope de dispatch foi recebido;
- `Stop` sinaliza o fim da execução e permite capturar o resultado naquele ponto;
- `SubagentStart` e `SubagentStop` tornam o uso de subagentes nativos observável;
- `SessionEnd` encerra a associação entre sessão e node.

O evento `SubagentStart` não é tratado como mecanismo cross-provider de bloqueio. Quando o provider
não permite impedir a criação, o Agent Infinite registra e apresenta o evento. A prevenção continua
baseada nas instruções, na skill e no contrato MCP.

### 4.3 PTY: plano interativo

O PTY continua sendo o terminal real exibido ao usuário e o transporte usado para entregar uma
tarefa ao target. O texto enviado contém um envelope legível com `dispatch_id`, origem e tarefa. O
hook confirma o lifecycle; quando o hook não existe, o detector de terminal assume essa função.

## 5. Configuração efêmera por projeto

Cada workspace guarda uma política de integração:

```json
{
  "integration": {
    "hooks": "auto"
  }
}
```

- `auto`: ativa hooks suportados e usa fallback quando indisponíveis;
- `off`: inicia o provider sem hooks do Agent Infinite;
- `required`: falha de forma explícita se a integração não puder ser ativada.

`auto` é o padrão.

Ao iniciar um node, o aplicativo cria configuração efêmera para aquela sessão fora do repositório,
injeta MCP, hooks, identidade e credenciais de processo, e inicia o provider com overrides de linha
de comando. O encerramento remove a configuração. Uma reconciliação no próximo startup limpa
resíduos de sessões interrompidas.

Claude recebe settings ou plugin de sessão. Codex recebe overrides de configuração de sessão. Hooks
do Codex mantêm uma definição estável para que a confiança seja concedida uma vez e invalidada
somente quando a definição mudar. O aplicativo nunca usa bypass global de confiança.

O comando do hook não contém endpoint, token, node ou projeto. Esses valores chegam por variáveis de
ambiente de processo. Se o backend local estiver ausente, o token estiver inválido ou a sessão não
corresponder ao node, o hook encerra sem efeito.

## 6. Lifecycle do dispatch

Estados mínimos:

```text
created → queued → delivered → running → done
                                   ├──→ blocked
                                   ├──→ failed
                                   └──→ canceled
```

Fluxo normal:

1. O usuário pede ao orquestrador que acione um agente por label ou role.
2. O orquestrador consulta targets conectados quando necessário.
3. `delegate_task` valida source, edge, target e conteúdo.
4. Se um target existente estiver offline, o backend o inicia e mantém a tarefa em `queued`.
5. O backend aguarda o provider ficar `Idle`; menus de atualização, confiança ou confirmação não
   recebem texto nem Enter automaticamente.
6. O backend entrega ao PTY um envelope contendo o `dispatch_id`.
7. Hook ou detector confirma `delivered` e `running`.
8. Hook `Stop` ou detector conclui em `done`, `blocked` ou `failed` e captura uma saída limitada.
   No fallback por detector, um prompt submetido que permanece no histórico não encerra o dispatch:
   o backend precisa observar execução ocupada ou uma janela segura antes de aceitar o composer
   final.
9. O orquestrador encerra seu turno e permanece ocioso enquanto o target trabalha.
10. O backend acorda o orquestrador uma única vez com o resultado isolado; não há polling MCP.
11. Se a notificação for interrompida, uma única chamada a `get_dispatch_result` aguarda o estado
    terminal dentro do backend.

Somente nodes já existentes podem ser iniciados automaticamente. Criar, recrutar ou conectar um novo
node exige uma operação explícita e fica fora da primeira versão deste contrato. A primeira versão
suporta uma tarefa e uma resposta por dispatch; conversas com múltiplas rodadas serão uma extensão
posterior.

## 7. Segurança e isolamento

- Backend e callbacks permanecem em loopback.
- Cada sessão recebe token próprio, com expiração e vínculo a node, workspace e processo.
- Segredos não aparecem em arquivos do repositório, argumentos do hook ou logs.
- O backend rejeita eventos duplicados, fora de ordem ou provenientes de sessão encerrada.
- Configurações existentes do usuário são preservadas e continuam ativas conforme a precedência do
  provider.
- O aplicativo não sobrescreve hooks, skills ou instruções criadas pelo usuário.
- A confiança de hooks do Codex é explícita; recusa mantém o modo de fallback em vez de enfraquecer
  a proteção do provider.

## 8. Falhas e comportamento esperado

- **Target inexistente ou sem edge:** rejeitar e listar somente alternativas autorizadas.
- **Label ambígua:** retornar candidatos; não escolher automaticamente.
- **Target offline:** iniciar o node existente, enfileirar e exibir o estado.
- **Provider ainda inicializando ou aguardando confirmação:** manter o dispatch em `queued` e não
  enviar teclas ao terminal.
- **Codex atualizado e solicitando reinício:** reiniciar a sessão uma vez, aguardar `Idle` e só então
  entregar a tarefa.
- **Provider ausente ou autenticação inválida:** falhar o dispatch sem criar subagente nativo.
- **Hook indisponível em modo `auto`:** usar detector e marcar telemetria como degradada.
- **Hook indisponível em modo `required`:** não iniciar a sessão.
- **Backend reiniciado:** reconciliar sessões e marcar dispatches sem processo como interrompidos.
- **Subagente nativo criado:** registrar origem, provider e lifecycle; nunca apresentá-lo como node do
  canvas.

## 9. Observabilidade na interface

Cada dispatch deve apresentar origem, target, estado e horário. A edge pode indicar atividade, mas o
estado não pode depender somente de cor ou animação. O inspector deve diferenciar claramente:

- agente conectado;
- subagente nativo observado;
- integração completa por hooks;
- modo degradado pelo detector;
- erro de roteamento ou autenticação.

## 10. Critérios de aceite

1. “Peça ao Reviewer para revisar” usa o node conectado sem mencionar ID ou tool.
2. Codex e Claude executam o mesmo fluxo nos dois sentidos.
3. Um target offline existente é iniciado e recebe a tarefa.
4. Nenhum erro de dispatch cria subagente nativo silenciosamente.
5. Dois dispatches para o mesmo agente mantêm resultados separados.
6. Fechar o Agent Infinite invalida callbacks e remove configuração efêmera.
7. Abrir o provider manualmente no mesmo projeto não ativa hooks do Agent Infinite.
8. Recusar hooks do Codex mantém a sessão funcional em modo degradado.
9. Atividade e falhas aparecem no canvas com identidade humana e `dispatch_id` rastreável.
10. Testes reais cobrem Claude → Codex e Codex → Claude usando linguagem natural, sem IDs nem nomes
    de tools.
11. O orquestrador não consulta resultados em loop enquanto o target trabalha.
12. Um menu de atualização do provider nunca recebe o texto ou o Enter de um dispatch.

## 11. Aprovação de campo

Status: **aprovado na versão 0.2.3 em 16 de julho de 2026** para comunicação entre nodes Codex do
canvas.

O teste de aceite comprovou o fluxo completo:

1. o usuário pediu a delegação em linguagem natural, usando a label visível do agente;
2. o orquestrador criou um único dispatch via MCP e encerrou o turno sem polling;
3. o target recebeu a tarefa pelo terminal e produziu a resposta solicitada;
4. o backend capturou a resposta correta, acordou o orquestrador e preservou o vínculo com o
   `dispatch_id`;
5. o orquestrador apresentou o resultado ao usuário sem chamar `get_dispatch_result` repetidamente;
6. após um reinício completo do aplicativo e dos providers, novas delegações repetiram o fluxo com
   sucesso.

### Observação operacional

A primeira chamada imediatamente após a atualização instalada não concluiu, enquanto a segunda
funcionou. Depois de reiniciar todas as sessões, o comportamento permaneceu correto. Isso não
bloqueia a aprovação do contrato, mas indica que uma instalação sobre sessões já abertas não deve
ser usada como teste limpo. Até existir reconciliação de hot upgrade, o procedimento de validação é
fechar o aplicativo e os providers, instalar a nova versão, reabrir o workspace e então delegar.

Esta parte deve ser reaberta somente se uma inicialização limpa apresentar falha repetível de
entrega, resultado associado ao dispatch errado, notificação duplicada, polling do orquestrador ou
fallback para subagente nativo sem solicitação explícita.

## 12. Mapa da implementação

- Contrato persistido e política `auto | off | required`: `backend/internal/contracts`,
  `backend/internal/workspace` e `PATCH /api/workspaces/integration`.
- Resolução humana, autorização por edge, fila por target, auto-start, resultados isolados e
  recuperação após restart: `backend/internal/orchestration/service.go`.
- Tools MCP e regra de precedência: `backend/internal/mcpserver/server.go`.
- Tokens de sessão, ordem/expiração de callbacks, forwarder e contexto de `SessionStart`:
  `backend/internal/hookbridge`.
- Settings efêmeros de Claude e overrides inline de Codex: `backend/internal/agent/profile.go`.
- Estados de integração, dispatches e subagentes nativos observados: inspector do renderer em
  `apps/desktop/src/renderer/src/CanvasWorkspace.tsx`.
- Journal de atividade fora do repositório: `%LOCALAPPDATA%/AgentInfinite/activity.jsonl`.
- Contratos HTTP: `contracts/openapi.yaml`; conjunto de avaliação MCP:
  `backend/internal/mcpserver/evals.xml`.

Evidência executável: `go test ./...`, o E2E Electron determinístico e
`TestRealBidirectionalProviderFlow`. O teste real usa labels em linguagem natural e falha se o
target não executar ou se o resultado não voltar ao orquestrador.

## 13. Referências de produto

- [Codex hooks](https://learn.chatgpt.com/docs/hooks)
- [Codex MCP](https://learn.chatgpt.com/docs/extend/mcp)
- [Codex subagents](https://learn.chatgpt.com/docs/agent-configuration/subagents)
- [Claude Code hooks](https://code.claude.com/docs/en/hooks)
- [Claude Code CLI](https://code.claude.com/docs/en/cli-usage)
- [Maestri connections](https://www.themaestri.app/en/docs/connections)
- [Maestri terminals and roles](https://www.themaestri.app/en/docs/terminals)
