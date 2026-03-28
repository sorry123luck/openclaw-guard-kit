param(
    [string]$InstallDir = (Split-Path $PSScriptRoot -Parent),
    [string]$SourceDir = ""
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

function Read-JsonFile {
    param([string]$Path)

    $raw = Get-Content -LiteralPath $Path -Raw -Encoding UTF8
    if ([string]::IsNullOrWhiteSpace($raw)) {
        return $null
    }

    return ($raw | ConvertFrom-Json)
}

function Copy-DirectoryContent {
    param(
        [string]$SourceDir,
        [string]$DestinationDir
    )

    Ensure-Directory $DestinationDir

    Get-ChildItem -LiteralPath $DestinationDir -Force -ErrorAction SilentlyContinue |
        Remove-Item -Recurse -Force -ErrorAction SilentlyContinue

    Get-ChildItem -LiteralPath $SourceDir -Force | ForEach-Object {
        Copy-Item -LiteralPath $_.FullName -Destination $DestinationDir -Recurse -Force
    }
}

function Set-ManagedBlock {
    param(
        [string]$FilePath,
        [string]$BeginMarker,
        [string]$EndMarker,
        [string]$Body
    )

    Ensure-Directory (Split-Path -Path $FilePath -Parent)

    $trimmedBody = $Body.TrimEnd("`r", "`n")
    $block = $BeginMarker + "`r`n" + $trimmedBody + "`r`n" + $EndMarker

    $current = ""
    if (Test-Path -LiteralPath $FilePath) {
        $current = Get-Content -LiteralPath $FilePath -Raw -Encoding UTF8
    }

    $pattern = [regex]::Escape($BeginMarker) + ".*?" + [regex]::Escape($EndMarker)

    if ([regex]::IsMatch($current, $pattern, [System.Text.RegularExpressions.RegexOptions]::Singleline)) {
        $updated = [regex]::Replace(
            $current,
            $pattern,
            $block,
            [System.Text.RegularExpressions.RegexOptions]::Singleline
        )
    }
    else {
        $trimmedCurrent = $current.TrimEnd("`r", "`n")
        if ([string]::IsNullOrWhiteSpace($trimmedCurrent)) {
            $updated = $block + "`r`n"
        }
        else {
            $updated = $trimmedCurrent + "`r`n`r`n" + $block + "`r`n"
        }
    }

    Set-Content -LiteralPath $FilePath -Value $updated -Encoding UTF8
}

function Render-Template {
    param(
        [string]$TemplateText,
        [hashtable]$Values
    )

    $result = $TemplateText
    foreach ($key in $Values.Keys) {
        $token = "{{" + $key + "}}"
        $result = $result.Replace($token, [string]$Values[$key])
    }

    return $result
}
function Get-DetectorAutoStartName {
    return "OpenClaw Guard Detector"
}

function Get-DetectorCommandLine {
    param(
        [string]$DetectorExe,
        [string]$OpenClawRoot
    )

    $quotedExe = '"' + $DetectorExe + '"'
    $quotedRoot = '"' + $OpenClawRoot + '"'
    return "$quotedExe watch --root $quotedRoot --log-level info"
}

function Register-DetectorAutoStart {
    param(
        [string]$DetectorExe,
        [string]$OpenClawRoot
    )

    $runKey = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Run"
    if (-not (Test-Path -LiteralPath $runKey)) {
        New-Item -Path $runKey -Force | Out-Null
    }

    $commandLine = Get-DetectorCommandLine -DetectorExe $DetectorExe -OpenClawRoot $OpenClawRoot
    Set-ItemProperty -Path $runKey -Name (Get-DetectorAutoStartName) -Value $commandLine

    return $commandLine
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

        if ([string]::IsNullOrWhiteSpace($procExe)) {
            continue
        }

        if ($procExe.ToLowerInvariant() -ne $fullExe) {
            continue
        }

        if ([string]::IsNullOrWhiteSpace($cmd)) {
            return $true
        }

        $cmdLower = $cmd.ToLowerInvariant()
        if ($cmdLower.Contains("watch") -and $cmdLower.Contains($fullRoot)) {
            return $true
        }
    }

    return $false
}

function Start-DetectorIfNeeded {
    param(
        [string]$DetectorExe,
        [string]$OpenClawRoot
    )

    $commandLine = Get-DetectorCommandLine -DetectorExe $DetectorExe -OpenClawRoot $OpenClawRoot

    if (Test-DetectorRunning -DetectorExe $DetectorExe -OpenClawRoot $OpenClawRoot) {
        return [pscustomobject]@{
            Running     = $true
            StartedNow  = $false
            CommandLine = $commandLine
            Message     = "Detector already running."
        }
    }

    Start-Process -FilePath $DetectorExe -ArgumentList @("watch", "--root", $OpenClawRoot, "--log-level", "info") -WindowStyle Hidden | Out-Null
    Start-Sleep -Seconds 2

    $running = Test-DetectorRunning -DetectorExe $DetectorExe -OpenClawRoot $OpenClawRoot

    return [pscustomobject]@{
        Running     = $running
        StartedNow  = $true
        CommandLine = $commandLine
        Message     = $(if ($running) { "Detector started." } else { "Detector start requested." })
    }
}
$InstallDir = (Resolve-Path -LiteralPath $InstallDir).Path
if ([string]::IsNullOrWhiteSpace($SourceDir)) {
    $SourceDir = Split-Path $PSScriptRoot -Parent
}

