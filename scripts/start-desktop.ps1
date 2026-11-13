# Portable launcher: no service installation, no SSH login, no terminal activation.
[CmdletBinding()]
param(
    [string]$BundleRoot,
    [string]$DataRoot,
    [switch]$PrepareOnly
)
$ErrorActionPreference = 'Stop'
if (-not $PSBoundParameters.ContainsKey('BundleRoot')) {
    $BundleRoot = Split-Path -Parent (Split-Path -Parent $PSCommandPath)
}
$BundleRoot = [IO.Path]::GetFullPath($BundleRoot)
$cli = Join-Path $BundleRoot 'xshctl.exe'
$desktop = Join-Path $BundleRoot 'xshdesk.exe'
if (-not (Test-Path -LiteralPath $cli)) { throw "Missing portable CLI: $cli" }
if (-not $PrepareOnly -and -not (Test-Path -LiteralPath $desktop)) { throw "Missing desktop: $desktop" }
function Quote-Argument([string]$value) {
    if ($value.Contains('"') -or $value.Contains("`n") -or $value.Contains("`r")) { throw 'Invalid argument path' }
    return '"' + $value.TrimEnd('\') + '"'
}
if (-not $DataRoot) { $DataRoot = Join-Path $env:LOCALAPPDATA 'XshellBridge-local' }
$runtime = Join-Path ([IO.Path]::GetFullPath($DataRoot)) 'runtime'
foreach ($agentFile in @('xshell_agent.js','remote_bootstrap.js','log_parser.js','XshLogCapture.exe')) {
    if (-not (Test-Path -LiteralPath (Join-Path $BundleRoot $agentFile) -PathType Leaf)) { throw "Missing portable agent: $agentFile" }
}
$configFiles = @('bridge.json','client.json','agent.json','admin.json')
$present = @($configFiles | Where-Object { Test-Path -LiteralPath (Join-Path $runtime $_) })
if ($present.Count -eq 0) {
    & $cli init --dir $runtime
    if ($LASTEXITCODE -ne 0) { throw 'Initialization failed; existing files were retained' }
} elseif ($present.Count -ne $configFiles.Count) {
    throw "Incomplete runtime configuration; nothing overwritten: $runtime"
}
Write-Output "Runtime: $runtime"
if ($PrepareOnly) { return }

$clientPath = Join-Path $runtime 'client.json'
function Test-Bridge {
    $previousPreference = $ErrorActionPreference
    try {
        $ErrorActionPreference = 'Continue'
        & $cli --config $clientPath doctor *> $null
        return $LASTEXITCODE -eq 0
    } finally { $ErrorActionPreference = $previousPreference }
}
$cfg = Get-Content -LiteralPath $clientPath -Raw -Encoding UTF8 | ConvertFrom-Json
$endpoint = [uri]$cfg.endpoint
if ($endpoint.Scheme -ne 'http' -or $endpoint.Host -notin @('127.0.0.1','[::1]','::1') -or $endpoint.AbsolutePath -ne '/' -or $endpoint.Query -or $endpoint.Fragment -or $endpoint.UserInfo) { throw 'Bridge endpoint must be numeric loopback HTTP' }
# Authenticated doctor prevents accidentally reusing another instance on this port.
$healthy = Test-Bridge
$healthyBeforeStart = $healthy
if ($healthyBeforeStart) {
    Write-Output '已通过当前配置的认证健康检查，复用当前 Bridge；将打开新的桌面窗口。版本不一致时请人工核对，不会自动终止或重启 Bridge。'
}
if (-not $healthy) {
    $probe = New-Object Net.Sockets.TcpClient
    $occupied = $false
    try {
        $pending = $probe.ConnectAsync($endpoint.DnsSafeHost,$endpoint.Port)
        $occupied = $pending.Wait(1000) -and $probe.Connected
    } catch { $occupied = $false } finally { $probe.Dispose() }
    if ($occupied) { throw '端口已被占用，但认证健康检查未通过，无法确认是可复用的 Bridge。请核对配置或监听冲突；未终止或重启任何进程。' }
    # Only the CLI OS guard and PID liveness check may decide marker recovery.
    $originalPID = $null
    $markerPath = Join-Path $runtime 'bridge.lock'
    $hadOriginalMarker = Test-Path -LiteralPath $markerPath
    if (Test-Path -LiteralPath $markerPath -PathType Leaf) {
        $markerItem = Get-Item -LiteralPath $markerPath
        if (-not ($markerItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -and $markerItem.Length -le 32) {
            $markerText = [IO.File]::ReadAllText($markerPath)
            if ($markerText -cmatch '\A[1-9][0-9]*\n?\z') {
                $parsedPID = 0
                if ([int]::TryParse($markerText.TrimEnd("`n"), [ref]$parsedPID)) { $originalPID = $parsedPID }
            }
        }
    }
    $bridgeProcess = Start-Process -FilePath $cli -ArgumentList @('serve','--dir',(Quote-Argument $runtime)) -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $runtime 'desktop-bridge.stdout.log') -RedirectStandardError (Join-Path $runtime 'desktop-bridge.stderr.log')
    for ($attempt=0; $attempt -lt 20; $attempt++) {
        Start-Sleep -Milliseconds 250
        if ($bridgeProcess.HasExited) { break }
        if (Test-Bridge) { $healthy = $true; break }
    }
    if (-not $healthy) {
        $pidNotice = if ($hadOriginalMarker) { '原始 PID 缺失或格式异常，请人工核查标记；不要直接删除锁。' } else { '启动前不存在 bridge.lock；请查看本机私有错误日志，核查启动失败原因。' }
        if ($null -ne $originalPID) {
            $oldProcess = Get-Process -Id $originalPID -ErrorAction SilentlyContinue
            if ($null -ne $oldProcess) {
                $pidNotice = "原始 PID $originalPID 当前仍存在，进程可能正在启动或属于另一实例，不能作为陈旧锁处理；进程身份须人工核对，不要删除锁。"
            } else {
                $pidNotice = "原始 PID $originalPID 当前未查到；这不是已退出的权威证明，以 CLI 的系统检查为准。"
            }
        }
        $statusNotice = if ($bridgeProcess.HasExited) { 'Bridge 子进程在启动期间已退出。' } else { 'Bridge 尚未通过认证健康检查。' }
        $errorLog = Join-Path $runtime 'desktop-bridge.stderr.log'
        throw "$statusNotice $pidNotice 请核对配置或监听冲突，并在本机查看私有错误日志：$errorLog 。未自动重试、重启、终止进程或删除锁；版本不一致请人工处理。"
    }
}
Start-Process -FilePath $desktop -ArgumentList @('--mode','Local','--dir',(Quote-Argument $runtime)) | Out-Null
Write-Output 'Desktop opened. Closing its window stops its Bridge and owned tunnels.'
