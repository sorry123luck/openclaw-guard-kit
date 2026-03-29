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
function Invoke-GoBuild {
    param(
        [string]$Root,
        [string]$OutputPath,
        [string]$PackagePath
    )

    Push-Location $Root
    try {
        & go build -o $OutputPath $PackagePath
        if ($LASTEXITCODE -ne 0) {
            throw "go build failed for $PackagePath"
        }
    }
    finally {
        Pop-Location
    }
}

function Ensure-GuardArtifact {
    param(
        [string]$Root,
        [string]$InstallRoot,
        [string]$FileName,
        [string]$PackagePath,
        [bool]$Rebuild
    )

    $repoArtifact = Join-Path $Root $FileName
    $destination = Join-Path $InstallRoot $FileName

    if ((Test-Path -LiteralPath $repoArtifact) -and -not $Rebuild) {
        Copy-Item -LiteralPath $repoArtifact -Destination $destination -Force
        return $destination
    }

    $goCmd = Get-Command go -ErrorAction SilentlyContinue
    if (-not $goCmd) {
        throw "Go toolchain not found in PATH, and prebuilt artifact is missing: $FileName"
    }

    Write-Step "Building $FileName..." "正在编译程序文件"
    Invoke-GoBuild -Root $Root -OutputPath $destination -PackagePath $PackagePath
    return $destination
}

function Get-InstallVersion {
    param([string]$Root)

    $gitCmd = Get-Command git -ErrorAction SilentlyContinue
    if ($gitCmd) {
        try {
            $commit = (& git -C $Root rev-parse --short HEAD 2>$null)
            if ($LASTEXITCODE -eq 0 -and -not [string]::IsNullOrWhiteSpace($commit)) {
                return $commit.Trim()
            }
        }
        catch {
        }
    }

    return (Get-Date -Format "yyyyMMdd-HHmmss")
}

$ProjectDir = Resolve-ExistingPath -Path $ProjectDir -Label "Project directory"
$OpenClawRoot = Resolve-ExistingPath -Path $OpenClawRoot -Label "OpenClaw root"

Write-Step "Checking package integrity..." "正在检查程序完整性"

$requiredPaths = @(
    (Join-Path $ProjectDir "go.mod"),
    (Join-Path $ProjectDir "cmd\guard\main.go"),
    (Join-Path $ProjectDir "cmd\guard-detector\main.go"),
    (Join-Path $ProjectDir "cmd\guard-ui\main.go"),
    (Join-Path $ProjectDir "templates\AGENTS.append.md"),
    (Join-Path $ProjectDir "templates\TOOLS.append.md"),
    (Join-Path $ProjectDir "installer\update.ps1"),
    (Join-Path $ProjectDir "installer\uninstall.ps1"),
    (Join-Path $ProjectDir "installer\toggle-detector.ps1"),
    (Join-Path $ProjectDir "tools\wecom-bridge\index.mjs"),
    (Join-Path $ProjectDir "tools\wecom-bridge\package.json")
)

foreach ($requiredPath in $requiredPaths) {
    if (-not (Test-Path -LiteralPath $requiredPath)) {
        throw "Required file missing: $requiredPath"
    }
}

$guardUiManifestSource = Join-Path $ProjectDir "guard-ui.exe.manifest"
$wecomBridgeSourceDir = Join-Path $ProjectDir "tools\wecom-bridge"

Write-Step "Loading OpenClaw configuration..." "正在识别 OpenClaw 环境"

$configPath = Get-OpenClawConfigPath -Root $OpenClawRoot
$configObject = Read-JsonFile -Path $configPath
$defaultAgentId = Get-DefaultAgentId -Config $configObject
$workspaceInfos = Get-WorkspaceInfos -Config $configObject -OpenClawRootPath $OpenClawRoot
Write-Host ("Detected workspaces: " + (($workspaceInfos | ForEach-Object { $_.Path } | Sort-Object -Unique) -join "; "))

Write-Step "Preparing install directory..." "正在准备安装目录"

Ensure-Directory $InstallDir
$logsDir = Join-Path $InstallDir "logs"
$bundleDir = Join-Path $InstallDir "bundle"
$bundleTemplatesDir = Join-Path $bundleDir "templates"
$bundleToolsDir = Join-Path $bundleDir "tools\wecom-bridge"
$installToolsDir = Join-Path $InstallDir "tools\wecom-bridge"
$installInstallerDir = Join-Path $InstallDir "installer"

Ensure-Directory $logsDir
Ensure-Directory $bundleTemplatesDir
Ensure-Directory $bundleToolsDir
Ensure-Directory $installToolsDir
Ensure-Directory $installInstallerDir

Write-Step "Preparing program files..." "正在准备程序文件"

$guardExe = Ensure-GuardArtifact -Root $ProjectDir -InstallRoot $InstallDir -FileName "guard.exe" -PackagePath ".\cmd\guard" -Rebuild:$ForceRebuild
$guardDetectorExe = Ensure-GuardArtifact -Root $ProjectDir -InstallRoot $InstallDir -FileName "guard-detector.exe" -PackagePath ".\cmd\guard-detector" -Rebuild:$ForceRebuild
$guardUiExe = Ensure-GuardArtifact -Root $ProjectDir -InstallRoot $InstallDir -FileName "guard-ui.exe" -PackagePath ".\cmd\guard-ui" -Rebuild:$ForceRebuild

