param()

$ErrorActionPreference = 'Stop'

$repoRoot = Split-Path $PSScriptRoot -Parent
$distDir = Join-Path $PSScriptRoot 'dist'
$hostExe = Join-Path $distDir 'webext-fsa-app.exe'

if (Test-Path (Join-Path $repoRoot '.tools\go\bin\go.exe')) {
  $goExe = Join-Path $repoRoot '.tools\go\bin\go.exe'
} else {
  $goCommand = Get-Command go -ErrorAction SilentlyContinue
  if (-not $goCommand) {
    throw "Go toolchain not found. Expected either $repoRoot\\.tools\\go\\bin\\go.exe or go.exe on PATH."
  }
  $goExe = $goCommand.Source
}

New-Item -ItemType Directory -Force -Path $distDir | Out-Null

Push-Location $PSScriptRoot
try {
  & $goExe build -trimpath -ldflags '-s -w' -o $hostExe .
} finally {
  Pop-Location
}

Write-Host "Built helper: $hostExe"
