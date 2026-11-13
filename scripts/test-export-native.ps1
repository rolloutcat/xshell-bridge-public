param()
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
$output = if ($env:REPORT_DIR) { $env:REPORT_DIR } else { Join-Path $projectRoot 'dist\_test-artifacts\export-native' }
New-Item -ItemType Directory -Force $output | Out-Null
$junction = Join-Path $output ('junction-' + [Guid]::NewGuid().ToString('N'))
$junctionTarget = Join-Path $output 'reparse-target'
New-Item -ItemType Directory -Force $junctionTarget | Out-Null
New-Item -ItemType Junction -Path $junction -Value $junctionTarget | Out-Null
$csc = Join-Path $env:WINDIR 'Microsoft.NET\Framework64\v4.0.30319\csc.exe'
$sources = @('Backend.cs','Backend.Approvals.cs','Backend.Removal.cs','Backend.HostReverse.cs','HostReverse.cs','LocalAgent.cs','Backend.LocalAgent.cs','Updates.cs','UpdateCandidate.cs','UpdateHandoff.cs','CodexLocator.cs','Integrations.cs','ConnectionProfiles.cs','BackendHostReverseTests.cs') | ForEach-Object { Join-Path $projectRoot "desktop\$_" }
$refs = @('/r:System.Windows.Forms.dll','/r:System.Drawing.dll','/r:System.Web.Extensions.dll','/r:System.Net.Http.dll','/r:System.IO.Compression.dll','/r:System.IO.Compression.FileSystem.dll')
& $csc /nologo /langversion:5 /target:exe /main:XshellDesktop.BackendHostReverseTests "/out:$output\BackendHostReverseTests.exe" "/resource:$projectRoot\VERSION,XshellDesktop.VERSION" @refs @sources
if ($LASTEXITCODE -ne 0) { throw 'Export diagnostics compilation failed' }
Push-Location $projectRoot
try {
    & (Join-Path $output 'BackendHostReverseTests.exe') --export-diagnostics $junction
    if ($LASTEXITCODE -ne 0) { throw 'Export diagnostics failed' }
} finally { Pop-Location }
& $csc /nologo /langversion:5 /target:exe /main:XshellDesktop.ReversePathDialogTests "/out:$output\ReversePathDialogTests.exe" /r:System.Windows.Forms.dll /r:System.Web.Extensions.dll (Join-Path $projectRoot 'desktop\HostReverse.cs') (Join-Path $projectRoot 'desktop\ReversePathDialog.cs') (Join-Path $projectRoot 'desktop\ReversePathDialogTests.cs')
if ($LASTEXITCODE -ne 0) { throw 'Reverse chooser compilation failed' }
& (Join-Path $output 'ReversePathDialogTests.exe')
if ($LASTEXITCODE -ne 0) { throw 'Reverse chooser tests failed' }
