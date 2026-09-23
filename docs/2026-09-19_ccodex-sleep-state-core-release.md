# Codex Sleep State 0.5.0 核心封装报告

> **严重故障更正**：用户实际运行发现正常节点返回不符规则的状态头后，被误标为失败，且本地 503 触发宿主账号轮换。旧报告的验收没有覆盖这一真实签名包成功/失败链路。根因、复现与 0.5.1 修复见 [代理故障报告](2026-09-19_ccodex-sleep-state-0.5.1-proxy-repair-report.md)；不要将下文历史“通过”结论解释为 0.5.0 实际业务可用。

日期：2026-09-19。目标：将上游核心作为 Sub2API OAuth 保护传输交付，补齐 0.4.0 中不完整的请求头与状态机适配。

本次固定上游 [b18fabf9ad8e9d7af7d9d0306b623ba6091a39d6](https://github.com/gylive/ccodex-sleep-state/tree/b18fabf9ad8e9d7af7d9d0306b623ba6091a39d6)，即 9 月 19 日合并的 v0.4.0 核心。插件版本为 **0.5.0**，避免与已经交付的插件 0.4.0 混淆。签名继续使用原发布者 `local-account-protection-v1`，不生成临时开发密钥。

## 使用入口

历史 [0.5.0 插件包](../.retired-plugin-artifacts/ccodex-sleep-state-0.5.0-before-0.5.1-20260919/ccodex-sleep-state-0.5.0.s2plugin) 已归档，请改用页首故障报告中的 0.5.1。下文保留历史使用说明：在「请求头保护」点击「一键启用保护」，同时确认宿主已经启用该插件且 OAuth 请求路由绑定到它。来源与节点仍可使用本地 HTTP/SOCKS 或订阅。

实际请求提供当前鉴权后，刷新运行状态即可查看当前/备用 state、期望与观测长度、剩余时间、冷却和暂停原因。节点连通性测试不调用模型；会话的“采集 state”会发送短模型请求并消耗额度，支持选择单个节点且仍受冷却和上游暂停限制。页面不保存或显示完整凭据/state。

## 从部分适配改为内嵌核心

生产调用路径是 `plugin.Forward → transport.CoreRuntime.Forward → upstream/gateway.Engine.ServeHTTP`。内嵌上游 `gateway`、`turnstate`、`settings`、`routepool`、`fsutil` 源码与原始测试，保留 GPL-3.0 许可证和原始源文件 SHA-256。复用现有订阅和 Mihomo 路由层，将路由转换为核心使用的不可变 transport。

独立程序的 Codex 配置接管、CCS 配置事务、启动脚本和本地管理 HTTP 服务没有在插件里启动；宿主已经承担插件生命周期、OAuth 鉴权、账号选择、通道路由和计费。这些边界不应被当作缺少 turn-state 核心的理由。

| 证据 | 0.4.0 缺口 | 0.5.0 的实际实现 |
| --- | --- | --- |
| E-001：上游 `engine.borrow/probe`，本地 `TestCoreAdapterCriticalHeadersProbeIsolationAndInjection` | 采集克隆了整套正式请求头，带入会话链和压缩声明；采集 body 缺少字段 | 六个采集身份头白名单，完整短请求 envelope，正式注入和响应转发 |
| E-002：上游会话键，`TestCoreAdapterLimitsSurviveReloadButNewCredentialIsIsolated` | 只按宿主账号/模型隔离 | 宿主账号隔离网关，网关继续按 OAuth 凭据、工作区和模型隔离；暂停跨配置与路由重建保留 |
| E-003：上游 `refresh/Run/HoldActive` 与原始回归 | 每请求扫描整池；缺少主用固定、主备模式、统一次数/间隔/采集槽 | 默认每轮最多 6 次、全进程串行，session 合并和统一冷却；held/standby、个人/Team 规则和运行诊断；手动自定义 292 不会被自动 Team 提示覆盖 |
| E-004：上游 `RejectAndPromote`，shape/compact/truncated adapter 测试 | 坏 active 没有立即失效；RPC 无法完整表达核心失败边界 | 不合格响应拦正文，备用仅用于下一请求；已派发后本地错误返回 `RequestSent=true`，截断流没有成功 End |
| E-005：上游 `encoding/compact` 与真实 HTTPS 适配测试 | 压缩字节直接当 JSON，compact 当普通生成 | gzip/zstd、wire/解压/窗口限制、V1→V2 bridge 和 V2 识别；compact 不额外采集或覆盖原 state |
| E-006：上游 `selectEgress/routepool`，跨进程持久化测试 | 只有连接结果，没有已用/失败/停用生命周期 | state/random/fixed 正式出口；持久化池、原子认领、跨进程回收、手动停用与完成事件顺序保护 |
| E-007：配置桥接和浏览器验收 | 没有主用/备用、长度、冷却或采集入口 | 首屏请求头保护、账号规则、模式/预算、运行会话、单节点采集和生命周期池操作 |

具体请求头契约：

| 请求 | 保留或生成 | 明确排除 |
| --- | --- | --- |
| 采集 | Authorization、ChatGPT-Account-Id、User-Agent、Version、Originator、OpenAI-Beta；JSON/SSE 类型 | 原 X-Codex-Turn-State、session_id、conversation_id、Cookie、压缩声明 |
| 正式普通生成 | 宿主准备好的出站头；有效 state 按策略覆盖 X-Codex-Turn-State | Cookie、Proxy-Authorization、逐跳头 |
| compact | 原客户端 state、上游压缩协议、关联出口 | 不因压缩触发普通采集或应用普通生成形状检查 |
| 响应 | X-Codex-Turn-State 进入 RPC 并由宿主专用路径回传 | Set-Cookie；不合格正式响应正文 |

上游采集 body 的 `parallel_tool_calls:true`、`include:["reasoning.encrypted_content"]` 同步保留。未降低请求模型或推理档位。

## 宿主适配与生命周期

网关按账号、目标、配置及 route generation 缓存。订阅发生变化时停止旧后台采集，旧请求可以排空其快照；切换注入或主备模式使用核心原位 setter，不清空有效 state 和采集间隔。被上游拒绝的凭据不因修改配置而获得重试机会；健康且不再被任何会话使用的旧凭据记录可以释放。

节点池保存在当前宿主安装布局的 `installed/local.sub2api.ccodex-sleep-state/.state/routepool.json`。只持久匿名路由 ID、状态、次数和原因码，使用 Windows/Linux 文件锁与原子提交；修改前重新读取，处理升级排空、临时配置进程与运行进程的重叠。路径拒绝符号链接/重解析目录和非安装布局，不把失败降级到临时目录。此路径是对 0.2.7 安装布局的适配，尚不是 SDK 的稳定数据目录契约。

Core 状态通过现有 `config.save` 的一次性 `core_request` 返回，操作在规范化后移除。普通恢复不重放采集或池操作；停用时的临时进程不假装有运行会话。界面刷新运行状态只使用已保存配置，保留正在编辑的草稿，保存策略后清除旧快照。

## 可复现验证

在 `plugins/ccodex-sleep-state` 目录：

```powershell
go test ./... -count=1
go vet ./...
python scripts/test-ui.py
go run ./cmd/verify -package dist/ccodex-sleep-state-0.5.0.s2plugin -public-key dist/publisher-public-key.txt -key-id local-account-protection-v1
```

核心 adapter 测试位于 [core_test.go](../plugins/ccodex-sleep-state/internal/transport/core_test.go)，使用本地 HTTPS 上游、真实嵌入引擎和宿主帧协议；测试值为合成凭据与合成 state，不访问用户登录文件。上游原始回归位于 [internal/upstream](../plugins/ccodex-sleep-state/internal/upstream)。配置、持久化、生命周期和活跃策略切换另有回归。

签名包实际安装与浏览器 gate：在 `backend` 设置 `SUB2API_CCODEX_SLEEP_STATE_PACKAGE`、`SUB2API_CCODEX_SLEEP_STATE_PUBLIC_KEY`、`SUB2API_CCODEX_SLEEP_STATE_KEY_ID=local-account-protection-v1`，再执行：

```powershell
go test ./internal/service -run '^TestCCodexSleepStateSignedPackageProcess$' -count=1 -v
go test ./internal/handler/admin -run '^TestCCodexPluginHostUIFlow$' -count=1 -v
```

浏览器 gate 另设 `SUB2API_CCODEX_UI_PYTHON` 指向已安装 Playwright/Chromium 的 Python。使用实际包安装器、PluginManager、配置接口与签名静态资源，仓储为内存测试实现，不改生产数据库。结果日志记录包 SHA-256，避免拿源码或其他版本的结果证明交付包。

## 最终交付验收

交付文件：`plugins/ccodex-sleep-state/dist/ccodex-sleep-state-0.5.0.s2plugin`。SHA-256：`BEF6184659E8AAFF3D54F7A6F63B78AAC540A898FF607B40305C4B289F47FA85`。

发布者公钥与 0.4.0 一致：`9jp4x3g0CBBD2Vks9t7EFsuwEcZjICg+Ib3sYW/i+YQ=`。首次接收该发布者仍需通过宿主的信任确认；包内签名和公钥不会绕过信任策略。

| 最终检查 | 结果 |
| --- | --- |
| 插件全量测试与 vet | 通过，包含完整内嵌核心、适配、本地 HTTPS 请求头、状态与流式边界回归 |
| 长期运行与热更新回归 | 4,200 次健康凭据轮换回收；旧请求晚到 403 保留；配置切换不丢 state/冷却；旧后台任务停止；人工停用/回收优先于旧请求完成 |
| 签名和资源一致性 | `version=0.5.0 files=18 runtimes=3`；包内代码生成的运行时、UI 和文档逐项匹配最终构建文件，上游来源哈希匹配固定 checkout |
| 签名包真实宿主服务 | `TestCCodexSleepStateSignedPackageProcess` 通过，3.69 秒；安装/签名/v2握手/配置/健康，以及无鉴权 401 和已派发传输失败的 RequestSent 语义 |
| 签名包真实宿主与浏览器 | `TestCCodexPluginHostUIFlow` 通过，32.40 秒；节点获取/测试/选择/重开、核心设置、无虚构会话、池操作跨进程保存、独立出口、草稿保留与无效来源提示 |
| 本机实际代理 | `127.0.0.1:10808` 经最终包取得 OpenAI 目标 HTTP 421，确认链路连通；不是付费模型成功证明 |
| Linux amd64 | WSL Debian 实际运行最终 runtime，smoke 返回 `healthy=true, passed=true, version=0.5.0`；持久化测试实际执行通过 |
| Linux arm64 | QEMU 用户态运行最终 runtime，smoke 返回相同成功结果；只读挂载、无网络、不使用特权 binfmt |

签名资源实际截图：[核心设置](../plugins/ccodex-sleep-state/.build/host-ui-core/host-core-controls.png)、[无运行会话的真实提示](../plugins/ccodex-sleep-state/.build/host-ui-core/host-core-empty-state.png)、[跨进程回收后的节点池](../plugins/ccodex-sleep-state/.build/host-ui-core/host-pool-recycled.png)。没有使用模拟的“已采到 state”状态充当真实账号验收。

实际宿主输出直接保存为 [host-flow.log](../plugins/ccodex-sleep-state/.build/host-ui-core/host-flow.log)，含本次最终包的哈希与通过记录。

`dist` 仅保留 0.5.0 包、校验文件、公钥和信任示例。原 0.4.0 四个交付文件移入 `.retired-plugin-artifacts/ccodex-sleep-state-0.4.0-before-0.5.0-20260919/`，可以恢复。编译缓存、上游核对副本和截图均位于 `.build/`，未混入分享目录。

## 明确的验证边界

- 当前 Sub2API 普通 Responses 路径不会把所有原始 OpenAI-Beta 值交给插件；插件保留收到的值，不能恢复此前被宿主过滤的值。X-Codex-Turn-State 有宿主显式请求和响应支持。
- 宿主不提供权威套餐字段。自动模式沿用上游 JWT 提示与工作区匹配规则，不是套餐真实性验证；可以手动选择 Team。
- 没有发送用户真实 OAuth 生成或付费线上探测，也没有宣称真实账号已经采到目标长度、提升质量或长期无故障。上游自身的真实服务限制也不会因打包而消失。
- 本机缺少 CGO 编译器，未声称通过 race detector；并发认领、请求排空和取消有明确功能回归。
- ARM64 smoke 使用 QEMU 用户态模拟，不能写成原生 ARM 硬件压力验证。

0.4.0 的节点验收结论仍可追溯，但不足以证明当时已经完成上游核心封装；本报告替代该层面的完成声明。
