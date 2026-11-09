function Initialize-Smoke {
    if (-not $DatabaseUrl) { $DatabaseUrl = Read-SmokeSetting 'TEST_DATABASE_URL' }
    if (-not $RedisAddr) { $RedisAddr = Read-SmokeSetting 'CH_REDIS_ADDR' '127.0.0.1:6379' }
    Assert-Smoke (-not [string]::IsNullOrWhiteSpace($DatabaseUrl)) 'DatabaseUrl or TEST_DATABASE_URL in .env is required'
    $go = Get-Command go -ErrorAction SilentlyContinue
    if (-not $go) { throw 'Go toolchain required on PATH' }
    $script:Password = 'Sm0ke-' + [Guid]::NewGuid().ToString('N')
    $script:Credential = [Guid]::NewGuid().ToString('N')
    $key = New-Object byte[] 32
    $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
    try { $rng.GetBytes($key) } finally { $rng.Dispose() }
    $script:ChildEnv = @{
        SMOKE_SCHEMA = $script:Schema; SMOKE_DATABASE_URL = $DatabaseUrl; SMOKE_ARTIFACTS = $script:Artifacts;
        CH_DB_DSN = ''; CH_REDIS_ADDR = $RedisAddr; CH_REDIS_PASSWORD = (Read-SmokeSetting 'CH_REDIS_PASSWORD');
        CH_REDIS_DB = (Read-SmokeSetting 'CH_REDIS_DB' '0'); CH_CRYPTO_MASTER_KEY = [Convert]::ToBase64String($key);
        CH_CRYPTO_KEY_VERSION = '1'; CH_JWT_SECRET = [Guid]::NewGuid().ToString('N'); CH_APP_ENV = 'dev';
        CH_DB_TXN_TIMEOUT = '10s'; CH_DB_MIGRATE_TIMEOUT = '20s'; CH_DB_MAX_OPEN = '5'; CH_DB_MAX_IDLE = '2';
        CH_CACHE_OP_TIMEOUT = '2s'; CH_WORKER_CONCURRENCY = '2'; CH_WORKER_QUEUES = 'sync,refresh,billing,notify';
        CH_SEED_ADMIN_PASSWORD = $script:Password; SMOKE_CREDENTIAL = $script:Credential;
        CH_STORAGE_ENDPOINT = 'http://127.0.0.1:9'; CH_STORAGE_BUCKET = 's3-smoke'; CH_LOG_LEVEL = 'info'; CH_LOG_FORMAT = 'json'
    }
    foreach ($value in @($DatabaseUrl, $script:Password, $script:Credential, $script:ChildEnv.CH_CRYPTO_MASTER_KEY, $script:ChildEnv.CH_JWT_SECRET)) {
        $script:Secrets.Add($value)
    }
    foreach ($target in @('helper', 'api', 'worker', 'cli')) {
        $path = './cmd/' + $target
        if ($target -eq 'helper') { $path = './scripts/smoke' }
        Invoke-SmokeNative $go.Source @('build', '-o', (Join-Path $script:Artifacts ($target + '.exe')), $path) "build $target" | Out-Null
    }
    $script:Helper = Join-Path $script:Artifacts 'helper.exe'
    $script:SchemaCreated = $true
    $isolated = Invoke-SmokeHelper 'create'
    Assert-Smoke ($isolated.schema -eq $script:Schema) 'schema identity mismatch'
    $script:ChildEnv.CH_DB_DSN = $isolated.dsn
    $script:Secrets.Add($isolated.dsn)
    $claim = Invoke-SmokeHelper 'redis-reserve'
    $script:ChildEnv.CH_REDIS_DB = [string]$claim.db
    $script:RedisClaimed = $true
    Assert-Smoke ($claim.claimed -and $claim.db -gt 0) 'Redis logical DB was not exclusively reserved'
    $state = Invoke-SmokeHelper 'migrate'
    Assert-Smoke ($state.version -eq $state.latest -and $state.latest -gt 0 -and -not $state.dirty) 'migration version/latest/dirty mismatch'
    Write-Host "  Isolated schema=$script:Schema Redis DB=$($claim.db) migration=$($state.version) dirty=false (up/down/up)"
}

