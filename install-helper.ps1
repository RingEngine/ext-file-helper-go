param(
  [string]$HostExePath = (Join-Path $PSScriptRoot 'dist\webext-fsa-app.exe')
)

$ErrorActionPreference = 'Stop'

$resolvedHostExe = (Resolve-Path $HostExePath).Path
$hostName = 'webext.fsa.app'
$manifestDir = Join-Path $env:LOCALAPPDATA 'ExtFileHelperGo'
$manifestPath = Join-Path $manifestDir "$hostName.json"
$allowedExtensions = @(
  'file@example.com',
  'framework@example.com'
)

New-Item -ItemType Directory -Force -Path $manifestDir | Out-Null

$manifest = @{
  name = $hostName
  description = 'File System Access Native Messaging Host'
  path = $resolvedHostExe
  type = 'stdio'
  allowed_extensions = $allowedExtensions
}

$manifest | ConvertTo-Json -Depth 4 | Set-Content -Path $manifestPath -Encoding UTF8

$registryPath = "HKCU\Software\Mozilla\NativeMessagingHosts\$hostName"
reg.exe add $registryPath /ve /t REG_SZ /d $manifestPath /f | Out-Null

Write-Host "Installed helper for ext-file."
Write-Host "Manifest: $manifestPath"
Write-Host "Registry:  $registryPath"
Write-Host ""
Write-Host "Restart Firefox after installation."
