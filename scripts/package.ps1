$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot

& (Join-Path $PSScriptRoot 'build.ps1')
if ($LASTEXITCODE -ne 0) { throw "Production build failed with exit code $LASTEXITCODE" }

pnpm --filter @agent-infinite/desktop dist:win
if ($LASTEXITCODE -ne 0) { throw "Windows packaging failed with exit code $LASTEXITCODE" }

$installer = Get-ChildItem -LiteralPath (Join-Path $root 'apps/desktop/release') -Filter 'Agent-Infinite-Setup-*.exe' |
  Sort-Object LastWriteTime -Descending |
  Select-Object -First 1
if (-not $installer) { throw 'The Windows installer was not produced.' }

Write-Output "Installer: $($installer.FullName)"
