# Local-only bootstrap. Never prints or imports administrative credentials.
$ErrorActionPreference = 'Stop'
Push-Location (Join-Path $PSScriptRoot '..')
try {
    $credentialFile = Join-Path (Get-Location).Path '.secrets/runtime-password'
    if (-not (Test-Path -LiteralPath $credentialFile)) {
        New-Item -ItemType Directory -Force '.secrets' | Out-Null
        $bytes = New-Object byte[] 32
        $rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
        try { $rng.GetBytes($bytes) } finally { $rng.Dispose() }
        $password = [BitConverter]::ToString($bytes).Replace('-', '').ToLowerInvariant()
        [System.IO.File]::WriteAllText($credentialFile, $password)
    }
    $password = [System.IO.File]::ReadAllText($credentialFile).Trim()
    if ($password -notmatch '^[a-f0-9]{64}$') { throw 'Invalid local runtime credential format.' }

    docker compose up -d --wait postgres
    if ($LASTEXITCODE -ne 0) { throw 'PostgreSQL startup failed.' }
    docker compose run --rm migrate
    if ($LASTEXITCODE -ne 0) { throw 'Migration failed.' }

    # Pass the secret through stdin, not a process argument. The container shell
    # reads configured DB/user values; psql quotes its password variable as SQL.
    $sql = "\set runtime_password $password`nALTER ROLE vault_runtime LOGIN PASSWORD :'runtime_password';"
    $sql | docker compose exec -T postgres sh -c 'exec psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"'
    if ($LASTEXITCODE -ne 0) { throw 'Runtime credential provisioning failed.' }

    # Resolve the non-secret configured database name without dumping Compose/env.
    $dbName = docker compose exec -T postgres printenv POSTGRES_DB
    if ($LASTEXITCODE -ne 0) { throw 'Database name lookup failed.' }
    $escapedDbName = [Uri]::EscapeDataString(($dbName | Out-String).Trim())
    $previousURL = $env:DATABASE_URL
    $previousEnvironment = $env:APP_ENV
    $previousOrigin = $env:PUBLIC_ORIGIN
    try {
        $env:DATABASE_URL = "postgres://vault_runtime:${password}@127.0.0.1:5432/${escapedDbName}?sslmode=disable"
        if (-not $env:APP_ENV) { $env:APP_ENV = 'development' }
        if (-not $env:PUBLIC_ORIGIN -and $env:APP_ENV -eq 'development') { $env:PUBLIC_ORIGIN = 'http://127.0.0.1:5173' }
        Push-Location 'apps/api'
        try {
            go run ./cmd/server
            if ($LASTEXITCODE -ne 0) { throw 'API process failed.' }
        } finally { Pop-Location }
    } finally {
        $env:DATABASE_URL = $previousURL
        $env:APP_ENV = $previousEnvironment
        $env:PUBLIC_ORIGIN = $previousOrigin
    }
} finally {
    Remove-Variable password, sql -ErrorAction SilentlyContinue
    Pop-Location
}
