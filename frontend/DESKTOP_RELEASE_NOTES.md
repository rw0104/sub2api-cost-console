# Sub2API Cost Console v0.3.21

## 主要更新

- **「本地服务监听」移到「API 接入」**：监听设置不再放在「版本与更新」面板，现在位于 API 接入中心的网关地址下方。监听范围改为下拉选择（仅本机 / 局域网 / 自定义 IPv4），自定义地址和端口可以直接输入。
- **Codex 模型列表与官方一致**：分组内只有官方 OAuth 账号、且账号的模型白名单都是同名条目时，Codex 拉到的是官方模型清单，不再列出白名单里那些官方客户端已不展示的旧模型。
- 其余功能与 v0.3.20 相同，Windows 原生一键启动沿用。

## 当前兼容内核

- 桌面：`v0.3.21`
- 上游 Sub2API：`v0.2.13`
- 上游提交：`3040209f205472038c1ba745a1bedd2edd9053b1`
- 本地扩展：`v1.3.13`
- 成本算法：`v1.6.1`
- 必需能力：`account_cost_loss_ledger.v1`、`account_economics_sampling.v1`、`plugin_extensions.v2`、`openai.oauth.protection_transport.v1`、`openai.oauth.request_header_probe.v1`、`plugin_publisher_trust.v1`

## 技术变更

- `openAIConfiguredCodexModelIDs`：OAuth 账号上的同名映射只算白名单，不算自定义别名。分组内没有其他配置时不再本地生成目录，改为转发账号的官方 Codex 清单。
- 以下情况保持原行为，仍由本地生成目录：存在真正的别名映射（键和值不同）、API Key 账号的映射、影子账号的映射、分组启用了模型白名单。
- 监听设置组件从 `DesktopUpdateCenter` 移到 `CostApiAccessPanel`；内核启动失败页上的同一入口保留，用于端口被占用时恢复。

## Codex 模型列表说明

- 官方清单需要联网向账号的上游拉取；拉取失败时 Codex 会收到错误并沿用它已有的列表。
- 想自己限定列表，请在分组上启用「模型白名单」；想加自定义名称，请在账号上配置别名映射。
- 使用本地目录文件（`model_catalog_json`）的 Codex 配置不受影响，仍显示文件里的内容；改用 `model_catalog_url` 或重新下载目录文件后才会变化。

## 升级行为

- 可在“版本与更新”中升级，也可以退出程序后使用安装器原位升级；应用身份、数据库连接和已有业务配置保留。
- 桌面、兼容内核或扩展版本变化后的首次启动会停用旧插件一次，保留配置、已签名原包、发布者信任及路由作用域。
- 独立内核更新会校验版本、上游提交、扩展版本、能力清单和归档 SHA-256；缺少成本能力或来源不匹配时拒绝安装，不自动降级。

## 验证结果

- 正式发布以本版本同一源码提交通过完整后端 CI、前端测试、Rust 测试、生产构建和发布契约为前提。
- 发布门禁覆盖 `go test -tags=unit ./...`、`go test -tags=integration ./...`、`golangci-lint run --timeout=30m ./...`、前端测试与 lint、Rust 桌面测试，以及内核/安装器签名和 SHA-256 校验。
- 发布后实际下载验收需核对安装器更新签名、兼容内核包、manifest、二进制版本、上游源码 SHA 和开发资料校验和。

## 回滚说明

- 可回退到 v0.3.20 桌面及扩展 1.3.12 内核，没有数据库迁移需要处理；回退后 Codex 模型列表恢复为按账号白名单生成，监听设置入口回到「版本与更新」面板。
- Tauri updater 的 `.sig` 用于自动更新验签，不代表 Windows Authenticode 发行者签名。

## 用户交流

欢迎加入 QQ 用户交流群 **960663114**，交流安装使用、插件配置和运维经验。[查看 README 交流群入口](https://github.com/rw0104/sub2api-cost-console#community)。
