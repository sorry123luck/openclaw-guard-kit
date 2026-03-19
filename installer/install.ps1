param(
    [string]$ServiceName = "OpenClawGuard",
    [string]$DisplayName = "OpenClaw Guard",
    [string]$BinaryPath = (Join-Path (Split-Path $PSScriptRoot -Parent) "guard.exe"),
    [string]$RootDir = (Join-Path $env:USERPROFILE ".openclaw"),
    [string]$AgentID = "main",
    [int]$Interval = 2,
    [string]$LogFile = (Join-Path $env:USERPROFILE ".openclaw-guard\logs\service.log"),
    [string]$ConfigPath = ""
)

$ErrorActionPreference = "Stop"

function Quote-Arg([string]$Value) {
    if ([string]::IsNullOrWhiteSpace($Value)) {
        return '""'
    }
    if ($Value.Contains(" ")) {
        return '"' + $Value + '"'
    }
    return $Value
}

$resolvedBinary = (Resolve-Path $BinaryPath).Path
$logDir = Split-Path $LogFile -Parent
if ($logDir -and -not (Test-Path $logDir)) {
    New-Item -ItemType Directory -Path $logDir -Force | Out-Null
}

$existing = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
if ($existing) {
    throw "Service '$ServiceName' already exists. Uninstall it first."
}

$args = @(
    "run-service",
    "--root", $RootDir,
    "--agent", $AgentID,
    "--interval", "$Interval",
    "--log-file", $LogFile
)

if (-not [string]::IsNullOrWhiteSpace($ConfigPath)) {
    $args += @("--config", $ConfigPath)
}

$quotedArgs = $args | ForEach-Object { Quote-Arg $_ }
$binPath = ('"{0}" {1}' -f $resolvedBinary, ($quotedArgs -join " "))

New-Service `
    -Name $ServiceName `
    -DisplayName $DisplayName `
    -BinaryPathName $binPath `
    -StartupType Automatic `
    -Description "OpenClaw guard background service"

Start-Service -Name $ServiceName

$svc = Get-Service -Name $ServiceName
Write-Host "Installed service: $ServiceName"
Write-Host "Status: $($svc.Status)"
Write-Host "BinaryPathName: $binPath"