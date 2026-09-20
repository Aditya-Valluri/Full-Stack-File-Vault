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
        $process = New-Object System.Diagnostics.Process
        $process.StartInfo = $start
        try {
            $null = $process.Start()
            # Write, not WriteLine: whitespace and newlines are password bytes.
            $utf8 = New-Object System.Text.UTF8Encoding($false)
            $passwordBytes = $utf8.GetBytes([Runtime.InteropServices.Marshal]::PtrToStringBSTR($pointer))
            try {
                # BaseStream works on Windows PowerShell 5.1; no BOM or newline.
                $process.StandardInput.BaseStream.Write($passwordBytes, 0, $passwordBytes.Length)
                $process.StandardInput.BaseStream.Flush()
            } finally {
                [Array]::Clear($passwordBytes, 0, $passwordBytes.Length)
            }
            $process.StandardInput.Close()
            $process.WaitForExit()
            if ($process.ExitCode -ne 0) { throw 'Account provisioning failed.' }
        } finally { $process.Dispose() }
    } finally {
        [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($pointer)
        $secret.Dispose()
    }
} finally { Pop-Location }
