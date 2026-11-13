param([Parameter(Mandatory=$true)][string]$BundleRoot)
$ErrorActionPreference='Stop'
if ($PSVersionTable.PSEdition -ne 'Desktop' -or $PSVersionTable.PSVersion.Major -ne 5) { throw 'Windows PowerShell 5.1 required' }
& (Join-Path $PSScriptRoot 'test-portable.ps1') -BundleRoot $BundleRoot
