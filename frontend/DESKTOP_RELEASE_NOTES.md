# Sub2API Cost Console v0.3.22

## 主要更新

- 接入上游 Sub2API v0.2.14，包含管理员初始化、EasyPay 支付回调校验和 Codex 远程模型目录发现修复。
- 新安装在未指定管理员信息时生成随机登录邮箱和密码；已有部署不会重新创建管理员或更改凭据。
- Codex 远程模型目录配置启用 API Key 模型发现，覆盖“使用密钥”、桌面 API 接入配置和 Windows 原生一键启动。
- 保留成本账本、插件扩展、本地服务监听设置、Windows 托盘恢复和 Header Probe 观测显示。

## 当前兼容内核

- 桌面：`v0.3.22`
- 上游 Sub2API：`v0.2.14`
- 上游提交：`0363b8cdba8cec3e2ba4b2dbd49c4481143fa55d`
- 本地扩展：`v1.3.14`
- 成本算法：`v1.6.1`
- 必需能力：`account_cost_loss_ledger.v1`、`account_economics_sampling.v1`、`plugin_extensions.v2`、`openai.oauth.protection_transport.v1`、`openai.oauth.request_header_probe.v1`、`plugin_publisher_trust.v1`

## 技术变更

- 使用与官方 v0.2.14 标签一致的完整源码树作为合并基线，保留宿主成本功能、插件隔离和桌面跨域配置。
- EasyPay 回调只接受规定的参数；支付返回地址移除用户提供的查询参数与片段，阻止订单创建签名被复用为伪造支付成功通知。
- 管理员初始化统一邮箱与密码校验：邮箱必须符合登录接口规则，密码长度为 8 至 72 字节，随机凭据仅在实际创建管理员时生成。
- 远程 Codex 目录使用 `api_key_model_discovery = true`；文件目录模式保留原有行为，服务端模型目录规则沿用上游。
- 合入上游 Vue 与 source-map-js 依赖更新，保留桌面 Tauri 依赖和现有安全覆盖规则；插件协议、SDK 接口和六项必需能力未变。

## 升级行为

- 可在“版本与更新”中升级，也可以退出程序后使用安装器原位升级；应用身份、数据库连接和已有业务配置保留。
- 桌面、兼容内核或扩展版本变化后的首次启动会停用旧插件一次，保留配置、已签名原包、发布者信任及路由作用域；确认兼容后可重新启用。
- 独立内核更新校验版本、上游提交、扩展版本、能力清单和归档 SHA-256；发布流程先提供匹配的内核包，再发布桌面安装器。
- 使用旧 Codex 配置的用户可重新复制 API 接入配置，或在已有 `[features]` 段中添加 `api_key_model_discovery = true`；桌面启动参数会自动包含该设置。

## 验证结果

- 正式发布以同一源码提交通过完整后端 CI、前端测试、Rust 测试、生产构建和发布契约为前提。
- 发布门禁覆盖 `go test -tags=unit ./...`、`go test -tags=integration ./...`、`golangci-lint run --timeout=30m ./...`、前端测试与 lint、Rust 桌面测试，以及内核来源与能力校验。
- 发布后实际下载验收核对安装器更新签名、兼容内核包、manifest、二进制版本、源码 SHA 和开发资料校验和；自动化验证不替代真实安装升级体验测试。

## 回滚说明

- 本轮没有新增数据库迁移。需要回退时可使用 v0.3.21 桌面及其匹配的 v0.2.13 / 扩展 1.3.13 内核；回退将失去本轮安全修复和 Codex 发现调整。
- Tauri updater 的 `.sig` 用于自动更新验签，不代表 Windows Authenticode 发行者签名。

## 用户交流

欢迎加入 QQ 用户交流群 **960663114**，交流安装使用、插件配置和运维经验。[查看 README 交流群入口](https://github.com/rw0104/sub2api-cost-console#community)。
