param([string]$GoExe='go', [Parameter(Mandatory=$true)][string]$Destination)
$ErrorActionPreference='Stop'
# Caller must supply a fresh build-staging destination, never a source directory.
$dest=[IO.Path]::GetFullPath($Destination)
if(Test-Path -LiteralPath $dest){throw 'License destination must not already exist'}
$root=& $GoExe list -m -f '{{.Path}}'
if($LASTEXITCODE -ne 0){throw 'Cannot determine root module'}
$modules=@(& $GoExe list -deps -f '{{with .Module}}{{.Path}}|{{.Version}}|{{.Dir}}{{end}}' ./cmd/xshctl | Where-Object { $_ } | Sort-Object -Unique)
if($LASTEXITCODE -ne 0){throw 'Cannot enumerate Go modules; download selected dependencies first'}
$goroot=& $GoExe env GOROOT
if($LASTEXITCODE -ne 0){throw 'Cannot determine GOROOT'}
$goLicense=Join-Path $goroot 'LICENSE'
if(-not(Test-Path -LiteralPath $goLicense -PathType Leaf)){throw 'Go LICENSE missing'}
New-Item -ItemType Directory -Path $dest | Out-Null
New-Item -ItemType Directory -Path (Join-Path $dest 'go') | Out-Null
Copy-Item -LiteralPath $goLicense -Destination (Join-Path $dest 'go\LICENSE')
$urls=New-Object 'System.Collections.Generic.List[string]'
$urls.Add("module`tversion`tsource`tlicense_directory")
$urls.Add("go`ttoolchain`thttps://go.dev/`tgo")
$i=0
foreach($line in $modules){
    $parts=$line -split '\|',3
    if($parts.Count -ne 3){throw 'Invalid module enumeration'}
    if($parts[0] -eq $root){continue}
    $module=$parts[0]; $version=$parts[1]; $directory=$parts[2]
    if(-not $version -or -not $directory -or -not(Test-Path -LiteralPath $directory -PathType Container)){throw "Selected module unavailable: $module $version"}
    $licenses=@(Get-ChildItem -LiteralPath $directory -File | Where-Object {$_.Name -like 'LICENSE*' -or $_.Name -like 'COPYING*'})
    if($licenses.Count -eq 0){throw "Root license missing: $module $version"}
    $i++; $relative='module-'+$i
    $target=Join-Path $dest $relative
    New-Item -ItemType Directory -Path $target | Out-Null
    foreach($license in $licenses){Copy-Item -LiteralPath $license.FullName -Destination $target}
    $source='https://pkg.go.dev/'+$module+'@'+$version
    $urls.Add("$module`t$version`t$source`t$relative")
}
[IO.File]::WriteAllLines((Join-Path $dest 'sourceURLs.tsv'),$urls,(New-Object Text.UTF8Encoding($false)))
