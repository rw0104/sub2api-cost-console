# Sub2API Cost Console v0.3.23

## 主要更新

- 接入上游 Sub2API v0.2.15，新增 Cline 与 Command Code 平台，并将平台列表、多协议转发、账号表单和模型同步统一到平台清单。
- 吸收上游协议转换、计费、WebSocket、远程模型目录和前端竞态修复；保留成本账本、插件扩展、桌面监听设置、Windows 托盘恢复与 Header Probe 观测。
- 插件配置 iframe 继续使用宿主生成代数、来源校验、Bridge token 校验和就绪超时，关闭或重开配置窗口时丢弃迟到会话。

## 当前兼容内核

- 桌面：`v0.3.23`
- 上游 Sub2API：`v0.2.15`
- 上游提交：`f2669c8cf62555cd92389b3f55920e9e6e7c6ff2`
- 本地扩展：`v1.3.15`
- 成本算法：`v1.6.1`
- 必需能力：`account_cost_loss_ledger.v1`、`account_economics_sampling.v1`、`plugin_extensions.v2`、`openai.oauth.protection_transport.v1`、`openai.oauth.request_header_probe.v1`、`plugin_publisher_trust.v1`

## 技术变更

- 使用与官方 v0.2.15 标签一致的完整源码树作为合并基线，保留宿主成本功能、插件隔离、桌面跨域配置和受控升级流程。
- 新增 Cline 订阅/积分计费与 Command Code 多协议分流；平台能力、账号表单、路由和模型目录由平台清单驱动。
- 合入上游 Chat ↔ Responses ↔ Anthropic 转换、Responses Lite web_search、Claude/Grok、图片用量、计费隔离和前端异步竞态修复。
- 按 Go 1.27.2 与 golangci-lint 2.14.0 更新后端 CI 工具链；发布流程仍要求完整后端 CI、前端与 Rust 契约通过。

## 升级行为

- 可在“版本与更新”中升级，也可以退出程序后使用安装器原位升级；应用身份、数据库连接和已有业务配置保留。
- 桌面、兼容内核或扩展版本变化后的首次启动会停用旧插件一次，保留配置、已签名原包、发布者信任及路由作用域；确认兼容后可重新启用。
- 独立内核更新校验版本、上游提交、扩展版本、能力清单和归档 SHA-256；发布流程先提供匹配的内核包，再发布桌面安装器。
- Windows 原生一键启动会继续使用当前桌面监听设置与内嵌兼容内核；使用旧 Codex 配置的用户可重新复制 API 接入配置。

## 验证结果

- 正式发布以同一源码提交通过完整后端 CI、前端测试、Rust 测试、生产构建和发布契约为前提。
- 发布门禁覆盖 `go test -tags=unit ./...`、`go test -tags=integration ./...`、`golangci-lint run --timeout=30m ./...`、前端测试与 lint、Rust 桌面测试，以及内核来源与能力校验。
- 发布后实际下载验收核对安装器更新签名、兼容内核包、manifest、二进制版本、源码 SHA 和开发资料校验和；自动化验证不替代真实安装升级体验测试。

## 回滚说明

- 本轮没有新增宿主数据库迁移。需要回退时可使用 v0.3.22 桌面及其匹配的 v0.2.14 / 扩展 1.3.14 内核；回退将失去本轮平台清单、协议转换和上游安全修复。
- Tauri updater 的 `.sig` 用于自动更新验签，不代表 Windows Authenticode 发行者签名。

## 用户交流

欢迎加入 QQ 用户交流群 **960663114**，交流安装使用、插件配置和运维经验。[查看 README 交流群入口](https://github.com/rw0104/sub2api-cost-console#community)。
