# Real gateway lifecycle, executable examples, and metering in the owned S3 schema.
function New-SmokeOpenClient {
    $r = Invoke-SmokeHTTP 'POST' '/api/v1/admin/api-clients' @{ name = 'M8c drill' } -Expected 201 -Label 'create open application'
    $script:OpenClient = $r.body.data.client
    $script:OpenKey = $r.body.data.key
    $script:Secrets.Add($script:OpenKey.secret)
    $clientID = $script:OpenClient.id
    $r = Invoke-SmokeHTTP 'POST' "/api/v1/admin/api-clients/$clientID/routes" @{ route_id = $script:OpenRouteID; status = 'active' } -Label 'authorize open route'
    $r = Invoke-SmokeHTTP 'POST' "/api/v1/admin/api-clients/$clientID/orders" @{ order_id = $script:OpenOrderID } -Expected 201 -Label 'authorize open order'
    $script:OpenPath = '/open/v1/orders/' + $r.body.data.public_id + '/status'
}

function Invoke-SmokeOpen([string]$Path, [string]$Secret, [int]$Expected = 200, [int]$Code = 0, [string]$Label = 'signed open call') {
    $result = Invoke-SmokeHelper 'request' @{ Method = 'GET'; URL = $script:BaseURL + $Path;
        AppID = $script:OpenClient.app_id; Secret = $Secret; Headers = @{} }
    $script:HTTP.Add([pscustomobject]@{ step = $script:Step; label = $Label; status = $result.status; request_id = $result.request_id })
    Write-Host ("  {0}: HTTP {1} code={2} request_id={3}" -f $Label, $result.status, $result.body.code, $result.request_id)
    Assert-Smoke ($result.status -eq $Expected -and $result.body.code -eq $Code) "$Label response status/code mismatch"
    Assert-Smoke (-not [string]::IsNullOrWhiteSpace($result.request_id)) "$Label missing request_id"
    return $result
}

function Test-SmokeOpen {
    $r = Invoke-SmokeHTTP 'POST' '/api/v1/admin/api-routes' @{ name = 'M8c order status'; code = 'order.status';
        capability = 'order.status'; method = 'GET'; path = '/open/v1/orders/:id/status'; route_group = 'orders';
        visibility = 'public'; status = 'enabled'; billing_mode = 'free' } -Expected 201 -Label 'register open capability'
    $script:OpenRouteID = $r.body.data.id
    $row = Invoke-SmokeSQL 'SELECT jsonb_build_object(''id'',id) FROM sales_order WHERE shop_id=$1 ORDER BY id LIMIT 1' @($script:ShopID)
    $script:OpenOrderID = $row.id
    New-SmokeOpenClient
    $r = Invoke-SmokeOpen $script:OpenPath $script:OpenKey.secret
    Assert-Smoke (-not [string]::IsNullOrWhiteSpace($r.call_id)) 'open request has no server call_id'
    $persisted = Invoke-SmokeSQL @'
SELECT jsonb_build_object('meters',(SELECT count(*) FROM api_call_meter WHERE call_id=$1),
'logs',(SELECT count(*) FROM api_call_log WHERE call_id=$1 AND state='succeeded' AND metered_units=1 AND NOT billed))
'@ @($r.call_id)
    Assert-Smoke ($persisted.meters -eq 1 -and $persisted.logs -eq 1) 'successful open call lacks one durable log/meter'
    Test-SmokeSigningExamples
    $usage = Invoke-SmokeOpen '/open/v1/usage' $script:OpenKey.secret -Label 'query real usage'
    Assert-Smoke ($usage.body.data.calls -eq 3 -and $usage.body.data.metered_calls -eq 3 -and $usage.body.data.processing_calls -eq 0) 'examples/usage do not match three successful logical calls'
    switch ($GatewayDrill) {
        'Rotation' { Test-SmokeKeyRotation }
        'Revocation' { Test-SmokeKeyRevocation }
        'Quota' { Test-SmokeQuotas }
    }
    Write-Host '  PASS: open signature -> HTTP 200 -> durable meter/log -> usage; bash and Go examples verified'
}

function Test-SmokeSigningExamples {
    $script:ChildEnv.CH_OPEN_BASE_URL = $script:ChildEnv.SMOKE_BACKEND
    $script:ChildEnv.CH_OPEN_APP_ID = $script:OpenClient.app_id
    $script:ChildEnv.CH_OPEN_SECRET = $script:OpenKey.secret
    $script:ChildEnv.CH_OPEN_PATH = $script:OpenPath
    $go = (Get-Command go).Source
    $exe = Join-Path $script:Artifacts 'open-example.exe'
    Invoke-SmokeNative $go @('build', '-o', $exe, './scripts/gateway/example') 'build Go signing example' | Out-Null
    $response = Invoke-SmokeNative $exe @() 'Go signing example' | ConvertFrom-Json
    Assert-Smoke ($response.code -eq 0 -and $response.data.id -eq ($script:OpenPath -split '/')[4]) 'Go example signature failed'
    $bash = Join-Path $env:ProgramFiles 'Git/bin/bash.exe'
    if (-not (Test-Path -LiteralPath $bash)) {
        $bash = (Get-Command bash -ErrorAction Stop).Source
    }
    $response = Invoke-SmokeNative $bash @('scripts/gateway/examples/order-status.sh') 'bash curl signing example' | ConvertFrom-Json
    Assert-Smoke ($response.code -eq 0 -and $response.data.id -eq ($script:OpenPath -split '/')[4]) 'bash example signature failed'
    $script:ChildEnv.Remove('CH_OPEN_SECRET')
}

