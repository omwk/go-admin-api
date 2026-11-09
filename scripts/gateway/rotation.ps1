# Real short-window rotation drill; all fixtures are isolated and cleaned.
param([string]$DatabaseUrl, [string]$RedisAddr)
& (Join-Path $PSScriptRoot '../smoke.ps1') -DatabaseUrl $DatabaseUrl -RedisAddr $RedisAddr -GatewayDrill Rotation
exit $LASTEXITCODE
