# Team, Worktree and custom-role management (0.8.0)

- The **X** action beside a Git Worktree asks for confirmation and removes it through the existing
  safe backend operation. A worktree that still owns operational nodes, has uncommitted changes, or
  cannot be removed by Git remains protected and the UI shows the backend explanation.
- The **X** action beside an Agent Team asks for confirmation and removes the Team, its nodes, edges
  and linked Git Worktrees. Git refuses dirty linked worktrees, so the Team is not partially removed.
- The **+** action in **ROLES** saves a custom role in the workspace canvas. It appears alongside the
  built-in role shortcuts and can be selected while creating an agent. Custom roles can also be
  removed from the same section; built-in roles remain available.
