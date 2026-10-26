# Starts the gateway with .env loaded into the process environment.
# PowerShell does not read .env automatically, so this wrapper exists to make
# .\ops\run.ps1 behave like `make dev` on a Unix host.

$ErrorActionPreference = 'Stop'

$root = Split-Path -Parent $PSScriptRoot
$envFile = Join-Path $root '.env'

if (-not (Test-Path $envFile)) {
    Write-Error "missing .env at $envFile - copy .env.example and fill it in"
}

Get-Content $envFile | ForEach-Object {
    if ($_ -match '^\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$') {
        $key = $Matches[1]
        $value = $Matches[2].Trim()
        if ($value -ne '') {
            Set-Item -Path "env:$key" -Value $value
        }
    }
}

$exe = Join-Path $root 'backend\bin\paygate.exe'
if (-not (Test-Path $exe)) {
    Write-Host 'building backend...' -ForegroundColor Cyan
    Push-Location (Join-Path $root 'backend')
    try {
        go build -o 'bin\paygate.exe' ./cmd/gateway
        if ($LASTEXITCODE -ne 0) { throw 'go build failed' }
    } finally {
        Pop-Location
    }
}

Write-Host ('starting paygate on ' + $env:PAYGATE_ADDR) -ForegroundColor Green
& $exe @args
