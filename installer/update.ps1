param(
  [string]$InstallDir = (Join-Path $env:USERPROFILE ".openclaw-guard-kit"),
  [string]$RepoOwner = "sorry123luck",
  [string]$RepoName = "openclaw-guard-kit",
  [string]$AssetName = "openclaw-guard-kit-windows-x64.zip"
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

function Write-Step {
  param(
    [string]$English,
    [string]$Chinese = ""
  )
  if ([string]::IsNullOrWhiteSpace($Chinese)) {
    Write-Host $English
    return
  }
  Write-Host "$English ($Chinese)"
}

function Ensure-Directory {
  param([string]$Path)
  if ([string]::IsNullOrWhiteSpace($Path)) {
    return
  }
  if (-not (Test-Path -LiteralPath $Path)) {
    New-Item -ItemType Directory -Path $Path -Force | Out-Null
  }
}

function Get-TempWorkRoot {
  $root = Join-Path $env:TEMP "openclaw-guard-kit-release-update"
  Ensure-Directory $root
  return $root
}

function Get-LatestAssetUrl {
  param(
    [string]$Owner,
    [string]$Name,
    [string]$Asset
  )
  # Gitee API - get latest release tag (public repo, no token needed)
  $apiUrl = "https://gitee.com/api/v5/repos/$Owner/$Name/releases/latest"
  try {
    $resp = Invoke-RestMethod -Uri $apiUrl -UseBasicParsing -TimeoutSec 10
    if ($resp.tag_name) {
      return "https://gitee.com/$Owner/$Name/releases/download/$($resp.tag_name)/$Asset"
    }
  } catch {
    Write-Host "WARN: Failed to fetch latest release from Gitee, using hardcoded tag"
  }
  # Fallback to hardcoded tag
  return "https://gitee.com/$Owner/$Name/releases/download/v.1.0.0/$Asset"
}

function Expand-SingleRootIfPresent {
  param([string]$ExtractedRoot)

  $dirs = Get-ChildItem -LiteralPath $ExtractedRoot -Directory
  $files = Get-ChildItem -LiteralPath $ExtractedRoot -File

  if ($dirs.Count -eq 1 -and $files.Count -eq 0) {
    return $dirs[0].FullName
  }

  return $ExtractedRoot
}

function Resolve-PackageRoot {
  param([string]$ExtractedRoot)

  $candidate = Expand-SingleRootIfPresent -ExtractedRoot $ExtractedRoot

  $guardExe = Join-Path $candidate "guard.exe"
  $updateScript = Join-Path $candidate "installer\update-from-dir.ps1"

  if ((Test-Path -LiteralPath $guardExe) -and (Test-Path -LiteralPath $updateScript)) {
    return $candidate
  }

  throw "Release package root is invalid: $candidate"
}

Write-Step "Downloading latest release package..." "正在下载最新发布包"
$workRoot = Get-TempWorkRoot
$stamp = Get-Date -Format "yyyyMMdd-HHmmss"
$sessionRoot = Join-Path $workRoot $stamp
$zipPath = Join-Path $sessionRoot $AssetName
$extractRoot = Join-Path $sessionRoot "expanded"

Ensure-Directory $sessionRoot
Ensure-Directory $extractRoot

$assetUrl = Get-LatestAssetUrl -Owner $RepoOwner -Name $RepoName -Asset $AssetName
Write-Host "URL: $assetUrl"

try {
  Invoke-WebRequest -Uri $assetUrl -OutFile $zipPath -UseBasicParsing
} catch {
  throw "Failed to download latest release asset: $assetUrl`n$($_.Exception.Message)"
}

if (-not (Test-Path -LiteralPath $zipPath)) {
  throw "Downloaded asset not found: $zipPath"
}

Write-Step "Extracting release package..." "正在解压发布包"
Expand-Archive -LiteralPath $zipPath -DestinationPath $extractRoot -Force

$packageRoot = Resolve-PackageRoot -ExtractedRoot $extractRoot
$updateScript = Join-Path $packageRoot "installer\update-from-dir.ps1"

Write-Step "Running package updater..." "正在执行升级脚本"
& powershell -ExecutionPolicy Bypass -File $updateScript `
  -InstallDir $InstallDir `
  -SourceDir $packageRoot

if ($LASTEXITCODE -ne 0) {
  throw "update-from-dir.ps1 failed with exit code $LASTEXITCODE"
}

Write-Step "Update completed." "升级完成"