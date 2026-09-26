# 插件健康探测误取消与请求头观测排查

## 问题和现场证据

基线提交：`eb7539023d7073f32d0b118cf21afbc739260013`。用户直接启用已安装的 Codex Header Probe 1.0.1，没有重装插件；账号页出现“尚未观测到请求头”及 `0/— B · 0/— 块`。

2026-09-25 至 2026-09-26 的本机后端日志反复出现 `plugin_reconcile_failed: 插件健康检查失败: 扩展健康检查失败: Canceled`，间隔约 63–65 秒。只读进程采样记录到插件 PID 从 30980 变为 31380，后者在错误日志约一秒后启动。后端继续运行，说明插件被宿主反复重建；没有据此认定插件自身崩溃。

2026-09-26 再次核对，安装目录和活动内核的 SHA-256 均为 `0aca4b9124a44df7ba072f85500b086399dd2f2d7b7b82a84c3a0a0430369474`，仍为修复前构建。采样日志使用 Asia/Shanghai（UTC+8），例如 `2026-09-27T01:21:20+0800` 对应本机 `2026-09-26 10:21:20 PDT`，不能混用日期判断版本是否生效。

宿主仓库是 `rw0104/sub2api-cost-console`；本次观测插件来自独立仓库 `rw0104/s2plugin` 的 `plugins/ccodex-header-probe/`。本次修改归属宿主，不修改插件源码、签名包、配置、数据库或用户运行中的进程。

## 根因

调用链为 `PluginManager.Start/reconcileLoop → reconcileOnce → pluginRuntime.checkReadiness → Health`。

`checkReadiness` 返回缓存结果，并通过 goroutine 执行健康 RPC。原调用方创建局部 timeout context，调用 `checkReadiness` 后立即执行 `cancel()`，使尚未完成的异步 RPC 被取消。探测每 30 秒采样，连续 3 次失败后 `publishInstallationUnavailable` 删除运行时和路由并排空进程，下一轮 reconcile 再建立插件进程。前三个探测位于约 0/30/60 秒，因此与现场约一分钟一次的重建一致。

Header Probe 的观测摘要存在插件内存中，重建会清空已有摘要。反复重装插件无法修复宿主的取消逻辑。

## 修复

`reconcileOnce` 直接将管理器生命周期 context 传给 `checkReadiness`，移除局部 `WithTimeout` 和紧随其后的 `cancel()`。异步函数仍自行建立 5 秒 timeout，并在 RPC 完成后释放资源。管理器 Stop 的取消仍可传递到在途 RPC；30 秒采样、单次在途限制、成功清零和连续 3 次真实失败摘除策略保持有效。

上一轮临时使用 `context.WithoutCancel` 的修改已撤回。新增回归测试证明，该临时方案虽然避免立即取消，但会屏蔽管理器停止取消。最终生产代码只修改 `plugin_manager.go` 的调用点。

## 请求头零值的正确解释

这次生命周期缺陷能够解释状态丢失，但不能将所有零值都归因于它：

- `0/— B · 0/— 块` 可以表示已经观测到请求、但请求没有 `X-Codex-Turn-State`。只读探针不生成或注入 state，也不提供 `expected_length/expected_blocks`，所以目标值为 `—`。
- Header Probe 在保护传输执行之前观察请求，不能用它的零值证明后续保护传输没有注入状态头。
- 探针按账号保存最近一条请求摘要；同账号后续无头请求可覆盖之前有头的摘要。请求计数和最近观测时间比单个长度值更适合判断是否触发。
- “尚未观测到请求头”也可能来自没有账号摘要、绑定未命中、RPC 失败或账号页查询失败后清空展示，不能单独证明插件没有运行。

本轮不扩展原生 WebSocket 观测、不改变多探针选择、UI 状态聚合或模型映射。这些边界须与持续重建问题分开验收。

## 验证记录

新增 `plugin_readiness_regression_test.go`，通过真实 `reconcileOnce` 调用点，使用可控 Health 响应覆盖 v1 transport 与 v2 extension：

