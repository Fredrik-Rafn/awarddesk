param(
    [ValidateRange(1,65535)][int]$Port = 8080,
    [ValidateSet('none','ana','ba','all')][string]$Provider = 'none'
)
$ErrorActionPreference = 'Stop'
$names = @('BA_EMAIL','BA_PASSWORD','ANA_MEMBER_NUMBER','ANA_PASSWORD','ANYSOLVER_API_KEY','HYPER_API_KEY')
$previous = @{}
foreach ($name in $names) { $previous[$name] = [Environment]::GetEnvironmentVariable($name, 'Process') }
function Read-PrivateValue([string]$Label) {
    $secure = Read-Host $Label -AsSecureString
    $pointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure)
    try { return [Runtime.InteropServices.Marshal]::PtrToStringBSTR($pointer) }
    finally { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($pointer); $secure.Dispose() }
}
try {
    if ($Provider -in @('ana','all')) {
        if (-not $env:ANA_MEMBER_NUMBER) { $env:ANA_MEMBER_NUMBER = Read-Host 'ANA Mileage Club number' }
        if (-not $env:ANA_PASSWORD) { $env:ANA_PASSWORD = Read-PrivateValue 'ANA web password' }
    }
    if ($Provider -in @('ba','all')) {
        if (-not $env:BA_EMAIL) { $env:BA_EMAIL = Read-Host 'BA email' }
        if (-not $env:BA_PASSWORD) { $env:BA_PASSWORD = Read-PrivateValue 'BA password' }
        if (-not $env:ANYSOLVER_API_KEY) { $env:ANYSOLVER_API_KEY = Read-PrivateValue 'AnySolver API key' }
    }
    if ($Provider -ne 'none' -and -not $env:HYPER_API_KEY) {
        $env:HYPER_API_KEY = Read-PrivateValue 'Hyper Solutions API key'
    }
    Push-Location $PSScriptRoot
    try {
        Write-Host "Backend and frontend: http://127.0.0.1:$Port. Ctrl+C stops the server."
        go run . -port $Port
        if ($LASTEXITCODE -ne 0) { throw 'Awarddesk exited with an error' }
    } finally { Pop-Location }
} finally {
    foreach ($name in $names) { [Environment]::SetEnvironmentVariable($name, $previous[$name], 'Process') }
}
