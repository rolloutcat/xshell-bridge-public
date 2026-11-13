# Xshell Bridge 0.8.9

复用用户已经登录的 Xshell 终端，让 CLI/MCP 通过同一个 Bridge 提交命令、等待审批并读取输出。支持本机和远程 Xshell；不读取 SSH 密码，不自动登录目标服务器。

## 项目缘起

项目源于实际工作中的接入需求：部分内网服务器要求通过堡垒机网页发起单点登录，再启动 Xshell 客户端进行运维。用户需要沿用这条认证和连接链路，同时希望让 AI 协助完成日志查询、状态检查等终端任务。

因此，本项目参照 OpsKat 的运维交互与审批设计，独立实现了面向已登录 Xshell 会话的桥接工具。用户负责登录和目标核对，AI 通过 CLI 或 MCP 提交任务，由 Bridge 统一处理授权、执行状态及输出。核心能力不依赖特定 AI 客户端，也可以单独使用 CLI。

## 适用场景

- **堡垒机网页启动 Xshell**：用户在网页完成认证、选择资产并启动 Xshell 客户端，进入最终目标的 Shell 后接入 Bridge，继续使用现有登录链路进行日志查询、状态检查等运维操作。
- **通过 SSH 接入远程 Xshell**：Xshell 运行在另一台 Windows 机器上，Bridge 工作台运行在本机；通过 SSH 反向隧道连接两端，在远程 Xshell 中手动运行导出的脚本，再从本机 CLI/MCP 提交任务和查看输出。
- **本机已有 Xshell 会话**：用户已直接登录目标，在指定终端运行接入脚本并激活，即可通过同一套审批流程提交命令。

具体步骤、连接方向和适用条件见[使用指南中的接入场景](docs/user-guide.md#按使用场景接入)。堡垒机认证、资产授权和目标登录仍由用户完成；纯浏览器 WebSSH 终端不属于本项目接入范围。

## 从这里开始

源码仓库：[rolloutcat/xshell-bridge-public](https://github.com/rolloutcat/xshell-bridge-public)。发行文件发布到 [Releases](https://github.com/rolloutcat/xshell-bridge-public/releases)。Windows x64 用户下载 lite 包，并使用同名 `.sha256` 文件核对下载；例如运行 `Get-FileHash .\xshell-bridge-portable-windows-x64-lite.zip -Algorithm SHA256`。源码构建步骤见[验证与交付](docs/validation.md#源码验证与构建)。

公开发行仅提供 **Windows x64 lite 包**，**不包含 Microsoft Edge WebView2 Runtime**，需要运行机器已安装 Evergreen Runtime 和 .NET Framework 4.8。安装与检测说明见[使用指南](docs/user-guide.md#运行要求与-lite-包)。

首次使用请按[使用指南](docs/user-guide.md)完成启动、终端接入和任务操作。后续按需要查阅以下文档；每项内容只在对应文档维护。

| 文档 | 内容 |
|---|---|
| [使用指南](docs/user-guide.md) | 启动、本机与远程接入、AI 接入、审批和输出查看 |
| [维护与排障](docs/maintenance.md) | 程序与脚本更新、终端记录清理、日志和导出故障、进程诊断 |
| [架构与协议](docs/protocol.md) | 组件关系、角色身份、状态机、接口及持久化约束 |
| [验证与交付](docs/validation.md) | 本地检查、构建、真实环境验收和历史证据 |

审批命令表、日志采集细节及 CLI/MCP 命令参考由主文档按需引出，不另设总目录。

项目源码采用 [MIT 许可证](LICENSE)。第三方组件遵循各自许可，见[第三方声明](THIRD_PARTY_NOTICES.md)；Xshell 需另行安装。

## 执行与授权

命令分为[危险拒绝、人工审批和自动放行](docs/approval-policy.md)：`hard-deny-v1` 阻止已识别的危险操作，Python 等复杂脚本仍可逐次审批。当前默认只读规则包为 `builtin-readonly-v5`，新增常用 ls、64 MiB 字节参数及有限 timeout 包装的只读自动审批。源码、运行中的 Bridge、已安装 Skill 和接入脚本可能不同，使用时分别核对。断线、代次变化或结果未知时只核查原任务，不自动重放。本地模拟测试不代表真实 Xshell/VM 验收通过。

0.8.7 起，关闭或强制结束工作台会同时结束所属 Bridge；MCP 默认通过 HTTP 共享后台，新对话不再启动常驻 xshctl。旧版需完成程序更新和 MCP 配置迁移，步骤见[维护与排障](docs/maintenance.md#进程与连接诊断)。

## 致谢与设计借鉴

感谢 [OpsKat](https://github.com/opskat/opskat) 项目及其贡献者公开运维工具的设计与实现。本项目在命令审批交互、单次与临时授权、批量审批以及危险拒绝优先等方面借鉴了其设计思路，再结合 Xshell 已登录会话接入的需求独立实现。

具体借鉴点、固定版本源码链接和实现差异见[OpsKat 致谢与设计借鉴](docs/opskat-comparison.md)。OpsKat 不作为本项目运行依赖；第三方许可及来源说明见[第三方声明](THIRD_PARTY_NOTICES.md)。