1. 原始基线：6 个子测试在 `context canceled` 断言处失败，稳定复现调度后立即取消。
2. 临时 WithoutCancel 方案：2 个管理器取消子测试失败，证明它丢失正常关闭语义。
3. 最终修复：验证 reconcile 返回后 Health 仍有效、多轮成功保留同一实例及观测状态、没有重叠探测、管理器取消可传播、单次失败不摘除、成功清零以及连续 3 次真实失败仍摘除。

测试检查真实生成的 5 秒 deadline；其中 DeadlineExceeded 分支使用受控错误注入，不冒充等待完整超时定时器的验收。

可选真实插件测试使用显式 `SUB2API_TEST_HEADER_PROBE_DIR`，从已安装目录复制插件二进制到测试临时目录，使用合成账号和合成状态头走宿主观察 RPC。不读取用户配置、凭据或完整真实请求头，不调用模型上游，不修改已运行插件。该测试验证运行时行为，不替代签名安装/升级流程验证。

2026-09-26 本机 Windows 验证结果：

```powershell
# 在 backend 目录执行，真实插件测试需先指定已解包目录
go test ./internal/service -run '^TestPluginReconcileReadiness' -count=1
go test ./internal/service ./internal/pluginruntime ./pkg/pluginapi/v2 ./internal/repository -run 'Test(Plugin|HeaderProbe|ParseHeaderProbe|EgressBroker|DrainController|Canonical|DesktopUpgrade|SupportedExtensionCapabilityHeaderProbe|FetchOpenAI.*Header|OpenAI.*Plugin)' -count=1 -json
```

- 定向回归：最终实现的 6 个子测试全部通过。
- 四个相关包全部通过；JSON 测试结果中共有 184 个测试/子测试 pass、0 fail。
- 明确跳过 4 项可选测试：PluginExtensionContainerProcessIntegration、PluginHostAPIContainerIntegration、PluginSharePackageSignatureBaseline、PluginRuntimeIntegration。这些依赖额外的容器或包环境，不能据此声称完整环境矩阵通过。
- 真实 Header Probe 1.0.1 二进制（SHA-256 `86e5f7d60687b738d8d3a8806c7e6f163d9c6e86f5085102c852cb2a8dd8603a`）通过独立进程验证：合成头实测 290 个字符、10 块；无头请求仍累计为第 2 次；4 轮健康采样后同实例累计 6 次观测且没有健康失败。
- `gofmt -d` 检查三个 Go 文件无输出，`git diff --check` 通过。当前 Windows 环境 CGO 关闭，本轮未执行 race 检查。

上述真实进程测试通过推进采样时间字段覆盖多轮，不等于在用户现有数据库和 UI 中持续运行 3 分钟。现场活动内核尚未替换，不能宣称用户当前实例已恢复。

## 影响分析和交付

全仓 GitNexus 索引停留在 `2a7b3d2`。按项目的局部索引规则，仅为本次宿主运行时、管理器及相关调用文件建立独立快照索引，未重建全仓、未并行运行 analyzer。`reconcileOnce` 的直接生产调用方为 Start 和 reconcileLoop，风险为 LOW；局部图涉及 Start 的两个执行流程。图未识别 `checkReadiness` 的动态接收者调用边，已用源代码核对唯一生产调用点，不能将图中的 0 当成没有调用方。

提交前执行 `detect_changes` 并检查 staged diff。安装包若重新构建，必须保持正式 `productName=Sub2API Cost Console`、`identifier=com.sub2api.cost-console`、主程序名和端口 18765，才能复用现有 backend/core 目录。测试构建记录实际宿主 Git 提交；sidecar `--version` 的 upstream commit 字段仍表示上游基线，不能拿它代替本地提交证明。

现场恢复判据：活动内核哈希与修复包一致；既有插件重新启用后持续观察至少 3 分钟，runtime/PID 不再周期性变化，Canceled 重建日志消失；匹配请求使请求计数和最近观测时间推进。有头才要求长度/块数非零，无头请求的零值属于合法观测结果。