$SourceDir = (Resolve-Path -LiteralPath $SourceDir).Path

$sourceTemplatesDir = Join-Path $SourceDir "templates"
$sourceSkillDir = Join-Path $SourceDir "skills\openclaw-guard-kit"
$sourceWecomBridgeDir = Join-Path $SourceDir "tools\wecom-bridge"
$sourceInstallerDir = Join-Path $SourceDir "installer"

$sourceGuardExe = Join-Path $SourceDir "guard.exe"
$sourceDetectorExe = Join-Path $SourceDir "guard-detector.exe"
$sourceGuardUiExe = Join-Path $SourceDir "guard-ui.exe"
$sourceGuardUiManifest = Join-Path $SourceDir "guard-ui.exe.manifest"
$sourceToggleDetectorScript = Join-Path $sourceInstallerDir "toggle-detector.ps1"

$requiredUpdateSources = @(
    $sourceGuardExe,
    $sourceDetectorExe,
    $sourceGuardUiExe,
    (Join-Path $sourceTemplatesDir "AGENTS.append.md"),
    (Join-Path $sourceTemplatesDir "TOOLS.append.md"),
    (Join-Path $sourceSkillDir "SKILL.md"),
    (Join-Path $sourceWecomBridgeDir "index.mjs"),
    (Join-Path $sourceInstallerDir "update.ps1"),
    (Join-Path $sourceInstallerDir "uninstall.ps1"),
    (Join-Path $sourceInstallerDir "toggle-detector.ps1")
)

foreach ($requiredPath in $requiredUpdateSources) {
    if (-not (Test-Path -LiteralPath $requiredPath)) {
        throw "Update source missing: $requiredPath"
    }
}
$manifestPath = Join-Path $InstallDir "openclaw-guard-kit-install-manifest.json"

if (-not (Test-Path -LiteralPath $manifestPath)) {
    throw "Install manifest not found: $manifestPath"
}

$manifest = Read-JsonFile -Path $manifestPath
$bundleTemplatesDir = $manifest.bundle.templatesDir
$bundleSkillDir = $manifest.bundle.skillDir
$sharedSkillDir = $manifest.sharedSkillDir
$logsDir = Join-Path $InstallDir "logs"
Write-Step "Stopping detector..." "正在停止守护检测器"
Stop-DetectorIfRunning -DetectorExe $manifest.artifacts.guardDetectorExe

Write-Step "Stopping managed guard processes..." "正在停止守护相关进程"
Stop-ManagedGuardProcesses -Manifest $manifest
Ensure-Directory $logsDir

if (-not (Test-Path -LiteralPath (Join-Path $bundleTemplatesDir "AGENTS.append.md"))) {
    throw "Bundled AGENTS template missing."
}
if (-not (Test-Path -LiteralPath (Join-Path $manifest.bundle.wecomBridgeDir "index.mjs"))) {
    throw "Bundled WeCom bridge missing."
}
if (-not (Test-Path -LiteralPath (Join-Path $bundleTemplatesDir "TOOLS.append.md"))) {
    throw "Bundled TOOLS template missing."
}
if (-not (Test-Path -LiteralPath (Join-Path $bundleSkillDir "SKILL.md"))) {
    throw "Bundled skill missing."
}

Write-Step "Refreshing bundled resources..." "正在刷新安装资源"

Copy-Item -LiteralPath $sourceGuardExe -Destination $manifest.artifacts.guardExe -Force
Copy-Item -LiteralPath $sourceDetectorExe -Destination $manifest.artifacts.guardDetectorExe -Force
Copy-Item -LiteralPath $sourceGuardUiExe -Destination $manifest.artifacts.guardUiExe -Force

if (Test-Path -LiteralPath $sourceGuardUiManifest) {
    Copy-Item -LiteralPath $sourceGuardUiManifest -Destination $manifest.artifacts.guardUiManifest -Force
}

Copy-Item -LiteralPath (Join-Path $sourceTemplatesDir "AGENTS.append.md") -Destination (Join-Path $bundleTemplatesDir "AGENTS.append.md") -Force
Copy-Item -LiteralPath (Join-Path $sourceTemplatesDir "TOOLS.append.md") -Destination (Join-Path $bundleTemplatesDir "TOOLS.append.md") -Force
Copy-Item -LiteralPath (Join-Path $sourceInstallerDir "update.ps1") -Destination $manifest.installer.updateScript -Force
Copy-Item -LiteralPath (Join-Path $sourceInstallerDir "uninstall.ps1") -Destination $manifest.installer.uninstallScript -Force

