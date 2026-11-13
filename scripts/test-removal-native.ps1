param([string[]]$TestNames = @('BackendRemovalTests'))
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
$output = if ($env:REPORT_DIR) { $env:REPORT_DIR } else { Join-Path $projectRoot 'dist\_test-artifacts\removal-native' }
New-Item -ItemType Directory -Force $output | Out-Null
$csc = Join-Path $env:WINDIR 'Microsoft.NET\Framework64\v4.0.30319\csc.exe'
$sources = @('Backend.cs','Backend.Approvals.cs','Backend.Removal.cs','Backend.HostReverse.cs','HostReverse.cs','LocalAgent.cs','Backend.LocalAgent.cs','Updates.cs','UpdateCandidate.cs','UpdateHandoff.cs','CodexLocator.cs','Integrations.cs','ConnectionProfiles.cs')
$sources = @($sources | ForEach-Object { Join-Path $projectRoot "desktop\$_" })
$refs = @('/r:System.Windows.Forms.dll','/r:System.Drawing.dll','/r:System.Web.Extensions.dll','/r:System.Net.Http.dll','/r:System.IO.Compression.dll','/r:System.IO.Compression.FileSystem.dll')
foreach ($testName in $TestNames) {
    if ($testName -notmatch '^Backend(?:Removal|Approvals|HostReverse)?Tests$') { throw 'Unsupported test name' }
    $exe = Join-Path $output "$testName.exe"
    & $csc /nologo /langversion:5 /target:exe "/main:XshellDesktop.$testName" "/out:$exe" "/resource:$projectRoot\VERSION,XshellDesktop.VERSION" @refs @sources (Join-Path $projectRoot "desktop\$testName.cs")
    if ($LASTEXITCODE -ne 0) { throw "$testName compilation failed" }
    & $exe
    if ($LASTEXITCODE -ne 0) { throw "$testName failed" }
}
