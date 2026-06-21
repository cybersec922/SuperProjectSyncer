# Local two-peer sync test (same PC, ports 7741 + 7742)
# Usage: .\test\run-test.ps1
#        .\test\run-test.ps1 -Stop   # stop test peers only

param(
    [switch]$Stop
)

$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
Set-Location $Root

function Stop-TestPeers {
    Get-Process sps -ErrorAction SilentlyContinue | Stop-Process -Force
    Start-Sleep -Milliseconds 500
}

if ($Stop) {
    Stop-TestPeers
    Write-Host "Test peers stopped."
    exit 0
}

$go = "C:\Program Files\Go\bin\go.exe"
if (-not (Test-Path $go)) { $go = "go" }

Stop-TestPeers

foreach ($d in @("test\sync-a", "test\sync-b", "test\data-a", "test\data-b", "bin")) {
    New-Item -ItemType Directory -Force -Path $d | Out-Null
}

Write-Host "Building sps..."
& $go build -o bin/sps.exe ./cmd/sps
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

$sps = Join-Path $Root "bin\sps.exe"
$logA = Join-Path $Root "test\peer-a.log"
$logB = Join-Path $Root "test\peer-b.log"
$errA = Join-Path $Root "test\peer-a.err.log"
$errB = Join-Path $Root "test\peer-b.err.log"

Write-Host "Starting peer A (7741)..."
Start-Process -FilePath $sps -ArgumentList "run", "--config", "test\config-a.toml" `
    -WorkingDirectory $Root -WindowStyle Hidden `
    -RedirectStandardOutput $logA -RedirectStandardError $errA

Write-Host "Starting peer B (7742)..."
Start-Process -FilePath $sps -ArgumentList "run", "--config", "test\config-b.toml" `
    -WorkingDirectory $Root -WindowStyle Hidden `
    -RedirectStandardOutput $logB -RedirectStandardError $errB

$deadline = (Get-Date).AddSeconds(10)
$connected = $false
while ((Get-Date) -lt $deadline) {
    Start-Sleep -Milliseconds 500
    $log = @(
        Get-Content $logA -ErrorAction SilentlyContinue
        Get-Content $errA -ErrorAction SilentlyContinue
        Get-Content $logB -ErrorAction SilentlyContinue
        Get-Content $errB -ErrorAction SilentlyContinue
    ) -join "`n"
    if ($log -match "connected to|inbound peer") {
        $connected = $true
        break
    }
}
if (-not $connected) {
    Write-Host "WARN: peers may not be connected yet. Check test\peer-a.log and test\peer-b.log"
} else {
    Write-Host "Peers connected."
}

# Sync test A -> B
$msgA = "hello from sync-a $(Get-Date -Format o)"
Set-Content -Path "test\sync-a\hello.txt" -Value $msgA -Encoding utf8
Start-Sleep -Seconds 2

if (Test-Path "test\sync-b\hello.txt") {
    Write-Host "[OK] A -> B: $(Get-Content test\sync-b\hello.txt)"
} else {
    Write-Host "[FAIL] A -> B: hello.txt not in test\sync-b"
}

# Sync test B -> A
$msgB = "hello from sync-b $(Get-Date -Format o)"
Set-Content -Path "test\sync-b\reply.txt" -Value $msgB -Encoding utf8
Start-Sleep -Seconds 2

if (Test-Path "test\sync-a\reply.txt") {
    Write-Host "[OK] B -> A: $(Get-Content test\sync-a\reply.txt)"
} else {
    Write-Host "[FAIL] B -> A: reply.txt not in test\sync-a"
}

# Ignore test
Set-Content -Path "test\sync-a\.env" -Value "SECRET=123" -Encoding utf8
Start-Sleep -Seconds 2
if (Test-Path "test\sync-b\.env") {
    Write-Host "[FAIL] ignore: .env was synced (should be blocked)"
} else {
    Write-Host "[OK] ignore: .env blocked"
}

Write-Host ""
Write-Host "Test peers still running. Edit files in:"
Write-Host "  test\sync-a  <->  test\sync-b"
Write-Host ""
Write-Host "Stop:  .\test\run-test.ps1 -Stop"
Write-Host "Logs:  test\peer-a.log  test\peer-b.log"
