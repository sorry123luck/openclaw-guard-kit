param(
    [string]$ProjectDir = (Split-Path $PSScriptRoot -Parent),
    [string]$InstallDir = (Join-Path $env:USERPROFILE ".openclaw-guard-kit"),
    [string]$OpenClawRoot = (Join-Path $env:USERPROFILE ".openclaw"),
    [switch]$ForceRebuild
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
    }
    else {
        Write-Host "$English ($Chinese)"
    }
}

function Ensure-Directory {
    param([string]$Path)
    if ([string]::IsNullOrWhiteSpace($Path)) { return }
    if (-not (Test-Path -LiteralPath $Path)) {
        New-Item -ItemType Directory -Path $Path -Force | Out-Null
    }
}

function Resolve-ExistingPath {
    param(
        [string]$Path,
        [string]$Label
    )
    try {
        return (Resolve-Path -LiteralPath $Path).Path
    }
    catch {
        throw "$Label not found: $Path"
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

function Get-OpenClawConfigPath {
    param([string]$Root)
    $configPath = Join-Path $Root "openclaw.json"
    if (-not (Test-Path -LiteralPath $configPath)) {
        throw "OpenClaw config file not found: $configPath"
    }
    return (Resolve-Path -LiteralPath $configPath).Path
}

function Get-DefaultAgentId {
    param($Config)
    if ($null -ne $Config -and
        $null -ne $Config.agents -and
        $null -ne $Config.agents.list -and
        $Config.agents.list.Count -gt 0 -and
        -not [string]::IsNullOrWhiteSpace($Config.agents.list[0].id)) {
        return [string]$Config.agents.list[0].id
    }
    return "main"
}

function Get-DetectorAutoStartName {
    return "OpenClaw Guard Detector"
}

function Get-OfflineFlagPath {
    param([string]$Root)
    return (Join-Path $Root ".offline")
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
    return $commandLine
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

function Stop-InstalledChain {
    param(
        [string]$InstallDir,
        [string]$OpenClawRoot
    )

    $manifestPath = Join-Path $InstallDir "openclaw-guard-kit-install-manifest.json"
    $manifest = Read-JsonFile -Path $manifestPath

    if ($null -ne $manifest -and $null -ne $manifest.artifacts) {
        Stop-ProcessByExecutablePath -ExecutablePath $manifest.artifacts.guardUiExe
        Stop-ProcessByExecutablePath -ExecutablePath $manifest.artifacts.guardExe
        Stop-ProcessByExecutablePath -ExecutablePath $manifest.artifacts.guardDetectorExe
        return
    }

    Stop-ProcessByExecutablePath -ExecutablePath (Join-Path $InstallDir "guard-ui.exe")
    Stop-ProcessByExecutablePath -ExecutablePath (Join-Path $InstallDir "guard.exe")
    Stop-ProcessByExecutablePath -ExecutablePath (Join-Path $InstallDir "guard-detector.exe")
}

function Start-DetectorIfNeeded {
    param(
        [string]$DetectorExe,
        [string]$OpenClawRoot,
        [string]$AgentID
    )

    if (Test-DetectorRunning -DetectorExe $DetectorExe -OpenClawRoot $OpenClawRoot) {
        return $false
    }

    Start-Process -FilePath $DetectorExe -ArgumentList @(
        "--root", $OpenClawRoot,
        "--agent", $AgentID,
        "--log-level", "info"
    ) -WindowStyle Hidden | Out-Null

    Start-Sleep -Seconds 2
    return (Test-DetectorRunning -DetectorExe $DetectorExe -OpenClawRoot $OpenClawRoot)
}

function Sync-Directory {
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

$ProjectDir = Resolve-ExistingPath -Path $ProjectDir -Label "Project dir"
$InstallDir = [System.IO.Path]::GetFullPath($InstallDir)
$OpenClawRoot = [System.IO.Path]::GetFullPath($OpenClawRoot)

Write-Step "Checking package integrity..." "正在检查程序完整性"

$guardExeSource = Resolve-ExistingPath -Path (Join-Path $ProjectDir "guard.exe") -Label "guard.exe"
$detectorExeSource = Resolve-ExistingPath -Path (Join-Path $ProjectDir "guard-detector.exe") -Label "guard-detector.exe"
$guardUiExeSource = Resolve-ExistingPath -Path (Join-Path $ProjectDir "guard-ui.exe") -Label "guard-ui.exe"
$installScriptSource = Resolve-ExistingPath -Path (Join-Path $ProjectDir "installer\install.ps1") -Label "installer\install.ps1"
$updateScriptSource = Resolve-ExistingPath -Path (Join-Path $ProjectDir "installer\update.ps1") -Label "installer\update.ps1"
$updateFromDirSource = Resolve-ExistingPath -Path (Join-Path $ProjectDir "installer\update-from-dir.ps1") -Label "installer\update-from-dir.ps1"
$uninstallScriptSource = Resolve-ExistingPath -Path (Join-Path $ProjectDir "installer\uninstall.ps1") -Label "installer\uninstall.ps1"
$toggleScriptSource = Resolve-ExistingPath -Path (Join-Path $ProjectDir "installer\toggle-detector.ps1") -Label "installer\toggle-detector.ps1"
$wecomBridgeSourceDir = Resolve-ExistingPath -Path (Join-Path $ProjectDir "tools\wecom-bridge") -Label "tools\wecom-bridge"
$wecomBridgeEntry = Resolve-ExistingPath -Path (Join-Path $wecomBridgeSourceDir "index.mjs") -Label "tools\wecom-bridge\index.mjs"
$wecomBridgePackage = Resolve-ExistingPath -Path (Join-Path $wecomBridgeSourceDir "package.json") -Label "tools\wecom-bridge\package.json"

$guardUiManifestSource = Join-Path $ProjectDir "guard-ui.exe.manifest"
if (Test-Path -LiteralPath $guardUiManifestSource) {
    $guardUiManifestSource = (Resolve-Path -LiteralPath $guardUiManifestSource).Path
}
else {
    $guardUiManifestSource = $null
}

$packageManifestPath = Join-Path $ProjectDir "installer\package-manifest.json"
$packageManifest = Read-JsonFile -Path $packageManifestPath

Write-Step "Loading OpenClaw configuration..." "正在识别 OpenClaw 环境"
$openClawConfigPath = Get-OpenClawConfigPath -Root $OpenClawRoot
$openClawConfig = Read-JsonFile -Path $openClawConfigPath
$agentID = Get-DefaultAgentId -Config $openClawConfig

Write-Step "Stopping old guard chain if needed..." "正在停止旧的守护链"
Stop-InstalledChain -InstallDir $InstallDir -OpenClawRoot $OpenClawRoot

Write-Step "Preparing install directory..." "正在准备安装目录"
Ensure-Directory $InstallDir
Ensure-Directory (Join-Path $InstallDir "installer")
Ensure-Directory (Join-Path $InstallDir "tools")
Ensure-Directory (Join-Path $InstallDir "logs")

Write-Step "Copying program files..." "正在复制程序文件"
Copy-Item -LiteralPath $guardExeSource -Destination (Join-Path $InstallDir "guard.exe") -Force
Copy-Item -LiteralPath $detectorExeSource -Destination (Join-Path $InstallDir "guard-detector.exe") -Force
Copy-Item -LiteralPath $guardUiExeSource -Destination (Join-Path $InstallDir "guard-ui.exe") -Force

if ($null -ne $guardUiManifestSource) {
    Copy-Item -LiteralPath $guardUiManifestSource -Destination (Join-Path $InstallDir "guard-ui.exe.manifest") -Force
}

Copy-Item -LiteralPath $installScriptSource -Destination (Join-Path $InstallDir "installer\install.ps1") -Force
Copy-Item -LiteralPath $updateScriptSource -Destination (Join-Path $InstallDir "installer\update.ps1") -Force
Copy-Item -LiteralPath $updateFromDirSource -Destination (Join-Path $InstallDir "installer\update-from-dir.ps1") -Force
Copy-Item -LiteralPath $uninstallScriptSource -Destination (Join-Path $InstallDir "installer\uninstall.ps1") -Force
Copy-Item -LiteralPath $toggleScriptSource -Destination (Join-Path $InstallDir "installer\toggle-detector.ps1") -Force

if (Test-Path -LiteralPath (Join-Path $ProjectDir "README.md")) {
    Copy-Item -LiteralPath (Join-Path $ProjectDir "README.md") -Destination (Join-Path $InstallDir "README.md") -Force
}
if (Test-Path -LiteralPath (Join-Path $ProjectDir "LICENSE")) {
    Copy-Item -LiteralPath (Join-Path $ProjectDir "LICENSE") -Destination (Join-Path $InstallDir "LICENSE") -Force
}

Write-Step "Copying WeCom bridge..." "正在复制企业微信桥接工具"
Sync-Directory -SourceDir $wecomBridgeSourceDir -DestinationDir (Join-Path $InstallDir "tools\wecom-bridge")

Write-Step "Registering detector auto start..." "正在注册 detector 自启动"
$detectorExeInstalled = Join-Path $InstallDir "guard-detector.exe"
$autoStartCommand = Register-DetectorAutoStart -DetectorExe $detectorExeInstalled -OpenClawRoot $OpenClawRoot -AgentID $agentID

Clear-OfflineFlag -Root $OpenClawRoot

$manifest = [ordered]@{
    schemaVersion = 2
    packageName   = if ($null -ne $packageManifest -and -not [string]::IsNullOrWhiteSpace($packageManifest.packageName)) { $packageManifest.packageName } else { "openclaw-guard-kit-windows-x64" }
    version       = if ($null -ne $packageManifest -and -not [string]::IsNullOrWhiteSpace($packageManifest.version)) { $packageManifest.version } else { "dev" }
    builtAtUtc    = if ($null -ne $packageManifest -and -not [string]::IsNullOrWhiteSpace($packageManifest.builtAtUtc)) { $packageManifest.builtAtUtc } else { (Get-Date).ToUniversalTime().ToString("o") }
    installDir    = $InstallDir
    openClawRoot  = $OpenClawRoot
    agentId       = $agentID
    detectorAutoStartName    = (Get-DetectorAutoStartName)
    detectorAutoStartCommand = $autoStartCommand
    artifacts = [ordered]@{
        guardExe         = (Join-Path $InstallDir "guard.exe")
        guardDetectorExe = (Join-Path $InstallDir "guard-detector.exe")
        guardUiExe       = (Join-Path $InstallDir "guard-ui.exe")
        guardUiManifest  = (Join-Path $InstallDir "guard-ui.exe.manifest")
        wecomBridgeDir   = (Join-Path $InstallDir "tools\wecom-bridge")
    }
    installer = [ordered]@{
        installScript        = (Join-Path $InstallDir "installer\install.ps1")
        updateScript         = (Join-Path $InstallDir "installer\update.ps1")
        updateFromDirScript  = (Join-Path $InstallDir "installer\update-from-dir.ps1")
        uninstallScript      = (Join-Path $InstallDir "installer\uninstall.ps1")
        toggleDetectorScript = (Join-Path $InstallDir "installer\toggle-detector.ps1")
    }
}

$manifestPath = Join-Path $InstallDir "openclaw-guard-kit-install-manifest.json"
$manifest | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath $manifestPath -Encoding UTF8

Write-Step "Starting detector..." "正在启动 detector"
$started = Start-DetectorIfNeeded -DetectorExe $detectorExeInstalled -OpenClawRoot $OpenClawRoot -AgentID $agentID

Write-Step "Install completed." "安装完成"
Write-Host "Install dir: $InstallDir"
Write-Host "OpenClaw root: $OpenClawRoot"
Write-Host "Agent: $agentID"
if ($started) {
    Write-Host "Detector started successfully."
}
else {
    Write-Host "Detector start requested. Please confirm running state manually if needed."
}