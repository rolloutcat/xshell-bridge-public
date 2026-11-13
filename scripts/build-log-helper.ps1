param([string]$Output = '')
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
if (-not $Output) { $Output = Join-Path $root 'dist\log-helper\XshLogCapture.exe' }
New-Item -ItemType Directory -Force (Split-Path -Parent $Output) | Out-Null
$csc = Join-Path $env:WINDIR 'Microsoft.NET\Framework64\v4.0.30319\csc.exe'
& $csc /nologo /optimize+ /langversion:5 /platform:anycpu /target:winexe /r:System.Web.Extensions.dll "/out:$Output" (Join-Path $root 'agent\LogCapture.cs')
if ($LASTEXITCODE -ne 0) { throw 'Log helper compilation failed' }
Write-Output "Built: $Output"
