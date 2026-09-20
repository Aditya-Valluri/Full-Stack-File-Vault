# Prepare isolated, ignored credentials for the local production-mode rehearsal.
# Never use this self-signed localhost certificate for an Internet deployment.
$ErrorActionPreference = 'Stop'
$repository = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$directory = Join-Path $repository '.secrets/application'
[IO.Directory]::CreateDirectory($directory) | Out-Null
$encoding = New-Object System.Text.UTF8Encoding($false)
function Save-NewSecret([string]$name, [string]$value) {
    $path = Join-Path $directory $name
    if (Test-Path -LiteralPath $path) {
        if ([IO.File]::ReadAllText($path).Trim() -ne $value) { throw "Existing $name differs; credentials were not rotated." }
    } else { [IO.File]::WriteAllText($path, $value, $encoding) }
}
function Get-Password([string]$name) {
    $path = Join-Path $directory $name
    if (Test-Path -LiteralPath $path) { $value = [IO.File]::ReadAllText($path).Trim() }
    else {
        $bytes = New-Object byte[] 32
        $random = [Security.Cryptography.RandomNumberGenerator]::Create()
        try { $random.GetBytes($bytes) } finally { $random.Dispose() }
        $value = [BitConverter]::ToString($bytes).Replace('-', '').ToLowerInvariant()
        Save-NewSecret $name $value
    }
    if ($value -notmatch '^[a-f0-9]{64}$') { throw 'Invalid generated credential format.' }
    return $value
}
$operatorPassword = Get-Password 'postgres-password'
$runtimePassword = Get-Password 'runtime-password'
$collectorPassword = Get-Password 'gc-password'
try {
    Save-NewSecret 'operator-url' "postgres://vault_operator:${operatorPassword}@postgres:5432/vault?sslmode=disable"
    Save-NewSecret 'runtime-url' "postgres://vault_runtime:${runtimePassword}@postgres:5432/vault?sslmode=disable"
    Save-NewSecret 'gc-url' "postgres://vault_gc:${collectorPassword}@postgres:5432/vault?sslmode=disable"
} finally { Remove-Variable operatorPassword, runtimePassword, collectorPassword -ErrorAction SilentlyContinue }
$certificate = Join-Path $directory 'tls.crt'
$key = Join-Path $directory 'tls.key'
if ((Test-Path -LiteralPath $certificate) -xor (Test-Path -LiteralPath $key)) { throw 'Incomplete certificate pair; inspect it before continuing.' }
if (-not (Test-Path -LiteralPath $certificate)) {
    docker run --rm --mount "type=bind,source=$directory,target=/certs" golang:1.27.1-bookworm sh -c 'openssl req -x509 -newkey rsa:3072 -nodes -days 7 -keyout /certs/tls.key -out /certs/tls.crt -subj /CN=localhost -addext "subjectAltName=DNS:localhost,IP:127.0.0.1" 2>/dev/null'
    if ($LASTEXITCODE -ne 0) { throw 'Local certificate creation failed.' }
}
docker run --rm --mount "type=bind,source=$directory,target=/certs" golang:1.27.1-bookworm sh -c 'chown 101:101 /certs/tls.key && chmod 400 /certs/tls.key'
if ($LASTEXITCODE -ne 0) { throw 'Local TLS key ownership setup failed.' }
Write-Host 'Local application credentials and localhost certificate are ready. No existing credentials were rotated.'
