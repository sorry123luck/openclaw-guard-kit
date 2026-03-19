param(
    [string]$ServiceName = "OpenClawGuard"
)

$ErrorActionPreference = "Stop"

$svc = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
if (-not $svc) {
    Write-Host "Service '$ServiceName' does not exist."
    exit 0
}

if ($svc.Status -ne "Stopped") {
    Stop-Service -Name $ServiceName -Force
    $svc.WaitForStatus("Stopped", "00:00:20")
}

sc.exe delete $ServiceName | Out-Null

Write-Host "Removed service: $ServiceName"