$toggleDetectorTarget = $null
if ($manifest.installer.PSObject.Properties.Name -contains "toggleDetectorScript" -and -not [string]::IsNullOrWhiteSpace($manifest.installer.toggleDetectorScript)) {
    $toggleDetectorTarget = $manifest.installer.toggleDetectorScript
}
else {
    $toggleDetectorTarget = Join-Path (Join-Path $manifest.installDir "installer") "toggle-detector.ps1"
}

Copy-Item -LiteralPath $sourceToggleDetectorScript -Destination $toggleDetectorTarget -Force
$manifest.installer | Add-Member -NotePropertyName toggleDetectorScript -NotePropertyValue $toggleDetectorTarget -Force

Copy-DirectoryContent -SourceDir $sourceSkillDir -DestinationDir $bundleSkillDir
Copy-DirectoryContent -SourceDir $sourceWecomBridgeDir -DestinationDir $manifest.bundle.wecomBridgeDir
Copy-DirectoryContent -SourceDir $sourceWecomBridgeDir -DestinationDir $manifest.artifacts.wecomBridgeDir

Write-Step "Refreshing shared skill..." "正在刷新共享技能"
Copy-DirectoryContent -SourceDir $bundleSkillDir -DestinationDir $sharedSkillDir

Write-Step "Refreshing workspace rules..." "正在刷新工作区规则"

$agentsTemplateText = Get-Content -LiteralPath (Join-Path $bundleTemplatesDir "AGENTS.append.md") -Raw -Encoding UTF8
$toolsTemplateText = Get-Content -LiteralPath (Join-Path $bundleTemplatesDir "TOOLS.append.md") -Raw -Encoding UTF8

foreach ($workspacePath in $manifest.workspaces) {
    Ensure-Directory $workspacePath

    $agentsFile = Join-Path $workspacePath "AGENTS.md"
    $toolsFile = Join-Path $workspacePath "TOOLS.md"

    Set-ManagedBlock `
        -FilePath $agentsFile `
        -BeginMarker $manifest.markers.agentsBegin `
        -EndMarker $manifest.markers.agentsEnd `
        -Body $agentsTemplateText

    $toolsBody = Render-Template -TemplateText $toolsTemplateText -Values @{
        GUARD_INSTALL_DIR   = $manifest.installDir
        GUARD_EXE           = $manifest.artifacts.guardExe
        GUARD_DETECTOR_EXE  = $manifest.artifacts.guardDetectorExe
        GUARD_UI_EXE        = $manifest.artifacts.guardUiExe
        OPENCLAW_ROOT       = $manifest.openClawRoot
        WORKSPACE_PATH      = $workspacePath
        DEFAULT_AGENT_ID    = $manifest.defaultAgentId
        SHARED_SKILL_DIR    = $manifest.sharedSkillDir
    }

    Set-ManagedBlock `
        -FilePath $toolsFile `
        -BeginMarker $manifest.markers.toolsBegin `
        -EndMarker $manifest.markers.toolsEnd `
        -Body $toolsBody
}
Write-Step "Registering detector auto-start..." "正在注册 detector 自启动"
$detectorCommandLine = Register-DetectorAutoStart -DetectorExe $manifest.artifacts.guardDetectorExe -OpenClawRoot $manifest.openClawRoot

Write-Step "Starting detector..." "正在启动守护检测器"
$detectorStartResult = Start-DetectorIfNeeded -DetectorExe $manifest.artifacts.guardDetectorExe -OpenClawRoot $manifest.openClawRoot

$manifest.updatedAt = (Get-Date).ToString("o")
$manifest.updatedFrom = $SourceDir

if ($manifest.PSObject.Properties.Name -contains "doctor") {
    [void]$manifest.PSObject.Properties.Remove("doctor")
}

$manifest.detector = [pscustomobject]@{
    executable          = $manifest.artifacts.guardDetectorExe
    commandLine         = $detectorCommandLine
    autoStartName       = (Get-DetectorAutoStartName)
    autoStartRegistered = $true
    running             = $detectorStartResult.Running
    startedNow          = $detectorStartResult.StartedNow
    message             = $detectorStartResult.Message
}

$manifest | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $manifestPath -Encoding UTF8

Write-Step "Update completed." "更新完成"
Write-Host "Install dir: $InstallDir"
Write-Host "Shared skill dir: $sharedSkillDir"

if ($detectorStartResult.Running) {
    Write-Host "Detector is running."
}
else {
    Write-Host "Detector start was requested, but running state could not be confirmed."
}