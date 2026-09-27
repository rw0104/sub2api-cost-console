# CI、上游合并与双通道发布经验

本记录用于接续已有排查，防止把旧失败反复当成新问题，或重复开发已完成的修复。证据基线为 [v0.3.11 CI 清单](CI_BASELINE_V0.3.11.json)；执行新的修复后应更新本页状态，不覆盖历史失败基线。

## 目前核实的原因

1. **上游同步存在语义合并遗漏。** `9f3c1de9f`、`d332aa969` 后，部分成本损失通知、账号模型投影、配额 credits 独立缓存与可配置推理力度计费调用没有保留。`b3d60016e` 补了字段和 helper，使测试重新能编译，但不等于对应业务已接回。恢复实现应对照历史业务合同和原回归测试，不能仅删除失败断言。
2. **测试范围不同。** `go test ./... -run '^$'` 只编译；不带 `-tags=unit` 会漏掉标记为 unit 的测试；插件定向测试不会覆盖完整计费和配额逻辑。桌面构建成功不能替代完整后端 CI。
3. **发布工作流曾独立于后端质量门禁。** v0.3.11 的 Desktop Release 成功，而同一提交 `d3146065d` 的后端 Unit tests 和 golangci-lint 失败。与旧版失败相同仅表示没有新增失败名，不代表无业务影响，也不代表修复完成。
4. **lint 债务未清理。** 历史 33 条诊断分为 errcheck 19、gofmt 3、gosec 5、ineffassign 1、staticcheck 1、unused 4。gosec 警告需要逐条检查信任边界，不能笼统归为误报或全局禁用；unused 的倍率函数曾直接暴露生产调用缺失。
5. **桌面和内核通道未同步。** v0.3.11 增加必需能力 `openai.oauth.request_header_probe.v1`，但 `core-stable/core-latest.json` 仍为 2026-09-23 清单，不含该能力。桌面发布仅上传安装器，内核自动同步仅比较版本、上游提交、扩展及算法，遗漏能力清单。因此新桌面能运行内置内核，却在检查独立内核更新时失败。责任在发布链路，不在用户重装方式。

## 已完成修复，先复用再开发

| 问题 | 修复/证据 | 状态 |
| --- | --- | --- |
| readiness 异步 Health 被局部 cancel 立即取消，约每分钟重建插件 | `c66a226a2`；v1/v2 定向红绿、真实 Header Probe 私有副本测试 | 已修复并随 v0.3.11 发布 |
| 新能力加入后 Rust 的完整能力测试样本过时 | `d3146065d`；逐项移除当前必需能力验证 | 已修复；66 项 Rust 测试通过 |
| 测试安装包更改应用身份，看不到已有 backend/core | 保留正式 productName、identifier、mainBinaryName、18765 端口；不复制凭据 | 交付纠正，后续包必须保持身份 |
| 28 项顶层后端失败 | 已恢复计费、目录投影、配额缓存、终局账本生产接线；原行为回归及新增兼容回归通过 | 实现已修复，完整同 SHA CI 为发布门禁 |
| 原报告 33 条 lint 及被截断诊断 | golangci-lint 2.13.0，Windows/Linux 全仓检查均 exit 0、0 issues | 已清理；未禁用 linter，不再以截断后的数量视为问题总数 |
| 最新桌面检查 core-stable 缺少 Header Probe 能力 | 已证实旧清单 5 项、新桌面 6 项；扩展 1.3.4 同步打包并下载验证 | 发布流程已修复；线上是否完成以稳定通道下载验收为准 |

## 避免再次发生

