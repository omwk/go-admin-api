function Read-SmokeSetting([string]$Name, [string]$Default = '') {
    $value = [Environment]::GetEnvironmentVariable($Name)
    if ($value) { return $value }
    $file = Join-Path $script:Root '.env'
    if (Test-Path -LiteralPath $file) {
        foreach ($line in [IO.File]::ReadAllLines($file)) {
            $text = $line.Trim()
            if ($text.StartsWith('#') -or -not $text.Contains('=')) { continue }
            $parts = $text -split '=', 2
            if ($parts[0].Trim() -eq $Name) { return $parts[1].Trim().Trim('"').Trim("'") }
        }
    }
    return $Default
}

function Assert-Smoke([bool]$Condition, [string]$Message) {
    if (-not $Condition) { throw $Message }
}

function Get-SmokeRemaining {
    $remaining = [int][Math]::Floor(($script:Deadline - [DateTime]::UtcNow).TotalMilliseconds)
    if ($remaining -le 0) { throw 'step or overall deadline exceeded' }
    return $remaining
}

function Wait-Smoke([scriptblock]$Check, [string]$Message) {
    while ($true) {
        if (& $Check) { return }
        $remaining = Get-SmokeRemaining
        if ($remaining -le 500) { throw $Message }
        Start-Sleep -Milliseconds 500
    }
}

function Invoke-SmokeHelper([string]$Action, $InputObject = $null) {
    $inputText = ''
    if ($null -ne $InputObject) { $inputText = ConvertTo-Json -InputObject $InputObject -Depth 30 -Compress }
    $raw = Invoke-SmokeNative $script:Helper @($Action) "helper $Action" $inputText
    return ($raw | ConvertFrom-Json)
}

function Invoke-SmokeSQL([string]$SQL, [object[]]$Values = @()) {
    return (Invoke-SmokeHelper 'sql' @{ sql = $SQL; args = @($Values) })
}

function Invoke-SmokeHTTP([string]$Method, [string]$Path, $Body = $null,
    [int]$Expected = 200, [bool]$Anonymous = $false, [bool]$Browser = $false, [int]$Code = 0,
    [string]$Label = '') {
    $headers = @{ 'Content-Type' = 'application/json' }
    if (-not $Anonymous -and $script:AccessToken) { $headers.Authorization = 'Bearer ' + $script:AccessToken }
    if ($Method -ne 'GET') { $headers['Idempotency-Key'] = [Guid]::NewGuid().ToString('N') }
    if ($Browser -and $script:BrowserCookie) { $headers.Cookie = $script:BrowserCookie }
    if ($Path.EndsWith('/inquiry')) { $headers.Origin = $script:BaseURL; $headers['Sec-Fetch-Site'] = 'same-origin' }
    $inputObject = @{ Method = $Method; URL = $script:BaseURL + $Path; Headers = $headers; Body = $Body }
    $result = Invoke-SmokeHelper 'request' $inputObject
    if (-not $Label) { $Label = "$Method request" }
    $record = [pscustomobject]@{ step = $script:Step; label = $Label; status = $result.status; request_id = $result.request_id }
    $script:HTTP.Add($record)
    Write-Host ("  {0}: HTTP {1} request_id={2}" -f $Label, $result.status, $result.request_id)
    Assert-Smoke ($result.status -eq $Expected) "$Label expected HTTP $Expected, got $($result.status) (request_id=$($result.request_id))"
    Assert-Smoke (-not [string]::IsNullOrWhiteSpace($result.request_id)) "$Label missing response request_id"
    if ($Expected -ne 204) { Assert-Smoke ($result.body.code -eq $Code) "$Label unexpected business code $($result.body.code)" }
    return $result
}

