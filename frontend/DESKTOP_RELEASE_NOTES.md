# Sub2API Cost Console v0.3.19

## 主要更新

- 集成上游 Sub2API **v0.2.12**，新增 TypeSafe / Jev System One 原生平台、充值优惠阶梯、账号优先级快捷调整和 API Key 分组排序。
- 同步上游安全修复：Grok CLI 1.0.46 身份头与 426 处理、Antigravity 错误信息脱敏、邮箱验证码并发限流、重置密码令牌哈希存储与单次消费、匿名订单按 IP 限流。
- 完整保留宿主扩展：账户成本账本、插件系统、CORS 与 `X-Plugin-Revision`、Fast/priority 定价倍率、Windows 托盘恢复和 Header Probe 观测显示。
- Windows 原生一键启动、现有应用身份和兼容内核自动更新继续沿用。

## 当前兼容内核

- 桌面：`v0.3.19`
- 上游 Sub2API：`v0.2.12`
- 上游提交：`5106065716e494204fc0e8db16f68f6e9d576be0`
- 本地扩展：`v1.3.11`
- 成本算法：`v1.6.1`
- 必需能力：`account_cost_loss_ledger.v1`、`account_economics_sampling.v1`、`plugin_extensions.v2`、`openai.oauth.protection_transport.v1`、`openai.oauth.request_header_probe.v1`、`plugin_publisher_trust.v1`

## 技术变更

- 以官方 v0.2.12 源码树为新的合成上游基线，并在其上重放成本账本、插件协议、桌面启动与更新通道扩展。
- 新增 `payment_orders.bonus_amount` 和 `typesafe` 平台迁移；首次启动会自动执行数据库迁移。
- TypeSafe System One 使用原生非流式协议，支持账号测试、分组/渠道/配额、计费、内容审核与提示词审计；不兼容的流式或 Chat/Responses/Messages 请求会返回 `404`。

## 升级行为

- 可在“版本与更新”中升级，也可以退出程序后使用安装器原位升级；应用身份、数据库连接和已有业务配置保留。
- 桌面、兼容内核或扩展版本变化后的首次启动会停用旧插件一次，保留配置、已签名原包、发布者信任及路由作用域。
- 独立内核更新会校验版本、上游提交、扩展版本、能力清单和归档 SHA-256；缺少成本能力或来源不匹配时拒绝安装，不自动降级。
- v0.2.12 的重置密码令牌存储方式改变，升级前已发出但尚未使用的旧链接会失效，需要重新申请。

## 验证结果

- 正式发布以本版本同一源码提交通过完整后端 CI、前端测试、Rust 测试、生产构建和发布契约为前提。
- 发布门禁覆盖 `go test -tags=unit ./...`、`go test -tags=integration ./...`、`golangci-lint run --timeout=30m ./...`、前端测试与 lint、Rust 桌面测试，以及内核/安装器签名和 SHA-256 校验。
- 发布后实际下载验收需核对安装器更新签名、兼容内核包、manifest、二进制版本、上游源码 SHA 和开发资料校验和。

## 回滚说明

- 可回退到 v0.3.18 桌面及其 v0.2.11 兼容内核；v0.2.12 已执行的数据库迁移不会自动逆向，回滚前请保留数据库备份。
- Tauri updater 的 `.sig` 用于自动更新验签，不代表 Windows Authenticode 发行者签名。

## 用户交流

欢迎加入 QQ 用户交流群 **960663114**，交流安装使用、插件配置和运维经验。[查看 README 交流群入口](https://github.com/rw0104/sub2api-cost-console#community)。
