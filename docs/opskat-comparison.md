# OpsKat 致谢与设计借鉴

[返回总入口](../README.md)

感谢 [OpsKat](https://github.com/opskat/opskat) 及其贡献者公开运维工作台、命令审批与授权策略的设计和源码，为本项目的审批体验与安全边界设计提供了参考。下表记录具体借鉴内容，以及面向 Xshell 已登录会话接入所做的独立实现。

参考固定提交 [`4a218eb05241a2f57622c13d9ec517771aca3b4d`](https://github.com/opskat/opskat/tree/4a218eb05241a2f57622c13d9ec517771aca3b4d)，仅参考交互与授权分层；没有复制 GPL 代码、资源或引入运行依赖。许可证说明见 [THIRD_PARTY_NOTICES](../THIRD_PARTY_NOTICES.md)。

| 参考行为 | 本项目当前实现 |
|---|---|
| 展示原始审批主体 | 显示完整原命令、目录、超时、目标和身份，决定绑定显示快照 |
| 并发排队、整批批准或拒绝 | 明确选定最多 20 个显示任务，锁内原子校验，任一变化整批失败 |
| 单次批准与记住授权 | 一键批准一次、拒绝、记住 30 分钟；精确绑定当前接入 |
| 永久策略先于临时 grant，可选模型/人工兜底 | 独立版本化静态默认只读包，先于原有临时权限；未匹配人工处理，无模型调用 |
| 解析失败不能误命中规则 | 严格字面语法与完整参数匹配，复杂语法不自动批准；不实现宽泛 `*` 默认授权 |

固定源码依据：[审批引擎](https://github.com/opskat/opskat/blob/4a218eb05241a2f57622c13d9ec517771aca3b4d/internal/approval/approval.go)、[命令审批](https://github.com/opskat/opskat/blob/4a218eb05241a2f57622c13d9ec517771aca3b4d/cmd/opsctl/command/approval.go)、[终端交互](https://github.com/opskat/opskat/blob/4a218eb05241a2f57622c13d9ec517771aca3b4d/cmd/opsctl/command/tty_approval.go)、[策略](https://github.com/opskat/opskat/blob/4a218eb05241a2f57622c13d9ec517771aca3b4d/cmd/opsctl/command/policy.go)、[技能说明](https://github.com/opskat/opskat/blob/4a218eb05241a2f57622c13d9ec517771aca3b4d/plugin/opsctl/skills/opsctl/SKILL.md)。

本项目的 [具体命令表、默认启用语义和限制](approval-policy.md) 为本项目独立设计；不声称 OpsKat 使用相同内置名单或相同默认开关。Bridge 仍只复用用户主动接入的 Xshell，会话激活、代次变化、断线与未知结果禁止重放等规则由本项目状态机约束。

## 危险拒绝与脚本审批

继续参考上述固定提交的[内置危险组](https://github.com/opskat/opskat/blob/4a218eb05241a2f57622c13d9ec517771aca3b4d/internal/model/entity/policy_group_entity/policy_group.go)、[Shell 分解](https://github.com/opskat/opskat/blob/4a218eb05241a2f57622c13d9ec517771aca3b4d/internal/ai/policy/command_shell.go)、[SQL 分类](https://github.com/opskat/opskat/blob/4a218eb05241a2f57622c13d9ec517771aca3b4d/internal/ai/policy/query_policy.go)及[脚本审核模式测试](https://github.com/opskat/opskat/blob/4a218eb05241a2f57622c13d9ec517771aca3b4d/internal/ai/permission/review_decode_test.go)。采用 deny 先于 allow/grant、明确危险与 NeedConfirm 分离、未知脚本默认交给人的行为原则。

本项目独立实现 hard-deny-v1，沿用共享 Store 状态机；没有复制 OpsKat 代码或引入其运行依赖。Shell 解析直接使用独立 BSD-3-Clause 许可模块 mvdan.cc/sh/v3；SQL 使用本项目有边界的词法分类，未引入 OpsKat 的 TiDB 解析器、多资产策略存储或模型审核。因此不声称与 OpsKat 的全部规则、方言或脚本分析能力等价。当前实际范围见[审批策略](approval-policy.md)。
