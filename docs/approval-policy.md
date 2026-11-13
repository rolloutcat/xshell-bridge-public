# 危险拒绝、人工审批与默认只读规则

[返回总入口](../README.md) · [使用指南](user-guide.md#审批与任务跟踪)

## 三类处理结果

Bridge 在所有授权之前运行固定策略 `hard-deny-v1`，与默认只读包的开关独立，不能通过关闭只读包、临时授权、自定义参数规则或人工批准覆盖。

| 结果 | 含义与后续行为 |
|---|---|
| 危险拒绝 | 已识别出明确危险操作。新提交返回 HTTP 403 / `COMMAND_DENIED`，不创建待审批任务；CLI 非零退出，MCP 返回工具错误。`explain` 返回 `blocked`、`reason_code=command_denied` 和具体规则 ID。人工批准一次、记住或批量批准均不能覆盖。 |
| 人工审批 | 未命中明确危险规则，但无可用自动授权；或脚本、动态参数、未知选项、解析失败导致静态检查不能确定内容。新任务为 `pending`，可批准一次或拒绝。需要逐次审批的任务标记 `requires_exact_approval=true`，不复用或创建记住授权，不命中自定义参数规则。**无法检查不等于已确认危险。** |
| 自动放行 | 未命中拒绝或逐次审批要求，命中默认只读包，或现有精确授权/参数规则。只读包仍只覆盖下文的完整字面命令，不自动放行 Python 或数据库查询。 |

提交、单条/批量批准及派发前均使用同一检查器。派发前发现已排队任务命中危险规则，将其持久化为 `denied` 并写入 `policy_deny` 审计；存储失败时不派发。批量批准先检查全部选中项，任意危险项导致整批失败。已经派发、运行或结果未知的任务不自动中断、不重放；重复 request_id 仍查询原任务，不改写历史批准来源。操作员手动拒绝仍使用 `USER_DENIED`，与代码策略拒绝区分。

## 明确危险操作范围

| 类别 | 当前拒绝范围 |
|---|---|
| 文件、磁盘 | `rm` 同时具有递归与强制参数（组合、拆分、长参数及不同顺序）；递归删除 `/`、`/etc`、`/usr`、`/var` 等关键目录；mkfs/mkfs.*、mke2fs、mkswap、wipefs、shred、blkdiscard、fdisk、sfdisk、parted、sgdisk；`dd of=/dev/...` 和直接写设备的重定向（标准输出、标准错误、`/dev/null` 除外）；`find -delete`；递归修改关键目录权限/归属。 |
| 主机与进程 | shutdown、reboot、poweroff、halt、killall、pkill；systemctl 的整机电源、休眠、救援或隔离操作；init/telinit 0 或 6；kill 的全进程/当前进程组目标。普通单服务 restart、普通指定 PID 的 kill 仍需相应审批。 |
| SQL | mysql/mariadb `-e`、psql `-c`、sqlcmd `-Q/-q`、sqlite3 内联 SQL，以及可读取的 heredoc。拒绝 DROP、TRUNCATE、GRANT/REVOKE、用户/角色/实例级配置变更、没有本层 WHERE 的 UPDATE/DELETE、PREPARE/EXECUTE/CALL/DO 等间接执行、COPY/OUTFILE/DUMPFILE/LOAD 等导出或加载入口。逐条检查多语句，区分引号、注释和括号层级；子查询的 WHERE 不能替代外层条件。 |
| Redis | FLUSHALL/FLUSHDB、SHUTDOWN、DEBUG、复制关系/故障切换/库交换、模块或迁移操作；CONFIG/ACL/CLUSTER 写操作；SCRIPT/FUNCTION 的清除、删除或中断。可见 Lua 中直接调用这些命令也拒绝。未知 Lua、EVALSHA/FCALL 和脚本管理转逐次人工审批。 |
| MongoDB | mongo/mongosh 的可见内联脚本中显式 dropDatabase/dropCollection/drop、deleteMany/remove、shutdownServer 和用户权限管理调用。未识别的 JavaScript 保持逐次人工审批。 |
| 容器、集群与基础设施 | Docker/Podman 删除、prune、kill、Compose down/rm/kill；kubectl delete/drain/debug、replace --force；terraform destroy/taint/force-unlock。常规 restart/apply/patch 等不因工具名称被直接拒绝；嵌套执行需人工审批。 |
| etcd、Kafka、对象存储 | etcdctl 范围删除、权限/成员/快照等管理变更；Kafka 删除主题/记录/消费组、执行重置位点或修改 ACL；AWS S3 递归删除、删桶，s3api 批量删除和桶策略变更。 |
| Python | 保留日志读取、正则统计、JSON 输出、语法编译检查和状态采集。显式 rmtree/removedirs/unlink/rmdir/truncate、os.remove，以及可见的 subprocess/os.system 危险命令、数据库 execute 危险 SQL 命中拒绝规则。其他 Python 保持逐次人工审批。 |

Shell 使用独立的 Bash 语法树解析，检查复合命令、管道、替换、分支及函数体，并处理常见 sudo/env/command/exec/nohup/timeout/nice/xargs 包装和字面 `sh -c`。即使分支或函数未执行，其中的明确危险操作仍可能被拒绝。普通文本不当作命令，例如 `grep -F 'rm -rf /' /tmp/log` 不命中删除规则。

这是**确定性风险检查加人工审批**，不是执行沙箱或完整的多语言安全分析器。Python/JavaScript 检查识别显式调用，不解析全部别名、动态分派或运行时生成内容；SQL 为保守的词法与层级分类，不替代各数据库语法解析器，也不能证明带 WHERE 的修改范围安全。外部脚本文件、解码执行、变量生成的命令、未知客户端选项、未支持的方言和传入数据库客户端的外部 stdin 均需人工检查。不要据“未命中 deny”推断安全，也不要改写命令绕过拒绝。

历史命令仅用于离线检查误拦，不执行、不重新审批、不改变原任务状态。Python 历史流程的保留不授予今后脚本自动执行权限。源码更新不会替换运行中的旧 Bridge；实际策略以 `xshctl policy show` / `get_command_policy` 返回的 `hard_deny` 为准。

## 默认包与授权范围

默认基础只读检查包 `builtin-readonly-v5` 是独立实现的确定性规则，不调用模型、不付费、不引入 OpsKat 依赖；设计来源见 [OpsKat 对照](opskat-comparison.md)。v5 随 0.8.9 引入，在 v4 通用读取基础上扩充 ls、字节参数和有限 timeout，运行中的后台仍须单独核对版本与规则状态。全新安装和已有状态中缺少设置时默认启用；操作员明确停用后持久保存，重启不重新开启。审批中心和自动审批授权页显示状态、覆盖范围和切换按钮。只有操作员固定 IPC 可以切换；CLI/MCP/client/agent 没有管理权限。客户端可用 `xshctl policy show` 或 MCP `get_command_policy` 只读发现当前开关、规则示例与限制；固定 `/v1/policy` 仍仅返回可比较快照。目录不执行、不授权，也不保证后续目标状态。

终端必须已经人工核对并激活，Session ID/generation 完全匹配，未归档、未阻断、指纹完整且心跳在 15 秒内。默认匹配仅适用于新提交，永远不补批旧 pending，也不重放已经派发或 unknown 的任务。停用时，尚未派发的默认批准任务退回 pending；已派发任务不变。重新启用也不重新批准这些任务。自定义临时规则和精确授权不受开关影响。

## 基础巡检命令

| 程序 | 完整参数（逐项精确匹配） |
|---|---|
| hostname | 无参数或 `-f` |
| whoami、pwd、uptime | 无参数 |
| id | 无参数、`-u`、`-g`、`-G`、`-un`、`-gn` |
| uname | `-a`、`-s`、`-r`、`-sr`、`-n`、`-m` |
| date | 无参数、`-u`、`--utc`、`-Iseconds`、`+%F`、`+%T`；不允许设置时间或文件输入 |
| free | `-h`、`-m`、`-g` |
| df | `-h`、`-hT`、`-T`、`-i`、`-ih`、`-hi`；无路径参数 |
| nproc、lscpu | 无参数 |
| vmstat | `1 1` |
| ps | `-eo pid,comm,pcpu,pmem --sort=-pcpu`；不显示完整参数或环境 |
| ip | `address show`、`addr show`、`-brief address show`、`link show`、`route show`、`route list`、`-6 route show` |
| ss | `-ltn`、`-lntu`、`-ltnp`、`-lntp`、`-lntup`、`-lnutp`、`--numeric --listening --tcp`、`--numeric --listening --udp`；进程信息需远端权限 |
| systemctl | `--no-pager status`、`--no-pager is-active`、`--no-pager is-enabled` 后接一个字面 `<unit.service>`；ASCII 字母数字开头，主体仅字母数字、`_ . @ -`，主体最多 120 字节；`show` 必须指定一个 unit 和一个属性选项（`-p 属性`、`--property 属性` 或 `--property=属性`），unit 与选项可以换序；`--no-pager` 可位于子命令前后。`show` 的属性仅限 `ActiveState`、`SubState`、`LoadState`、`UnitFileState`、`MainPID`、`Result`、`ExecMainStatus`、`Description`，不重复；`--no-pager list-units --type=service --no-legend` 或 `--no-pager list-unit-files --type=service --no-legend`；选项可换序，list-units 可加 `--failed`，不得重复或添加其他参数 |


## 文件与服务日志读取

| 程序 | 读取形式 |
|---|---|
| ls | 无参数或短选项 `-l -a -h -A -d -t -r -S -1 -F -p -i -s -n`（可组合），以及 `--all --almost-all --directory --human-readable --numeric-uid-gid --reverse --size --inode --classify --color=never`；最多 32 个字面路径，省略路径时列当前目录。`--` 后可使用前导 `-` 的文件名。递归、未知选项和通配符不自动审批。 |
| cat | 不带选项或短选项 `-n -b -s -A -E -T -v`（可组合）、对应 GNU 长选项；至少一个文件。 |
| head、tail | 默认或 `-n` / `-c`、`--lines` / `--bytes`，支持独立或相连计数。行数为 0–1000000，字节数为 0–67108864（64 MiB），均为十进制整数，不接受 K/M 等后缀；可带正负号，原样交给远端 GNU 工具：如 tail `+N` 从第 N 行/字节起，head `-N` 除去末尾 N 行/字节。文件可多个。`-f`、`-F`、`--follow`、重试和 PID 跟随不自动审批。 |
| grep | `-n -i -v -E -F -w -x -c -l -L -o -q -h -H -r -R` 可组合；`-m -A -B -C`、对应长选项计数 0–1000000；`-e` / `--regexp` 可多次指定模式。支持对应长选项，`-f` 不自动审批。递归可能读取大量文件。模式以 `-` 开头须用 `-e` 或 `--`。 |
| journalctl | 必须 `--no-pager` 和有限 `-n` / `--lines`（1–1000000）；可选单个 `-u` / `--unit` 字面 service、`--since` / `--until` 字面时间、`-b` / `--boot` 或 `--boot=偏移`，输出格式限 `short`、`short-iso`、`short-precise`、`short-unix`、`cat`、`json`、`json-pretty`。持续跟随及 vacuum、rotate、flush、sync、setup-keys 等写操作不匹配。 |

文件读取允许任意字面相对或绝对路径，包含 `..`、中文和空格，不按目录或后缀过滤；每段最多 32 个文件，前导 `-` 的文件名用 `--` 分隔。独立命令须有文件输入（递归 grep 可省略路径，GNU 默认读取当前目录）；后续管道段可从 stdin 读取。输出和耗时仍受现有任务限制，并非强制每次 grep 指定 `-m`。无需额外文件授权。

## 有限 timeout 包装

仅支持单层 `timeout [-k 秒数 | --kill-after 秒数 | --kill-after=秒数] [--] 时限 程序 参数`。时限为 1–60 的十进制整秒，宽限可省略或为 1–10 秒，均可带小写 s 后缀；两者之和必须不超过当前任务 timeout_ms。0、小数、分钟/小时、其他信号、foreground、未知或重复选项及嵌套 timeout 不自动批准。

移除包装仅用于静态匹配：内部完整 argv、其他管道段和全部复合命令仍须命中原只读规则，实际派发保留原始命令。示例 `timeout -k 2s 25s grep -F -m 1 ERROR /var/log/app.log` 可使用 timeout_ms=35000。timeout 只约束被包装的那一段，不自动限制整个管道；任务总超时、输出上限和未知结果不重放规则保持不变。包停用、子命令为脚本/写操作或超出上述范围时仍按既有策略处理；明确危险命令继续硬拒绝，不因 timeout 放行。历史 pending 不补批。

字节/行数上限是参数范围，不保证实际读取量：例如 tail +N 从指定位置一直读到末尾，head -N 排除末尾部分。远端文件大小、输出上限和任务超时仍适用。

## 语法与任务限制

工作目录须显式给出规范 POSIX 绝对路径，仅 ASCII 字母数字及 `_ . / @ + -`，不接受 `.` / `..` 路径段、冗余分隔符或控制字符；推荐 `/`。超时为 1000–60000 毫秒，单条命令最多 4096 字节、128 个词、10 个以 `;`、`&&` 或换行分隔的命令段，每段最多 8 个管道程序。每个完整 argv 都必须匹配；管道后级限 grep/head/tail。允许字面单引号、双引号及反斜杠转义、相邻引号；程序名称本身须不带引号或转义。单引号中 `$()` 和反引号是字面值；双引号中未转义的 `$` 和反引号不匹配，转义按 shell 语义处理。引号内换行、Tab、控制字符、反斜杠换行、未闭合引号、空段、末尾 `;` / `&&`、`||`、后台 `&`、注释、未引用通配符、花括号、波浪号、变量/命令/进程替换和重定向均不匹配。不是完整 shell 解析器；复杂写法保守转人工。无选项 `-` 表示 stdin，独立读取命令不自动批准。环境赋值、解释器、外连及写操作均不在默认包中；未命中既有授权时留待人工。

这是操作员已激活终端上的便利授权，沿用受信任的远端 PATH、程序与主机状态；不承诺远端可执行文件沙箱或抵御被修改的同名程序。systemctl 显式禁用 pager。默认只读包未匹配仅表示不能依靠此包自动批准；危险拒绝由前置策略独立判定。

## 仲裁与历史记录

仲裁先执行危险策略和静态检查；有限 timeout 可在内部命令完全只读时命中默认包。其余脚本或无法静态确定的命令逐次人工审批，其他命令依次检查默认包、精确临时授权、参数规则和人工审批；自定义精确授权仍优先于自定义参数规则。默认包无 30 分钟到期时间。可复用命令的精确授权保持 30 分钟，参数规则保持原有有效期与接入范围，重连/重启仍清除这些临时权限。`explain` / `explain_command` 使用与真实提交相同的默认匹配器，逐项返回命中或未命中原因；解释针对当前新提交，不代表历史批准。同程序但参数未列入时会给出目录示例；示例不会代替原命令自动执行。旧包批准的待派发任务不能凭新包自动派发，重启后原待处理任务按原状态机取消；旧停用状态继续停用。任务中的 `builtin_rule_id`、`approval_source`、`approval_history` 和审计保留当时依据；切换规则包不会重写历史，人工再批准会追加历史记录。

## 人工审批约束

待审批卡片展示名称、分组、实际目标指纹和完整命令；“目标与任务详情”可展开查看完整 ID、接入代次、目录及超时。会话缺失或接入代次不匹配时禁用审批；刷新期间也不提交旧快照。点击“批准一次 / 拒绝 / 批准并记住 30 分钟”立即记录该显示快照的决定，无第二次确认。原生层内部准备与复核两个新快照，并与显示快照逐字段一致比较；服务端在同一 Store 锁内重新核对状态、命令与完整目标身份，再原子更新。变化会拒绝整次操作。终端激活仍须一次身份确认。

批量只作用于明确选定、仍显示的 1–20 个任务；新到任务不会自动进入选集。隐藏项存在时禁用整批操作，先清除筛选查看全部细节；单条操作不要求先勾选。拒绝沿用撤销匹配自定义规则/精确授权的语义，可能使相关未选中队列任务回到 pending。重复点击或丢失响应后只查询原任务，不重新审批、不重新提交。成功只显示页面提示、清除选择并刷新列表；不确定响应保留页面警示与原任务 ID。工作台自动刷新；助手持续有界轮询原任务，无需用户回复“已批准”。

旧后台缺少能力时显示“更新后台”，打开现有更新页；单独重开桌面不能替换旧 Bridge。本版本不自动终止进程或中断当前 SSH。便携包构建与隔离测试不代表真实 Xshell/VM 验收通过，真实环境按[真实环境验收](validation.md#真实环境验收)核对。
