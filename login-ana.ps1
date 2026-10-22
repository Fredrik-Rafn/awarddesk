param()
$ErrorActionPreference = 'Stop'
$previousMember = $env:ANA_MEMBER_NUMBER
$previousPassword = $env:ANA_PASSWORD
$previousHyperKey = $env:HYPER_API_KEY
try {
    if (-not $env:ANA_MEMBER_NUMBER) {
        $env:ANA_MEMBER_NUMBER = Read-Host 'ANA Mileage Club number'
    }
    if (-not $env:ANA_PASSWORD) {
        $secure = Read-Host 'ANA web password' -AsSecureString
        $pointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure)
        try {
            $env:ANA_PASSWORD = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($pointer)
        } finally {
            [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($pointer)
        }
    }
    if (-not $env:HYPER_API_KEY) {
        $secureKey = Read-Host 'Hyper Solutions API key (Akamai)' -AsSecureString
        $keyPointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secureKey)
        try {
            $env:HYPER_API_KEY = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($keyPointer)
        } finally {
            [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($keyPointer)
        }
    }
    Push-Location $PSScriptRoot
    try {
        go run ./cmd/ana-login -env
        if ($LASTEXITCODE -ne 0) {
            throw 'ANA login was not verified. See the status above.'
        }
    } finally {
        Pop-Location
    }
} finally {
    $env:ANA_MEMBER_NUMBER = $previousMember
    $env:ANA_PASSWORD = $previousPassword
    $env:HYPER_API_KEY = $previousHyperKey
}