function Set-SmokeCookie($Response) {
    $cookies = @($Response.cookies | Where-Object { $_.Name -eq 'share_access' })
    Assert-Smoke ($cookies.Count -eq 1) 'expected one browser cookie'
    $cookie = $cookies[0]
    Assert-Smoke ($cookie.Secure -and $cookie.HttpOnly -and $cookie.Path -eq '/api/v1/share') 'browser cookie security/path mismatch'
    $script:BrowserCookie = 'share_access=' + $cookie.Value
    $script:Secrets.Add($cookie.Value)
}

function Set-SmokeLogin {
    $r = Invoke-SmokeHTTP 'POST' '/api/v1/auth/login' @{ username = 'admin'; password = $script:Password } -Anonymous $true -Label 'admin login'
    Assert-Smoke (-not [string]::IsNullOrWhiteSpace($r.body.data.access_token)) 'login returned no access token'
    $script:AccessToken = $r.body.data.access_token
    $script:Secrets.Add($script:AccessToken)
    $script:Secrets.Add($r.body.data.refresh_token)
}

function Invoke-SmokeStep([int]$Number, [scriptblock]$Action) {
    $script:Step = $Number
    $row = $script:Results[$Number - 1]
    $clock = [Diagnostics.Stopwatch]::StartNew()
    $script:Deadline = [DateTime]::UtcNow.AddSeconds($StepTimeoutSeconds)
    $limit = $script:Started.AddSeconds($TotalTimeoutSeconds - 15)
    if ($Number -eq 14) { $limit = [DateTime]::UtcNow.AddSeconds(14) }
    if ($script:Deadline -gt $limit) { $script:Deadline = $limit }
    Write-Host ("Step {0} ({1})" -f $Number, $row.Name)
    try {
        Get-SmokeRemaining | Out-Null
        & $Action | Out-Null
        Get-SmokeRemaining | Out-Null
        $row.Result = 'PASS'
    } catch {
        $row.Result = 'FAIL'
        $row.Reason = $_.Exception.Message
        Write-Host ("Step {0} ({1}) failed: {2}" -f $Number, $row.Name, $row.Reason) -ForegroundColor Red
        throw
    } finally { $clock.Stop(); $row.Seconds = [Math]::Round($clock.Elapsed.TotalSeconds, 2) }
}

function Stop-SmokeRuntime {
    foreach ($entry in $script:Processes) { Stop-SmokeProcess $entry }
    Assert-Smoke (@($script:Processes | Where-Object { -not $_.Process.HasExited }).Count -eq 0) 'child process remains alive'
}

function Remove-SmokeArtifacts {
    if (-not (Test-Path -LiteralPath $script:Artifacts)) { return }
    $resolved = [IO.Path]::GetFullPath($script:Artifacts)
    $tempPrefix = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\') + '\s3-smoke-'
    if (-not $resolved.StartsWith($tempPrefix, [StringComparison]::OrdinalIgnoreCase)) {
        throw 'refusing artifact deletion outside owned temp directory'
    }
    Wait-Smoke {
        try {
            Remove-Item -LiteralPath $resolved -Recurse -Force
            return (-not (Test-Path -LiteralPath $resolved))
        } catch { return $false }
    } 'temporary artifacts could not be removed before cleanup deadline'
}

function Save-SmokeEvidence {
    foreach ($entry in $script:Processes) {
        $text = $entry.Out.GetAwaiter().GetResult() + $entry.Err.GetAwaiter().GetResult()
        foreach ($secret in $script:Secrets) { if ($secret) { $text = $text.Replace($secret, '[REDACTED]') } }
        $text = $text -replace '/api/v1/share/[A-Za-z0-9_-]+', '/api/v1/share/[REDACTED]'
        # Only long-lived process output belongs in artifacts, never helper JSON.
        if ($entry.Label -in @('api', 'worker', 'tls')) {
            [IO.File]::WriteAllText((Join-Path $script:Artifacts ($entry.Label + '.log')), $text)
        }
    }
    $script:Results | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath (Join-Path $script:Artifacts 'summary.json') -Encoding ASCII
    $script:HTTP | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath (Join-Path $script:Artifacts 'http.json') -Encoding ASCII
}
