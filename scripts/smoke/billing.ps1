# M9c: signed cash call, immutable funding/settlement, reconciliation and draft.
function Test-SmokeBilling {
    # A fresh app also isolates this step from optional key/quota drills.
    New-SmokeOpenClient
    $route = Invoke-SmokeSQL 'SELECT jsonb_build_object(''version'',version) FROM api_route WHERE id=$1' @($script:OpenRouteID)
    Invoke-SmokeHTTP 'PUT' "/api/v1/admin/api-routes/$script:OpenRouteID" @{
        name = 'M9c order status'; code = 'order.status'; capability = 'order.status'; method = 'GET';
        path = '/open/v1/orders/:id/status'; route_group = 'orders'; visibility = 'public'; status = 'enabled';
        billing_mode = 'per_call'; price_cents = 5; units_per_call = 1; currency = 'CNY'; version = $route.version
    } -Label 'price one call at five cents' | Out-Null
    $wallet = Invoke-SmokeHTTP 'POST' '/api/v1/admin/wallets' @{ app_id = $script:OpenClient.app_id } -Expected 201 -Label 'open wallet'
    Invoke-SmokeHTTP 'POST' "/api/v1/admin/wallets/$($wallet.body.data.id)/recharge" @{
        amount_cents = 100; note = 'S3 isolated funding'
    } -Expected 201 -Label 'fund wallet' | Out-Null
    $call = Invoke-SmokeHelper 'request' @{ Method = 'GET'; URL = $script:BaseURL + $script:OpenPath;
        AppID = $script:OpenClient.app_id; Secret = $script:OpenKey.secret;
        Headers = @{ 'Idempotency-Key' = [Guid]::NewGuid().ToString('N') } }
    $script:HTTP.Add([pscustomobject]@{ step = $script:Step; label = 'signed paid call'; status = $call.status; request_id = $call.request_id })
    Assert-Smoke ($call.status -eq 200 -and $call.body.code -eq 0 -and $call.call_id) 'paid call failed or missing receipt'
    $facts = Invoke-SmokeSQL @'
SELECT jsonb_build_object('balance',w.balance_cents,'frozen',w.frozen_cents,
'settles',(SELECT count(*) FROM wallet_txn WHERE call_id=$2 AND type='settle' AND amount_cents=5),
'meters',(SELECT count(*) FROM api_call_meter WHERE call_id=$2),
'day',(SELECT service_date::text FROM call_charge WHERE call_id=$2 AND state='settled'))
FROM wallet w WHERE w.app_id=$1
'@ @($script:OpenClient.app_id, $call.call_id)
    Assert-Smoke ($facts.balance -eq 95 -and $facts.frozen -eq 0 -and $facts.settles -eq 1 -and $facts.meters -eq 1) 'cash call did not settle exactly five cents once'
    $run = Invoke-SmokeHTTP 'POST' '/api/v1/admin/billing-reconciliations' @{ service_date = $facts.day } -Expected 201 -Label 'reconcile service day'
    Assert-Smoke ($run.body.data.status -eq 'completed' -and $run.body.data.differences -eq 0) 'reconciliation did not finish cleanly'
    $draft = Invoke-SmokeHTTP 'POST' '/api/v1/admin/billing-statements:generate' @{
        app_id = $script:OpenClient.app_id; month = $facts.day.Substring(0, 7)
    } -Expected 201 -Label 'generate monthly draft'
    Assert-Smoke ($draft.body.data.status -eq 'draft' -and $draft.body.data.total_cents -eq 5) 'draft total includes funding or omits consumption'
    $detail = Invoke-SmokeHTTP 'GET' "/api/v1/admin/billing-statements/$($draft.body.data.id)" -Label 'read draft evidence'
    $lines = @($detail.body.data.lines)
    $sum = ($lines | Measure-Object amount_cents -Sum).Sum
    Assert-Smoke ($lines.Count -eq 1 -and $sum -eq $detail.body.data.total_cents -and $lines[0].call_id -eq $call.call_id) 'statement lines do not equal total or link original call'
    Write-Host '  PASS: signed call -> five-cent settlement -> clean reconciliation -> draft total equals evidence'
}
