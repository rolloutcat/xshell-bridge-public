param([Parameter(Mandatory=$true)][string]$BundleRoot)
$ErrorActionPreference = 'Stop'
foreach ($name in @('xshctl.exe','xshell_agent.js','remote_bootstrap.js','log_parser.js','log_path_diagnostic.js','XshLogCapture.exe','docs\approval-policy.md','docs\opskat-comparison.md')) {
    if (-not (Test-Path -LiteralPath (Join-Path $BundleRoot $name) -PathType Leaf)) { throw "Missing bundle file: $name" }
}
$base = [IO.Path]::GetFullPath((Join-Path (Split-Path -Parent $PSScriptRoot) 'dist\_test-artifacts\portable-init')).TrimEnd('\')
New-Item -ItemType Directory -Path $base -Force | Out-Null
$name = 'xsh-portable-test-' + [guid]::NewGuid().ToString('N')
$root = [IO.Path]::GetFullPath((Join-Path $base $name))
if ([IO.Path]::GetDirectoryName($root) -ne $base -or (Test-Path -LiteralPath $root)) { throw 'Unsafe fixture root' }
try {
    & "$PSScriptRoot\start-desktop.ps1" -BundleRoot $BundleRoot -DataRoot $root -PrepareOnly
    $runtime = Join-Path $root 'runtime'
    $hashes = @{}
    foreach ($file in @('bridge.json','client.json','agent.json','admin.json')) { $hashes[$file] = (Get-FileHash -LiteralPath (Join-Path $runtime $file)).Hash }
    & "$PSScriptRoot\start-desktop.ps1" -BundleRoot $BundleRoot -DataRoot $root -PrepareOnly
    foreach ($file in $hashes.Keys) { if ((Get-FileHash -LiteralPath (Join-Path $runtime $file)).Hash -ne $hashes[$file]) { throw 'Existing config overwritten' } }
    if (Test-Path -LiteralPath (Join-Path $runtime 'bridge.lock')) { throw 'PrepareOnly started Bridge' }
    if (Test-Path -LiteralPath (Join-Path $runtime 'xshell_agent.js')) { throw 'Unexpected local script install' }
    $agent = Get-Content -LiteralPath (Join-Path $runtime 'agent.json') -Raw -Encoding UTF8 | ConvertFrom-Json
    if ($agent.binding_mode -ne 'manual_terminal') { throw 'Invalid default binding' }
} finally {
    if ([IO.Path]::GetDirectoryName($root) -ne $base -or [IO.Path]::GetFileName($root) -ne $name) { throw 'Unsafe cleanup' }
    if (Test-Path -LiteralPath $root) { [IO.Directory]::Delete($root,$true) }
}
Write-Output 'PASS: isolated host initialization preserves existing configuration and does not start Bridge'
