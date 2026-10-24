# Sub2API Cost Console v0.4.1

## 主要更新

- 修复保护传输插件重新启用后 Codex 连不上网关的问题。宿主在 `v0.3.5` 起丢失了写入“比较基线”的挂钩，真实请求到达插件时 `OriginalBodyJson` 恒为空；启用完整性检查的保护传输插件（例如账号保护 mode1 默认 `enforce`）会把每个 `/responses` 请求判为拒绝，客户端收到 403 `extension_error`。现在 Responses、透传、Chat Completions 与 WebSocket HTTP Bridge 都会在出站前写入最终请求体作为基线，插件同时拿到映射后的模型和默认 instructions 摘要。
- 命中保护传输的账号固定使用 HTTP/SSE 上游，避免 WebSocket 上游绕过插件。
- API 接入中心的 Codex 示例配置改为 `env_key = "SUB2API_API_KEY"`，并附带对应的 PowerShell 环境变量命令。Codex 0.149 起，`requires_openai_auth = false` 的自定义 Provider 不再读取 `auth.json`，旧示例会让 Codex 不带密钥访问网关并收到 401。“复制密钥命令”在 Codex 预设下复制同名变量，其余预设仍复制 `OPENAI_API_KEY`。
- 本轮同步上游 Sub2API `v0.2.18`（官方发布提交 `ac1b8f1271be0d674fa6f074403c1410f30a3a81`；合并头 `37067e8e340e9440d173a92b4faa05e8ce58368f`），并保留宿主侧插件运行时、V1/V2 API、契约测试和能力清单。
- 接入作用域管理员 API Key、Gemini 生图分组能力、账号配额快照、流式断开处理、OpenAI overload 重试和新的计费/模型定价修复。
- Windows 原生一键启动沿用 v0.3.26 的平台协议选择规则，本轮没有行为变化。

## 当前兼容内核

- 桌面：`v0.4.1`
- 上游 Sub2API：`v0.2.18`
- 上游提交：`ac1b8f1271be0d674fa6f074403c1410f30a3a81`
- 合并头：`37067e8e340e9440d173a92b4faa05e8ce58368f`
- 本地扩展：`v1.3.20`
- 成本算法：`v1.6.1`
- 必需能力：`account_cost_loss_ledger.v1`、`account_economics_sampling.v1`、`plugin_extensions.v2`、`openai.oauth.protection_transport.v1`、`openai.oauth.request_header_probe.v1`、`plugin_publisher_trust.v1`

## 技术变更

- 回归来源：`9f3c1de9f`（2026-09-23 合并上游 v0.2.8）整段删除了四个转发入口里宿主自有的保护传输挂钩，之后的上游同步没有恢复。`v0.3.4` 之前的版本仍带有这些调用。
- 新增 `OpenAIGatewayService.withProtectionOriginal`，仅在账号命中保护传输时才写入基线，其余账号原样返回上下文。调用点紧贴构造上游请求之前，使用最终已准备好的请求体，每次重试都会刷新：`Forward`、`forwardOpenAIPassthrough`、`forwardAsChatCompletions`，以及 `proxyOpenAIWSHTTPBridgeTurn` 内的请求构造闭包。
- `Forward` 在命中保护传输时把 `wsDecision.Transport` 固定为 `http_sse`，原因记录为 `account_protection_plugin`。
- 新增回归测试 `openai_protection_baseline_test.go`：用进程内假保护传输驱动上述四个入口（含经 `ProxyResponsesWebSocketFromClient` 的端到端 WS 桥接），断言插件收到非空且与线上请求体一致的基线、`mapped_model` 与 instructions 摘要，且没有直连上游；账号不在绑定范围、API Key 账号则不写基线并直连。将四个入口文件还原到修复前，对应用例全部失败。
- 已知未覆盖：`/v1/messages` 转 OpenAI OAuth、count_tokens、alpha/search 等入口仍不提供基线，行为与此前版本一致，开发指南已注明；插件遇到空基线应按“未提供”处理。
- 插件命中时 Codex 的 WebSocket 仍经 HTTP Bridge，`generate:false` 预热会被当作真实生成请求发往上游，该行为未在本轮改变，作为后续事项跟踪。
- 上游合并保留插件宿主专属路径；插件契约、V1/V2 兼容、沙箱和生命周期测试作为发布门禁，任何失败都阻止发布。
- 插件协议和能力清单与上一版兼容；扩展版本递增是因为内核二进制内容变化，已发布的同名内核包不能用不同 SHA-256 覆盖。

## 升级行为

- 可在“版本与更新”中升级，也可以退出程序后使用安装器原位升级；应用身份、数据库连接和已有业务配置保留。
- 新增上游迁移 246/247；首次启动会按现有迁移器升级数据库，发布前必须验证迁移和回滚路径。
- 桌面、兼容内核或扩展版本变化后的首次启动会停用旧插件一次，保留配置、已签名原包、发布者信任及路由作用域；确认兼容后重新启用即可。本轮升级后重新启用保护传输插件，真实请求不再被空基线拒绝。
- 兼容内核先于桌面安装器发布，校验版本、上游提交、扩展版本、能力清单和归档 SHA-256。
- 必需能力清单与 v0.3.26 相同；桌面会按版本、上游提交、扩展版本、能力和 SHA-256 校验后拉取本轮内核。
- 已按旧示例配置 Codex 的用户需要改用 `env_key`：把配置里的 `requires_openai_auth = false` 保持不变，加入 `env_key = "SUB2API_API_KEY"`，并在启动 Codex 前设置同名环境变量。通过桌面“启动 Codex CLI”按钮启动的用户不受影响。

## 验证结果

- 后端：`go test ./... -run '^$'` 编译通过；插件契约/迁移/生命周期、沙箱、V1/V2 兼容测试通过。Windows race 检查因本机没有 gcc 跳过，必须由同一源码 SHA 的 Linux CI 补齐。
- 前端：完整 Vitest、ESLint、类型检查和桌面构建作为发布门禁；插件页面回归覆盖配置 revision、签名包、路由和二次验证。
- 发布门禁要求同一源码提交通过完整后端 CI（unit、integration、golangci-lint）、前端测试与 lint、Rust 桌面测试和生产构建；稳定内核通道在桌面安装器之前发布并回读校验。
- 本地未使用真实签名的保护传输插件包和真实 Codex 客户端；生产插件 E2E 与 race 结果以同一源码 SHA 的 CI 为准。
- 已知的非绿检查：发布提交上的安全扫描未通过，两项均为近期新公布的公告，与本轮改动无关，依赖清单本轮未改动。govulncheck 报告 Go 标准库 `crypto/x509`（GO-2026-6809，Go 1.27.3 修复，可经 passkey 登录路径到达）；前端审计报告 follow-redirects 的两条高危公告（GHSA-q3jh-wrc6-jgq8、GHSA-8r9p-f939-6h3c）。需要在后续版本升级 Go 工具链和该依赖。

## 回滚说明

- 本轮包含数据库迁移，回退前必须备份数据库并确认迁移兼容；桌面可回退到 v0.4.0 及匹配的内核 `v0.2.17` / 扩展 `1.3.19`。
- 兼容内核不允许降级发布，也不允许同名包更换内容。出现问题时向前修复并递增扩展版本；客户端可使用本地回滚槽位回到上一个内核。
- Tauri updater 的 `.sig` 用于自动更新验签，不代表 Windows Authenticode 发行者签名。

## 用户交流

欢迎加入 QQ 用户交流群 **960663114**，交流安装使用、插件配置和运维经验。[查看 README 交流群入口](https://github.com/rw0104/sub2api-cost-console#community)。
