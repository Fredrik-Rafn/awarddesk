param([ValidateRange(1,65535)][int]$Port = 8080)
$ErrorActionPreference = 'Stop'
Push-Location $PSScriptRoot
try { go run . -port $Port; if ($LASTEXITCODE -ne 0) { throw 'Awarddesk exited with an error' } }
finally { Pop-Location }
