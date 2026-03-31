param(
  [string]$InstallDir = (Join-Path $env:USERPROFILE ".openclaw-guard-kit"),
  [string]$RepoOwner = "sorry123luck",
  [string]$RepoName = "openclaw-guard-kit",
  [string]$AssetName = "openclaw-guard-kit-windows-x64.zip",
  [ValidateSet("gitee", "github")]
  [string]$PrimarySource = "gitee"
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

function Write-Info {
  param([string]$Msg)
  Write-Host "  INFO: $Msg"
}

function Ensure-Directory {
  param([string]$Path)
  if ([string]::IsNullOrWhiteSpace($Path)) { return }
  if (-not (Test-Path -LiteralPath $Path)) {
    New-Item -ItemType Directory -Path $Path -Force | Out-Null
  }
}

function Get-TempWorkRoot {
  $root = Join-Path $env:TEMP "openclaw-guard-kit-release-update"
  Ensure-Directory $root
  return $root
}

function Get-LatestTag {
  param([string]$Source)
  if ($Source -eq "gitee") {
    $apiUrl = "https://gitee.com/api/v5/repos/$RepoOwner/$RepoName/releases/latest"
    try {
      $resp = Invoke-RestMethod -Uri $apiUrl -UseBasicParsing -TimeoutSec 15
      if ($resp.tag_name) {
        return @{ Tag = $resp.tag_name; Source = "gitee" }
      }
    } catch {
      Write-Info "Gitee latest API failed: $($_.Exception.Message)"
    }
  } else {
    $apiUrl = "https://api.github.com/repos/$RepoOwner/$RepoName/releases/latest"
    try {
      $resp = Invoke-RestMethod -Uri $apiUrl -UseBasicParsing -TimeoutSec 15 -Headers @{ "User-Agent" = "powershell" }
      if ($resp.tag_name) {
        return @{ Tag = $resp.tag_name; Source = "github" }
      }
    } catch {
      Write-Info "GitHub latest API failed: $($_.Exception.Message)"
    }
  }
  return $null
}

function Get-DownloadUrl {
  param([string]$Source, [string]$Tag)
  if ($Source -eq "gitee") {
    return "https://gitee.com/$RepoOwner/$RepoName/releases/download/$Tag/$AssetName"
  } else {
    return "https://github.com/$RepoOwner/$RepoName/releases/download/$Tag/$AssetName"
  }
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

# Build source order
$sourceOrder = @()
if ($PrimarySource -eq "gitee") {
  $sourceOrder = @("gitee", "github")
} else {
  $sourceOrder = @("github", "gitee")
}

Write-Step "Resolving latest release tag..." "正在解析最新版本标签"
$resolved = $null
foreach ($src in $sourceOrder) {
  $resolved = Get-LatestTag -Source $src
  if ($resolved) {
    Write-Info "Using source: $($resolved.Source), tag: $($resolved.Tag)"
    break
  }
}

if (-not $resolved) {
  throw "Failed to resolve latest release tag from all sources"
}

Write-Step "Downloading latest release package..." "正在下载最新发布包"
Write-Host "  Source: $($resolved.Source) | Tag: $($resolved.Tag)"

$workRoot = Get-TempWorkRoot
$stamp = Get-Date -Format "yyyyMMdd-HHmmss"
$sessionRoot = Join-Path $workRoot $stamp
$zipPath = Join-Path $sessionRoot $AssetName
$extractRoot = Join-Path $sessionRoot "expanded"

Ensure-Directory $sessionRoot
Ensure-Directory $extractRoot

$downloaded = $false
$usedSource = $resolved.Source

# Build ordered download URLs (2-source fallback)
$downloadUrls = @()
if ($PrimarySource -eq "gitee") {
  $downloadUrls = @(
    @{ Source = "gitee";     Url = "https://gitee.com/$RepoOwner/$RepoName/releases/download/$($resolved.Tag)/$AssetName" },
    @{ Source = "github";    Url = "https://github.com/$RepoOwner/$RepoName/releases/download/$($resolved.Tag)/$AssetName" }
  )
} else {
  $downloadUrls = @(
    @{ Source = "github";    Url = "https://github.com/$RepoOwner/$RepoName/releases/download/$($resolved.Tag)/$AssetName" },
    @{ Source = "gitee";    Url = "https://gitee.com/$RepoOwner/$RepoName/releases/download/$($resolved.Tag)/$AssetName" }
  )
}

foreach ($entry in $downloadUrls) {
  $url = $entry.Url
  $src = $entry.Source
  Write-Info "Trying $src : $url"
  try {
    Invoke-WebRequest -Uri $url -OutFile $zipPath -UseBasicParsing -TimeoutSec 180
    $downloaded = $true
    $usedSource = $src
    Write-Info "Download succeeded from $src"
    break
  } catch {
    Write-Info "$src download attempt failed: $($_.Exception.Message)"
  }
}

if (-not $downloaded) {
  throw "Failed to download release package from all sources"
}

Write-Host "  Downloaded from: $usedSource"
Write-Host "  File: $zipPath"

if (-not (Test-Path -LiteralPath $zipPath)) {
  throw "Downloaded asset not found: $zipPath"
}

$fileSize = (Get-Item $zipPath).Length
if ($fileSize -lt 1MB) {
  throw "Downloaded file is too small ($fileSize bytes), likely a 404 page"
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
