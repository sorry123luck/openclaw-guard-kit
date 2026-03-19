param(
    [string]$ProjectDir = (Split-Path $PSScriptRoot -Parent),
    [string]$ServiceName = "OpenClawGuard"
)

$ErrorActionPreference = "Stop"

Push-Location $ProjectDir
try {
    $svc = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
    $wasRunning = $false

    if ($svc -and $svc.Status -ne "Stopped") {
        Stop-Service -Name $ServiceName -Force
        $svc.WaitForStatus("Stopped", "00:00:20")
        $wasRunning = $true
    }

    go build -o .\guard.exe .\cmd\guard

    if ($svc -and $wasRunning) {
        Start-Service -Name $ServiceName
    }

    Write-Host "Update completed."
}
finally {
    Pop-Location
}