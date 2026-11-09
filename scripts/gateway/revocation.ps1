# Real leak-response drill; the first post-commit request must be rejected.
param([string]$DatabaseUrl, [string]$RedisAddr)
& (Join-Path $PSScriptRoot '../smoke.ps1') -DatabaseUrl $DatabaseUrl -RedisAddr $RedisAddr -GatewayDrill Revocation
exit $LASTEXITCODE
