$ErrorActionPreference = 'Stop'
if ($PSVersionTable.PSVersion.Major -ne 5 -or $PSVersionTable.PSEdition -ne 'Desktop') { throw 'Run this test with Windows PowerShell 5.1' }
$helper = Join-Path $PSScriptRoot 'write-portable-archive.ps1'
# Deliberately fail before loading anything when the production helper is absent.
if (-not (Test-Path -LiteralPath $helper -PathType Leaf)) { throw 'Missing production archive writer' }
. $helper
if (-not (Get-Command Write-PortableArchive -CommandType Function -ErrorAction SilentlyContinue)) { throw 'Missing Write-PortableArchive' }
Add-Type -AssemblyName System.IO.Compression
$testRoot = Join-Path (Split-Path -Parent $PSScriptRoot) 'dist\_test-artifacts\portable-archive'
New-Item -ItemType Directory -Path $testRoot -Force | Out-Null
$work = Join-Path $testRoot ('portable-writer-test-' + [guid]::NewGuid().ToString('N'))
$bundle = Join-Path $work 'fresh bundle'
$outside = Join-Path $work 'outside'
$link = Join-Path $bundle 'linked'
$zip = Join-Path $work 'portable.zip'
try {
    New-Item -ItemType Directory -Path $bundle,$outside,(Join-Path $bundle 'skills'),(Join-Path $bundle 'empty') | Out-Null
    $unicodeName = ([string][char]0x4E2D + [char]0x6587 + ' space.txt')
    $unicodeRelative = 'skills/' + $unicodeName
    $payload = [byte[]](0,1,2,10,13,127,128,255)
    $text = 'normal input' + "`r`n" + [char]0x4E2D + [char]0x6587
    [IO.File]::WriteAllBytes((Join-Path $bundle ('skills\' + $unicodeName)), $payload)
    [IO.File]::WriteAllText((Join-Path $bundle 'normal text.txt'), $text, (New-Object Text.UTF8Encoding($false)))
    [IO.File]::WriteAllText((Join-Path $outside 'must-not-appear.txt'), 'outside')
    [IO.File]::WriteAllText($zip, 'old archive sentinel')
    Write-PortableArchive -BundleRoot $bundle -ZipPath $zip
    $expected = @{}
    $expected['fresh bundle/' + $unicodeRelative] = [Convert]::ToBase64String($payload)
    $expected['fresh bundle/normal text.txt'] = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($text))
    $file = [IO.File]::OpenRead($zip)
    try {
        $archive = [IO.Compression.ZipArchive]::new($file, [IO.Compression.ZipArchiveMode]::Read, $true)
        try {
            if ($archive.Entries.Count -ne $expected.Count) { throw 'Unexpected archive inventory' }
            foreach ($entry in $archive.Entries) {
                # Inspect actual .NET FullName, not Python ZipInfo.filename.
                if ($entry.FullName.Contains('\') -or -not $expected.ContainsKey($entry.FullName)) { throw ('Noncanonical entry: ' + $entry.FullName) }
                $input = $entry.Open()
                $memory = New-Object IO.MemoryStream
                try {
                    $input.CopyTo($memory)
                    if ([Convert]::ToBase64String($memory.ToArray()) -cne $expected[$entry.FullName]) { throw 'Input bytes changed' }
                } finally { $memory.Dispose(); $input.Dispose() }
            }
        } finally { $archive.Dispose() }
    } finally { $file.Dispose() }
    # A second authorized build must replace, rather than append old entries.
    [IO.File]::Delete((Join-Path $bundle 'normal text.txt'))
    Write-PortableArchive -BundleRoot $bundle -ZipPath $zip
    $file = [IO.File]::OpenRead($zip)
    try {
        $archive = [IO.Compression.ZipArchive]::new($file, [IO.Compression.ZipArchiveMode]::Read, $true)
        try {
            if ($archive.Entries.Count -ne 1 -or $archive.Entries[0].FullName -cne ('fresh bundle/' + $unicodeRelative)) { throw 'Replacement retained stale entries' }
        } finally { $archive.Dispose() }
    } finally { $file.Dispose() }
    # Reject output inside the bundle before touching the existing destination.
    $insideZip = Join-Path $bundle 'inside.zip'
    [IO.File]::WriteAllText($insideZip, 'inside sentinel')
    $rejected = $false
    try { Write-PortableArchive -BundleRoot $bundle -ZipPath $insideZip } catch { $rejected = $true }
    if (-not $rejected -or [IO.File]::ReadAllText($insideZip) -cne 'inside sentinel') { throw 'Inside-bundle destination was not safely rejected' }
    [IO.File]::Delete($insideZip)
    # Junction creation needs no symlink privilege on Windows. Do not skip this test.
    & $env:ComSpec /d /c "mklink /J `"$link`" `"$outside`"" > $null
    if ($LASTEXITCODE -ne 0) { throw 'Could not create reparse-point fixture' }
    $before = (Get-FileHash -LiteralPath $zip -Algorithm SHA256).Hash
    $rejected = $false
    try { Write-PortableArchive -BundleRoot $bundle -ZipPath $zip } catch { $rejected = $true }
    if (-not $rejected -or (Get-FileHash -LiteralPath $zip -Algorithm SHA256).Hash -cne $before) { throw 'Reparse rejection modified existing archive' }
    if (@(Get-ChildItem -LiteralPath $work -Filter '.portable-*.tmp').Count -ne 0) { throw 'Temporary archive leaked' }
    if ([IO.File]::ReadAllText((Join-Path $outside 'must-not-appear.txt')) -cne 'outside') { throw 'Outside fixture changed' }
    Write-Output 'PASS: portable archive writer (Windows PowerShell 5.1 / actual .NET entries)'
} finally {
    if ([IO.Directory]::Exists($work)) {
        $canonicalTemp = [IO.Path]::GetFullPath((Resolve-Path -LiteralPath $testRoot).ProviderPath).TrimEnd('\')
        $cleanupRoot = [IO.Path]::GetFullPath((Resolve-Path -LiteralPath $work).ProviderPath).TrimEnd('\')
        if (-not [string]::Equals($cleanupRoot, [IO.Path]::GetFullPath($work).TrimEnd('\'), [StringComparison]::OrdinalIgnoreCase) -or
            -not [string]::Equals([IO.Path]::GetDirectoryName($cleanupRoot), $canonicalTemp, [StringComparison]::OrdinalIgnoreCase) -or
            [IO.Path]::GetFileName($cleanupRoot) -cnotmatch '^portable-writer-test-[0-9a-f]{32}$' -or
            ((Get-Item -LiteralPath $cleanupRoot -Force).Attributes -band [IO.FileAttributes]::ReparsePoint)) {
            throw 'Unsafe fixture cleanup root'
        }
        # Only unlink the known fixture, after validating its exact parent and root.
        $cleanupBundle = Join-Path $cleanupRoot 'fresh bundle'
        $cleanupLink = Join-Path $cleanupBundle 'linked'
        if (-not [string]::Equals([IO.Path]::GetFullPath($bundle), $cleanupBundle, [StringComparison]::OrdinalIgnoreCase) -or
            -not [string]::Equals([IO.Path]::GetFullPath($link), $cleanupLink, [StringComparison]::OrdinalIgnoreCase) -or
            -not [string]::Equals([IO.Path]::GetDirectoryName([IO.Path]::GetFullPath($link)), $cleanupBundle, [StringComparison]::OrdinalIgnoreCase)) {
            throw 'Unsafe fixture junction path'
        }
        if (Test-Path -LiteralPath $cleanupBundle) {
            $bundleItem = Get-Item -LiteralPath $cleanupBundle -Force
            if (-not $bundleItem.PSIsContainer -or ($bundleItem.Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw 'Unsafe fixture bundle' }
            if (Test-Path -LiteralPath $cleanupLink) {
                $linkItem = Get-Item -LiteralPath $cleanupLink -Force
                if (-not $linkItem.PSIsContainer -or -not ($linkItem.Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw 'Unexpected junction fixture type' }
                [IO.Directory]::Delete($cleanupLink, $false)
            }
        }
        # Inspect one directory at a time; never descend into a reparse point.
        $pending = New-Object 'System.Collections.Generic.Stack[string]'
        $pending.Push($cleanupRoot)
        while ($pending.Count -gt 0) {
            foreach ($item in Get-ChildItem -LiteralPath $pending.Pop() -Force) {
                if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Unexpected reparse point in fixture cleanup' }
                if ($item.PSIsContainer) { $pending.Push($item.FullName) }
            }
        }
        # Recheck the validated root immediately before recursive removal.
        if (-not [string]::Equals([IO.Path]::GetFullPath((Resolve-Path -LiteralPath $cleanupRoot).ProviderPath).TrimEnd('\'), $cleanupRoot, [StringComparison]::OrdinalIgnoreCase) -or
            -not [string]::Equals([IO.Path]::GetDirectoryName($cleanupRoot), $canonicalTemp, [StringComparison]::OrdinalIgnoreCase) -or
            [IO.Path]::GetFileName($cleanupRoot) -cnotmatch '^portable-writer-test-[0-9a-f]{32}$' -or
            ((Get-Item -LiteralPath $cleanupRoot -Force).Attributes -band [IO.FileAttributes]::ReparsePoint)) {
            throw 'Unsafe fixture cleanup root'
        }
        Remove-Item -LiteralPath $cleanupRoot -Recurse -Force
    }
}
