param(
  [string]$Version = "dev",
  [string]$OutDir = "dist"
)

$ErrorActionPreference = "Stop"

$RepoRoot = Split-Path -Parent $PSScriptRoot
Set-Location $RepoRoot

$StageRoot = Join-Path $RepoRoot $OutDir
$PackageName = "openclaw-guard-kit-windows-x64"
$StageDir = Join-Path $StageRoot $PackageName
$ZipPath = Join-Path $StageRoot "$PackageName.zip"

if (Test-Path $StageDir) {
  Remove-Item -Recurse -Force $StageDir
}
if (Test-Path $ZipPath) {
  Remove-Item -Force $ZipPath
}

New-Item -ItemType Directory -Force -Path $StageDir | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $StageDir "installer") | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $StageDir "skills\openclaw-guard-kit") | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $StageDir "templates") | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $StageDir "tools\wecom-bridge") | Out-Null

Write-Host "Building guard.exe..."
go build -trimpath -o (Join-Path $StageDir "guard.exe") .\cmd\guard

Write-Host "Building guard-detector.exe..."
go build -trimpath -o (Join-Path $StageDir "guard-detector.exe") .\cmd\guard-detector

Write-Host "Building guard-ui.exe..."
go build -trimpath -o (Join-Path $StageDir "guard-ui.exe") .\cmd\guard-ui

if (Test-Path ".\guard-ui.exe.manifest") {
  Copy-Item ".\guard-ui.exe.manifest" (Join-Path $StageDir "guard-ui.exe.manifest") -Force
}

$installerFiles = @(
  "install.ps1",
  "install-package.ps1",
  "update.ps1",
  "update-from-dir.ps1",
  "uninstall.ps1",
  "toggle-detector.ps1"
)

foreach ($name in $installerFiles) {
  $src = Join-Path $RepoRoot "installer\$name"
  if (Test-Path $src) {
    Copy-Item $src (Join-Path $StageDir "installer\$name") -Force
  }
}

# root-level templates
Copy-Item ".\templates\*" (Join-Path $StageDir "templates") -Recurse -Force

# root-level skills
Copy-Item ".\skills\openclaw-guard-kit\*" (Join-Path $StageDir "skills\openclaw-guard-kit") -Recurse -Force

# root-level tools
Copy-Item ".\tools\wecom-bridge\*" (Join-Path $StageDir "tools\wecom-bridge") -Recurse -Force

$manifest = [ordered]@{
  packageName = $PackageName
  version     = $Version
  builtAtUtc  = (Get-Date).ToUniversalTime().ToString("o")
}

$manifest | ConvertTo-Json -Depth 4 | Set-Content -Encoding utf8 (Join-Path $StageDir "installer\package-manifest.json")

Write-Host "Compressing package..."
Compress-Archive -Path (Join-Path $StageDir "*") -DestinationPath $ZipPath -Force

Write-Host "Done:"
Write-Host $ZipPath