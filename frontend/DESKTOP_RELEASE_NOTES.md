# Sub2API Cost Console v0.3.24

## 主要更新

- 补齐 Cline、Command Code 及其他供应商分组的 Windows 原生一键启动：按平台清单选择网关协议，预览和启动使用相同规则。
- SDK 开发包从 go.mod 读取最低 Go 版本，入门说明与实际模块一致；当前要求 Go 1.27.2 或更高版本。
- 默认分支同步完整的上游 v0.2.15 合并及宿主修复，后续内核自动更新从已验证的基线继续。

## 当前兼容内核

- 桌面：`v0.3.24`
- 上游 Sub2API：`v0.2.15`
- 上游提交：`f2669c8cf62555cd92389b3f55920e9e6e7c6ff2`
- 本地扩展：`v1.3.16`
- 成本算法：`v1.6.1`
- 必需能力：`account_cost_loss_ledger.v1`、`account_economics_sampling.v1`、`plugin_extensions.v2`、`openai.oauth.protection_transport.v1`、`openai.oauth.request_header_probe.v1`、`plugin_publisher_trust.v1`

## 技术变更

- 桌面启动器根据内核同源平台清单的 gateway 字段解析协议。Cline、Command Code 等使用 OpenAI 网关；TypeSafe 使用 Anthropic 网关；Gemini、Antigravity、Grok 与组合分组保留各自配置。
- 未知平台的网关客户端启动被明确阻止，避免发送无效参数；ChatGPT Desktop 的独立启动不依赖账号平台。
- SDK 的 START_HERE.md 和来源清单记录模块声明的最低 Go 版本，独立工程创建步骤读取 SDK 的 go.mod。
- 模型白名单及两个模型同步入口沿用 Sub2API 上游实现。

## 升级行为

- 可在“版本与更新”中升级，也可以退出程序后使用安装器原位升级；应用身份、数据库连接和已有业务配置保留。
- 桌面、兼容内核或扩展版本变化后的首次启动会停用旧插件一次，保留配置、已签名原包、发布者信任及路由作用域；确认兼容后可重新启用。
- 兼容内核先于桌面安装器发布，校验版本、上游提交、扩展版本、能力清单和归档 SHA-256。
- 原生客户端的模型选择仍按客户端配置及账号可用模型决定。

## 验证结果

- 发布门禁要求同一源码提交通过完整后端 CI（unit、integration、golangci-lint）、前端测试与 lint、Rust 桌面测试和生产构建。
- 定向回归覆盖各平台的启动参数、未知平台阻止、ChatGPT 独立启动和既有协议路径。
- SDK 开发包在交付前验证测试、离线示例编译、签名示例包及真实进程 RPC；发布后核对下载文件 SHA-256、更新签名和内核身份。

## 回滚说明

- 相对 v0.3.23，本轮没有新增数据库迁移。可回退到 v0.3.23 及匹配的内核 v0.2.15 / 扩展 1.3.15，但会失去本轮原生启动适配和 SDK 说明修复。
- 从更早版本升级仍会执行上游 v0.2.15 的数据库迁移，回退前应备份数据库。
- Tauri updater 的 `.sig` 用于自动更新验签，不代表 Windows Authenticode 发行者签名。

## 用户交流

欢迎加入 QQ 用户交流群 **960663114**，交流安装使用、插件配置和运维经验。[查看 README 交流群入口](https://github.com/rw0104/sub2api-cost-console#community)。
