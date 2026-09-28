param([switch]$PromptCredentials)
# OAuth-only diagnostic: does not call Gmail or send email.
$ErrorActionPreference = 'Stop'
function Read-SenderSetting([string]$Name) {
    if ($PromptCredentials) {
        $secure = Read-Host $Name -AsSecureString
        $pointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure)
        try {
            $entered = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($pointer).Trim()
            if ([string]::IsNullOrWhiteSpace($entered) -or $entered -match '\s' -or $entered.Contains('"') -or $entered.Contains("'")) {
                throw 'Expected a bare credential value.'
            }
            if ($Name -eq 'GMAIL_SENDER_CLIENT_ID' -and $entered -notmatch '^[A-Za-z0-9-]+\.apps\.googleusercontent\.com$') {
                throw 'Invalid client ID format.'
            }
            return $entered
        } finally {
            $entered = $null
            [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($pointer)
            $secure.Dispose()
        }
    }

    $value = [Environment]::GetEnvironmentVariable($Name, 'Process')
    $file = [Environment]::GetEnvironmentVariable($Name + '_FILE', 'Process')
    if ($value -and $file) { throw 'Conflicting sources.' }
    if ($file) {
        $item = Get-Item -LiteralPath $file
        if ($item.PSIsContainer -or $item.Length -gt 16384) { throw 'Invalid file.' }
        $value = [IO.File]::ReadAllText($item.FullName).Trim()
    }
    if ([string]::IsNullOrWhiteSpace($value)) { throw 'Missing setting.' }
    return $value
}
$form = $null
$response = $null
try {
    if (-not $PromptCredentials -and ($env:AUTH_MAIL_MODE -ne 'gmail' -or $env:AUTH_MAIL_FROM -cne 'fullstackfilevault@gmail.com')) {
        Write-Output 'CONFIGURATION_ERROR: use gmail mode and the plain dedicated sender address.'
        return
    }
    try {
        $form = @{
            grant_type = 'refresh_token'
            client_id = (Read-SenderSetting 'GMAIL_SENDER_CLIENT_ID')
            client_secret = (Read-SenderSetting 'GMAIL_SENDER_CLIENT_SECRET')
            refresh_token = (Read-SenderSetting 'GMAIL_SENDER_REFRESH_TOKEN')
        }
    } catch {
        Write-Output 'CONFIGURATION_ERROR: missing, malformed or unreadable credentials, or conflicting sources.'
        return
    }
    try {
        # One bounded request. No redirects or automatic retries.
        $response = Invoke-RestMethod -Uri 'https://oauth2.googleapis.com/token' -Method Post -ContentType 'application/x-www-form-urlencoded' -Body $form -TimeoutSec 10 -MaximumRedirection 0 -Verbose:$false -Debug:$false
        if (-not $response.access_token) {
            Write-Output 'OAUTH_ERROR: no access token returned.'
            return
        }
        if ($response.scope) {
            $scopes = @($response.scope -split '\s+' | Where-Object { $_ })
            if ($scopes.Count -ne 1 -or $scopes[0] -cne 'https://www.googleapis.com/auth/gmail.send') {
                Write-Output 'SCOPE_MISMATCH: grant is not exactly gmail.send; stop and reauthorize the dedicated client.'
                return
            }
            Write-Output 'OAUTH_OK: refresh succeeded; returned scope is gmail.send only.'
        } else {
            Write-Output 'OAUTH_OK: refresh succeeded; scope omitted. Check original consent privately.'
        }
        Write-Output 'No email sent. Mailbox identity, Gmail API enablement and delivery remain unverified.'
    } catch {
        $failure = $_
        $category = ''
        $httpStatus = 0
        $networkStatus = ''
        $exception = $failure.Exception
        for ($depth = 0; $depth -lt 5 -and $exception; $depth++) {
            if ($exception -is [System.Net.WebException]) {
                $networkStatus = [string]$exception.Status
                if ($exception.Response) { $httpStatus = [int]$exception.Response.StatusCode }
                break
            }
            $exception = $exception.InnerException
        }
        try {
            $details = $failure.ErrorDetails.Message
            if (-not $details -and $exception -is [System.Net.WebException] -and $exception.Response) {
                # Windows PowerShell 5.1 can leave ErrorDetails empty.
                # Read at most 16 KiB privately; never emit provider text.
                $stream = $exception.Response.GetResponseStream()
                if ($stream) {
                    $reader = New-Object System.IO.StreamReader($stream)
                    try {
                        if ($stream.CanTimeout) { $stream.ReadTimeout = 2000 }
                        $buffer = New-Object char[] 16384
                        $count = $reader.Read($buffer, 0, $buffer.Length)
                        $details = -join $buffer[0..([Math]::Max(0, $count - 1))]
                    } finally {
                        $reader.Dispose()
                        if ($buffer) { [Array]::Clear($buffer, 0, $buffer.Length) }
                    }
                }
            }
            if ($details -and $details.Length -le 16384) {
                $parsed = $details | ConvertFrom-Json
                $category = [string]$parsed.error
            }
        } catch { $category = '' }
        switch ($category) {
            'invalid_client' { Write-Output 'OAUTH_INVALID_CLIENT: incorrect client ID/secret or unavailable client.' }
            'invalid_grant' { Write-Output 'OAUTH_INVALID_GRANT: token expired, revoked, invalid, or issued to another client.' }
            'unauthorized_client' { Write-Output 'OAUTH_UNAUTHORIZED_CLIENT: client cannot use this refresh grant.' }
            'invalid_scope' { Write-Output 'OAUTH_INVALID_SCOPE: authorization scope rejected.' }
            default {
                if ($httpStatus -gt 0) {
                    Write-Output ("OAUTH_HTTP_REJECTED: HTTP " + $httpStatus + "; provider reason unavailable. No retry performed.")
                } else {
                    switch ($networkStatus) {
                        'NameResolutionFailure' { Write-Output 'OAUTH_DNS_FAILED: token endpoint name could not be resolved.' }
                        'ProxyNameResolutionFailure' { Write-Output 'OAUTH_PROXY_DNS_FAILED: configured proxy could not be resolved.' }
                        'ConnectFailure' { Write-Output 'OAUTH_CONNECT_FAILED: connection to the token endpoint or proxy failed.' }
                        'Timeout' { Write-Output 'OAUTH_TIMEOUT: token request exceeded its deadline.' }
                        'TrustFailure' { Write-Output 'OAUTH_TLS_TRUST_FAILED: certificate validation failed; do not disable verification.' }
                        'SecureChannelFailure' { Write-Output 'OAUTH_TLS_FAILED: secure channel could not be established.' }
                        default { Write-Output 'OAUTH_CHECK_FAILED: unclassified local or transport failure. No retry performed.' }
                    }
                }
            }
        }
    }
} finally {
    if ($form) { $form.Clear() }
    $response = $null
    $details = $null
    $parsed = $null
    $failure = $null
    $exception = $null
    # Provider error records may retain details; clear this diagnostic session's error history.
    $Error.Clear()
    Remove-Item Function:\Read-SenderSetting -ErrorAction SilentlyContinue
}
