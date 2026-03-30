param(
    [string]$InstallDir = (Join-Path $env:USERPROFILE ".openclaw-guard-kit")
)

$ErrorActionPreference = "Stop"

function Write-Step {
    param(
        [string]$English,
        [string]$Chinese = ""
    )
    if ([string]::IsNullOrWhiteSpace($Chinese)) {
        Write-Host $English
    }
    else {
        Write-Host "$English ($Chinese)"
    }
}

function Read-JsonFile {
    param([string]$Path)
    if (-not (Test-Path -LiteralPath $Path)) {
        throw "File not found: $Path"
    }
    $raw = Get-Content -LiteralPath $Path -Raw -Encoding UTF8
    if ([string]::IsNullOrWhiteSpace($raw)) {
        throw "File is empty: $Path"
    }
    return ($raw | ConvertFrom-Json)
}

function Get-DetectorAutoStartName {
    return "OpenClaw Guard Detector"
}

function Get-OfflineFlagPath {
    param([string]$Root)
    return (Join-Path $Root ".offline")
}

function Set-OfflineFlag {
    param([string]$Root)
    $flagPath = Get-OfflineFlagPath -Root $Root
    Set-Content -LiteralPath $flagPath -Value "disabled" -Encoding UTF8
}

function Clear-OfflineFlag {
    param([string]$Root)
    $flagPath = Get-OfflineFlagPath -Root $Root
    if (Test-Path -LiteralPath $flagPath) {
        Remove-Item -LiteralPath $flagPath -Force -ErrorAction SilentlyContinue
    }
}

function Get-DetectorCommandLine {
    param(
        [string]$DetectorExe,
        [string]$OpenClawRoot,
        [string]$AgentID
    )

    $quotedExe = '"' + $DetectorExe + '"'
    $quotedRoot = '"' + $OpenClawRoot + '"'
    $quotedAgent = '"' + $AgentID + '"'
    return "$quotedExe --root $quotedRoot --agent $quotedAgent --log-level info"
}

function Register-DetectorAutoStart {
    param(
        [string]$DetectorExe,
        [string]$OpenClawRoot,
        [string]$AgentID
    )

    $runKey = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Run"
    if (-not (Test-Path -LiteralPath $runKey)) {
        New-Item -Path $runKey -Force | Out-Null
    }

    $commandLine = Get-DetectorCommandLine -DetectorExe $DetectorExe -OpenClawRoot $OpenClawRoot -AgentID $AgentID
    Set-ItemProperty -Path $runKey -Name (Get-DetectorAutoStartName) -Value $commandLine
}

function Unregister-DetectorAutoStart {
    $runKey = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Run"
    if (Test-Path -LiteralPath $runKey) {
        Remove-ItemProperty -Path $runKey -Name (Get-DetectorAutoStartName) -ErrorAction SilentlyContinue
    }
}

function Stop-ProcessByExecutablePath {
    param([string]$ExecutablePath)

    if ([string]::IsNullOrWhiteSpace($ExecutablePath)) { return }
    if (-not (Test-Path -LiteralPath $ExecutablePath)) { return }

    $fullExe = [System.IO.Path]::GetFullPath($ExecutablePath).ToLowerInvariant()
    $exeName = [System.IO.Path]::GetFileName($ExecutablePath).Replace("'", "''")

    $procs = Get-CimInstance Win32_Process -Filter "Name = '$exeName'" -ErrorAction SilentlyContinue
    foreach ($proc in @($procs)) {
        $procExe = [string]$proc.ExecutablePath
        if ([string]::IsNullOrWhiteSpace($procExe)) { continue }
        if ($procExe.ToLowerInvariant() -eq $fullExe) {
            Stop-Process -Id $proc.ProcessId -Force -ErrorAction SilentlyContinue
        }
    }
}

