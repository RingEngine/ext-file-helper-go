param()

$ErrorActionPreference = 'Stop'

$projectRoot = $PSScriptRoot
$distDir = Join-Path $projectRoot 'dist'
$toolCacheDir = Join-Path $projectRoot '.tools\cache'
$wixExtractRoot = Join-Path $projectRoot '.tools\wix'
$wixCliMsiPath = Join-Path $toolCacheDir 'wix-cli-x64.msi'
$wixCliUrl = 'https://github.com/wixtoolset/wix/releases/download/v6.0.1/wix-cli-x64.msi'
$wixExe = Join-Path $wixExtractRoot 'PFiles64\WiX Toolset v6.0\bin\wix.exe'
$wxsPath = Join-Path $projectRoot 'wix\Product.wxs'

$mainGoPath = Join-Path $projectRoot 'main.go'
$mainGo = Get-Content -Raw $mainGoPath
$match = [regex]::Match($mainGo, 'version\s*=\s*"(?<version>\d+\.\d+\.\d+)"')
if (-not $match.Success) {
  throw "Failed to read helper version from $mainGoPath"
}
$helperVersion = $match.Groups['version'].Value
$msiPath = Join-Path $distDir ("ext-file-helper-go-$helperVersion.msi")

& (Join-Path $projectRoot 'build.ps1')

if (-not (Test-Path $wixExe)) {
  New-Item -ItemType Directory -Force -Path $toolCacheDir | Out-Null
  if (-not (Test-Path $wixCliMsiPath)) {
    Invoke-WebRequest -Uri $wixCliUrl -OutFile $wixCliMsiPath
  }

  if (Test-Path $wixExtractRoot) {
    Remove-Item -LiteralPath $wixExtractRoot -Recurse -Force
  }
  New-Item -ItemType Directory -Force -Path $wixExtractRoot | Out-Null

  $install = Start-Process msiexec.exe -ArgumentList @(
    '/a',
    $wixCliMsiPath,
    '/qn',
    ('TARGETDIR=' + $wixExtractRoot)
  ) -PassThru -Wait
  if ($install.ExitCode -ne 0) {
    throw "Failed to extract WiX CLI MSI. msiexec exit code: $($install.ExitCode)"
  }
}

if (-not (Test-Path $wixExe)) {
  throw "WiX CLI not found at $wixExe after extraction."
}

Push-Location $projectRoot
try {
  & $wixExe build $wxsPath `
    -arch x64 `
    -d HelperVersion=$helperVersion `
    -o $msiPath
} finally {
  Pop-Location
}

Write-Host "Built MSI: $msiPath"
