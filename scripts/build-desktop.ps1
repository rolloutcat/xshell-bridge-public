param([switch]$SkipTests, [switch]$IncludeExternalTests)
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
$appIcon = Join-Path $projectRoot 'assets\app-icon.ico'
if (-not (Test-Path -LiteralPath $appIcon -PathType Leaf)) { throw 'Missing assets/app-icon.ico; run node scripts/render-icon.cjs first' }
$sdkVersion = '1.0.4314.35'
$sdkHash = '90778DEB1B77E36292828B4B5833C9255C27533F40E0DDD55891AE718944EDC4'
$sdkDir = Join-Path $projectRoot 'dist\webview2-sdk'
$sdkZip = Join-Path $sdkDir 'package.zip'
$sdkExtracted = Join-Path $sdkDir 'extracted'
$output = Join-Path $projectRoot 'dist\xshell-desktop'
$csc = Join-Path $env:WINDIR 'Microsoft.NET\Framework64\v4.0.30319\csc.exe'
if (-not (Test-Path -LiteralPath $csc)) { throw '.NET Framework 4.8 / C# compiler is required on the build machine' }
New-Item -ItemType Directory -Force $sdkDir,$output | Out-Null
& (Join-Path $PSScriptRoot 'build-log-helper.ps1') -Output (Join-Path $output 'XshLogCapture.exe')
Copy-Item -LiteralPath (Join-Path $projectRoot 'agent\log_parser.js'),(Join-Path $projectRoot 'agent\xshell_agent.js'),(Join-Path $projectRoot 'agent\remote_bootstrap.js') -Destination $output -Force
if (-not (Test-Path -LiteralPath $sdkZip)) {
    Invoke-WebRequest -UseBasicParsing -Uri "https://api.nuget.org/v3-flatcontainer/microsoft.web.webview2/$sdkVersion/microsoft.web.webview2.$sdkVersion.nupkg" -OutFile $sdkZip
}
if ((Get-FileHash -LiteralPath $sdkZip -Algorithm SHA256).Hash -ne $sdkHash) { throw 'WebView2 SDK hash mismatch; nothing compiled' }
Expand-Archive -LiteralPath $sdkZip -DestinationPath $sdkExtracted -Force
$refs = @('/r:System.Windows.Forms.dll','/r:System.Drawing.dll','/r:System.Web.Extensions.dll','/r:System.Net.Http.dll')
$refs += @('/r:System.IO.Compression.dll','/r:System.IO.Compression.FileSystem.dll')
$updates = Join-Path $projectRoot 'desktop\Updates.cs'
$refs += $updates
$updateCandidate = Join-Path $projectRoot 'desktop\UpdateCandidate.cs'
$refs += $updateCandidate
# Build-only fixture, including with -SkipTests. Never copied into the bundle.
& $csc /nologo /langversion:5 /target:exe /main:XshellDesktop.PortableImportCheck "/out:$output\PortableImportCheck.exe" /r:System.Web.Extensions.dll /r:System.Net.Http.dll /r:System.IO.Compression.dll $updates $updateCandidate (Join-Path $projectRoot 'desktop\PortableImportCheck.cs')
if ($LASTEXITCODE -ne 0) { throw 'Portable import checker compilation failed' }
$updateHandoff = Join-Path $projectRoot 'desktop\UpdateHandoff.cs'
$refs += $updateHandoff
if (-not $SkipTests) {
    & $csc /nologo /target:exe /main:XshellDesktop.UpdateHandoffTests "/out:$output\UpdateHandoffTests.exe" /r:System.Web.Extensions.dll $updateCandidate $updateHandoff (Join-Path $projectRoot 'desktop\UpdateHandoffTests.cs')
    if ($LASTEXITCODE -ne 0) { throw 'Update handoff tests compilation failed' }
    & (Join-Path $output 'UpdateHandoffTests.exe')
    if ($LASTEXITCODE -ne 0) { throw 'Update handoff tests failed' }
    & $csc /nologo /target:exe "/out:$output\UpdateCandidateTests.exe" $updateCandidate (Join-Path $projectRoot 'desktop\UpdateCandidateTests.cs')
    if ($LASTEXITCODE -ne 0) { throw 'Update candidate tests compilation failed' }
    & (Join-Path $output 'UpdateCandidateTests.exe')
    if ($LASTEXITCODE -ne 0) { throw 'Update candidate tests failed' }
}
$codexLocator = Join-Path $projectRoot 'desktop\CodexLocator.cs'
$refs += $codexLocator
$versionFile = Join-Path $projectRoot 'VERSION'
$refs += "/resource:$versionFile,XshellDesktop.VERSION"
$core = Join-Path $sdkExtracted 'lib\net462\Microsoft.Web.WebView2.Core.dll'
$forms = Join-Path $sdkExtracted 'lib\net462\Microsoft.Web.WebView2.WinForms.dll'
# HostReverse is backend-only source, not a global compiler reference/source.
$hostReverse = Join-Path $projectRoot 'desktop\HostReverse.cs'
$reverseDialog = Join-Path $projectRoot 'desktop\ReversePathDialog.cs'
$backend = @((Join-Path $projectRoot 'desktop\Backend.cs'), (Join-Path $projectRoot 'desktop\Backend.Approvals.cs'), (Join-Path $projectRoot 'desktop\Backend.Removal.cs'), $hostReverse, (Join-Path $projectRoot 'desktop\Backend.HostReverse.cs'), (Join-Path $projectRoot 'desktop\LocalAgent.cs'), (Join-Path $projectRoot 'desktop\Backend.LocalAgent.cs'))
$integrations = Join-Path $projectRoot 'desktop\Integrations.cs'
$profiles = Join-Path $projectRoot 'desktop\ConnectionProfiles.cs'
if (-not $SkipTests) {
    & $csc /nologo /target:exe "/out:$output\CodexLocatorTests.exe" @refs $integrations $profiles (Join-Path $projectRoot 'desktop\CodexLocatorTests.cs')
    if ($LASTEXITCODE -ne 0) { throw 'Codex locator tests failed to compile' }
    & (Join-Path $output 'CodexLocatorTests.exe')
    if ($LASTEXITCODE -ne 0) { throw 'Codex locator tests failed' }
    & $csc /nologo /target:exe "/out:$output\UpdatesTests.exe" @refs (Join-Path $projectRoot 'desktop\UpdatesTests.cs')
    if ($LASTEXITCODE -ne 0) { throw 'Updates tests compilation failed' }
    & (Join-Path $output 'UpdatesTests.exe')
    if ($LASTEXITCODE -ne 0) { throw 'Updates tests failed' }
    & $csc /nologo /target:exe "/out:$output\ConnectionProfilesTests.exe" @refs $profiles (Join-Path $projectRoot 'desktop\ConnectionProfilesTests.cs')
    if ($LASTEXITCODE -ne 0) { throw 'Connection profile tests failed to compile' }
    & (Join-Path $output 'ConnectionProfilesTests.exe')
    if ($LASTEXITCODE -ne 0) { throw 'Connection profile tests failed' }
}
if (-not $SkipTests) {
    & $csc /nologo /target:exe "/out:$output\SkillFilesTests.exe" @refs $integrations $profiles (Join-Path $projectRoot 'desktop\SkillFilesTests.cs')
    if ($LASTEXITCODE -ne 0) { throw 'Skill file lifecycle tests failed to compile' }
    & (Join-Path $output 'SkillFilesTests.exe')
    if ($LASTEXITCODE -ne 0) { throw 'Skill file lifecycle tests failed' }
    & $csc /nologo /target:exe "/out:$output\IntegrationsTests.exe" @refs $backend $integrations $profiles (Join-Path $projectRoot 'desktop\IntegrationsTests.cs')
    if ($LASTEXITCODE -ne 0) { throw 'Integration lifecycle tests failed to compile' }
    & (Join-Path $output 'IntegrationsTests.exe')
    if ($LASTEXITCODE -ne 0) { throw 'Integration lifecycle tests failed' }
    & $csc /nologo /target:exe "/out:$output\IntegrationsExternalTests.exe" @refs $integrations $profiles (Join-Path $projectRoot 'desktop\IntegrationsExternalTests.cs')
    if ($LASTEXITCODE -ne 0) { throw 'Codex adapter tests failed to compile' }
    & (Join-Path $output 'IntegrationsExternalTests.exe')
    if ($LASTEXITCODE -ne 0) { throw 'Codex adapter tests failed' }
    & $csc /nologo /langversion:5 /target:exe /main:XshellDesktop.ReversePathDialogTests "/out:$output\ReversePathDialogTests.exe" /r:System.Windows.Forms.dll /r:System.Web.Extensions.dll $hostReverse $reverseDialog (Join-Path $projectRoot 'desktop\ReversePathDialogTests.cs')
    if ($LASTEXITCODE -ne 0) { throw 'Reverse chooser tests compilation failed' }
    & (Join-Path $output 'ReversePathDialogTests.exe')
    if ($LASTEXITCODE -ne 0) { throw 'Reverse chooser tests failed' }
    & $csc /nologo /langversion:5 /target:exe /main:XshellDesktop.HostReverseTests "/out:$output\HostReverseTests.exe" /r:System.Web.Extensions.dll $hostReverse (Join-Path $projectRoot 'desktop\HostReverseTests.cs') (Join-Path $projectRoot 'desktop\HostReverseLifecycleTests.cs')
    if ($LASTEXITCODE -ne 0) { throw 'Host reverse module tests compilation failed' }
    & (Join-Path $output 'HostReverseTests.exe')
    if ($LASTEXITCODE -ne 0) { throw 'Host reverse module tests failed' }
    & $csc /nologo /langversion:5 /target:exe /main:XshellDesktop.BackendHostReverseTests "/out:$output\BackendHostReverseTests.exe" @refs $backend $integrations $profiles (Join-Path $projectRoot 'desktop\BackendHostReverseTests.cs')
    if ($LASTEXITCODE -ne 0) { throw 'Host reverse backend tests compilation failed' }
    & (Join-Path $output 'BackendHostReverseTests.exe')
    if ($LASTEXITCODE -ne 0) { throw 'Host reverse backend tests failed' }
    & $csc /nologo /langversion:5 /target:exe /main:XshellDesktop.LocalAgentTests "/out:$output\LocalAgentTests.exe" /r:System.Web.Extensions.dll $hostReverse (Join-Path $projectRoot 'desktop\LocalAgent.cs') (Join-Path $projectRoot 'desktop\LocalAgentTests.cs')
    if ($LASTEXITCODE -ne 0) { throw 'Local agent tests compilation failed' }
    & (Join-Path $output 'LocalAgentTests.exe')
    if ($LASTEXITCODE -ne 0) { throw 'Local agent tests failed' }
    & $csc /nologo /langversion:5 /target:exe /main:XshellDesktop.BackendLocalAgentTests "/out:$output\BackendLocalAgentTests.exe" @refs $backend $integrations $profiles (Join-Path $projectRoot 'desktop\BackendLocalAgentTests.cs')
    if ($LASTEXITCODE -ne 0) { throw 'Local agent backend tests compilation failed' }
    & (Join-Path $output 'BackendLocalAgentTests.exe')
    if ($LASTEXITCODE -ne 0) { throw 'Local agent backend tests failed' }
    & (Join-Path $output 'BackendHostReverseTests.exe') --export-diagnostics
    if ($LASTEXITCODE -ne 0) { throw 'Export diagnostics tests failed' }
    & $csc /nologo /target:exe "/out:$output\BackendApprovalsTests.exe" @refs $backend $integrations $profiles (Join-Path $projectRoot 'desktop\BackendApprovalsTests.cs')
    if ($LASTEXITCODE -ne 0) { throw 'Native approvals tests compilation failed' }
    & (Join-Path $output 'BackendApprovalsTests.exe')
    if ($LASTEXITCODE -ne 0) { throw 'Native approvals tests failed' }
    & $csc /nologo /target:exe "/out:$output\BackendRemovalTests.exe" @refs $backend $integrations $profiles (Join-Path $projectRoot 'desktop\BackendRemovalTests.cs')
    if ($LASTEXITCODE -ne 0) { throw 'Native removal tests compilation failed' }
    & (Join-Path $output 'BackendRemovalTests.exe')
    if ($LASTEXITCODE -ne 0) { throw 'Native removal tests failed' }
    & $csc /nologo /target:exe "/out:$output\BackendTests.exe" @refs $backend $integrations $profiles (Join-Path $projectRoot 'desktop\BackendTests.cs')
    if ($LASTEXITCODE -ne 0) { throw 'Desktop test compilation failed' }
    & (Join-Path $output 'BackendTests.exe')
    if ($LASTEXITCODE -ne 0) { throw 'Desktop approval tests failed' }
    & $csc /nologo /target:exe "/out:$output\IntegrationDriver.exe" @refs $backend $integrations $profiles (Join-Path $projectRoot 'desktop\IntegrationDriver.cs')
    if ($LASTEXITCODE -ne 0) { throw 'Integration driver compilation failed' }
}
if ($IncludeExternalTests -and -not $SkipTests) {
    Push-Location $projectRoot
    try {
        & node (Join-Path $projectRoot 'tests\remote-agent.test.js')
        if ($LASTEXITCODE -ne 0) { throw 'External remote agent tests failed' }
    } finally { Pop-Location }
    $externalTests = @(Get-ChildItem -LiteralPath (Join-Path $projectRoot 'tests') -Filter 'desktop-*.test.js' -File)
    if ($externalTests.Count -eq 0) { throw 'No external desktop tests found' }
    foreach ($test in $externalTests) {
        Push-Location $projectRoot
        try {
            & node $test.FullName
            if ($LASTEXITCODE -ne 0) { throw "External desktop test failed: $($test.Name)" }
        } finally { Pop-Location }
    }
}
& $csc /nologo /target:winexe /platform:x64 /define:UPDATE_HANDOFF_EXE /main:XshellDesktop.UpdateHandoff "/out:$output\xshupdater.exe" "/win32icon:$appIcon" /r:System.Web.Extensions.dll /r:System.Windows.Forms.dll $updateCandidate $updateHandoff
if ($LASTEXITCODE -ne 0) { throw 'Update helper compilation failed' }
# HTML parsers normalize CRLF before CSP hashing. Hash exactly those bytes.
$html = [IO.File]::ReadAllText((Join-Path $projectRoot 'desktop\app.html')).Replace("`r`n","`n").Replace("`r","`n")
$attachmentScript = [IO.File]::ReadAllText((Join-Path $projectRoot 'desktop\attachment-state.js')).Replace("`r`n","`n").Replace("`r","`n")
$html = $html.Replace('/*__ATTACHMENT_STATE__*/',$attachmentScript)
$updatesScript = [IO.File]::ReadAllText((Join-Path $projectRoot 'desktop\updates-ui.js')).Replace("`r`n","`n").Replace("`r","`n")
$html = $html.Replace('/*__UPDATES_UI__*/',$updatesScript)
$script = [regex]::Match($html,'(?s)<script>(.*?)</script>').Groups[1].Value
$sha = [Security.Cryptography.SHA256]::Create()
try { $scriptHash = [Convert]::ToBase64String($sha.ComputeHash([Text.Encoding]::UTF8.GetBytes($script))) } finally { $sha.Dispose() }
$html = $html.Replace('__SCRIPT_HASH__',$scriptHash)
$embeddedHtml = Join-Path $output 'app.embedded.html'
[IO.File]::WriteAllText($embeddedHtml,$html,(New-Object Text.UTF8Encoding($false)))
& $csc /nologo /target:winexe /platform:x64 "/out:$output\xshdesk.exe" "/win32icon:$appIcon" @refs "/r:$core" "/r:$forms" "/resource:$embeddedHtml,XshellDesktop.app.html" $backend $integrations $profiles $reverseDialog (Join-Path $projectRoot 'desktop\Shell.cs')
if ($LASTEXITCODE -ne 0) { throw 'Desktop compilation failed' }
Copy-Item -LiteralPath $core,$forms -Destination $output -Force
Copy-Item -LiteralPath (Join-Path $sdkExtracted 'runtimes\win-x64\native\WebView2Loader.dll') -Destination $output -Force
Copy-Item -LiteralPath (Join-Path $projectRoot 'desktop\xshdesk.exe.config') -Destination $output -Force
Copy-Item -LiteralPath (Join-Path $sdkExtracted 'LICENSE.txt') -Destination (Join-Path $output 'WebView2-LICENSE.txt') -Force
Write-Output "Built: $output\xshdesk.exe (Windows x64; .NET Framework 4.8; WebView2 Evergreen runtime)"
