# Sub2API Cost Console v0.3.20

## 主要更新

- **Codex 模型目录**：原生一键启动和「API 接入」里的 Codex 配置片段现在带上 `model_catalog_url`，Codex 会从本地网关加载模型目录，`gpt-6.1-sol` 等新模型按网关返回的名称和能力显示。
- **自定义监听地址与端口**：在「版本与更新」面板的「本地服务监听」中可设置 `localhost` / `127.0.0.1` / `0.0.0.0` / 指定 IPv4 和端口，保存后重启桌面端生效。
- 集成上游 Sub2API **v0.2.13**：API Key 在结算完成前被删除时仍正常扣减余额；TypeSafe API Key 账号支持上游计费探测。

## 当前兼容内核

- 桌面：`v0.3.20`
- 上游 Sub2API：`v0.2.13`
- 上游提交：`3040209f205472038c1ba745a1bedd2edd9053b1`
- 本地扩展：`v1.3.12`
- 成本算法：`v1.6.1`
- 必需能力：`account_cost_loss_ledger.v1`、`account_economics_sampling.v1`、`plugin_extensions.v2`、`openai.oauth.protection_transport.v1`、`openai.oauth.request_header_probe.v1`、`plugin_publisher_trust.v1`

## 技术变更

- 以官方 v0.2.13 源码树为新的合成上游基线，本版没有新增数据库迁移。
- Windows 原生一键启动 Codex 时追加 `model_providers.sub2api.model_catalog_url`，指向本地网关的 `/v1/models`。
- 桌面壳不再使用编译期固定的 `127.0.0.1:18765`：监听设置保存在应用数据目录的 `desktop-listen.json`，内核启动、健康检查、端口回收和界面 API 地址都按它解析。

## 已有 Codex 配置需要手动处理

桌面端不会改写你的 `~/.codex/config.toml`。如果之前按旧片段配置过，Codex 仍会使用旧的模型列表，请任选其一：

- 在对应的 `[model_providers.<名称>]` 段内加一行 `model_catalog_url = "http://127.0.0.1:18765/v1/models"`，并删除顶层的 `model_catalog_json = "..."`（如果有）；
- 或继续使用本地目录文件：在 API Key 的「使用密钥」弹窗里重新下载模型目录，覆盖 `model_catalog_json` 指向的文件。

如需默认使用新模型，把 `model` / `review_model` 改为 `gpt-6.1-sol`，然后重启 Codex。

## 监听地址说明

- 默认仍是 `127.0.0.1:18765`，不改设置则行为不变。`localhost` 按 `127.0.0.1` 保存；暂不支持 IPv6 和主机名。
- 选择 `0.0.0.0` 或局域网 IP 后，局域网设备可以通过明文 HTTP 访问管理后台和 API，请使用强管理员密码并留意 Windows 防火墙提示。首次安装向导完成前始终只监听本机。
- 修改端口后，已经写入外部客户端（Codex、Claude Code、Cursor 等）的 Base URL 需要改成新地址；桌面内的配置片段和一键启动会自动使用新地址。
- 插件独立测试版继续使用固定的隔离端口，不提供此设置。

## 升级行为

- 可在“版本与更新”中升级，也可以退出程序后使用安装器原位升级；应用身份、数据库连接和已有业务配置保留。
- 桌面、兼容内核或扩展版本变化后的首次启动会停用旧插件一次，保留配置、已签名原包、发布者信任及路由作用域。
- 独立内核更新会校验版本、上游提交、扩展版本、能力清单和归档 SHA-256；缺少成本能力或来源不匹配时拒绝安装，不自动降级。

## 验证结果

- 正式发布以本版本同一源码提交通过完整后端 CI、前端测试、Rust 测试、生产构建和发布契约为前提。
- 发布门禁覆盖 `go test -tags=unit ./...`、`go test -tags=integration ./...`、`golangci-lint run --timeout=30m ./...`、前端测试与 lint、Rust 桌面测试，以及内核/安装器签名和 SHA-256 校验。
- 发布后实际下载验收需核对安装器更新签名、兼容内核包、manifest、二进制版本、上游源码 SHA 和开发资料校验和。

## 回滚说明

- 可回退到 v0.3.19 桌面及其 v0.2.12 兼容内核；v0.2.13 没有新增数据库迁移。回退后自定义监听设置不再生效，桌面恢复为 `127.0.0.1:18765`，已改用新地址的外部客户端需要改回。
- Tauri updater 的 `.sig` 用于自动更新验签，不代表 Windows Authenticode 发行者签名。

## 用户交流

欢迎加入 QQ 用户交流群 **960663114**，交流安装使用、插件配置和运维经验。[查看 README 交流群入口](https://github.com/rw0104/sub2api-cost-console#community)。
