# Provision an account in the isolated HTTPS application, with hidden local input.
param(
    [Parameter(Mandatory = $true)]
    [ValidatePattern('^[A-Za-z0-9][A-Za-z0-9._-]{2,63}$')]
    [string]$Login,
    [ValidateSet('USER', 'ADMIN')][string]$Role = 'USER'
)
$ErrorActionPreference = 'Stop'
$repository = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$docker = (Get-Command docker -ErrorAction Stop).Source
$secret = Read-Host 'Choose your password (15+ characters)' -AsSecureString
$pointer = [IntPtr]::Zero
try {
    $pointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secret)
    $start = New-Object System.Diagnostics.ProcessStartInfo
    $start.FileName = $docker
    $start.WorkingDirectory = $repository
    # Explicit project name prevents accidental use of the development database.
    # Login and role are restricted to safe characters by parameter validation.
    $start.Arguments = "compose -p full-stack-file-vault-application -f compose.application.yaml run --rm -T provision -login $Login -role $Role"
    $start.UseShellExecute = $false
    $start.CreateNoWindow = $true
    $start.RedirectStandardInput = $true
    $process = New-Object System.Diagnostics.Process
    $process.StartInfo = $start
    try {
        $null = $process.Start()
        # Do not append a newline: the provisioner hashes exact UTF-8 bytes.
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
        if ($process.ExitCode -ne 0) { throw 'Account was not created. Review the provisioning message above.' }
        Write-Host "Account created. Sign in as $Login at https://localhost:8443"
    } finally { $process.Dispose() }
} finally {
    if ($pointer -ne [IntPtr]::Zero) { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($pointer) }
    $secret.Dispose()
}
