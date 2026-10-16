# Sub2API Cost Console v0.3.26

## 主要更新

- 同步上游 Sub2API `v0.2.17`：支持 Gemini 4 Argon（含 Cyber Permissive 变体）及其计费、Antigravity 准确的上游模型目录，Gemini 兼容入口转发思考档位并流式返回思考摘要。
- ChatGPT 订阅上游按公开别名投影后判定 GPT-5.5 退役，退役模型不再出现在模型列表中，也不会被调度。
- Vertex 服务账号已不存在时，账号置为错误并换号；Vertex 共享容量不足导致的 429 不再冷却账号，改为同号退避重试。
- 网关返回的 Responses 对象一律带 `created_at`；OpenAI API Key 账号在 `/v1/messages` 上支持 web_search 模拟；Codex 导入按 workspace 隔离。
- 支付修复：EasyPay 订单查询与通知确认、支付宝退款状态查询、退款失败的原子收尾，以及 Stripe 从本地订单快照读取 client secret。
- 账号列表恢复内联筛选。
- Windows 原生一键启动沿用 v0.3.25 的平台协议选择规则，本轮没有行为变化。

## 当前兼容内核

- 桌面：`v0.3.26`
- 上游 Sub2API：`v0.2.17`
- 上游提交：`060d0160417782cd962a0c7f460f6cd4565d4916`
- 本地扩展：`v1.3.18`
- 成本算法：`v1.6.1`
- 必需能力：`account_cost_loss_ledger.v1`、`account_economics_sampling.v1`、`plugin_extensions.v2`、`openai.oauth.protection_transport.v1`、`openai.oauth.request_header_probe.v1`、`plugin_publisher_trust.v1`

## 技术变更

- 上游把 `Account.IsModelSupported` 重构为先判退役、再判模型映射，OpenAI 调度与 WebSocket 路径改为直接调用 `supportsOpenAIRequestModel`。宿主的 K12 模型白名单随之迁到两条路径共同经过的 `isModelMappingSupported`，调度路径仍受白名单约束。
- 本轮上游没有新增数据库迁移，也没有改动 `go.mod`、ent 生成代码或工作流。
- OpenAI 账号的上游出站仍全部经过插件接缝 `doOpenAIUpstream`，上游没有新增绕过它的直连调用，保护传输与请求头观测不受影响。
- Vertex 服务账号失效沿用通用认证错误的停用路径，与已有凭据错误处理一致，不写终态成本损失账本。
- 账号成本口径与成本算法版本不变。

## 升级行为

- 可在“版本与更新”中升级，也可以退出程序后使用安装器原位升级；应用身份、数据库连接和已有业务配置保留。
- 本轮没有新增迁移，首次启动不会改动数据库结构。
- 桌面、兼容内核或扩展版本变化后的首次启动会停用旧插件一次，保留配置、已签名原包、发布者信任及路由作用域；确认兼容后可重新启用。
- 兼容内核先于桌面安装器发布，校验版本、上游提交、扩展版本、能力清单和归档 SHA-256。
- 必需能力清单与 v0.3.25 相同，因此旧桌面也会自动拉取本轮内核。

## 验证结果

- 发布门禁要求同一源码提交通过完整后端 CI（unit、integration、golangci-lint）、前端测试与 lint、Rust 桌面测试和生产构建。
- 上游合并以 synthetic 基线方式进行，合成提交的目录树与上游 `v0.2.17` 标签逐字节一致；`verify-core-source` 校验通过。
- 唯一的源码冲突位于 `account.go` 的模型判定，按上游新结构合并后保留了 K12 白名单。README 按既有规则保留本仓库版本。

## 回滚说明

- 本轮没有数据库迁移，可直接回退到桌面 v0.3.25 及匹配的内核 `v0.2.16` / 扩展 `1.3.17`，但会失去本轮同步的上游修复。回退前建议备份数据库。
- 兼容内核不允许降级发布，也不允许同名包更换内容。出现问题时向前修复，发布 `0.2.17` + 扩展 `1.3.19`；客户端可使用本地回滚槽位回到上一个内核。
- Tauri updater 的 `.sig` 用于自动更新验签，不代表 Windows Authenticode 发行者签名。

## 用户交流

欢迎加入 QQ 用户交流群 **960663114**，交流安装使用、插件配置和运维经验。[查看 README 交流群入口](https://github.com/rw0104/sub2api-cost-console#community)。
