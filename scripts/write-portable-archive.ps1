# Loading this file defines the writer; it does not build or write anything.
function Write-PortableArchive {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory=$true)][string]$BundleRoot,
        [Parameter(Mandatory=$true)][string]$ZipPath
    )
    $ErrorActionPreference = 'Stop'
    Add-Type -AssemblyName System.IO.Compression
    # No ZipFile/Compression.FileSystem dependency: all ZIP I/O uses streams.
    $checkPath = {
        param([string]$Path)
        for ($p = [IO.Path]::GetFullPath($Path); $p; $p = [IO.Path]::GetDirectoryName($p)) {
            if ([IO.File]::Exists($p) -or [IO.Directory]::Exists($p)) {
                if (([IO.File]::GetAttributes($p) -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw "Reparse point rejected: $p" }
            }
        }
    }
    $bundle = [IO.Path]::GetFullPath($BundleRoot).TrimEnd([char[]]'\/')
    if (-not [IO.Directory]::Exists($bundle)) { throw 'Bundle directory does not exist' }
    & $checkPath $bundle
    $top = [IO.Path]::GetFileName($bundle)
    if ([string]::IsNullOrEmpty($top) -or $top.Contains(':') -or $top.Contains('\') -or $top.Contains('/')) { throw 'Invalid bundle root name' }
    $prefix = $bundle + [IO.Path]::DirectorySeparatorChar
    $zip = [IO.Path]::GetFullPath($ZipPath)
    if ($zip.Equals($bundle, [StringComparison]::OrdinalIgnoreCase) -or $zip.StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase)) { throw 'ZIP destination must be outside bundle' }
    $dist = [IO.Path]::GetDirectoryName($zip)
    & $checkPath $zip
    if (-not [IO.Directory]::Exists($dist)) { throw 'ZIP destination directory does not exist' }
    if ([IO.Directory]::Exists($zip)) { throw 'ZIP destination is a directory' }
    # Inspect before descending, never recursive enumeration through junctions.
    $pending = New-Object 'System.Collections.Generic.Stack[string]'
    $files = New-Object 'System.Collections.Generic.List[object]'
    $names = New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::OrdinalIgnoreCase)
    $pending.Push($bundle)
    while ($pending.Count -gt 0) {
        $dir = $pending.Pop()
        & $checkPath $dir
        foreach ($entry in [IO.Directory]::GetFileSystemEntries($dir)) {
            $full = [IO.Path]::GetFullPath($entry)
            if (-not $full.StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase)) { throw 'Path escaped bundle' }
            & $checkPath $full
            $attributes = [IO.File]::GetAttributes($full)
            if (($attributes -band [IO.FileAttributes]::Directory) -ne 0) {
                $pending.Push($full)
            } else {
                $relative = $full.Substring($prefix.Length).Replace('\','/')
                $name = $top + '/' + $relative
                foreach ($part in $name.Split('/')) {
                    if ($part -eq '' -or $part -eq '.' -or $part -eq '..' -or $part.Contains(':') -or $part.EndsWith('.') -or $part.EndsWith(' ') -or $part.IndexOfAny([IO.Path]::GetInvalidFileNameChars()) -ge 0 -or $part -match '^(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])(?:\.|$)') { throw 'Unsafe archive name' }
                }
                if (-not $names.Add($name)) { throw 'Duplicate archive name' }
                $files.Add([pscustomobject]@{ Path=$full; Name=$name; Length=0L })
            }
        }
    }
    if ($files.Count -eq 0) { throw 'Bundle contains no files' }
    $temp = Join-Path $dist ('.portable-' + [guid]::NewGuid().ToString('N') + '.tmp')
    $ownsTemp = $false
    try {
        $output = [IO.FileStream]::new($temp, [IO.FileMode]::CreateNew, [IO.FileAccess]::ReadWrite, [IO.FileShare]::None)
        $ownsTemp = $true
        try {
            $archive = [IO.Compression.ZipArchive]::new($output, [IO.Compression.ZipArchiveMode]::Create, $true)
            try {
                foreach ($item in ($files | Sort-Object Name)) {
                    & $checkPath $item.Path
                    if (-not ([IO.Path]::GetFullPath($item.Path)).StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase)) { throw 'Path escaped bundle' }
                    $input = [IO.FileStream]::new($item.Path, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read)
                    try {
                        $item.Length = $input.Length
                        # Explicit forward-slash FullName; no filesystem-derived ZIP names.
                        $entry = $archive.CreateEntry($item.Name, [IO.Compression.CompressionLevel]::Optimal)
                        $target = $entry.Open()
                        try { $input.CopyTo($target, 65536) } finally { $target.Dispose() }
                        if ($input.Position -ne $item.Length -or $input.Length -ne $item.Length) { throw 'Input changed while archiving' }
                    } finally { $input.Dispose() }
                }
            } finally { $archive.Dispose() }
            $output.Flush($true)
            $output.Position = 0
            # Validate the completed temporary ZIP using the actual .NET reader.
            $archive = [IO.Compression.ZipArchive]::new($output, [IO.Compression.ZipArchiveMode]::Read, $true)
            try {
                if ($archive.Entries.Count -ne $files.Count) { throw 'ZIP entry count mismatch' }
                $expected = @{}
                foreach ($item in $files) { $expected.Add($item.Name, $item.Length) }
                $buffer = New-Object byte[] 65536
                foreach ($entry in $archive.Entries) {
                    if ($entry.FullName.Contains('\') -or -not $expected.ContainsKey($entry.FullName) -or $entry.Length -ne $expected[$entry.FullName]) { throw 'ZIP entry validation failed' }
                    $input = $entry.Open()
                    try {
                        $count = 0L
                        while (($n = $input.Read($buffer, 0, $buffer.Length)) -gt 0) {
                            $count += $n
                            if ($count -gt $entry.Length) { throw 'ZIP expanded length mismatch' }
                        }
                        if ($count -ne $entry.Length) { throw 'ZIP expanded length mismatch' }
                    } finally { $input.Dispose() }
                    $expected.Remove($entry.FullName)
                }
                if ($expected.Count -ne 0) { throw 'ZIP inventory mismatch' }
            } finally { $archive.Dispose() }
        } finally { $output.Dispose() }
        & $checkPath $zip
        # Same-directory atomic publication. Never delete the old ZIP first.
        if ([IO.File]::Exists($zip)) { [IO.File]::Replace($temp, $zip, [NullString]::Value) }
        else { [IO.File]::Move($temp, $zip) }
        $ownsTemp = $false
    } finally {
        if ($ownsTemp -and [IO.File]::Exists($temp)) { [IO.File]::Delete($temp) }
    }
}
