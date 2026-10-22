# Sub2API Cost Console v0.4.0

## 主要更新

- 修复保护传输插件重新启用后 Codex 连不上网关的问题。宿主在 `v0.3.5` 起丢失了写入“比较基线”的挂钩，真实请求到达插件时 `OriginalBodyJson` 恒为空；启用完整性检查的保护传输插件（例如账号保护 mode1 默认 `enforce`）会把每个 `/responses` 请求判为拒绝，客户端收到 403 `extension_error`。现在 Responses、透传、Chat Completions 与 WebSocket HTTP Bridge 都会在出站前写入最终请求体作为基线，插件同时拿到映射后的模型和默认 instructions 摘要。
- 命中保护传输的账号固定使用 HTTP/SSE 上游，避免 WebSocket 上游绕过插件。
- API 接入中心的 Codex 示例配置改为 `env_key = "SUB2API_API_KEY"`，并附带对应的 PowerShell 环境变量命令。Codex 0.149 起，`requires_openai_auth = false` 的自定义 Provider 不再读取 `auth.json`，旧示例会让 Codex 不带密钥访问网关并收到 401。“复制密钥命令”在 Codex 预设下复制同名变量，其余预设仍复制 `OPENAI_API_KEY`。
- 本轮不同步上游，沿用上游 `v0.2.17`；没有数据库迁移，没有新增必需能力。
- Windows 原生一键启动沿用 v0.3.26 的平台协议选择规则，本轮没有行为变化。

## 当前兼容内核

- 桌面：`v0.4.0`
- 上游 Sub2API：`v0.2.17`
- 上游提交：`060d0160417782cd962a0c7f460f6cd4565d4916`
- 本地扩展：`v1.3.19`
- 成本算法：`v1.6.1`
- 必需能力：`account_cost_loss_ledger.v1`、`account_economics_sampling.v1`、`plugin_extensions.v2`、`openai.oauth.protection_transport.v1`、`openai.oauth.request_header_probe.v1`、`plugin_publisher_trust.v1`

## 技术变更

- 回归来源：`9f3c1de9f`（2026-09-23 合并上游 v0.2.8）整段删除了四个转发入口里宿主自有的保护传输挂钩，之后的上游同步没有恢复。`v0.3.4` 之前的版本仍带有这些调用。
- 新增 `OpenAIGatewayService.withProtectionOriginal`，仅在账号命中保护传输时才写入基线，其余账号原样返回上下文。调用点紧贴构造上游请求之前，使用最终已准备好的请求体，每次重试都会刷新：`Forward`、`forwardOpenAIPassthrough`、`forwardAsChatCompletions`，以及 `proxyOpenAIWSHTTPBridgeTurn` 内的请求构造闭包。
- `Forward` 在命中保护传输时把 `wsDecision.Transport` 固定为 `http_sse`，原因记录为 `account_protection_plugin`。
- 新增回归测试 `openai_protection_baseline_test.go`：用进程内假保护传输驱动上述四个入口（含经 `ProxyResponsesWebSocketFromClient` 的端到端 WS 桥接），断言插件收到非空且与线上请求体一致的基线、`mapped_model` 与 instructions 摘要，且没有直连上游；账号不在绑定范围、API Key 账号则不写基线并直连。将四个入口文件还原到修复前，对应用例全部失败。
- 已知未覆盖：`/v1/messages` 转 OpenAI OAuth、count_tokens、alpha/search 等入口仍不提供基线，行为与此前版本一致，开发指南已注明；插件遇到空基线应按“未提供”处理。
- 插件命中时 Codex 的 WebSocket 仍经 HTTP Bridge，`generate:false` 预热会被当作真实生成请求发往上游，该行为未在本轮改变，作为后续事项跟踪。
- 插件协议、能力清单和 SDK 接口与 v0.3.26 相同；扩展版本递增是因为内核二进制内容变化，已发布的同名内核包不能用不同 SHA-256 覆盖。

## 升级行为

- 可在“版本与更新”中升级，也可以退出程序后使用安装器原位升级；应用身份、数据库连接和已有业务配置保留。
- 本轮没有新增迁移，首次启动不会改动数据库结构。
- 桌面、兼容内核或扩展版本变化后的首次启动会停用旧插件一次，保留配置、已签名原包、发布者信任及路由作用域；确认兼容后重新启用即可。本轮升级后重新启用保护传输插件，真实请求不再被空基线拒绝。
- 兼容内核先于桌面安装器发布，校验版本、上游提交、扩展版本、能力清单和归档 SHA-256。
- 必需能力清单与 v0.3.26 相同，因此旧桌面也会自动拉取本轮内核。
- 已按旧示例配置 Codex 的用户需要改用 `env_key`：把配置里的 `requires_openai_auth = false` 保持不变，加入 `env_key = "SUB2API_API_KEY"`，并在启动 Codex 前设置同名环境变量。通过桌面“启动 Codex CLI”按钮启动的用户不受影响。

## 验证结果

- 后端：新增回归用例在修复后通过；相关的 OpenAI 网关、透传、Chat Completions、HTTP Bridge、WebSocket、插件与保护传输测试子集通过；`go vet` 与 `gofmt` 干净。
- 前端：API 接入中心面板 36 个用例通过，新增 `env_key` 示例与密钥命令断言；类型检查与 ESLint 通过。
- 发布门禁要求同一源码提交通过完整后端 CI（unit、integration、golangci-lint）、前端测试与 lint、Rust 桌面测试和生产构建；稳定内核通道在桌面安装器之前发布并回读校验。
- 未使用真实签名的保护传输插件包和真实 Codex 客户端做端到端验证。

## 回滚说明

- 本轮没有数据库迁移，可直接回退到桌面 v0.3.26 及匹配的内核 `v0.2.17` / 扩展 `1.3.18`，但会失去本轮的保护传输修复。回退前建议备份数据库。
- 兼容内核不允许降级发布，也不允许同名包更换内容。出现问题时向前修复，发布 `0.2.17` + 扩展 `1.3.20`；客户端可使用本地回滚槽位回到上一个内核。
- Tauri updater 的 `.sig` 用于自动更新验签，不代表 Windows Authenticode 发行者签名。

## 用户交流

欢迎加入 QQ 用户交流群 **960663114**，交流安装使用、插件配置和运维经验。[查看 README 交流群入口](https://github.com/rw0104/sub2api-cost-console#community)。
