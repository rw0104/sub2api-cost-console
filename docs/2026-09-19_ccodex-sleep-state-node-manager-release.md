# Codex Sleep State 0.4.0 修复与验收报告

> **后续更正**：本报告覆盖 0.4.0 的节点管理与连接诊断，不能证明完整上游核心已封装。关键请求头、凭据隔离、有界采集与压缩协议的后续补齐见 [0.5.0 核心封装报告](2026-09-19_ccodex-sleep-state-core-release.md)。0.4.0 包与哈希保留为历史证据，不再是当前交付。

日期：2026-09-19。范围：订阅导入、代理诊断、节点测试与选择，以及可分享的签名包。推荐宿主为 Sub2API 内核 0.2.7 / 桌面 0.3.4；本次宿主集成测试使用仓库中的 0.2.7 实现。

本次修复了旧界面把诊断成功误报成进程退出的问题，并补齐节点列表、逐项测试、固定选择和重开持久化。不能把本次连通性验收解释为生产账号推理、长期稳定性或原生 ARM 硬件负载验证。

## 安装和使用

历史分享文件已归档为 [ccodex-sleep-state-0.4.0.s2plugin](../.retired-plugin-artifacts/ccodex-sleep-state-0.4.0-before-0.5.0-20260919/ccodex-sleep-state-0.4.0.s2plugin)。包内包含 Windows amd64、Linux amd64、Linux arm64 运行时；当前交付见页首 0.5.0 报告。

沿用既有发布者 `local-account-protection-v1` 和原 Ed25519 密钥，未生成临时开发密钥。首次导入该发布者时，接收者仍需按宿主提示确认信任；签名证明包完整性与发布者身份，不自动绕过宿主信任策略。已经信任该发布者的宿主可以验证同一身份的升级包。

公钥：`9jp4x3g0CBBD2Vks9t7EFsuwEcZjICg+Ib3sYW/i+YQ=`。

SHA-256：`B1FDE1DE8B23A134986D787C675499E6243818590665E95BD33D006EC8058EE7`。

1. 在宿主插件管理导入 `.s2plugin`，打开配置页的「来源与节点」。
2. 代理每行一个完整 URL，例如 `http://127.0.0.1:10808`；订阅每行一个完整链接。
3. 点击「保存并获取节点」，随后使用「测试全部节点」或各行「测试」。
4. 点击「使用此节点」保存固定出口；也可选择「自动轮换」并保存。重新打开页面仍保留节点和选择。
5. 节点测试与插件启用是两件事。正式转发还需要在宿主启用插件并配置 OAuth 保护传输路由，且账号/模型在策略范围内。

`127.0.0.1` 指运行 Sub2API 内核的机器或容器。本机内核可以连接本机代理；远程内核不会因此连接桌面电脑。宿主不自动继承终端代理环境变量，直接填写 URL 可以避免空环境变量来源。

## 根因与改动

| 证据 | 发现 | 修复路径 |
| --- | --- | --- |
| E-001：旧 UI 在配置测试后请求 `plugin.status`；宿主 `plugin_runtime.go` 的状态路径检查 v1 `r.api`，v2 运行时只设置 `extension/transport` | 已成功的网络诊断会被后续状态请求覆盖成“插件进程已退出”；原浏览器测试伪造成功状态，漏掉真实故障 | UI 改为一次 `config.save` 传入 `route_request`，从规范化配置读取 `route_report`；不依赖 `plugin.status` 或 `config.test` |
| E-002：宿主测试限时 30 秒，原订阅下载与全部节点测试可能累加超过这一限制 | 用户只得到通用 RPC 失败，没有节点结果 | 诊断整体限时 12 秒；未完成节点明确标记，允许逐项重试；下载/解析失败通过安全错误码返回 |
| E-003：真实签名包浏览器测试在选择节点后重开，报告丢失 | UI 提交 `[]`，持久化省略空字段，两者来源签名不同 | 全局来源的省略、`null`、`[]` 统一；新增订阅发现、UI 空数组、固定选择、保存/重开回归。账号级显式空代理数组仍保留覆盖语义 |
| E-004：配置、Mihomo 解析与真实回环代理回归 | 账号未设置出口时不能错误清空继承配置；HTTP 默认端口与浏览器 URL 规范化需要一致 | 恢复账号继承，HTTP/HTTPS 默认端口为 80/443，保持节点稳定 ID；验证认证密码和 SOCKS 远端 DNS |
| E-005：本机实际 Xray 监听 `127.0.0.1:10808`；HTTP CONNECT 返回 200，HTTP/SOCKS 对 OpenAI 目标取得响应 | 本机代理能够建立连接；目标拒绝访问不能等同于代理进程失效 | 节点表分别显示连通性、HTTP 状态、认证错误和超时；403/429/5xx 显示“已连通 · 目标限制” |

