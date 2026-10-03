# Sub2API Cost Console v0.3.21

## 主要更新

- **「本地服务监听」移到「API 接入」**：监听设置不再放在「版本与更新」面板，现在位于 API 接入中心的网关地址下方。
- **监听设置可以直接输入**：监听范围改为下拉选择（仅本机 / 局域网 / 自定义 IPv4），自定义地址和端口都是普通输入框。
- 其余功能与 v0.3.20 相同，Windows 原生一键启动沿用；内核源码与 v0.3.20 一致。

## 当前兼容内核

- 桌面：`v0.3.21`
- 上游 Sub2API：`v0.2.13`
- 上游提交：`3040209f205472038c1ba745a1bedd2edd9053b1`
- 本地扩展：`v1.3.13`
- 成本算法：`v1.6.1`
- 必需能力：`account_cost_loss_ledger.v1`、`account_economics_sampling.v1`、`plugin_extensions.v2`、`openai.oauth.protection_transport.v1`、`openai.oauth.request_header_probe.v1`、`plugin_publisher_trust.v1`

## 技术变更

- 监听设置组件从 `DesktopUpdateCenter` 移到 `CostApiAccessPanel`；内核启动失败页上的同一入口保留，用于端口被占用时恢复。
- 内核 Go 源码没有变化；内置网页随界面改动重新构建，因此扩展版本递增到 1.3.13。

## Codex 模型列表说明

Codex 里列出的模型来自账号配置：OpenAI 账号只要配置了模型白名单，网关就按白名单生成目录。想让列表与官方客户端一致，可任选其一：

- 清空 OpenAI OAuth 账号的模型白名单，网关会直接转发官方清单；
- 在分组上开启「Codex 模型清单固定账号」；
- 在分组上启用「模型白名单」，只勾选需要的模型。

## 升级行为

- 可在“版本与更新”中升级，也可以退出程序后使用安装器原位升级；应用身份、数据库连接和已有业务配置保留。
- 桌面、兼容内核或扩展版本变化后的首次启动会停用旧插件一次，保留配置、已签名原包、发布者信任及路由作用域。
- 独立内核更新会校验版本、上游提交、扩展版本、能力清单和归档 SHA-256；缺少成本能力或来源不匹配时拒绝安装，不自动降级。

## 验证结果

- 正式发布以本版本同一源码提交通过完整后端 CI、前端测试、Rust 测试、生产构建和发布契约为前提。
- 发布门禁覆盖 `go test -tags=unit ./...`、`go test -tags=integration ./...`、`golangci-lint run --timeout=30m ./...`、前端测试与 lint、Rust 桌面测试，以及内核/安装器签名和 SHA-256 校验。
- 发布后实际下载验收需核对安装器更新签名、兼容内核包、manifest、二进制版本、上游源码 SHA 和开发资料校验和。

## 回滚说明

- 可回退到 v0.3.20 桌面及扩展 1.3.12 内核，没有数据库迁移需要处理；回退后监听设置入口回到「版本与更新」面板。
- Tauri updater 的 `.sig` 用于自动更新验签，不代表 Windows Authenticode 发行者签名。

## 用户交流

欢迎加入 QQ 用户交流群 **960663114**，交流安装使用、插件配置和运维经验。[查看 README 交流群入口](https://github.com/rw0104/sub2api-cost-console#community)。
