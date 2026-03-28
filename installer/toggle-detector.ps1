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
    param(
        [Parameter(Mandatory = $true)][string]$Path
    )

    if (-not (Test-Path -LiteralPath $Path)) {
        throw "File not found: $Path"
    }

    return (Get-Content -LiteralPath $Path -Raw -Encoding UTF8 | ConvertFrom-Json)
}

function Stop-ProcessByExecutablePath {
    param(
        [string]$ExecutablePath
    )

    if ([string]::IsNullOrWhiteSpace($ExecutablePath)) {
        return
    }

    if (-not (Test-Path -LiteralPath $ExecutablePath)) {
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

function Test-DetectorRunning {
    param(
        [string]$DetectorExe,
        [string]$OpenClawRoot
    )

    if ([string]::IsNullOrWhiteSpace($DetectorExe)) {
        return $false
    }

    if (-not (Test-Path -LiteralPath $DetectorExe)) {
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

function Start-Detector {
    param(
        [string]$DetectorExe,
        [string]$OpenClawRoot
    )

    Start-Process -FilePath $DetectorExe -ArgumentList @(
        "watch",
        "--root", $OpenClawRoot,
        "--log-level", "info"
    ) -WindowStyle Hidden | Out-Null
}

$InstallDir = [System.IO.Path]::GetFullPath($InstallDir)
$manifestPath = Join-Path $InstallDir "openclaw-guard-kit-install-manifest.json"

Write-Step "Loading install manifest..." "正在读取安装清单"
$manifest = Read-JsonFile -Path $manifestPath

$detectorExe = $manifest.artifacts.guardDetectorExe
$guardExe = $manifest.artifacts.guardExe
$guardUiExe = $manifest.artifacts.guardUiExe
$openClawRoot = $manifest.openClawRoot

if ([string]::IsNullOrWhiteSpace($detectorExe) -or -not (Test-Path -LiteralPath $detectorExe)) {
    throw "Installed detector executable not found: $detectorExe"
}

if ([string]::IsNullOrWhiteSpace($openClawRoot)) {
    throw "openClawRoot missing in manifest."
}

if (Test-DetectorRunning -DetectorExe $detectorExe -OpenClawRoot $openClawRoot) {
    Write-Step "Detector is running. Stopping guard chain..." "detector 正在运行，正在关闭整条守护链"

    Stop-ProcessByExecutablePath -ExecutablePath $detectorExe
    Start-Sleep -Seconds 1

    Stop-ProcessByExecutablePath -ExecutablePath $guardUiExe
    Stop-ProcessByExecutablePath -ExecutablePath $guardExe

    Write-Host "Detector chain stopped."
}
else {
    Write-Step "Detector is not running. Starting detector..." "detector 未运行，正在启动"

    Start-Detector -DetectorExe $detectorExe -OpenClawRoot $openClawRoot
    Start-Sleep -Seconds 2

    if (Test-DetectorRunning -DetectorExe $detectorExe -OpenClawRoot $openClawRoot) {
        Write-Host "Detector started."
    }
    else {
        Write-Host "Detector start requested, but running state could not be confirmed."
    }
}