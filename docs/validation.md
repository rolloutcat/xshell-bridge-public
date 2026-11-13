# 验证与交付

[返回总入口](../README.md)

发布前分别检查 Go CLI/MCP、宿主机授权和任务生命周期、反向 SSH 的 fake 进程与来源隔离、本机脚本生成与更新、导出脚本的 32/64 位 Windows JScript 假终端、桌面审批事件及小窗口布局、便携 ZIP 导入。记录各命令的实际结果，不将模拟测试描述为真实 Xshell 或远程环境验收。

## 源码验证与构建

在仓库根目录执行，使用可用的 Go、Python、Node.js 和 Windows 构建环境：

```powershell
go test . ./cmd/... ./internal/...
go vet . ./cmd/... ./internal/...
python -m unittest discover -s tests -v
```

使用显式源码包范围，避免 `go test ./...` 递归枚举本地私有 runtime 或构建目录。完整构建入口为 `./scripts/build.ps1 -IncludeExternalTests`，可用 `-GoExe` 指定 Go；它运行 Go/Python 检查并调用便携构建，后者还运行 C#、归档和导入夹具。`-IncludeExternalTests` 额外启用桌面 Node 与远程脚本事件测试，布局脚本仍需单独运行。`-SystemWebViewOnly` 生成依赖系统 WebView2 的 lite 包；默认 full 包包含固定运行时。构建可能下载依赖并重写通用 ZIP 文件，先保存需保留的旧包。

## 交付核对

- 核对 `VERSION`、CLI、桌面、Agent 和三份 Skill 文件与所交付源码一致。
- 检查 README、文档及 Skill 相对链接，并核对打包脚本复制清单；文档路径更名或删除时同步处理消费者。
- 校验 ZIP 结构、导入器、SHA256、私有运行文件与测试产物排除规则；把版本、日期、准确文件大小和哈希写入该版本记录。
- 公开前检查源码、文档、二进制及 ZIP，不包含个人工作背景、真实目标、凭据、本机绝对目录或编辑器配置；保留公开项目标识、署名与第三方许可。公开完整 Git 仓库时另查历史提交，删除当前文件不能清除历史。源码快照包不包含 `.git`、运行数据或构建目录。
- 浏览器布局与真实 Xshell/VM 分开验收；已有历史通过记录不能替代修改后的必要复验。

历史结果集中见[版本与验证记录](release-history.md)，仅证明当次构建与测试范围。

便携包不包含 runtime、凭据、目标地址或日志。保留旧包；禁止自动重放。更新交接之前由用户停止接入、确认任务状态并断开工作台拥有的隧道。

## 专项回归

### v5 只读审批范围

`internal/bridge/builtin_v5_test.go` 覆盖 ls 参数白名单、64 MiB 字节边界与原行数边界、timeout 参数与总时限、子命令/管道完整匹配、危险拒绝优先、旧 pending 不补批及旧包派发失效。CLI/MCP 既有 policy/transport 测试同时覆盖三类新命令；历史真实命令不执行、不写入测试夹具。

### 危险拒绝与人工审批

`internal/bridge/deny_test.go` 覆盖危险命令变体、SQL/Redis/MongoDB、集群及脚本操作、普通文本不误判、Python 历史用法、deny 优先于人工检查、授权不能覆盖拒绝、批量原子性及派发复核。CLI/MCP 的现有 policy/transport 测试覆盖同一拒绝错误与解释；`tests/desktop-approvals.test.js` 区分危险拒绝/人工审批显示，并检查逐次审批任务禁用记住按钮。

2026-11-10 本地仅将既有 120 条历史命令作为字符串离线分类（其中 62 条涉及 Python），没有执行或修改历史任务；本轮策略未硬拒绝这些历史样本，71 条被要求逐次人工审批。仓库测试只使用合成命令，不保存历史命令、目标或日志。此结果只说明该样本的误拦情况，不证明所有脚本安全或真实 Xshell/数据库联调通过。

默认规则的匹配、停用、来源、重复 request、旧代次及重启由 `internal/bridge/builtin*_test.go` 覆盖，CLI/MCP 的目录、解释和提交由对应包内测试覆盖。按改动选择受影响的专项，避免把历史通过记录当作当前结论。

### 工作台退出与 MCP 复用

