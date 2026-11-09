# Real daily/monthly N+1 admission drill with UTC reset validation.
param([string]$DatabaseUrl, [string]$RedisAddr)
& (Join-Path $PSScriptRoot '../smoke.ps1') -DatabaseUrl $DatabaseUrl -RedisAddr $RedisAddr -GatewayDrill Quota
exit $LASTEXITCODE
