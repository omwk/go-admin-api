# Explicit capacity run; no product configuration is changed. PowerShell 5.1.
param(
    [string]$DatabaseUrl,
    [string]$RedisAddr,
    [ValidateRange(100, 100000)][int]$Samples = 2000,
    [string]$Concurrency = '1,4,16,32',
    [switch]$Billing,
    [string]$Output = (Join-Path ([IO.Path]::GetTempPath()) 'm8c-gateway-benchmark.json')
)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 2.0
$script:Root = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
$script:Started = [DateTime]::UtcNow
$StepTimeoutSeconds = 30; $TotalTimeoutSeconds = 600; $KeepArtifacts = $false; $exitCode = 0
$script:Step = 1
$script:Processes = New-Object 'Collections.Generic.List[object]'
$script:Secrets = New-Object 'Collections.Generic.List[string]'
$script:HTTP = New-Object 'Collections.Generic.List[object]'
$script:ChildEnv = @{}
$script:SchemaCreated = $false; $script:RedisClaimed = $false
$script:Artifacts = Join-Path ([IO.Path]::GetTempPath()) ('s3-smoke-' + [Guid]::NewGuid().ToString('N'))
$script:Schema = 's3_smoke_' + [Guid]::NewGuid().ToString('N')
$script:Results = @(1..14 | ForEach-Object { [pscustomobject]@{ Name = 'Benchmark setup/cleanup'; Result = ''; Reason = ''; Seconds = 0 } })
try {
    New-Item -ItemType Directory -Path $script:Artifacts | Out-Null
    . (Join-Path $script:Root 'scripts/smoke/support.ps1')
    . (Join-Path $script:Root 'scripts/smoke/process.ps1')
    . (Join-Path $script:Root 'scripts/smoke/setup.ps1')
    Invoke-SmokeStep 1 { Initialize-Smoke }
    if (-not $DatabaseUrl) { $DatabaseUrl = Read-SmokeSetting 'TEST_DATABASE_URL' }
    $report = [IO.Path]::GetFullPath($Output)
    Assert-Smoke (Test-Path -LiteralPath (Split-Path -Parent $report)) 'benchmark output parent must exist'
    $script:ChildEnv.TEST_DATABASE_URL = $DatabaseUrl
    $script:ChildEnv.CH_GATEWAY_BENCHMARK = '1'
    $script:ChildEnv.CH_GATEWAY_BENCHMARK_SAMPLES = [string]$Samples
    $script:ChildEnv.CH_GATEWAY_BENCHMARK_CONCURRENCY = $Concurrency
    $script:ChildEnv.CH_GATEWAY_BENCHMARK_OUTPUT = $report
    $testName = '^TestGatewayOverhead$'
    if ($Billing) { $testName = '^TestBillingCapacity$' }
    $script:Deadline = $script:Started.AddSeconds(570)
    $go = (Get-Command go).Source
    $text = Invoke-SmokeNative $go @('test', '-tags', 'gatewaybench', './internal/bootstrap', '-run', $testName, '-count=1', '-v', '-timeout', '8m') 'gateway capacity test'
    $text | Write-Host
    Assert-Smoke (Test-Path -LiteralPath $report) 'capacity run did not produce measurements'
    $data = Get-Content -LiteralPath $report -Raw | ConvertFrom-Json
    Assert-Smoke ((Get-Item -LiteralPath $report).LastWriteTimeUtc -ge $script:Started) 'capacity result is stale'
    Assert-Smoke ($data.samples_per_level -eq $Samples) 'capacity sample count mismatch'
    if ($Billing) {
        Assert-Smoke (@($data.scenarios).Count -eq 2) 'billing result must contain cash and entitlement scenarios'
        foreach ($scenario in $data.scenarios) {
            Assert-Smoke ($scenario.closed.errors -eq 0 -and $scenario.fixed.errors -eq 0) 'billing capacity result contains HTTP failures'
            Assert-Smoke ($scenario.fixed.target_qps -eq 50) 'billing fixed arrival rate mismatch'
        }
    } else {
        Assert-Smoke (@($data.levels).Count -gt 0) 'capacity result has no concurrency levels'
        Assert-Smoke ((@($data.levels | Where-Object { $_.errors -ne 0 })).Count -eq 0) 'capacity result contains HTTP failures'
    }
    Write-Host "PASS: capacity measurements recorded: $report"
} catch {
    $exitCode = 1
    foreach ($entry in @($script:Processes | Where-Object { $_.Label -eq 'gateway capacity test' })) {
        $entry.Out.GetAwaiter().GetResult() | Write-Host
    }
    Write-Host 'FAIL: capacity run; check infrastructure, sample bounds and opt-in test' -ForegroundColor Red
} finally {
    if (Get-Command Clear-Smoke -ErrorAction SilentlyContinue) {
        try { Invoke-SmokeStep 14 { Clear-Smoke; Remove-SmokeArtifacts } } catch { $exitCode = 1 }
    }
    foreach ($entry in $script:Processes) { $entry.Job.Dispose(); $entry.Process.Dispose() }
}
exit $exitCode
