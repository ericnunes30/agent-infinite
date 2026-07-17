$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot

Push-Location (Join-Path $root 'backend')
try {
  $unformatted = gofmt -l .
  if ($unformatted) { throw "Go files require gofmt:`n$unformatted" }
  go vet ./...
  if ($LASTEXITCODE -ne 0) { throw "go vet failed with exit code $LASTEXITCODE" }
} finally { Pop-Location }

pnpm --filter @agent-infinite/desktop lint
if ($LASTEXITCODE -ne 0) { throw "ESLint failed with exit code $LASTEXITCODE" }
pnpm format:check
if ($LASTEXITCODE -ne 0) { throw "Prettier check failed with exit code $LASTEXITCODE" }

