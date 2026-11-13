param([string]$GoExe='go', [switch]$SystemWebViewOnly, [switch]$IncludeExternalTests)
$ErrorActionPreference='Stop'
$projectRoot=Split-Path -Parent $PSScriptRoot
Push-Location $projectRoot
try {
    & $GoExe test . ./cmd/... ./internal/...
    if($LASTEXITCODE -ne 0){throw 'Go tests failed'}
    & $GoExe vet . ./cmd/... ./internal/...
    if($LASTEXITCODE -ne 0){throw 'Go vet failed'}
    python -m unittest discover -s tests -v
    if($LASTEXITCODE -ne 0){throw 'Python tests failed'}
    & "$PSScriptRoot\build-portable.ps1" -GoExe $GoExe -SystemWebViewOnly:$SystemWebViewOnly -IncludeExternalTests:$IncludeExternalTests
} finally {Pop-Location}