function Test-DetectorRunning {
    param(
        [string]$DetectorExe,
        [string]$OpenClawRoot
    )

    if ([string]::IsNullOrWhiteSpace($DetectorExe) -or -not (Test-Path -LiteralPath $DetectorExe)) {
        return $false
    }

    $fullExe = [System.IO.Path]::GetFullPath($DetectorExe).ToLowerInvariant()
    $fullRoot = [System.IO.Path]::GetFullPath($OpenClawRoot).ToLowerInvariant()
    $exeName = [System.IO.Path]::GetFileName($DetectorExe).Replace("'", "''")

    $procs = Get-CimInstance Win32_Process -Filter "Name = '$exeName'" -ErrorAction SilentlyContinue
    foreach ($proc in @($procs)) {
        $procExe = [string]$proc.ExecutablePath
        $cmd = [string]$proc.CommandLine

        if ([string]::IsNullOrWhiteSpace($procExe)) { continue }
        if ($procExe.ToLowerInvariant() -ne $fullExe) { continue }

        if ([string]::IsNullOrWhiteSpace($cmd)) {
            return $true
        }

        $cmdLower = $cmd.ToLowerInvariant()
        if ($cmdLower.Contains($fullRoot)) {
            return $true
        }
    }

    return $false
}

function Start-Detector {
    param(
        [string]$DetectorExe,
        [string]$OpenClawRoot,
        [string]$AgentID
    )

    Start-Process -FilePath $DetectorExe -ArgumentList @(
        "--root", $OpenClawRoot,
        "--agent", $AgentID,
        "--log-level", "info"
    ) -WindowStyle Hidden | Out-Null
}

$InstallDir = [System.IO.Path]::GetFullPath($InstallDir)
$manifestPath = Join-Path $InstallDir "openclaw-guard-kit-install-manifest.json"

Write-Step "Loading install manifest..." "正在读取安装清单"
$manifest = Read-JsonFile -Path $manifestPath

$detectorExe = [string]$manifest.artifacts.guardDetectorExe
$guardExe = [string]$manifest.artifacts.guardExe
$guardUiExe = [string]$manifest.artifacts.guardUiExe
$openClawRoot = [string]$manifest.openClawRoot
$agentID = if (-not [string]::IsNullOrWhiteSpace([string]$manifest.agentId)) { [string]$manifest.agentId } else { "main" }

if ([string]::IsNullOrWhiteSpace($detectorExe) -or -not (Test-Path -LiteralPath $detectorExe)) {
    throw "Installed detector executable not found: $detectorExe"
}
if ([string]::IsNullOrWhiteSpace($openClawRoot)) {
    throw "openClawRoot missing in manifest."
}

if (Test-DetectorRunning -DetectorExe $detectorExe -OpenClawRoot $openClawRoot) {
    Write-Step "Detector is running. Stopping guard chain and disabling auto start..." "detector 正在运行，正在关闭守护链并禁用自启动"

    Set-OfflineFlag -Root $openClawRoot
    Unregister-DetectorAutoStart

    Stop-ProcessByExecutablePath -ExecutablePath $guardUiExe
    Stop-ProcessByExecutablePath -ExecutablePath $guardExe
    Stop-ProcessByExecutablePath -ExecutablePath $detectorExe

    Write-Host "Detector chain stopped."
}
else {
    Write-Step "Detector is not running. Enabling auto start and starting detector..." "detector 未运行，正在启用自启动并启动 detector"

    Clear-OfflineFlag -Root $openClawRoot
    Register-DetectorAutoStart -DetectorExe $detectorExe -OpenClawRoot $openClawRoot -AgentID $agentID

    Start-Detector -DetectorExe $detectorExe -OpenClawRoot $openClawRoot -AgentID $agentID
    Start-Sleep -Seconds 2

    if (Test-DetectorRunning -DetectorExe $detectorExe -OpenClawRoot $openClawRoot) {
        Write-Host "Detector started."
    }
    else {
        Write-Host "Detector start requested, but running state could not be confirmed."
    }
}