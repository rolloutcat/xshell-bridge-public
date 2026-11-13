param([string]$GoExe='go', [switch]$SystemWebViewOnly, [switch]$IncludeExternalTests)
$ErrorActionPreference='Stop'
$root=Split-Path -Parent $PSScriptRoot
Push-Location $root
try {
    # Exercise the production writer under the affected runtime before building.
    & "$env:WINDIR\System32\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -ExecutionPolicy RemoteSigned -File "$PSScriptRoot\test-portable-archive.ps1"
    if ($LASTEXITCODE -ne 0) { throw 'Windows PowerShell portable archive writer tests failed' }
    . "$PSScriptRoot\write-portable-archive.ps1"
    & "$PSScriptRoot\build-desktop.ps1" -IncludeExternalTests:$IncludeExternalTests
    $suffix=if($SystemWebViewOnly){'lite'}else{'full'}
    $name="xshell-bridge-portable-windows-x64-$suffix"
    # Fresh staging avoids carrying credentials or stale build/test files into a release.
    $stage=Join-Path $root ('dist\package-'+[guid]::NewGuid().ToString('N'))
    $bundle=Join-Path $stage $name
    New-Item -ItemType Directory -Force $bundle,(Join-Path $bundle 'scripts'),(Join-Path $bundle 'docs') | Out-Null
    & $GoExe build -trimpath -o (Join-Path $bundle 'xshctl.exe') ./cmd/xshctl
    if($LASTEXITCODE -ne 0){throw 'CLI build failed'}
    $expectedVersion=[IO.File]::ReadAllText((Join-Path $root 'VERSION')).Trim()
    $actualVersion= & (Join-Path $bundle 'xshctl.exe') version
    if($LASTEXITCODE -ne 0 -or ([string]$actualVersion).Trim() -cne $expectedVersion){throw 'Built CLI version does not match VERSION'}
    foreach($file in @('XshLogCapture.exe','xshupdater.exe','xshdesk.exe','xshdesk.exe.config','Microsoft.Web.WebView2.Core.dll','Microsoft.Web.WebView2.WinForms.dll','WebView2Loader.dll','WebView2-LICENSE.txt')) {
        Copy-Item -LiteralPath (Join-Path $root "dist\xshell-desktop\$file") -Destination $bundle
    }
    # Fixed export sources only. Never copy a runtime directory or pairing configuration.
    Copy-Item -LiteralPath 'agent\xshell_agent.js','agent\remote_bootstrap.js','agent\log_parser.js','agent\log_path_diagnostic.js','README.md','VERSION' -Destination $bundle
    # README is the only documentation index. Copy the four guides and their references.
    Copy-Item -LiteralPath 'docs\user-guide.md','docs\maintenance.md','docs\protocol.md','docs\validation.md' -Destination (Join-Path $bundle 'docs')
    Copy-Item -LiteralPath 'docs\approval-policy.md','docs\log-capture.md','docs\opskat-comparison.md','docs\release-history.md' -Destination (Join-Path $bundle 'docs')
    Copy-Item -LiteralPath 'scripts\start-desktop.ps1' -Destination (Join-Path $bundle 'scripts')
    Copy-Item -LiteralPath 'skills','examples' -Destination $bundle -Recurse
    Copy-Item -LiteralPath 'LICENSE','THIRD_PARTY_NOTICES.md' -Destination $bundle
    & "$PSScriptRoot\collect-licenses.ps1" -GoExe $GoExe -Destination (Join-Path $bundle 'licenses')
    if(-not $SystemWebViewOnly) {
        $runtimeLicenses=Join-Path $bundle 'licenses\webview2-fixed-runtime'
        New-Item -ItemType Directory -Force $runtimeLicenses | Out-Null
        Copy-Item -LiteralPath 'licenses\WebView2-Fixed-Runtime.html','licenses\WebView2-Fixed-Runtime-source.txt' -Destination $runtimeLicenses
    }
    $launcher="@echo off`r`npowershell.exe -NoLogo -NoProfile -ExecutionPolicy RemoteSigned -File `"%~dp0scripts\start-desktop.ps1`"`r`nif errorlevel 1 pause`r`n"
    [IO.File]::WriteAllText((Join-Path $bundle 'Start.cmd'),$launcher,[Text.Encoding]::ASCII)
    if(-not $SystemWebViewOnly) {
        $version='156.0.4314.58'
        $cab=Join-Path $root "dist\Microsoft.WebView2.FixedVersionRuntime.$version.x64.cab"
        $url='https://msedge.sf.dl.delivery.mp.microsoft.com/filestreamingservice/files/b7919cc9-680d-4253-bf32-732988e68aa6/Microsoft.WebView2.FixedVersionRuntime.156.0.4314.58.x64.cab'
        if(-not(Test-Path -LiteralPath $cab)){Invoke-WebRequest -UseBasicParsing $url -OutFile $cab}
        if((Get-FileHash -LiteralPath $cab -Algorithm SHA256).Hash -ne '857DCFD6F7035508A29645FA2978C3A6A841F220DF77CC366D318894E19F4792'){throw 'Fixed Runtime SHA256 mismatch'}
        $extract=Join-Path $stage 'runtime-extract'
        New-Item -ItemType Directory $extract | Out-Null
        & "$env:WINDIR\System32\expand.exe" $cab '-F:*' $extract > $null
        if($LASTEXITCODE -ne 0){throw 'Fixed Runtime extraction failed'}
        $runtime=Join-Path $extract "Microsoft.WebView2.FixedVersionRuntime.$version.x64"
        $signature=Get-AuthenticodeSignature -LiteralPath (Join-Path $runtime 'msedgewebview2.exe')
        if($signature.Status -ne 'Valid' -or $signature.SignerCertificate.Subject -notmatch 'O=Microsoft Corporation'){throw 'Microsoft Runtime signature invalid'}
        Copy-Item -LiteralPath $runtime -Destination (Join-Path $bundle 'WebView2Runtime') -Recurse
        [IO.File]::WriteAllText((Join-Path $bundle 'WebView2-Runtime-source.txt'),"Official Microsoft Fixed Version Runtime $version x64`r`n$url`r`nhttps://developer.microsoft.com/en-us/microsoft-edge/webview2/`r`n",[Text.Encoding]::ASCII)
    }
    & "$PSScriptRoot\test-portable.ps1" -BundleRoot $bundle
    & "$env:WINDIR\System32\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -ExecutionPolicy RemoteSigned -File "$PSScriptRoot\test-launcher-defaults.ps1" -BundleRoot $bundle
    if ($LASTEXITCODE -ne 0) { throw 'Windows PowerShell launcher defaults failed' }
    $zip=Join-Path $root "dist\$name.zip"
    Write-PortableArchive -BundleRoot $bundle -ZipPath $zip
    $zipHash = (Get-FileHash -LiteralPath $zip -Algorithm SHA256).Hash.ToLowerInvariant()
    [IO.File]::WriteAllText(($zip+'.sha256'),($zipHash+'  '+[IO.Path]::GetFileName($zip)+"`n"),[Text.Encoding]::ASCII)
    & (Join-Path $root 'dist\xshell-desktop\PortableImportCheck.exe') $zip
    if ($LASTEXITCODE -ne 0) { throw 'Portable ZIP failed actual Updates.ImportZip validation' }
    Write-Output "SHA256: $zipHash"
    Write-Output "Bundle: $bundle"
} finally {Pop-Location}
