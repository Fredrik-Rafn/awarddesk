param([ValidateRange(1,65535)][int]$Port = 8080)
$ErrorActionPreference = 'Stop'


$ngrokProcess = $null
$previousURL = $env:AWARDDESK_PUBLIC_URL
$previousPassword = $env:AWARDDESK_DEMO_PASSWORD
$logBase = Join-Path ([IO.Path]::GetTempPath()) ('awarddesk-ngrok-' + [Guid]::NewGuid().ToString('N'))
$outputLog = $logBase + '.out.log'
$errorLog = $logBase + '.err.log'
Push-Location $PSScriptRoot
try {
    $ngrok = (Get-Command ngrok -ErrorAction SilentlyContinue).Source
    if (-not $ngrok -and (Test-Path -LiteralPath 'C:\ngrok\ngrok.exe')) { $ngrok = 'C:\ngrok\ngrok.exe' }
    if (-not $ngrok) { throw 'Install ngrok and add its authtoken first. See docs/demo.md.' }
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) { throw 'Go is not installed or is not on PATH.' }
    if (Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue) {
        throw "Port $Port is in use. Stop the current Awarddesk server with Ctrl+C before starting the demo."
    }
    if (Get-NetTCPConnection -LocalPort 4040 -State Listen -ErrorAction SilentlyContinue) {
        throw 'Another ngrok agent is using port 4040. Stop it first, or use the manual setup in docs/demo.md.'
    }
    $ngrokProcess = Start-Process -FilePath $ngrok -ArgumentList @('http', "127.0.0.1:$Port", '--inspect=false') -WindowStyle Hidden -PassThru -RedirectStandardOutput $outputLog -RedirectStandardError $errorLog
    $deadline = [DateTime]::UtcNow.AddSeconds(30)
    $publicURL = $null
    while ([DateTime]::UtcNow -lt $deadline) {
        if ($ngrokProcess.HasExited) { throw 'ngrok exited. Check your ngrok authtoken/account configuration with: ngrok config check' }
        try {
            $tunnels = Invoke-RestMethod -Uri 'http://127.0.0.1:4040/api/tunnels' -TimeoutSec 2
            $publicURL = ($tunnels.tunnels | Where-Object { $_.proto -eq 'https' } | Select-Object -First 1).public_url
        } catch { }
        if ($publicURL) { break }
        Start-Sleep -Milliseconds 250
    }
    if (-not $publicURL) { throw 'ngrok did not provide an HTTPS URL within 30 seconds. Check your ngrok account and connection.' }

    $env:AWARDDESK_PUBLIC_URL = $publicURL
    if (-not $env:AWARDDESK_DEMO_PASSWORD) { $env:AWARDDESK_DEMO_PASSWORD = [Guid]::NewGuid().ToString('N') }
    Write-Host "Demo URL: $publicURL"
    Write-Host 'Demo username: demo'
    Write-Host "Demo password: $env:AWARDDESK_DEMO_PASSWORD"
    Write-Host "Admin stays local: http://127.0.0.1:$Port/admin"
    Write-Host 'Keep this terminal open. Ctrl+C stops the app and its tunnel.'
    go run . -port $Port
    if ($LASTEXITCODE -ne 0) { throw 'Awarddesk exited with an error.' }
} finally {
    if ($ngrokProcess -and -not $ngrokProcess.HasExited) { Stop-Process -Id $ngrokProcess.Id -ErrorAction SilentlyContinue }
    $env:AWARDDESK_PUBLIC_URL = $previousURL
    $env:AWARDDESK_DEMO_PASSWORD = $previousPassword
    foreach ($file in @($outputLog, $errorLog)) {
        if (Test-Path -LiteralPath $file) { Remove-Item -LiteralPath $file -ErrorAction SilentlyContinue }
    }
    Pop-Location
}