生产宿主和 SDK 不需要修改。宿主目录本次增加的是集成测试。配置加载失败时页面禁止覆盖保存；没有使用忽略解密错误后覆盖旧配置的做法。

实现入口：

- [一次性诊断与结果生成](../plugins/ccodex-sleep-state/cmd/plugin/route_report.go)、[来源签名与报告清洗](../plugins/ccodex-sleep-state/internal/config/route_report.go)。
- [真实连接检查与错误分类](../plugins/ccodex-sleep-state/internal/transport/connectivity.go)。
- [节点管理界面](../plugins/ccodex-sleep-state/ui/app.js)。
- [真实宿主测试](../backend/internal/handler/admin/ccodex_plugin_ui_flow_test.go)、[签名包浏览器流程](../plugins/ccodex-sleep-state/scripts/test-host-ui.py)。

调用路径 P-001：签名资源页面 → 宿主 UI Bridge `config.save` → 实际 `PluginManager.SaveConfig` → v2 `ValidateConfig` 执行一次性诊断 → 返回移除命令的规范化配置 → 宿主保存 → 页面展示节点报告。`ApplyConfig` 和重启恢复不会重复发起测试；仅报告更新也不会清空正在使用的连接池和 state。

## 已完成的验证

| 验证 | 结果与边界 |
| --- | --- |
| 插件 `go test -count=1 ./...`、`go vet ./...` | 全部通过；包括来源、报告持久化、state/出口绑定、刷新和失败恢复 |
| 本地 HTTP CONNECT / HTTP 凭据 / SOCKS5 / SOCKS5H | 真实网络回归通过；覆盖特殊字符密码、407、远端 DNS 和凭据不转发给上游 |
| UI Bridge 契约浏览器测试 | 获取、单节点测试、选择重开、整池测试、来源错误、加载失败保护、过滤条件保留、390px 布局通过，无页面脚本错误 |
| 最终签名包服务集成 | `TestCCodexSleepStateSignedPackageProcess` 通过，实际安装与启动签名 Windows runtime |
| 最终签名包真实宿主与浏览器 | `TestCCodexPluginHostUIFlow` 通过；签名静态资源、实际保存接口、逐节点结果、固定选择、重新加载和无效订阅错误均通过 |
| 用户提供的真实订阅 | 最终包通过宿主解析出 78 个节点：69 个连通、3 个连通但目标限制、6 个连接失败；报告无整体错误，未暴露订阅凭据 |
| 本机实际 HTTP 代理 | `http://127.0.0.1:10808` 经最终插件及真实宿主诊断，取得 `api.openai.com/` HTTP 421；确认链路连通，不代表模型业务成功 |
| Linux amd64 | WSL Debian 中真实执行 smoke 与插件，返回 `healthy=true, passed=true, version=0.4.0` |
| Linux arm64 | QEMU 10.2.3 用户态模拟执行 smoke 与插件，返回相同成功结果；无全局 binfmt 或特权容器修改，非原生硬件测试 |
| 包与源码资源一致性 | Ed25519、16 个资源哈希、3 个运行时校验通过；包内 UI/文档/二进制逐项与当前构建文件一致；发布者公钥与旧包一致 |

本机 HTTP 与 SOCKS curl 对照：`https://api.openai.com/` 返回 HTTP 421，`https://chatgpt.com/backend-api/` 返回 HTTP 403，HTTP 代理 CONNECT 均成功。这些匿名探测证明相应目标能响应，不证明 OAuth 账号或模型请求成功。