if (Test-Path -LiteralPath $guardUiManifestSource) {
    Copy-Item -LiteralPath $guardUiManifestSource -Destination (Join-Path $InstallDir "guard-ui.exe.manifest") -Force
}

Write-Step "Staging install resources..." "正在写入安装资源"

Copy-Item -LiteralPath (Join-Path $ProjectDir "templates\AGENTS.append.md") -Destination (Join-Path $bundleTemplatesDir "AGENTS.append.md") -Force
Copy-Item -LiteralPath (Join-Path $ProjectDir "templates\TOOLS.append.md") -Destination (Join-Path $bundleTemplatesDir "TOOLS.append.md") -Force
Copy-Item -LiteralPath (Join-Path $ProjectDir "installer\update.ps1") -Destination (Join-Path $installInstallerDir "update.ps1") -Force
Copy-Item -LiteralPath (Join-Path $ProjectDir "installer\uninstall.ps1") -Destination (Join-Path $installInstallerDir "uninstall.ps1") -Force
Copy-Item -LiteralPath (Join-Path $ProjectDir "installer\toggle-detector.ps1") -Destination (Join-Path $installInstallerDir "toggle-detector.ps1") -Force
Copy-DirectoryContent -SourceDir $wecomBridgeSourceDir -DestinationDir $bundleToolsDir
Copy-DirectoryContent -SourceDir $wecomBridgeSourceDir -DestinationDir $installToolsDir


Write-Step "Registering detector auto-start..." "正在注册 detector 自启动"
$detectorCommandLine = Register-DetectorAutoStart -DetectorExe $guardDetectorExe -OpenClawRoot $OpenClawRoot

Write-Step "Starting detector..." "正在启动守护检测器"
$detectorStartResult = Start-DetectorIfNeeded -DetectorExe $guardDetectorExe -OpenClawRoot $OpenClawRoot
Write-Step "Writing install manifest..." "正在写入安装清单"

$installVersion = Get-InstallVersion -Root $ProjectDir
$manifestPath = Join-Path $InstallDir "openclaw-guard-kit-install-manifest.json"

$manifestObject = [pscustomobject]@{
    schema         = "openclaw-guard-kit.install.v1"
    packageName    = "openclaw-guard-kit"
    version        = $installVersion
    installedAt    = (Get-Date).ToString("o")
    installDir     = $InstallDir
    openClawRoot   = $OpenClawRoot
    openClawConfig = $configPath
    defaultAgentId = $defaultAgentId
    sharedSkillDir = $sharedSkillDir
    workspaces     = @($workspaceInfos | ForEach-Object { $_.Path })
    artifacts      = [pscustomobject]@{
    guardExe         = $guardExe
    guardDetectorExe = $guardDetectorExe
    guardUiExe       = $guardUiExe
    guardUiManifest  = $(if (Test-Path -LiteralPath (Join-Path $InstallDir "guard-ui.exe.manifest")) { Join-Path $InstallDir "guard-ui.exe.manifest" } else { "" })
    wecomBridgeDir   = $installToolsDir
    wecomBridgeEntry = (Join-Path $installToolsDir "index.mjs")
    }
    bundle         = [pscustomobject]@{
        templatesDir   = $bundleTemplatesDir
        skillDir       = $bundleSkillsDir
        wecomBridgeDir = $bundleToolsDir
    }
    installer      = [pscustomobject]@{
        updateScript         = (Join-Path $installInstallerDir "update.ps1")
        uninstallScript      = (Join-Path $installInstallerDir "uninstall.ps1")
        toggleDetectorScript = (Join-Path $installInstallerDir "toggle-detector.ps1")
    }
    markers        = [pscustomobject]@{
        agentsBegin = $agentsBeginMarker
        agentsEnd   = $agentsEndMarker
        toolsBegin  = $toolsBeginMarker
        toolsEnd    = $toolsEndMarker
    }
    detector       = [pscustomobject]@{
    executable          = $guardDetectorExe
    commandLine         = $detectorCommandLine
    autoStartName       = (Get-DetectorAutoStartName)
    autoStartRegistered = $true
    running             = $detectorStartResult.Running
    startedNow          = $detectorStartResult.StartedNow
    message             = $detectorStartResult.Message
  }
}

$manifestObject | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $manifestPath -Encoding UTF8

Write-Step "Installation completed." "安装完成"
Write-Host "Install dir: $InstallDir"
Write-Host "Shared skill dir: $sharedSkillDir"
Write-Host "Updated workspaces: $($workspaceInfos.Count)"
Write-Host "Manifest: $manifestPath"

if ($detectorStartResult.Running) {
    Write-Host "Detector is running."
}
else {
    Write-Host "Detector start was requested, but running state could not be confirmed."
}