- 上游合并后检查业务调用链，尤其配置读取→金额计算、终局事件→账本、模型投影→UI、配额获取→两类缓存；新增字段或恢复函数定义不能视为恢复行为。
- 运行匹配 CI 的命令与工具版本：`go test -tags=unit ./...`、`go test -tags=integration ./...`、`golangci-lint run --timeout=30m ./...`。定向测试用于定位，完整检查用于关闭债务。
- lint 默认会裁剪相同诊断的显示数量；最初“33 条”是该次报告数，不是全部问题上限。清理时使用 `--max-same-issues=0 --max-issues-per-linter=0`，检查进程退出码和 typechecking 错误；`0 issues` 配合非零退出码不能算通过。
- 本机 Windows 回归需把已安装的 Git `usr/bin` 临时加入测试进程 PATH，否则依赖 `sh` 的备份测试会因工具缺失而失败；不改业务代码掩盖环境问题。并行执行大型编译、全套测试和污点分析会使短超时测试受资源争用影响，先保留失败证据，再低负载定向复现，不盲目扩大超时。
- 多代理可并行编辑互不重叠模块，但全量编译/lint须在相关文件冻结后启动。package loader 若先枚举旧文件、后读到引用新 helper 的文件，会报告不存在于最终代码的 typechecking 失败；必须以冻结快照重跑，不能把中间态错误算成旧债务，也不能据 `0 issues` 宣布通过。
- WS 定价测试必须注入真实价格解析器并加载 APIKey.Group；仅设置 GroupID 或渠道价格样本不会保证经过对应生产计费分支。对目录价格漂移，使用实例私有的受控价格样本，保留行为和金额断言，不回退已更新的真实价格。
- CAS 旧回调测试必须明确构造不同的持久化版本/时间值。`TestOllamaProbeCallback_StaleLongDoesNotOverrideNewShort` 曾用第二次 `time.Now()+5s` 作为“新”截止时间，Windows 上可能与刚写入的旧值相同（30 次定向执行失败 27 次），此时 CAS 合理匹配。改为以已持久化值明确偏移 1ms；不靠 Sleep、不放宽断言，也不为无效样本改生产算法。
- unit 长期失败会遮住后续 integration 的结果。修复 unit 后发现 `TestPluginRepositoryV2MetadataAndScopeIsolation` 仍断言 v1 全局单 scope 独占，但迁移 246/247 已明确改为多插件按优先级及账号/用户/分组范围路由。应对照当前迁移与路由合同更新样本，并继续用真正非法的同插件重复 binding 验证事务回滚；不能为了旧测试恢复已废弃的全局唯一索引。
- 发布必须绑定同一源码 SHA 的完整后端 CI 结果；不能用另一个提交或仅桌面成功充当后端质量证明。记录跳过项和非绿检查。
- 协议能力或宿主补丁改变时递增扩展版本。桌面安装器与 `core-stable` 必须包含同一轮已验证内核；先上传实际二进制包，再发布含真实 SHA-256 的清单，最后发布依赖它的桌面版本。
- 已被稳定清单引用的同名内核包禁止用不同 SHA-256 覆盖，重发不同内容须递增扩展版本；否则即使先包后清单，清单上传中断也会让旧客户端下载到哈希不符的包。
- 判断内核更新时比较 required capabilities，不能仅比较版本号。保持缺少能力即拒绝安装的检查，不能通过删除必需能力或仅伪造 manifest 来消除提示。
- 签名、manifest、二进制 `--version`、实际 Git SHA 和打包后下载哈希需一致。upstream commit 是上游基线，不是宿主修复提交。
- 经验与失败基线保留在可跟踪的 docs 中，并从本地 AGENTS.md 与开发总日志链接；不要只放临时聊天或被 gitignore 忽略的目录。

## 状态解释与验证边界

2026-09-27 本机 `go test -tags=unit -p 2 ./... -json` 完整执行 exit 0，61 个有测试的包通过，原 28 项失败逐项核对全部通过；Windows/Linux 不限诊断数量的 lint 均 exit 0、0 issues。纠正后的插件仓储集成用例在临时 PostgreSQL 18.1 / Redis 8.4 容器中通过真实迁移、路由范围、唯一约束和事务回滚校验，容器已自动清理；发布仍须通过最终源码 SHA 的 GitHub 完整检查。

v0.3.12 的验收入口：[源码及安装包](https://github.com/rw0104/sub2api-cost-console/releases/tag/v0.3.12)、[稳定内核通道](https://github.com/rw0104/sub2api-cost-console/releases/tag/core-stable)、[CI 执行记录](https://github.com/rw0104/sub2api-cost-console/actions/workflows/backend-ci.yml)、[发布后下载验证报告](https://github.com/rw0104/sub2api-cost-console/releases/download/v0.3.12/RELEASE_VERIFICATION.json)。关闭事故须同时核对发布 tag 的源码 SHA、该 SHA 的完整 CI，以及下载后验签/哈希结果；只完成本地修复不等于线上已恢复。

Header Probe 是只读探针。请求不带状态头时长度 0 是合法结果；目标值 `—` 表示没有配置目标，不能单独证明插件失效。结合请求数、最近观测时间、runtime/PID 及绑定判断。当前只启用探针的原生 WebSocket 覆盖仍有限，不得宣称所有 v2 能力已经完整接入。

本记录只保存非机密原因、命令、提交和证据链接，不包含配置口令、token、完整真实请求头、数据库转储或私钥。
