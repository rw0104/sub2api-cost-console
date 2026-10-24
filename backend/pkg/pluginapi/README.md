# Sub2API 本地插件协议

本目录是插件开发者可以依赖的公开契约。`v1/plugin.proto` 和 `v1/runtime.go` 定义现有传输插件协议，`v1/manifest.schema.json` 定义 v1 包清单；`v2/` 定义通用用户态扩展的能力模型和协议源文件，`docs/` 记录开发和发布规范。Provider 私有实现不应放入本目录。

## 通用扩展契约 v2

[`v2/`](v2/) 是与现有 v1 传输插件并行的通用能力契约。第一版定义 `request.preprocess.v1` 请求预处理能力，并统一描述能力类型、权限、超时、失败策略、脱敏请求上下文和 `pass`/`modify`/`deny`/`error` 决策。

v2 已接入宿主进程管理、清单校验、能力路由、管理页和 OpenAI HTTP 出站预处理。可运行的 [请求策略示例](examples/preprocess/README.md) 包含运行时、配置 UI 和打包器。具体权限和修改白名单见 [v2 说明](v2/README.md)。

**正式桌面 v0.3.0 / 扩展 1.3.0 已包含这些能力。** 另提供 [v2 OpenAI OAuth 保护传输](docs/protection-transport.md)。完整的 SDK、proto、Schema、示例、打包器及依赖源码可从 [v0.3.0 发布页](https://github.com/rw0104/sub2api-cost-console/releases/tag/v0.3.0) 的 `sub2api-plugin-devkit-v0.3.0.zip` 下载。

## 开发文档

从零对接入口：[插件对接指南](../../../docs/PLUGIN_DEVELOPMENT.md)，包含可运行示例、密钥生成、签名打包、管理 API、安装调试、灰度与升级回滚。

- [v1 传输开发指南](docs/development.md)：维护旧传输协议的运行时、配置及测试。
- [UI Bridge](docs/ui-bridge.md)：沙箱配置 UI 的消息结构和安全要求。
- [包格式](docs/package-format.md)：清单、文件哈希、签名和版本规则。
- [安全边界](docs/security.md)：进程权限、敏感数据和故障策略。
- [v2 容器隔离](docs/sandbox.md)：无网络、只读文件、低权限与资源限制的配置和真实验证。
- [v2 Host API](docs/host-api.md)：按实例授权的日志、指标、配置、短期秘密读取和受限事件。

## 实体与运行方式

插件的交付实体是一个 `.s2plugin` 文件，本质上是带清单、签名、独立可执行文件和静态 UI 的 ZIP 包。管理员在独立的插件管理页手动上传，Sub2API 不从网络自动下载插件，也不要求 Docker。

启用后，Sub2API 以子进程方式拉起当前操作系统和 CPU 架构对应的二进制，通过本机 gRPC 流传递请求与响应。插件进程退出时会随 Sub2API 清理；停用时先停止接收新请求，再等待正在处理的请求结束。

多实例部署不要求共享插件目录。宿主会在数据库保存已验签的原始插件包，各实例缺少本地文件时会重新验签和解包，并周期性对齐启用状态、灰度比例和加密配置。所有实例必须连接同一数据库并使用相同的加密密钥。

独立进程是代码和发布边界，不是操作系统安全沙箱。插件拥有 Sub2API 服务用户所拥有的文件和网络权限，因此只应安装可信发布者的签名包。闭源二进制可提高源码分发门槛，但不能承诺无法反编译。

## 初期能力边界

v1 能力 `openai.oauth.outbound_transport.v1`：

- 仅匹配 `platform=openai` 且 `account_type=oauth` 的上游 HTTP 请求。
- API Key 账号、其他 provider、OAuth 登录与 Token 刷新流程不进入插件。
- 插件建立真实的上游 HTTP/TLS 连接并返回原始 HTTP 响应。
- 命中插件的 OAuth WebSocket 账号会使用 Sub2API 现有 HTTP Bridge，不直接建立上游 WebSocket，避免绕过 v1 HTTP 插件协议。
- Sub2API 继续负责响应状态处理、SSE 解析、错误映射、用量统计、计费和下游输出。
- 灰度比例以账号 ID 稳定分桶，未命中的 OAuth 账号继续使用原有内置路径。

## 宿主服务（HostService）

`HostService` 是宿主经 go-plugin broker 反向暴露给插件的通用能力层，独立于具体插件类型，供有状态插件使用。它通过 `TransportPlugin.InitHostServices` 在运行时协商：宿主启动后把一个 broker 流 id 交给插件，插件用它拨号回宿主并获得 `HostServiceClient`。

- **可选且向后兼容**：未实现 `InitHostServices` 的旧插件返回 `Unimplemented`，宿主静默跳过，转发能力不受影响。宿主服务有独立的 `HostServiceAPIVersion`，新增能力不会改变传输契约版本，也不会使既有插件失效。
- **随进程回收**：宿主服务实例与插件进程生命周期绑定，插件退出时 broker 关闭并自动 `GracefulStop`，无需插件手动清理。

当前提供的通用设施：

- **命名空间键值存储（KV）**：`KVGet` / `KVSet` / `KVDelete` / `KVList`，为插件持久化跨请求、跨副本、跨重启的状态。存储由 Redis 支撑，因此多实例部署天然共享同一份状态。
  - **命名空间隔离**：宿主根据服务该连接的运行时注入插件身份（`pluginKey`），插件无法伪造，也无法读写其它插件的命名空间。
- **护栏**：`namespace` / `key` 仅允许 `[A-Za-z0-9._-]`；单值上限 256 KiB；`ttl_seconds` 为 0 表示不过期、正值有上限；`KVList` 返回条数有上限。

- **账号只读目录（HostService API v2）**：`ListAccounts` 保留 `account_ids`，并额外返回 binding scope 内的 `AccountInfo`。`AccountInfo.metadata_json` 只包含宿主生成的非机密字段；已规范化的 `subscription` 包含 `plan_type`、`source` 和可选 `workspace_id`。账号范围由已启用的 OpenAI OAuth binding 固定，插件不能传入其它账号或扩大范围。`ResolveOutboundIdentity` 仍是唯一的凭据通道，旧插件只读取 `account_ids` 即可继续运行。

新增宿主设施时，在 `HostService` 上追加 RPC 即可，无需改动传输契约或清单格式。

v2 扩展若声明 `account.metadata.read`，可通过 `HostServices.ReadAccountMetadata` 读取当前 binding scope 内单个账号的有界非机密元数据；未命中 scope 返回 `found=false`，不会返回凭据或允许枚举。

## 包结构

```text
manifest.json
signature.json                 # 生产包必需
runtimes/linux-amd64/plugin
runtimes/linux-arm64/plugin
runtimes/windows-amd64/plugin.exe
ui/index.html
ui/assets/...
```

`manifest.json` 必须声明所有运行时和 UI 文件的 SHA-256。`signature.json` 使用发布者 Ed25519 私钥对 `manifest.json` 原始字节签名，并附上 `public_key`。正式桌面 0.3.0 会在首次导入时展示发布者并要求确认，确认后保存公钥，普通用户无需编辑配置。官方内置公钥及 `plugins.trusted_publishers` 预配置仍受优先保护。文件哈希由已签名清单保护。

插件默认保持停用。未签名包默认拒绝安装；`plugins.allow_unsigned` 只应用于开发者自己构建的本地调试包。

## 兼容性

清单必须同时声明：

- `requires.sub2api`：允许的 Sub2API 语义化版本范围。
- `requires.recommended_sub2api_version`：建议使用的宿主版本。
- `requires.tested_sub2api_versions`：发布者实际验证过的宿主版本。
- v1 使用 `plugin_protocol=1`、`transport_api=1`、`ui_bridge=1`；v2 使用 `plugin_protocol=2`、`extension_api=1`、`ui_bridge=1`。

宿主版本超出范围时，插件可以安装并查看，但保持“不兼容”状态且不能启用。版本在范围内但未列入已测试版本时，管理员必须再次确认才能启用。

## UI 隔离与 Bridge

插件 UI 由包内静态文件实现，宿主使用只有 `allow-scripts` 权限的 sandbox iframe 加载。iframe 没有管理员 Token，也不能直接访问管理 API。宿主为每次打开配置页生成短时资源 URL 和独立 Bridge Token，并且同时校验消息来源窗口与 Token。

UI 可以发送以下消息。消息按语义分层，鉴权与副作用一致对应（读操作免二次验证，写/主动测试需要二次验证），任何插件都可复用，不针对具体插件定制：

| 消息 | 语义 | 二次验证 | 映射的插件 RPC |
| --- | --- | --- | --- |
| `config.load` | 读取已保存配置 | 否 | 宿主数据库 |
| `config.save` | 写入配置 | 是 | `ValidateConfig` + `ApplyConfig` |
| `config.test` | 主动测试配置/连通性（可产生副作用） | 是 | `TestConfig` |
| `plugin.status` | 读取运行时状态（无副作用） | 否 | `Health`（`status_json`） |
| `ui.resize` / `ui.notify` | 仅 UI 交互 | — | — |

每个请求消息带 `request_id`，宿主以 `<type>.result` 返回结果。

- 配置整体使用 Sub2API 的密钥加密后存入数据库；运行中插件会先验证并应用新配置，数据库写入失败时恢复旧配置。
- `config.test` 的结果由插件 UI 自行展示（内联或经 `ui.notify`），宿主不再对成功结果强制弹出提示，避免插件把它当作轻量状态轮询时刷屏。
- `plugin.status` 是通用的**只读**状态通道：宿主经 `GET /admin/plugins/:id/status` 调用运行中插件的 `Health`，返回 `{healthy, message, status_json}`。`status_json` 的宿主 envelope 至少包含 `schema`、`revision`、`generated_at`、`plugin_id`、`runtime_instance_id`、`liveness`、`readiness`、`stale`、`binding_id`、`binding_scope_digest`、请求/错误/拒绝计数和 `last_error_code`；插件自定义 JSON 放在 `payload`，并保留兼容的旧顶层字段。插件必须以无副作用方式生成状态（不得应用配置、访问上游或触发探测），因此该端点只读、免二次验证。插件未运行时仍返回结构化 envelope，并以 `PLUGIN_NOT_RUNNING` 或 `RUNTIME_DRAINING` 标记原因。这样带状态面板的插件无需滥用 `config.test` 即可展示运行状态。

## 协议源码

- `v1/plugin.proto`：稳定的进程间消息定义。
- `v1/runtime.go`：Go 插件进程启动入口和宿主客户端声明。
- `v1/manifest.schema.json`：`manifest.json` 的 JSON Schema。

插件通过进程协议协作，不使用 Go 动态链接，也不要求插件与 Sub2API 使用相同编译器或共享内存 ABI。