function Start-SmokeRuntime {
    $listener = New-Object Net.Sockets.TcpListener -ArgumentList ([Net.IPAddress]::Loopback), 0
    $listener.Start()
    try { $port = $listener.LocalEndpoint.Port } finally { $listener.Stop() }
    $script:ChildEnv.CH_APP_HTTP_ADDR = "127.0.0.1:$port"
    $script:ChildEnv.SMOKE_BACKEND = "http://127.0.0.1:$port"
    Invoke-SmokeNative (Join-Path $script:Artifacts 'cli.exe') @('seed') 'bootstrap seed' | Out-Null
    $script:API = Start-SmokeProcess (Join-Path $script:Artifacts 'api.exe') @() 'api'
    $script:Worker = Start-SmokeProcess (Join-Path $script:Artifacts 'worker.exe') @() 'worker'
    $script:ChildEnv.SMOKE_WORKER_PID = [string]$script:Worker.Process.Id
    $script:TLS = Start-SmokeProcess $script:Helper @('serve') 'tls'
    Wait-Smoke { Test-Path -LiteralPath (Join-Path $script:Artifacts 'tls.json') } 'TLS fixture did not listen'
    $script:BaseURL = (Get-Content -LiteralPath (Join-Path $script:Artifacts 'tls.json') -Raw | ConvertFrom-Json).url
    Wait-Smoke {
        Assert-Smoke (-not $script:API.Process.HasExited) 'API exited before ready'
        try { Set-SmokeLogin; return $true } catch {
            if ($script:HTTP.Count -gt 0) { throw }
            return $false
        }
    } 'API login did not become available'
    $ready = Invoke-SmokeHTTP 'GET' '/readyz' -Label 'API readyz'
    Assert-Smoke ($ready.body.status -eq 'ok' -and $ready.body.data.db -eq 'ok' -and $ready.body.data.redis -eq 'ok') 'API dependencies not ready'
    Wait-Smoke {
        Assert-Smoke (-not $script:Worker.Process.HasExited) 'worker exited before ready'
        $observed = Invoke-SmokeHelper 'worker'
        if (-not $observed.found) { return $false }
        Assert-Smoke ((@($observed.queues | Sort-Object) -join ',') -eq 'billing,notify,refresh,sync') 'worker registered unexpected queue set'
        Assert-Smoke $observed.refresh_reserved 'worker has no reserved credential refresh capacity'
        Assert-Smoke ($observed.status -eq 'active') 'worker is not active'
        return $true
    } 'Inspector.Servers() did not find the live worker within 30 seconds'
    Write-Host "  API port=$port worker PID=$($script:Worker.Process.Id) Inspector queues=billing,notify,refresh,sync"
}

function Test-SmokeSeed {
    Invoke-SmokeNative (Join-Path $script:Artifacts 'cli.exe') @('seed') 'idempotent seed' | Out-Null
    Set-SmokeLogin
    $row = Invoke-SmokeSQL "SELECT jsonb_build_object('admins',count(*)) FROM sys_user WHERE username='admin' AND tenant_id=1"
    Assert-Smoke ($row.admins -eq 1) 'seed did not create exactly one administrator'
}

function Clear-Smoke {
    $failures = New-Object 'Collections.Generic.List[string]'
    foreach ($entry in $script:Processes) {
        try { Stop-SmokeProcess $entry } catch { $failures.Add($_.Exception.Message) }
    }
    Assert-Smoke (@($script:Processes | Where-Object { -not $_.Process.HasExited }).Count -eq 0) 'owned process remains alive'
    if ($KeepArtifacts -and $exitCode -ne 0) {
        Write-Host "  Explicit KeepArtifacts: schema=$script:Schema Redis DB=$($script:ChildEnv.CH_REDIS_DB) retained; processes stopped"
        return
    }
    if ($script:RedisClaimed) {
        try {
            $r = Invoke-SmokeHelper 'redis-clean'
            Assert-Smoke ($r.remaining_keys -eq 0) 'Redis cleanup left keys'
        } catch { $failures.Add($_.Exception.Message) }
    }
    if ($script:SchemaCreated) {
        try {
            $r = Invoke-SmokeHelper 'drop'
            Assert-Smoke ($r.remaining_schemas -eq 0) 'schema cleanup left data'
        } catch { $failures.Add($_.Exception.Message) }
    }
    Assert-Smoke ($failures.Count -eq 0) ($failures -join '; ')
    Write-Host '  Owned processes stopped; remaining_schemas=0; remaining_redis_keys=0'
}
