param()
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
$output = if ($env:REPORT_DIR) { $env:REPORT_DIR } else { Join-Path $projectRoot 'dist\_test-artifacts\local-agent-native' }
New-Item -ItemType Directory -Force $output | Out-Null
$previousTemp=$env:TEMP; $previousTmp=$env:TMP
$env:TEMP=Join-Path ([IO.Path]::GetFullPath($output)) 'fixtures'; $env:TMP=$env:TEMP
New-Item -ItemType Directory -Force $env:TEMP | Out-Null
try {
$junctionTarget = Join-Path $output ('junction-target-' + [guid]::NewGuid().ToString('N'))
$junction = Join-Path $output ('junction-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $junctionTarget | Out-Null
New-Item -ItemType Junction -Path $junction -Value $junctionTarget | Out-Null
$csc = Join-Path $env:WINDIR 'Microsoft.NET\Framework64\v4.0.30319\csc.exe'
$refs = @('/r:System.Windows.Forms.dll','/r:System.Drawing.dll','/r:System.Web.Extensions.dll','/r:System.Net.Http.dll','/r:System.IO.Compression.dll','/r:System.IO.Compression.FileSystem.dll')
$base = @('Backend.cs','Backend.Approvals.cs','Backend.Removal.cs','Backend.HostReverse.cs','HostReverse.cs','LocalAgent.cs','Backend.LocalAgent.cs','Updates.cs','UpdateCandidate.cs','UpdateHandoff.cs','CodexLocator.cs','Integrations.cs','ConnectionProfiles.cs') | ForEach-Object { Join-Path $projectRoot "desktop\$_" }
foreach ($test in @('LocalAgentTests','BackendLocalAgentTests')) {
    $exe = Join-Path $output "$test.exe"
    & $csc /nologo /langversion:5 /target:exe "/main:XshellDesktop.$test" "/out:$exe" "/resource:$projectRoot\VERSION,XshellDesktop.VERSION" @refs @base (Join-Path $projectRoot "desktop\$test.cs")
    if ($LASTEXITCODE -ne 0) { throw "$test compilation failed" }
    if ($test -eq 'LocalAgentTests') { & $exe $junction } else { & $exe }
    if ($LASTEXITCODE -ne 0) { throw "$test failed" }
}
} finally {
    $env:TEMP=$previousTemp; $env:TMP=$previousTmp
    if ($junction -and (Test-Path -LiteralPath $junction)) {
        $linkPath=[IO.Path]::GetFullPath($junction); $outputPath=[IO.Path]::GetFullPath($output).TrimEnd('\')+'\'
        if (-not $linkPath.StartsWith($outputPath,[StringComparison]::OrdinalIgnoreCase) -or ((Get-Item -LiteralPath $linkPath).Attributes -band [IO.FileAttributes]::ReparsePoint) -eq 0) { throw 'Unexpected junction cleanup path' }
        [IO.Directory]::Delete($linkPath)
    }
}