`internal/bridge/shutdown_test.go` 覆盖停机权限、任务终态、重复提交及持久化失败回滚；`internal/mcpserver/http_test.go` 覆盖同一 HTTP 服务的多个客户端、Origin/凭据检查、审批与重复 request_id。`BackendTests` 的隔离子进程测试覆盖单桌面锁、重复关闭不重提、Windows Job Object 在主进程被结束后清理所属进程。`IntegrationsExternalTests` 使用假 Codex 与内存环境变量验证旧 stdio 迁移、外部变更拒绝和失败恢复，不写真实用户环境。

发布时使用独立 runtime 和空闲回环端口运行实际 xshdesk/xshctl，分别验证窗口关闭消息和强制结束主程序，并确认退出后端口释放；不要用真实业务接入做退出测试。旧 stdio 自动退出仍有约 30–35 秒观察期，HTTP 模式不创建常驻 MCP 进程。

### 审批与界面

```powershell
node tests/desktop-approvals.test.js
node tests/desktop-followthrough.test.js
node tests/desktop-ui.test.js
node scripts/test-approval-native.cjs
node scripts/test-skill-native.cjs
node scripts/test-approval-layout.cjs
```

上述布局脚本通过 CDP 控制 Chrome，需要支持内置 WebSocket 的 Node.js 和可用 Chrome；可通过 `BROWSER_PATH` 指定浏览器、`REPORT_DIR` 指定报告目录。

检查当次报告和截图，Chrome 加合成消息宿主不能代表真实 WebView2 或 Xshell。脚本导出诊断另运行 `./scripts/test-export-native.ps1` 和 `node tests/desktop-reverse.test.js`；前者使用原生 Backend 与 STA/Control.Invoke 包装，不打开真实保存框或连接 SSH/Xshell。

### 终端清理

```powershell
$env:GOCACHE = Join-Path (Get-Location) 'dist/removal-tests/go-cache'
go test ./internal/bridge ./internal/client ./internal/mcpserver ./cmd/xshctl
node tests/desktop-removal.test.js
node tests/desktop-session-lifecycle.test.js
./scripts/test-removal-native.ps1 -TestNames BackendRemovalTests,BackendApprovalsTests,BackendTests,BackendHostReverseTests
```

需要重现 Go 健康握手 → 原生 `snapshot` → 生产界面消息流程时，可设置 `XSH_REMOVAL_GO_FIXTURE` 与 `XSH_REMOVAL_SNAPSHOT_FIXTURE` 为 `dist/removal-tests/` 下的两个 JSON 绝对路径，先运行 `go test ./internal/bridge -run TestRemovalHandshakeFixture -count=1`，再运行上述原生测试和 `desktop-session-lifecycle.test.js`。数据全部由隔离 Store 生成。`scripts/test-removal-layout.cjs` 可读取同一快照，使用 `PLAYWRIGHT_MODULE` / `BROWSER_PATH` 指定现有 Playwright 和浏览器，在 1280/900/640 宽度验证六记录恢复、可见禁用原因及百记录清理弹窗，并将截图写入 `dist/removal-tests/layout/`。

以上是隔离 Store/HTTP、原生模拟传输和页面事件验证。

## 真实环境验收

隔离测试和真实环境验收分开记录。真实使用前由用户核对宿主机、Xshell 所在机器、目标环境和授权范围；远程接入还需核对 VPN 与 SSH 主机密钥。

- 本机：在“本机 Xshell”检查状态，预览并确认生成或更新；核对受管完整路径，在每个最终目标终端手动运行，返回核对新代次和指纹并激活。
- 远程：在“远程 Xshell”显式连接、预览确认导出并私密传递脚本；在每个最终目标终端手动运行，返回核对新代次和指纹并激活。
- 执行：用授权的低风险命令验证审批、退出码和输出；同时记录工作台、运行中 Bridge、脚本及 Skill 版本和当前默认包状态。

确认重复批准、接入代次变化和断线不会自动重放；结果未知须核查原任务。切换目标或网络前先停止旧接入与隧道并核实目标。日志使用可打印文本并关闭时间戳、ANSI 和文件路径询问；按来源选择编码（自动支持 BOM 和无 BOM UTF-8）；任务中不要手动输入或切换目标。不要用模拟结果声称真实 Xshell、网络或生产验收通过。

终端清理本地回归与真实界面验收按[终端清理说明](maintenance.md#清理终端记录)区分记录。真实验收应核对单个活动接入停止并隐藏后 Xshell/SSH 仍保持、批量仅包含已失效记录、忙任务被拒绝、旧脚本不能恢复记录、重启后仍隐藏，以及原任务名称分组和输出仍可查看。完整身份长文本和 100 项预览应可滚动，确认按钮保留在底部；本地事件测试不能替代该真实窗口检查。
