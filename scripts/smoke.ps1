# S3: real API + worker + PostgreSQL + Redis. Windows PowerShell 5.1; ASCII only.
# Usage: powershell -NoProfile -ExecutionPolicy Bypass -File scripts/smoke.ps1
# See scripts/smoke/README.md for prerequisites, isolation and fault injection.
param(
    [string]$DatabaseUrl,
    [string]$RedisAddr,
    [switch]$KeepArtifacts,
    [ValidateRange(1, 30)][int]$StepTimeoutSeconds = 30,
    [ValidateRange(30, 300)][int]$TotalTimeoutSeconds = 300,
    [ValidateSet('None', 'SkipRevoke')][string]$Fault = 'None',
    [ValidateSet('None', 'Rotation', 'Revocation', 'Quota')][string]$GatewayDrill = 'None'
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 2.0
$script:Root = Split-Path -Parent $PSScriptRoot
$script:Started = [DateTime]::UtcNow
$script:Step = 1
$script:Processes = New-Object 'Collections.Generic.List[object]'
$script:Secrets = New-Object 'Collections.Generic.List[string]'
$script:HTTP = New-Object 'Collections.Generic.List[object]'
$script:ChildEnv = @{}
$script:AccessToken = ''
$script:BrowserCookie = ''
$script:SchemaCreated = $false
$script:RedisClaimed = $false
$script:Artifacts = Join-Path ([IO.Path]::GetTempPath()) ('s3-smoke-' + [Guid]::NewGuid().ToString('N'))
$script:Schema = 's3_smoke_' + [Guid]::NewGuid().ToString('N')
$script:Results = @('Dependencies', 'API and worker readiness', 'Seed and login', 'Shop and credential',
    'Product and order sync', 'Persisted data', 'Merge listings', 'Customer and quote', 'Password link',
    'Anonymous browsing', 'Encrypted inquiry', 'Reauthentication and audit', 'Revoke link', 'Cleanup', 'Open API and metering') |
    ForEach-Object -Begin { $n = 0 } -Process {
        $n++; [pscustomobject]@{ Step = $n; Name = $_; Result = 'SKIPPED'; Seconds = 0; Reason = 'not reached' }
    }
$exitCode = 0
try {
    New-Item -ItemType Directory -Path $script:Artifacts | Out-Null
    . (Join-Path $PSScriptRoot 'smoke/support.ps1')
    . (Join-Path $PSScriptRoot 'smoke/process.ps1')
    . (Join-Path $PSScriptRoot 'smoke/setup.ps1')
    . (Join-Path $PSScriptRoot 'smoke/steps.ps1')
    . (Join-Path $PSScriptRoot 'smoke/gateway.ps1')
    Invoke-SmokeStep 1 { Initialize-Smoke }
    Invoke-SmokeStep 2 { Start-SmokeRuntime }
    Invoke-SmokeStep 3 { Test-SmokeSeed }
    Invoke-SmokeStep 4 { New-SmokeShop }
    Invoke-SmokeStep 5 { Start-SmokeSync }
    Invoke-SmokeStep 6 { Test-SmokeData }
    Invoke-SmokeStep 7 { Merge-SmokeListings }
    Invoke-SmokeStep 8 { New-SmokeQuote }
    Invoke-SmokeStep 9 { New-SmokeLink }
    Invoke-SmokeStep 10 { Test-SmokeBrowse }
    Invoke-SmokeStep 11 { New-SmokeInquiry }
    Invoke-SmokeStep 12 { Test-SmokeDisclosure }
    Invoke-SmokeStep 13 { Revoke-SmokeLink }
    # Keep the existing cleanup step number; it always executes last in finally.
    Invoke-SmokeStep 15 { Test-SmokeOpen }
} catch {
    $exitCode = $script:Step
    if ($script:Results[$script:Step - 1].Result -ne 'FAIL') {
        $script:Results[$script:Step - 1].Result = 'FAIL'
        Write-Host "Step $script:Step failed during harness initialization" -ForegroundColor Red
    }
} finally {
    if (Get-Command Invoke-SmokeStep -ErrorAction SilentlyContinue) {
        try {
            Invoke-SmokeStep 14 {
                Clear-Smoke
                if (-not ($KeepArtifacts -and $exitCode -ne 0)) { Remove-SmokeArtifacts }
            }
        } catch { if ($exitCode -eq 0) { $exitCode = 14 } }
    }
    foreach ($row in $script:Results) {
        if ($row.Result -eq 'PASS') { $row.Reason = '' }
        elseif ($row.Result -eq 'SKIPPED') { $row.Reason = "stopped at first failure $exitCode" }
    }
    $script:Results | Format-Table Step, Name, Result, Seconds, Reason -AutoSize | Out-Host
    Write-Host ("Total: {0:N2}s; exit={1}; schema={2}" -f ([DateTime]::UtcNow - $script:Started).TotalSeconds, $exitCode, $script:Schema)
    if ($KeepArtifacts -and $exitCode -ne 0 -and (Get-Command Save-SmokeEvidence -ErrorAction SilentlyContinue)) {
        Save-SmokeEvidence
        Write-Host "Artifacts retained: $script:Artifacts"
    }
    foreach ($entry in $script:Processes) { $entry.Job.Dispose(); $entry.Process.Dispose() }
}
exit $exitCode
