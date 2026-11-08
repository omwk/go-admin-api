# Execute against the public listener, with automatic redirects disabled.
# Examples: -BaseUrl http://127.0.0.1:18081
#           -BaseUrl https://quotes.example -ApplicationBaseUrl http://private-api:8080
param(
    [Parameter(Mandatory=$true)][string]$BaseUrl,
    [string]$ApplicationBaseUrl = ''
)
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Net.Http
$handler = New-Object System.Net.Http.HttpClientHandler
$handler.AllowAutoRedirect = $false
$client = New-Object System.Net.Http.HttpClient($handler)
$client.Timeout = [TimeSpan]::FromSeconds(10)
$paths = @('/healthz', '/readyz', '/metrics', '/api/v1/admin/system/info')
function Check-Listener([string]$address, [string]$label, [int[]]$allowed) {
    $uri = [Uri]$address
    if (-not $uri.IsAbsoluteUri -or $uri.Scheme -notin @('http','https') -or $uri.UserInfo -or $uri.Query -or $uri.Fragment -or $uri.AbsolutePath -ne '/') {
        throw 'Use a listener origin without credentials, path or query'
    }
    foreach ($path in $paths) {
        $request = New-Object System.Net.Http.HttpRequestMessage([System.Net.Http.HttpMethod]::Get, ($address.TrimEnd('/') + $path))
        $response = $null
        try {
            $response = $client.SendAsync($request).GetAwaiter().GetResult()
            $status = [int]$response.StatusCode
            Write-Output ("{0} GET {1} -> {2}" -f $label, $path, $status)
            if ($status -notin $allowed) { throw ("Unexpected anonymous exposure: {0} {1}" -f $label, $path) }
        } finally {
            if ($response) { $response.Dispose() }
            $request.Dispose()
        }
    }
}
try {
    Check-Listener $BaseUrl 'PUBLIC' @(403,404)
    if ($ApplicationBaseUrl) { Check-Listener $ApplicationBaseUrl 'APPLICATION' @(401,403) }
    Write-Output 'PASS: all four system routes reject anonymous access.'
} finally {
    $client.Dispose()
    $handler.Dispose()
}
