# Verification entry point that does not depend on shell PATH inheritance.
# Usage:
#   powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1
#   ... -Integration     # also run integration tests (needs PostgreSQL)
#   ... -Migration       # also run migrate up/down/up
#
# Keep this file ASCII-only: Windows PowerShell 5.1 reads .ps1 as ANSI
# unless a BOM is present, which breaks non-ASCII literals.

param(
    [switch]$Integration,
    [switch]$Migration
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

# Sandboxes often expose only the Go toolchain on PATH.
$candidates = @(
    "$env:LOCALAPPDATA\Microsoft\WinGet\Links",
    'C:\Program Files\Go\bin',
    "$env:USERPROFILE\go\bin"
)
$env:Path = (($candidates | Where-Object { Test-Path $_ }) -join ';') + ';' + $env:Path

function Require-Tool([string]$name, [string]$hint) {
    $cmd = Get-Command $name -ErrorAction SilentlyContinue
    if (-not $cmd) { throw "missing tool: $name ($hint)" }
    return $cmd.Source
}

$go = Require-Tool 'go' 'install Go 1.23+'
$lint = Require-Tool 'golangci-lint' 'install golangci-lint v1.64.x'

Write-Host '== go vet ==' -ForegroundColor Cyan
& $go vet ./...
if ($LASTEXITCODE -ne 0) { throw 'go vet failed' }

Write-Host '== golangci-lint ==' -ForegroundColor Cyan
& $lint run
if ($LASTEXITCODE -ne 0) { throw 'lint failed' }

Write-Host '== go test ==' -ForegroundColor Cyan
& $go test ./...
if ($LASTEXITCODE -ne 0) { throw 'unit tests failed' }

function Read-DotEnv([string]$key) {
    if (-not (Test-Path '.env')) { return '' }
    foreach ($line in Get-Content '.env') {
        $trimmed = $line.Trim()
        if ($trimmed -eq '' -or $trimmed.StartsWith('#')) { continue }
        $idx = $trimmed.IndexOf('=')
        if ($idx -lt 1) { continue }
        if ($trimmed.Substring(0, $idx).Trim() -eq $key) {
            return $trimmed.Substring($idx + 1).Trim().Trim('"').Trim("'")
        }
    }
    return ''
}

if ($Integration) {
    if (-not $env:TEST_DATABASE_URL -and -not (Test-Path '.env')) {
        throw 'integration needs TEST_DATABASE_URL or a repo-root .env'
    }
    $savedTxnTimeout = $env:CH_DB_TXN_TIMEOUT
    $savedOpTimeout = $env:CH_CACHE_OP_TIMEOUT
    try {
        if (-not $env:CH_DB_TXN_TIMEOUT) { $env:CH_DB_TXN_TIMEOUT = '15s' }
        if (-not $env:CH_CACHE_OP_TIMEOUT) { $env:CH_CACHE_OP_TIMEOUT = '5s' }
        Write-Host '== integration ==' -ForegroundColor Cyan
        Write-Host "budgets: db.txn_timeout=$env:CH_DB_TXN_TIMEOUT cache.op_timeout=$env:CH_CACHE_OP_TIMEOUT"
        & $go test -count=1 -p 1 ./internal/kernel/db ./internal/bootstrap ./cmd/cli -run Integration
        if ($LASTEXITCODE -ne 0) { throw 'integration failed' }
    } finally {
        $env:CH_DB_TXN_TIMEOUT = $savedTxnTimeout
        $env:CH_CACHE_OP_TIMEOUT = $savedOpTimeout
    }
}

if ($Migration) {
    # The CLI reads CH_* env vars; load them from .env so no manual export is needed.
    if (Test-Path '.env') {
        foreach ($line in Get-Content '.env') {
            $item = $line.Trim()
            if ($item -eq '' -or $item.StartsWith('#')) { continue }
            $sep = $item.IndexOf('=')
            if ($sep -lt 1) { continue }
            $name = $item.Substring(0, $sep).Trim()
            if ($name -notlike 'CH_*') { continue }
            if ([Environment]::GetEnvironmentVariable($name)) { continue }
            $value = $item.Substring($sep + 1).Trim().Trim('"').Trim("'")
            Set-Item -Path ("env:" + $name) -Value $value
        }
    }
    # The CLI reads CH_DB_DSN; map the test DSN so no manual export is needed.
    if (-not $env:CH_DB_DSN) {
        $dsn = $env:TEST_DATABASE_URL
        if (-not $dsn) { $dsn = Read-DotEnv 'TEST_DATABASE_URL' }
        if (-not $dsn) { throw 'migration needs CH_DB_DSN or TEST_DATABASE_URL in .env' }
        $env:CH_DB_DSN = $dsn
    }
    Write-Host '== migrate up/down/up ==' -ForegroundColor Cyan
    & $go run ./cmd/cli migrate up
    if ($LASTEXITCODE -ne 0) { throw 'migrate up failed' }
    & $go run ./cmd/cli migrate down
    if ($LASTEXITCODE -ne 0) { throw 'migrate down failed' }
    & $go run ./cmd/cli migrate up
    if ($LASTEXITCODE -ne 0) { throw 'migrate up (second) failed' }
}

Write-Host 'all checks passed' -ForegroundColor Green
