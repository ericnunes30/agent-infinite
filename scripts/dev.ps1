$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$binary = Join-Path $root 'apps/desktop/resources/agent-infinite-backend.exe'

New-Item -ItemType Directory -Force -Path (Split-Path -Parent $binary) | Out-Null
Push-Location (Join-Path $root 'backend')
try {
  go build -o $binary ./cmd/agent-infinite
  if ($LASTEXITCODE -ne 0) { throw "Go build failed with exit code $LASTEXITCODE" }
} finally { Pop-Location }

pnpm --filter @agent-infinite/desktop dev