function New-SmokeRotatedKey([int]$Overlap = 5) {
    $r = Invoke-SmokeHTTP 'POST' "/api/v1/admin/api-clients/$($script:OpenClient.id)/secrets" @{ overlap_seconds = $Overlap } -Expected 201 -Label 'rotate open key'
    $script:Secrets.Add($r.body.data.secret)
    return $r.body.data
}

function Test-SmokeKeyRotation {
    $replacement = New-SmokeRotatedKey
    Invoke-SmokeOpen $script:OpenPath $script:OpenKey.secret -Label 'old key inside overlap' | Out-Null
    Invoke-SmokeOpen $script:OpenPath $replacement.secret -Label 'new key inside overlap' | Out-Null
    $keys = Invoke-SmokeHTTP 'GET' "/api/v1/admin/api-clients/$($script:OpenClient.id)/secrets" -Label 'read key expiration metadata'
    $old = @($keys.body.data | Where-Object { $_.id -eq $script:OpenKey.id })
    Assert-Smoke ($old.Count -eq 1 -and $old[0].expires_at) 'old key expiration not set'
    $expires = [DateTimeOffset]::Parse($old[0].expires_at).UtcDateTime
    Wait-Smoke { [DateTime]::UtcNow -gt $expires.AddMilliseconds(100) } 'rotation window did not expire'
    Invoke-SmokeOpen $script:OpenPath $script:OpenKey.secret -Expected 401 -Code 80102 -Label 'old key after overlap' | Out-Null
    Invoke-SmokeOpen $script:OpenPath $replacement.secret -Label 'new key after overlap' | Out-Null
    Write-Host '  PASS: dual-key rotation, both keys inside window; old=401/new=200 after window'
}

function Test-SmokeKeyRevocation {
    $replacement = New-SmokeRotatedKey 60
    Invoke-SmokeOpen $script:OpenPath $script:OpenKey.secret -Label 'warm compromised key' | Out-Null
    Invoke-SmokeHTTP 'DELETE' "/api/v1/admin/api-clients/$($script:OpenClient.id)/secrets/$($script:OpenKey.id)" -Label 'revoke compromised key' | Out-Null
    # No polling or retry: this is the first request after the committed revoke.
    Invoke-SmokeOpen $script:OpenPath $script:OpenKey.secret -Expected 401 -Code 80102 -Label 'first request after revoke' | Out-Null
    Invoke-SmokeOpen $script:OpenPath $replacement.secret -Label 'replacement after revoke' | Out-Null
    Write-Host '  PASS: leak response, first post-revocation request=401; replacement=200'
}

function Test-SmokeQuotas {
    foreach ($kind in @('daily_quota', 'monthly_quota')) {
        New-SmokeOpenClient
        $daily = 0; $monthly = 0
        if ($kind -eq 'daily_quota') { $daily = 2 } else { $monthly = 2 }
        Invoke-SmokeHTTP 'POST' "/api/v1/admin/api-clients/$($script:OpenClient.id)/routes" @{
            route_id = $script:OpenRouteID; status = 'active'; quota_daily = $daily; quota_monthly = $monthly
        } -Label "set $kind" | Out-Null
        1..2 | ForEach-Object { Invoke-SmokeOpen $script:OpenPath $script:OpenKey.secret -Label "$kind admitted call" | Out-Null }
        $before = [DateTime]::UtcNow
        $r = Invoke-SmokeOpen $script:OpenPath $script:OpenKey.secret -Expected 429 -Code 10005 -Label "$kind exhausted"
        Assert-Smoke ($r.body.details.limit_type -eq $kind) 'quota limit_type mismatch'
        $reset = $before.Date.AddDays(1)
        if ($kind -eq 'monthly_quota') { $reset = (New-Object DateTime $before.Year, $before.Month, 1).AddMonths(1) }
        $seconds = [int]$r.retry_after
        Assert-Smoke ($seconds -gt 0 -and [Math]::Abs($before.AddSeconds($seconds).Subtract($reset).TotalSeconds) -le 3) 'quota Retry-After does not identify UTC reset'
        $row = Invoke-SmokeSQL 'SELECT jsonb_build_object(''min'',min(admitted_count),''max'',max(admitted_count)) FROM api_quota WHERE client_id=$1' @($script:OpenClient.id)
        Assert-Smoke ($row.min -eq 2 -and $row.max -eq 2) 'rejected quota request consumed admission'
        $usage = Invoke-SmokeOpen '/open/v1/usage' $script:OpenKey.secret -Label "$kind usage"
        Assert-Smoke ($usage.body.data.calls -eq 3 -and $usage.body.data.metered_calls -eq 2) 'quota denial incorrectly metered'
        Write-Host ("  PASS: {0} HTTP=429 code=10005 limit_type={0} reset_at={1} Retry-After={2}" -f $kind, $reset.ToString("yyyy-MM-ddTHH:mm:ss'Z'"), $seconds)
    }
}
