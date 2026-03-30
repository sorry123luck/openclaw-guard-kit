param(
    [string]$InstallDir = (Join-Path $env:USERPROFILE ".openclaw-guard-kit"),
    [switch]$RemoveInstallDir = $true
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
        return $null
    }
    $raw = Get-Content -LiteralPath $Path -Raw -Encoding UTF8
    if ([string]::IsNullOrWhiteSpace($raw)) {
        return $null
    }
    return ($raw | ConvertFrom-Json)
}

function Get-DetectorAutoStartName {
    return "OpenClaw Guard Detector"
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

function Remove-IfExists {
    param([string]$Path)
    if (-not [string]::IsNullOrWhiteSpace($Path) -and (Test-Path -LiteralPath $Path)) {
        Remove-Item -LiteralPath $Path -Recurse -Force -ErrorAction SilentlyContinue
    }
}

function Remove-GuardRuntimeState {
    param([string]$OpenClawRoot)

    if ([string]::IsNullOrWhiteSpace($OpenClawRoot)) { return }

    $stateDir = Join-Path $OpenClawRoot ".guard-state"
    $offlineFlag = Join-Path $OpenClawRoot ".offline"

    Remove-IfExists -Path $stateDir
    Remove-IfExists -Path $offlineFlag
}

$InstallDir = [System.IO.Path]::GetFullPath($InstallDir)
$manifestPath = Join-Path $InstallDir "openclaw-guard-kit-install-manifest.json"

Write-Step "Loading install manifest..." "正在读取安装清单"
$manifest = Read-JsonFile -Path $manifestPath

if ($null -eq $manifest) {
    $manifest = [pscustomobject]@{
        installDir = $InstallDir
        openClawRoot = ""
        artifacts = [pscustomobject]@{
            guardExe = (Join-Path $InstallDir "guard.exe")
            guardDetectorExe = (Join-Path $InstallDir "guard-detector.exe")
            guardUiExe = (Join-Path $InstallDir "guard-ui.exe")
            guardUiManifest = (Join-Path $InstallDir "guard-ui.exe.manifest")
            wecomBridgeDir = (Join-Path $InstallDir "tools\wecom-bridge")
        }
    }
}

Write-Step "Removing detector auto start..." "正在移除 detector 自启动"
Unregister-DetectorAutoStart

Write-Step "Stopping installed processes..." "正在停止已安装进程"
if ($null -ne $manifest.artifacts) {
    Stop-ProcessByExecutablePath -ExecutablePath $manifest.artifacts.guardUiExe
    Stop-ProcessByExecutablePath -ExecutablePath $manifest.artifacts.guardExe
    Stop-ProcessByExecutablePath -ExecutablePath $manifest.artifacts.guardDetectorExe
}

Write-Step "Removing guard runtime state..." "正在移除守护运行状态"
Remove-GuardRuntimeState -OpenClawRoot ([string]$manifest.openClawRoot)

Write-Step "Removing installed files..." "正在移除已安装文件"
Remove-IfExists -Path (Join-Path $InstallDir "guard.exe")
Remove-IfExists -Path (Join-Path $InstallDir "guard-detector.exe")
Remove-IfExists -Path (Join-Path $InstallDir "guard-ui.exe")
Remove-IfExists -Path (Join-Path $InstallDir "guard-ui.exe.manifest")
Remove-IfExists -Path (Join-Path $InstallDir "README.md")
Remove-IfExists -Path (Join-Path $InstallDir "LICENSE")
Remove-IfExists -Path (Join-Path $InstallDir "tools")
Remove-IfExists -Path (Join-Path $InstallDir "installer")
Remove-IfExists -Path (Join-Path $InstallDir "logs")
Remove-IfExists -Path $manifestPath

if ($RemoveInstallDir -and (Test-Path -LiteralPath $InstallDir)) {
    try {
        Remove-Item -LiteralPath $InstallDir -Recurse -Force -ErrorAction Stop
    }
    catch {
        Write-Host "Install directory could not be fully removed now. You can delete it manually later."
    }
}

Write-Step "Uninstall completed." "卸载完成"
Write-Host "Install dir: $InstallDir"