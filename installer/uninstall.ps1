param(
    [string]$InstallDir = (Split-Path $PSScriptRoot -Parent),
    [switch]$RemoveInstallDir
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

function Read-JsonFile {
    param([string]$Path)

    $raw = Get-Content -LiteralPath $Path -Raw -Encoding UTF8
    if ([string]::IsNullOrWhiteSpace($raw)) {
        return $null
    }

    return ($raw | ConvertFrom-Json)
}

function Remove-ManagedBlock {
    param(
        [string]$FilePath,
        [string]$BeginMarker,
        [string]$EndMarker
    )

    if (-not (Test-Path -LiteralPath $FilePath)) {
        return
    }

    $current = Get-Content -LiteralPath $FilePath -Raw -Encoding UTF8
    $pattern = [regex]::Escape($BeginMarker) + ".*?" + [regex]::Escape($EndMarker)

    if (-not [regex]::IsMatch($current, $pattern, [System.Text.RegularExpressions.RegexOptions]::Singleline)) {
        return
    }

    $updated = [regex]::Replace(
        $current,
        $pattern,
        "",
        [System.Text.RegularExpressions.RegexOptions]::Singleline
    )

    $updated = $updated.Trim()
    if ([string]::IsNullOrWhiteSpace($updated)) {
        Remove-Item -LiteralPath $FilePath -Force
        return
    }

    Set-Content -LiteralPath $FilePath -Value ($updated + "`r`n") -Encoding UTF8
}

function Start-DelayedDirectoryDelete {
    param([string]$Path)

    $escaped = $Path.Replace('"', '""')
    $command = "ping 127.0.0.1 -n 3 > nul & rmdir /s /q `"$escaped`""
    Start-Process -FilePath "cmd.exe" -ArgumentList "/c $command" -WindowStyle Hidden | Out-Null
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

    if ([string]::IsNullOrWhiteSpace($ExecutablePath) -or -not (Test-Path -LiteralPath $ExecutablePath)) {
        return
    }

    $fullExe = [System.IO.Path]::GetFullPath($ExecutablePath).ToLowerInvariant()
    $exeName = [System.IO.Path]::GetFileName($ExecutablePath).Replace("'", "''")

    $procs = Get-CimInstance Win32_Process -Filter "Name = '$exeName'" -ErrorAction SilentlyContinue
    foreach ($proc in @($procs)) {
        $procExe = [string]$proc.ExecutablePath
        if ([string]::IsNullOrWhiteSpace($procExe)) {
            continue
        }

        if ($procExe.ToLowerInvariant() -eq $fullExe) {
            Stop-Process -Id $proc.ProcessId -Force -ErrorAction SilentlyContinue
        }
    }
}

function Stop-DetectorIfRunning {
    param([string]$DetectorExe)

    Stop-ProcessByExecutablePath -ExecutablePath $DetectorExe
    Start-Sleep -Seconds 1
}

function Stop-ManagedGuardProcesses {
    param($Manifest)

    Stop-ProcessByExecutablePath -ExecutablePath $Manifest.artifacts.guardUiExe
    Stop-ProcessByExecutablePath -ExecutablePath $Manifest.artifacts.guardExe
}
$InstallDir = (Resolve-Path -LiteralPath $InstallDir).Path
$manifestPath = Join-Path $InstallDir "openclaw-guard-kit-install-manifest.json"

if (-not (Test-Path -LiteralPath $manifestPath)) {
    throw "Install manifest not found: $manifestPath"
}

$manifest = Read-JsonFile -Path $manifestPath
Write-Step "Stopping detector..." "正在停止守护检测器"
Stop-DetectorIfRunning -DetectorExe $manifest.artifacts.guardDetectorExe

Write-Step "Removing detector auto-start..." "正在移除 detector 自启动"
Unregister-DetectorAutoStart

Write-Step "Stopping managed guard processes..." "正在停止守护相关进程"
Stop-ManagedGuardProcesses -Manifest $manifest
Write-Step "Removing workspace rules..." "正在移除工作区规则"

foreach ($workspacePath in $manifest.workspaces) {
    Remove-ManagedBlock `
        -FilePath (Join-Path $workspacePath "AGENTS.md") `
        -BeginMarker $manifest.markers.agentsBegin `
        -EndMarker $manifest.markers.agentsEnd

    Remove-ManagedBlock `
        -FilePath (Join-Path $workspacePath "TOOLS.md") `
        -BeginMarker $manifest.markers.toolsBegin `
        -EndMarker $manifest.markers.toolsEnd
}

Write-Step "Removing shared skill..." "正在移除共享技能"

if (-not [string]::IsNullOrWhiteSpace($manifest.sharedSkillDir) -and (Test-Path -LiteralPath $manifest.sharedSkillDir)) {
    Remove-Item -LiteralPath $manifest.sharedSkillDir -Recurse -Force
}

Write-Step "Removing installed files..." "正在移除已安装文件"

$pathsToRemove = @(
    $manifest.artifacts.guardExe,
    $manifest.artifacts.guardDetectorExe,
    $manifest.artifacts.guardUiExe,
    $manifest.artifacts.guardUiManifest,
    $manifest.artifacts.wecomBridgeDir,
    $manifest.bundle.templatesDir,
    $manifest.bundle.skillDir,
    $manifest.bundle.wecomBridgeDir,
    (Join-Path $manifest.installDir "logs"),
    $manifest.installer.updateScript,
    $manifest.installer.uninstallScript,
    $manifest.installer.toggleDetectorScript,
    $manifestPath
)

foreach ($path in $pathsToRemove) {
    if (-not [string]::IsNullOrWhiteSpace($path) -and (Test-Path -LiteralPath $path)) {
        Remove-Item -LiteralPath $path -Recurse -Force -ErrorAction SilentlyContinue
    }
}

$installerDir = Join-Path $manifest.installDir "installer"
if (Test-Path -LiteralPath $installerDir) {
    Get-ChildItem -LiteralPath $installerDir -Force -ErrorAction SilentlyContinue |
        Remove-Item -Recurse -Force -ErrorAction SilentlyContinue
}

Write-Step "Uninstall completed." "卸载完成"
Write-Host "Install dir: $InstallDir"

if ($RemoveInstallDir) {
    Write-Host "Scheduling install directory removal..."
    Start-DelayedDirectoryDelete -Path $InstallDir
}
else {
    Write-Host "Install directory was kept. Remove it manually if no longer needed."
}