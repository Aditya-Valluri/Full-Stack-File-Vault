param(
    [Parameter(Mandatory = $true)][ValidatePattern('^[A-Za-z0-9][A-Za-z0-9._-]{2,63}$')][string]$Login,
    [ValidateSet('USER', 'ADMIN')][string]$Role = 'USER'
)
$ErrorActionPreference = 'Stop'
if (-not $env:PROVISION_DATABASE_URL) { throw 'Set PROVISION_DATABASE_URL using operator credentials first.' }
Push-Location (Join-Path $PSScriptRoot '../apps/api')
try {
    New-Item -ItemType Directory -Force bin | Out-Null
    go build -o bin/provision-user.exe ./cmd/provision-user
    if ($LASTEXITCODE -ne 0) { throw 'Provisioning build failed.' }
    $secret = Read-Host 'New account password (15+ characters)' -AsSecureString
    $pointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secret)
    try {
        $start = New-Object System.Diagnostics.ProcessStartInfo
        $start.FileName = Join-Path (Get-Location).Path 'bin/provision-user.exe'
        # Both argument values are constrained by parameter validation above.
        $start.Arguments = "-login $Login -role $Role"
        $start.UseShellExecute = $false
        $start.CreateNoWindow = $true
        $start.RedirectStandardInput = $true
        $start.StandardInputEncoding = New-Object System.Text.UTF8Encoding($false)
        $process = New-Object System.Diagnostics.Process
        $process.StartInfo = $start
        try {
            $null = $process.Start()
            # Write, not WriteLine: whitespace and newlines are password bytes.
            $process.StandardInput.Write([Runtime.InteropServices.Marshal]::PtrToStringBSTR($pointer))
            $process.StandardInput.Close()
            $process.WaitForExit()
            if ($process.ExitCode -ne 0) { throw 'Account provisioning failed.' }
        } finally { $process.Dispose() }
    } finally {
        [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($pointer)
        $secret.Dispose()
    }
} finally { Pop-Location }
