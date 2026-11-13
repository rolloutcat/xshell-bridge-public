# 只读巡检配方

先阅读 [SKILL.md](../SKILL.md) 的目标、授权和不重放规则。

Python 日志分析、JSON/正则处理和状态采集仍可使用，复杂或动态脚本走逐次人工审批。明确命中危险规则才拒绝；不要把所有 Python 当作危险，也不要把未命中 deny 当作自动授权。区分方式见 [状态与错误](../SKILL.md#状态与错误)。

## 设计新任务

从用户需要的事实出发，仅选择必要的低风险只读命令。首次设计新命令可按需调用 `policy show` / `get_command_policy` 查询当前目录与开关，必要时用 `explain` / `explain_command` 分析具体新提交；不用为每条命令重复查询。目录提供示例而非授权，目标及开关可能变化，最终由 Bridge 判定。用户明确给出命令时保留原样；未明确指定时，可在首次提交前将语义和依赖独立的检查合理拆分，避免 `printf` 装饰或无关 Python、循环、重定向使整个任务转人工。不能拆分有依赖的操作、改变诊断目标，复杂检查需要时按正常审批提交。已 pending、denied 或 unknown 不得改写、拆分或更换 request ID 逃避审批；继续跟踪原任务。不要自动创建授权或开启开关。

## Linux 日常检查配方

日常排查按用户任务选取下面的相关命令，不默认执行整张表。只有用户请求主机环境盘点时，才按“少量基础检查 → 依据发现选择必要深化项 → 汇总”的流程推进。一次绑定一个实际已激活目标，多目标逐个绑定，不能假定批量入口。以下是远端 shell 命令文本，逐条提交或按需用 UTF-8 命令文件；不是让 PowerShell 在宿主机执行。

| 目的 | 远端命令示例 |
|---|---|
| 身份与系统 | `hostname; whoami; id; uname -sr; uptime` |
| 资源 | `free -h; df -h; nproc` |
| 进程摘要 | `ps -eo pid,comm,pcpu,pmem --sort=-pcpu \| head -n 25` |
| 监听端口 | `ss -ltn \| head -n 20` |
| 服务 | `systemctl --no-pager show --property=ActiveState,SubState nginx.service`，将服务名替换为任务中已核实的单个字面 unit；或 `systemctl --no-pager list-units --type=service --no-legend \| head -n 100`。 |
| 列目录 | `ls -lh /var/log` |
| 大日志末尾 16 MiB | `tail -c 16777216 /var/log/app.log \| grep -F ERROR` |
| 有限时长查找 | `timeout -k 2s 25s grep -F -n -m 1 -B 8 -A 65 ERROR /var/log/app.log`，任务 timeout_ms 至少 27000，建议留余量如 35000。 |
| 日志文件 | `tail -n 200 /var/log/app.log \| grep -F 'timeout'` |
| 服务日志 | `journalctl --no-pager -n 50 -u nginx.service` |

以上是独立基础检查：分别作为新任务提交，显式使用 `cwd /`、`timeout_ms 60000` 和各自唯一 request ID，不加 printf 标题或把无关脚本拼在一起。仅在用户实际任务需要时选择；目标必须有效，默认包可能被停用，最终以 Bridge 判定为准。已 pending、denied 或 unknown 的任务不能拆分或改写以绕过审批。

## 日志与文件命令选择

先沿用上下文已确认的文件或服务，并明确时间和读取范围。以下路径和服务是示例，不能当作已发现的真实目标。v4 允许任意字面文件路径；不用为普通读取另建授权。首次设计命令时按目的选择，用户给定的原命令仍保持原样。

| 需要的事实 | 远端命令示例 |
|---|---|
| 最近 200 行 | `tail -n 200 /var/log/app.log` |
| 文件开头 80 行 | `head -n 80 /var/log/app.log` |
| 字面关键词的前 100 条匹配，保留原文件行号 | `grep -F -n -m 100 -e 'timeout' /var/log/app.log` |
| 最近 300 行范围内的错误 | `tail -n 300 /var/log/app.log \| grep -E -e 'ERROR\|WARN'` |
| 整个文件中最后 50 条匹配，保留原文件行号 | `grep -n -E -e 'ERROR\|WARN' /var/log/app.log \| tail -n 50` |
| 整个文件的匹配行数 | `grep -F -c -e 'timeout' /var/log/app.log` |
| 最近一小时内最多 100 条服务日志 | `journalctl -u nginx.service --since '1 hour ago' -n 100 --no-pager` |
| 明确需要文件全文 | `cat /etc/os-release` |

字面错误信息、请求 ID、路径优先用 grep -F；确需正则才用 -E。优先直接 grep 文件，只有需要先限定末尾窗口时才组合 tail 管道。用 -e 传模式、用 -- 分隔前导连字符文件名；包含空格或中文的路径使用字面引号。复杂引号或多行通过 UTF-8 command-file 提交，避免 PowerShell 改变远端原文，见 [命令速查](commands.md)。不增加装饰性 printf、循环、awk 或 Python 来完成简单读取。

grep -m 限制的是每个文件从开头遇到的匹配数，不代表最新匹配；统计完整匹配行数时不加 -m，输出被截断的片段也不能用来推断总量。grep 退出 1 表示本次范围内无匹配，2 表示错误；读取失败不能报告为“无异常”。管道退出码通常来自末段，还需检查文件不存在或权限错误。汇报时注明文件、时间或行数范围及是否截断，局部无匹配不代表全部日志无异常。

默认目录支持的引号和管道是有限语法。任意路径不代表自动支持通配符、变量或解释器；需要这些写法时正常提交审批。持续跟随、分页器、重定向、替换和写操作不在默认读取范围。递归 grep 仅用于任务需要的明确目录；默认选择具体文件和有限输出。完整当前边界可按需用 policy show / get_command_policy 查询一次。

汇总已观察到的版本、资源、服务和检查范围，明确区分事实与推测、未知与失败；不默认读取全量配置、凭据、数据库或患者数据，不查询云 metadata 或跨目标 kubectl 上下文。不自动创建资产、写资产描述、安装软件或打开 SSH；保留已有手工备注，不扩大目标范围。
