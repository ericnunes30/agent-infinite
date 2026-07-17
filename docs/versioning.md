# Versionamento do Agent Infinite

O projeto usa três componentes de versão. Na geração atual, o formato é `0.X.Y`.

- o primeiro componente, atualmente `0`, aumenta somente para uma versão completamente nova e
  incompatível com as anteriores;
- `X` aumenta quando a release adiciona uma ou mais funcionalidades.
- `Y` aumenta quando a release contém somente correções de bugs, ajustes de confiabilidade ou
  manutenção compatível com as funcionalidades existentes.

Exemplos:

- `0.2.0` adiciona funcionalidades em relação à linha `0.1.Y`;
- `0.2.1` corrige bugs da funcionalidade existente em `0.2.0`;
- `0.3.0` adiciona outra funcionalidade sem representar uma geração totalmente incompatível.
- `0.4.0` adiciona a separação entre Agent Teams e Git Worktrees, mantendo migração aditiva para
  os canvases existentes.
- `0.4.1` corrige a seleção hierárquica Team → Worktree sem alterar o contrato persistido.
- `0.5.0` adiciona a página de gestão de Agent Teams e torna Git Worktree a única seleção ativa do
  canvas.
- `1.0.0` inicia uma geração completamente nova e incompatível com a linha `0.X.Y`.

Uma release não deve aumentar `X` apenas por conter uma correção. Quando funcionalidades e
correções forem entregues juntas, a presença da funcionalidade determina o aumento de `X` e `Y`
volta para zero.

## Locais que devem permanecer sincronizados

Antes de gerar um instalador, a mesma versão deve aparecer em:

- `package.json`;
- `apps/desktop/package.json`;
- `backend/internal/app/version.go`;
- implementação anunciada pelo servidor em `backend/internal/mcpserver/server.go`;
- identificação visual no renderer.

O instalador segue o nome `Agent-Infinite-Setup-0.X.Y-x64.exe`.