最终宿主验收日志：[host-flow.log](../plugins/ccodex-sleep-state/.build/host-ui/host-flow.log)，记录了被测包 SHA-256、上述脱敏结果及 23.62 秒的测试通过记录。截图：[节点测试与选择](../plugins/ccodex-sleep-state/.build/host-ui/host-node-results.png)、[订阅错误提示](../plugins/ccodex-sleep-state/.build/host-ui/host-source-error.png)。截图使用本地故障样本，展示认证失败与连接失败的区分，不代表真实订阅全部失败。

补充只读现场证据 E-006：当前本机宿主启动后的日志在 09:31:31 PDT 记录配置保存 200、旧测试接口 400、随后状态接口 500；09:33:13 测试和状态又返回 200。最近的启停记录为 09:35:40 停用成功。没有把更早、发生在本次启动前的解密错误或 gRPC 错误作为当前代理故障证据，也未改写生产配置。仅凭这些日志不能认定用户当前已启用插件或其实际模型请求正常。

### 复现命令

在插件目录执行：

```powershell
go test -count=1 ./...
go vet ./...
python scripts/test-ui.py
go run ./cmd/verify -package dist/ccodex-sleep-state-0.4.0.s2plugin -public-key dist/publisher-public-key.txt -key-id local-account-protection-v1
```

宿主验收在 `backend` 目录运行。需先设置包路径、公钥与可用 Playwright Python；可选的 `CCODEX_TEST_SUBSCRIPTION_URL` 和 `CCODEX_TEST_PROXY_URL` 从进程环境读取。本报告、测试输出和分享包均不包含用户订阅凭据。

```powershell
$env:SUB2API_CCODEX_SLEEP_STATE_PACKAGE = (Resolve-Path '../plugins/ccodex-sleep-state/dist/ccodex-sleep-state-0.4.0.s2plugin').Path
$env:SUB2API_CCODEX_SLEEP_STATE_PUBLIC_KEY = (Get-Content '../plugins/ccodex-sleep-state/dist/publisher-public-key.txt' -Raw).Trim()
$env:SUB2API_CCODEX_SLEEP_STATE_KEY_ID = 'local-account-protection-v1'
$env:SUB2API_CCODEX_UI_PYTHON = (Get-Command python).Source
go test ./internal/service -run '^TestCCodexSleepStateSignedPackageProcess$' -count=1 -v
go test ./internal/handler/admin -run '^TestCCodexPluginHostUIFlow$' -count=1 -v
```

复现需要 Go 依赖和已安装 Chromium 的 Playwright 环境；省略可选真实来源时，宿主测试仍使用本地可复现的订阅/代理故障样本。真实宿主 fixture 使用实际安装器、管理器、配置和静态资源 handler，但数据库仓储为测试内存实现，不读写用户生产数据库。

## 剩余边界

- 没有发起付费模型请求或真实账号 OAuth 推理；没有长时间生产流量验证。不能保证服务商节点以后不失效。
- 订阅刷新沿用请求触发机制，默认 900 秒。后台定时 worker、历史可用率和趋势面板仍未实现。
- 目前为 OAuth/process 保护传输；API Key/relay、Codex 配置接管和独立管理服务仍不在本插件范围内。
- 连接测试表是诊断信息，不是持续健康调度器；自动轮换的实际候选仍由转发时的路由池和探测决定。
- 宿主加密密钥丢失导致的历史配置解密失败属于宿主数据恢复问题，插件不应通过静默清空配置掩盖它。

原 [能力缺口分析](2026-09-18_ccodex-sleep-state-multi-route-gap-analysis-report.md) 中对 0.3.1 UI 完成状态的过度结论已更正。原需求保留，后续验收应继续以真实宿主流程为准。

交付目录仅保留 0.4.0 包、SHA-256 文件、发布者公钥和信任配置示例。原 0.3.1 四个交付文件移至 `.retired-plugin-artifacts/ccodex-sleep-state-0.3.1-before-0.4.0-20260919/`，可以恢复；构建中间文件和浏览器证据留在 `.build/`，不混入分享目录。
