# Keep this script ASCII-only for Windows PowerShell 5.1.
# No Go toolchain, make, or external commands are needed.
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$bootstrap = './internal/bootstrap'
$cli = './cmd/cli'

# Hand-maintained exceptions only. Every addition needs a reviewable reason.
$exclusions = @(
    [pscustomobject]@{
        Package = $bootstrap
        Name = 'TestAuthzIntegration2000UserBenchmark'
        Tag = 'authzbench'
        Reason = 'Requires real Redis and a 2000-user load; capacity measurement, excluded from PR CI.'
    }
)

# This is the single source of truth for the CI matrix and its commands.
$groups = @(
    [pscustomobject]@{
        Name = 'core'
        Pattern = '^Test(Identity|Shop|Credential|Auth|Session|Idempotency|File|Worker|Outbox|Admin|Profile|Seed|Scope|M2Operator)'
        Packages = @($bootstrap, $cli)
    }
    [pscustomobject]@{
        Name = 'catalog'
        Pattern = '^Test(Catalog|Product|Category|Listing|Inventory)'
        Packages = @($bootstrap)
    }
    [pscustomobject]@{
        Name = 'sync'
        Pattern = '^Test(Sync|Drift|Status|Orchestrat|Resolve|Page|Ingestion|Raw|Mapping)'
        Packages = @($bootstrap)
    }
    [pscustomobject]@{
        Name = 'replay'
        Pattern = '^Test(Replay|Grant|Pin)'
        Packages = @($bootstrap)
    }
    [pscustomobject]@{
        Name = 'export'
        Pattern = '^Test(Export|Admission|Retention)'
        Packages = @($bootstrap)
    }
    [pscustomobject]@{
        Name = 'order'
        Pattern = '^TestOrder'
        Packages = @($bootstrap)
    }
)

$tests = @(foreach ($package in @($bootstrap, $cli)) {
    $directory = Join-Path $root $package
    foreach ($file in Get-ChildItem -LiteralPath $directory -Filter '*_test.go' -File -Recurse | Sort-Object FullName) {
        $source = [IO.File]::ReadAllText($file.FullName)
        $testPackage = '.' + $file.DirectoryName.Substring($root.Length).Replace('\', '/')
        $declaration = '(?m)^\s*func\s+(Test\w*Integration\w*)\s*\(\s*\w+\s+\*testing\.T\s*\)'
        foreach ($match in [regex]::Matches($source, $declaration)) {
            [pscustomobject]@{ Name = $match.Groups[1].Value; Package = $testPackage }
        }
    }
})

$invalid = $false
$excludedCount = 0
foreach ($exclusion in $exclusions) {
    $entries = @($exclusions | Where-Object { $_.Package -ceq $exclusion.Package -and $_.Name -ceq $exclusion.Name })
    $found = @($tests | Where-Object { $_.Package -ceq $exclusion.Package -and $_.Name -ceq $exclusion.Name })
    if ($entries.Count -ne 1 -or $found.Count -ne 1 -or [string]::IsNullOrWhiteSpace($exclusion.Reason)) {
        Write-Host ("FAIL: exclusion {0} must name one existing test and include a reason." -f $exclusion.Name)
        $invalid = $true
    }
}
foreach ($test in $tests) {
    $excluded = @($exclusions | Where-Object { $_.Package -ceq $test.Package -and $_.Name -ceq $test.Name })
    $hits = @($groups | Where-Object { $test.Name -cmatch $_.Pattern })
    $names = @($hits | ForEach-Object { $_.Name }) -join ', '
    if ($hits.Count -gt 1) {
        Write-Host ("{0}: {1} -> {2} (duplicate coverage: {3})" -f $test.Package, $test.Name, $names, $hits.Count)
        $invalid = $true
    } elseif ($excluded.Count -eq 1) {
        # The generated commands explicitly skip these exact names, so no shard executes them.
        Write-Host ("{0}: {1} -> excluded (0 shards; {2}): {3}" -f $test.Package, $test.Name, $excluded[0].Tag, $excluded[0].Reason)
        $excludedCount++
    } elseif ($hits.Count -eq 0) {
        Write-Host ("{0}: {1} -> 0 (uncovered)" -f $test.Package, $test.Name)
        $invalid = $true
    } elseif ($hits[0].Packages -cnotcontains $test.Package) {
        Write-Host ("{0}: {1} -> {2} (package is not executed by this shard)" -f $test.Package, $test.Name, $names)
        $invalid = $true
    } else {
        Write-Host ("{0}: {1} -> {2}" -f $test.Package, $test.Name, $names)
    }
}

$sum = 0
$matrix = @(foreach ($group in $groups) {
    $groupCount = 0
    $commands = @(foreach ($package in $group.Packages) {
        $skipNames = @($exclusions | Where-Object { $_.Package -ceq $package } | ForEach-Object { [regex]::Escape($_.Name) })
        $skipPattern = if ($skipNames.Count -gt 0) { '^(' + ($skipNames -join '|') + ')$' } else { $null }
        $count = @($tests | Where-Object {
            $_.Package -ceq $package -and $_.Name -cmatch $group.Pattern -and
            (-not $skipPattern -or $_.Name -cnotmatch $skipPattern)
        }).Count
        $groupCount += $count
        $sum += $count
        $command = "go test $package -run '$($group.Pattern)' -count=1 -p 1"
        if ($skipPattern) { $command += " -skip '$skipPattern'" }
        Write-Host ("{0}: {1} integration tests; command: {2}" -f $group.Name, $count, $command)
        if ($package -ceq $cli) { Write-Host 'core shard additionally executes cmd/cli' }
        $command
    })
    Write-Host ("{0} shard total: {1} integration tests" -f $group.Name, $groupCount)
    if ($group.Name -ceq 'order' -and $groupCount -eq 0) {
        Write-Host 'FAIL: order shard must contain integration tests.'
        $invalid = $true
    }
    [ordered]@{ group = $group.Name; commands = $commands -join "`n" }
})

Write-Host ("Source {0} = shards {1} + excluded {2}" -f $tests.Count, $sum, $excludedCount)
if ($tests.Count -eq 0 -or ($sum + $excludedCount) -ne $tests.Count) {
    Write-Host 'FAIL: empty test inventory or reconciliation mismatch.'
    $invalid = $true
}
if ($invalid) {
    Write-Host 'FAIL: every integration test needs exactly one shard or an explicit exclusion.'
    exit 1
}

if ($env:GITHUB_OUTPUT) {
    $json = ConvertTo-Json -InputObject ([ordered]@{ include = $matrix }) -Depth 5 -Compress
    Add-Content -LiteralPath $env:GITHUB_OUTPUT -Value "matrix=$json" -Encoding UTF8
}
Write-Host 'PASS: every integration test has exactly one shard or an explicit exclusion.'
exit 0
