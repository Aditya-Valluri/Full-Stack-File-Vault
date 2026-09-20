# Local-only cleanup bootstrap. Runs in the foreground; Ctrl+C stops the worker.
[CmdletBinding()]
param([switch]$Once)
$ErrorActionPreference = 'Stop'
Push-Location (Join-Path $PSScriptRoot '..')
try {
    $credentialFile = Join-Path (Get-Location).Path '.secrets/gc-password'
    if (-not (Test-Path -LiteralPath $credentialFile)) {
        New-Item -ItemType Directory -Force '.secrets' | Out-Null
        $bytes = New-Object byte[] 32
        $rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
        try { $rng.GetBytes($bytes) } finally { $rng.Dispose() }
        $password = [BitConverter]::ToString($bytes).Replace('-', '').ToLowerInvariant()
        [IO.File]::WriteAllText($credentialFile, $password)
    }
    $password = [IO.File]::ReadAllText($credentialFile).Trim()
    if ($password -notmatch '^[a-f0-9]{64}$') { throw 'Invalid local cleanup credential format.' }
    docker compose up -d --wait postgres
    if ($LASTEXITCODE -ne 0) { throw 'PostgreSQL startup failed.' }
    docker compose run --rm migrate
    if ($LASTEXITCODE -ne 0) { throw 'Migration failed.' }
    # A validated random hex secret goes to psql stdin, never process arguments.
    $sql = "\set gc_password $password`nALTER ROLE vault_gc LOGIN PASSWORD :'gc_password';"
    $sql | docker compose exec -T postgres sh -c 'exec psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"'
    if ($LASTEXITCODE -ne 0) { throw 'Cleanup credential provisioning failed.' }
    $dbName = docker compose exec -T postgres printenv POSTGRES_DB
    if ($LASTEXITCODE -ne 0) { throw 'Database name lookup failed.' }
    $escapedDbName = [Uri]::EscapeDataString(($dbName | Out-String).Trim())
    $previousURL = $env:GC_DATABASE_URL
    $previousEnvironment = $env:APP_ENV
    $previousDirectory = $env:BLOB_STORAGE_DIR
    try {
        $env:GC_DATABASE_URL = "postgres://vault_gc:${password}@127.0.0.1:5432/${escapedDbName}?sslmode=disable"
        if (-not $env:APP_ENV) { $env:APP_ENV = 'development' }
        Push-Location 'apps/api'
        try {
            # Resolve relative paths from the same working directory as start-api.
            if (-not $env:BLOB_STORAGE_DIR) { $env:BLOB_STORAGE_DIR = 'data/blobs' }
            if (-not [IO.Path]::IsPathRooted($env:BLOB_STORAGE_DIR)) { $env:BLOB_STORAGE_DIR = Join-Path (Get-Location).Path $env:BLOB_STORAGE_DIR }
            $env:BLOB_STORAGE_DIR = [IO.Path]::GetFullPath($env:BLOB_STORAGE_DIR)
            if ($Once) { go run ./cmd/collect -once } else { go run ./cmd/collect }
            if ($LASTEXITCODE -ne 0) { throw 'Cleanup worker failed.' }
        } finally { Pop-Location }
    } finally {
        $env:GC_DATABASE_URL = $previousURL
        $env:APP_ENV = $previousEnvironment
        $env:BLOB_STORAGE_DIR = $previousDirectory
    }
} finally {
    Remove-Variable password, sql -ErrorAction SilentlyContinue
    Pop-Location
}
