# 客户端命令与 PowerShell 示例

先阅读 [SKILL.md](../SKILL.md) 的目标、授权和不重放规则。

首次按需求读取文件时可按需查询一次 `policy show`；优先明确文件并使用 head/tail 或 grep -m 限制输出，不改写用户原命令。v5 支持读取任意字面路径，递归 grep 可能读取大量文件，远端权限、PATH 和符号链接仍按实际语义处理。

巡检示例及审批限制见 [Linux 巡检配方](inspection.md)；这些示例不会替代用户的原命令自动执行，实际后台可能仍为旧版或已停用。

## 客户端命令速查

以下 CLI 均以 `xshctl --config <client.json>` 开头；选项必须放在对应命令的定位参数之后、远端命令之前。`--` 后的参数由 CLI 用空格连接为远端 shell 命令；含管道的命令在 PowerShell 中作为一个单引号字符串传入，含引号或多行优先用 UTF-8 文件避免原生程序参数转义差异。客户端参数如下：

| 用途 | CLI | 关键结果 / MCP |
|---|---|---|
| 会话 | `list sessions [--name NAME] [--environment ENV] [--state STATE]`；`get session ID` | 客户端过滤，名称及环境区分大小写且精确；会话有 `id`、`generation`、`name`、`environment`、`fingerprint`、`state`、`last_seen`；MCP 用 `list_sessions {}`，无单会话工具。 |
| 解析 | `resolve session --name NAME [--environment ENV] [--fuzzy]` | `status`、`match_kind`、`requires_selection`、`candidates`；MCP `resolve_session` 使用 `name`、`environment`、`fuzzy`。 |
| 任务列表 | `list jobs [--session-id ID] [--request-id RID] [--status STATUS]` | 客户端对完整数组按精确条件做 AND，不减少传输；job 数组，包含 `id`、`session_id`、`generation`、`request_id`、`command`、`cwd`、`timeout_ms`、`status`，不含输出内容；MCP 无 `list_jobs`。仅按需使用，避免暴露无关命令。 |
| 危险策略与默认命令目录 | `policy show` | MCP `get_command_policy {}`；读取 `hard_deny`（固定危险策略）及当前只读开关、分类示例和限制，不执行或授权、不保证未来状态；按需一次即可。 |
| 诊断 | `doctor` | Bridge `version`、`protocol`、能力字段和 `output_fidelity`，以及本地 `local_process_status`；无 MCP doctor。 |
| 提交 | `exec ID --generation G --type xshell --request-id RID --cwd / --timeout-ms 15000 -- hostname` | 明确危险返回 `COMMAND_DENIED`（CLI 退出 1，无新 job）；其余返回 job 对象，保存 `id`、`request_id`、`status`；未提供 request ID 时 CLI 生成并打印到 stderr。MCP `exec` 输入为 `session_id`、`generation`、`request_id`、`cwd`、`timeout_ms`、`command`，无 `type` 字段。 |
| 解释 | `explain ID --generation G --cwd / --timeout-ms 15000 -- hostname` | `blocked/command_denied` 为明确危险拒绝；`manual/exact_approval_required` 为逐次人工审批。另有 `decision`、`reason_code`、`message`、可选 `matched_id` 和 `checks`；MCP `explain_command`。CLI 的 exec 和 explain 也支持 `--command-file PATH` 代替 `-- command`，不可混用；默认 timeout 为 60000 ms。 |
| 观察 | `job get JOB`；`job wait JOB [--wait-ms 30000]` | get 返回 job 快照；wait 返回 `job`、`terminal`、`timed_out`，不带输出内容。MCP `get_job` / `wait_job` 参数分别为 `id`、可选 `wait_ms`。 |
| 输出 | `job output JOB [--cursor 0]` | `output`、`next_cursor`（字节偏移）、`status`、`exit_code`、`truncated`；MCP `read_output` 用 `id`、可选 `cursor`。 |
| 中断 / 脱离 | `job interrupt JOB`；`detach ID G` | MCP 分别为 `interrupt_job {"id":"JOB"}` 和 `detach_session {"id":"ID","generation":"G"}`；影响见 [状态与错误](../SKILL.md#状态与错误)。 |

CLI 另有 `list audit`、`version`、`help`；离线帮助支持 `xshctl help job wait`、`xshctl exec --help` 等；审计可能包含命令摘要，仅在任务需要时查询。普通请求成功退出 0、错误退出 1；exec pending 退出 3 但 JSON 已返回已登记任务。其他退出码见 [状态与错误](../SKILL.md#状态与错误)。`xshctl init` 是服务端私有 Bridge 初始化，不是主机盘点；没有盘点 init、batch、cp 或 MCP `list_jobs`。

v5 的 `read_limits.byte_maximum` 为 67108864，`numeric_maximum` 为 1000000；当前后台未返回 v5 时不假定新增范围可自动批准。有限 timeout 的主时限与强杀宽限必须合计不超过 `--timeout-ms`。

## PowerShell 端到端示例

日志与文件任务先按 [命令选择](inspection.md#日志与文件命令选择) 确定范围。含正则、内层引号、空格路径或多行的远端命令优先写入 UTF-8 `--command-file`，按下方示例原样提交；MCP 使用 command 字符串。不要为了命中自动审批删除用户指定的参数或改变时间范围。

将路径和目标名称换成实际值。用户已经手动接入、核对并激活目标；执行前核对解析结果的完整身份。下例仅示范一次提交；如果响应丢失，按 [SKILL.md](../SKILL.md) 的只读查找流程处理，不重跑脚本。

```powershell
$exe = 'C:\path\to\xshctl.exe'
$cfg = 'C:\path\to\client.json'
$resolvedText = & $exe --config $cfg resolve session --name '目标名称' --environment '环境名称'
if ($LASTEXITCODE -ne 0) { throw '目标未唯一精确匹配；停止并核对' }
$resolved = $resolvedText | ConvertFrom-Json
if ($resolved.status -cne 'matched' -or $resolved.match_kind -cne 'exact' -or $resolved.requires_selection -ne $false -or @($resolved.candidates).Count -ne 1) { throw '不是唯一精确匹配，停止并核对' }
$bound = $resolved.candidates[0] # 在此核对实际名称、环境、完整指纹及身份
if ($bound.state -cne 'active' -or -not $bound.id -or -not $bound.generation -or -not $bound.fingerprint) { throw '身份未就绪' }
$freshText = & $exe --config $cfg get session $bound.id
if ($LASTEXITCODE -ne 0) { throw '会话刷新失败' }
$fresh = $freshText | ConvertFrom-Json
if ($fresh.state -cne 'active' -or $fresh.id -cne $bound.id -or $fresh.generation -cne $bound.generation -or $fresh.fingerprint -cne $bound.fingerprint -or $fresh.name -cne $bound.name -or $fresh.environment -cne $bound.environment) { throw '目标变化，停止并重新核对' }
$rid = [guid]::NewGuid().ToString()
$jobText = & $exe --config $cfg exec $bound.id --generation $bound.generation --type xshell --request-id $rid --cwd / --timeout-ms 15000 -- 'hostname; uname -sr'
$submitCode = $LASTEXITCODE
if ($submitCode -ne 0 -and $submitCode -ne 3) { throw "提交结果不明或失败，保留 request ID $rid 并只读核查" }
$job = $jobText | ConvertFrom-Json
$jobId = $job.id
if (-not $jobId) { throw "缺少 job ID，保留 request ID $rid 并只读核查" }
if ($job.status -eq 'pending') { Write-Host "请在工作台核对审批 job $jobId；无需回复，我会跟踪原任务。" }
$waitText = & $exe --config $cfg job wait $jobId --wait-ms 30000
$waitCode = $LASTEXITCODE
if ($waitCode -ne 0 -and $waitCode -ne 2) { throw "查询失败，保留 job ID $jobId" }
$wait = $waitText | ConvertFrom-Json
if ($wait.timed_out) { Write-Host "继续有界等待原 job $jobId；当前状态 $($wait.job.status)" }
elseif ($wait.job.status -eq 'completed') {
    $cursor = 0
    do {
        $pageText = & $exe --config $cfg job output $jobId --cursor $cursor
        if ($LASTEXITCODE -ne 0) { throw "输出查询失败，保留 job ID $jobId 和 cursor $cursor" }
        $page = $pageText | ConvertFrom-Json
        Write-Host -NoNewline $page.output
        $previous = $cursor
        $cursor = $page.next_cursor
        if ($page.truncated) { Write-Warning '终端输出已截断' }
    } while ($cursor -gt $previous)
    Write-Host "远端 exit_code: $($wait.job.exit_code)"
} else { Write-Host "原 job $jobId 终态: $($wait.job.status)；不要重放" }
```

`wait` 退出 2 是本次观察超时，可在活跃回合继续对原 ID 有界等待；上述示例为简洁只等待一次。退出 0 包含 `unknown`，只有 `completed` 的远端 `exit_code` 是命令退出状态。管道例如 `-- 'ss -ltn | head -n 20'` 的单引号防止 PowerShell 解释；该命令仍由 Bridge 判定审批。

多行时在本机写 UTF-8 文件并传路径（以下沿用已核对的 `$exe`、`$cfg`、`$bound`；结果按上例同一 job 流程跟踪）：

```powershell
$scriptPath = 'C:\path\to\inspection.sh'
$scriptText = @'
hostname
uname -sr
free -h
'@
[System.IO.File]::WriteAllText($scriptPath, $scriptText, [System.Text.UTF8Encoding]::new($false))
$freshText = & $exe --config $cfg get session $bound.id
if ($LASTEXITCODE -ne 0) { throw '会话刷新失败' }
$fresh = $freshText | ConvertFrom-Json
if ($fresh.state -cne 'active' -or $fresh.id -cne $bound.id -or $fresh.generation -cne $bound.generation -or $fresh.fingerprint -cne $bound.fingerprint -or $fresh.name -cne $bound.name -or $fresh.environment -cne $bound.environment) { throw '目标变化，停止并重新核对' }
$rid2 = [guid]::NewGuid().ToString()
$jobText = & $exe --config $cfg exec $bound.id --generation $bound.generation --type xshell --request-id $rid2 --cwd / --timeout-ms 15000 --command-file $scriptPath
$submitCode = $LASTEXITCODE
if ($submitCode -ne 0 -and $submitCode -ne 3) { throw "提交结果不明或失败，保留 request ID $rid2 并只读核查" }
$job = $jobText | ConvertFrom-Json
$jobId = $job.id
if (-not $jobId) { throw "缺少 job ID，保留 request ID $rid2 并只读核查" }
Write-Host "跟踪原 job $jobId，状态 $($job.status)"
```
