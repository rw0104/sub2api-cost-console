# Sub2API Cost Console v0.3.16

## 主要更新

- 接入上游 Sub2API v0.2.10，带来 Claude Sonnet 5.5、Claude 原生重置额度查询、风控用户白名单、仪表盘 Token/金额切换，以及复合分组 WebSocket 路由修复。
- 同步上游流式用量归一化、缓存输入扣减、工具改写和 Antigravity 首内容前中断修复；保留宿主成本账本、插件策略和成本扩展。
- 延续桌面插件配置保存的 CORS 预检修复：允许 `If-Match`、`X-Plugin-Revision`，并读取插件版本与操作 ID 响应头。
- 保留 Windows 原生一键启动、原应用身份、托盘恢复和 Header Probe 观测显示修复；桌面与稳定内核按同一源码发布。

## 当前兼容内核

- 桌面：`v0.3.16`
- 上游 Sub2API：`v0.2.10`
- 上游提交：`2f3fed2fdb0787141294cec81487a5df30426f7f`
- 本地扩展：`v1.3.8`
- 成本算法：`v1.6.1`
- 必需能力：`account_cost_loss_ledger.v1`、`account_economics_sampling.v1`、`plugin_extensions.v2`、`openai.oauth.protection_transport.v1`、`openai.oauth.request_header_probe.v1`、`plugin_publisher_trust.v1`

## 技术变更

- 合并上游 v0.2.10 的复合分组模型路由与 Responses WebSocket 别名处理，保留客户端请求模型用于响应身份和用量记录，并用解析后的上游模型执行渠道映射。
- 同步 Claude Sonnet 5.5、原生重置额度、风控白名单、近期用量展示和流式用量修复；模型、缓存、工具改写与 Antigravity 回归覆盖上游变更。
- 成本扩展提升至 1.3.8；成本算法和六项必需能力保持不变，使用新名称发布内核包，避免覆盖稳定通道已引用的包。
- 宿主 CORS 继续暴露 `ETag`、`X-Plugin-Revision`、`X-Plugin-Operation-ID` 和 `Server-Timing`；来源白名单与 `If-Match` 版本保护保持不变。
- 本轮未新增数据库迁移；上游服务行为变化通过后端、前端和桌面契约验证。

## 升级行为

- 可在“版本与更新”中升级，或退出程序后使用本安装器原位升级；应用身份、数据库连接和已有业务配置保留。
- 桌面、兼容内核或扩展版本变化后的首次启动会停用旧插件一次，保留配置、已签名原包、发布者信任及路由作用域；确认兼容性后重新启用。同版本普通重启不会重复停用。
- 独立内核更新会校验版本、上游提交、扩展、能力清单和归档 SHA-256；缺少成本能力或来源不匹配时拒绝安装，不自动降级。

## 验证结果

- 正式发布以本版本同一源码提交通过完整后端 CI、前端测试、Rust 测试、生产构建和发布契约为前提；发布后下载验收结果见 [RELEASE_VERIFICATION.json](https://github.com/rw0104/sub2api-cost-console/releases/download/v0.3.16/RELEASE_VERIFICATION.json)。
- 后端门禁覆盖 `go test -tags=unit ./...`、`go test -tags=integration ./...` 和 `golangci-lint run --timeout=30m ./...`，包括计费、复合路由、流式用量、模型映射和插件传输回归。
- 发布契约门禁核对内核源码基线、扩展版本、算法版本、六项必需能力、二进制 `--version`、内核包 SHA-256 和桌面更新签名。
- 发布后需实际下载验证安装器更新签名、内核包与 SDK 的 SHA-256、manifest/二进制版本/源码 SHA，以及公开更新和 QQ 二维码地址。

## 回滚说明

- 本版不新增数据库迁移。需要回退时可使用 v0.3.15 桌面及其兼容内核，并核对实际启用的内核版本；独立内核更新不会因桌面回退自动撤销。
- 回退内核将恢复 v0.2.9 的上游模型路由、用量归一化和账号功能；历史已记录用量保持原样。v0.3.12 及更早桌面还可能存在已修复的托盘事件丢失问题。
- Tauri updater 的 `.sig` 用于自动更新验签，不代表 Windows Authenticode 发行者签名。

## 用户交流

欢迎加入 QQ 用户交流群 **960663114**，交流安装使用、插件配置和运维经验。[查看 README 交流群入口](https://github.com/rw0104/sub2api-cost-console#community)。

<img src="https://raw.githubusercontent.com/rw0104/sub2api-cost-console/v0.3.16/assets/qq-community-qrcode.jpg" alt="Sub2API Cost Console QQ 用户交流群二维码，群号 960663114" width="320" />