# Sub2API Cost Console v0.2.36

## 主要更新

### 集成上游 Sub2API v0.2.5

- 内置兼容内核升级至 `v0.2.5`，增加 OpenCode Zen / GO 账号和按模型选择 Chat Completions、Responses、Anthropic Messages 协议的能力。
- 同步充值与订阅三态站点开关、订阅批量操作、API Key 批量编辑、平台筛选及用量费用精度改进。
- 同步 OpenAI WebSocket 连接池容量、心跳、排队重选、会话抢占和执行作用域隔离修复。
- 同步 Ollama Cloud 用量窗口探测与异步重置、DeepSeek 模型校验和计费、Gemini 响应判定及 Grok 媒体槽位修复。

### 保留桌面恢复、成本能力与 Windows 原生一键启动

- 保留 Docker 与数据容器恢复、内核停止等待、单实例保护和内核双槽回滚。
- 保留成本损失账本、经济采样、K-12 模型限制及成本真实性面板。
- 保留 ChatGPT Desktop、Codex CLI、Claude Code、Cursor Agent、OpenCode 和 Grok CLI 的 Windows 原生一键启动。
- 合并上游临时刷新故障处理：网络、408、429 和 5xx 不清除会话，继续保留跨用户重放防护和桌面 Hash 路由的登录恢复。

## 当前兼容内核

- 上游 Sub2API：`v0.2.5`
- 上游提交：`30ed40a56a5f4b5ab7b8dd3d685353db3a531c84`
- 成本扩展：`v1.1.1`
- 成本算法：`v1.6.0`
- 必需能力：`account_cost_loss_ledger.v1`、`account_economics_sampling.v1`

## 技术变更

- 插件通信依赖 gRPC 升级至 `1.83.2`，修复安全扫描发现的 HTTP/2 内存耗尽与缺失 authority/Host 处理问题。
- 以已集成的官方 `v0.2.4` 源码树为基线合并 `v0.2.5` 增量，逐项处理 7 处冲突。
- 限流服务同时注入成本损失服务与 Ollama 用量探测服务，使用 Wire 重新生成依赖图。
- 桌面 App 入口同时保留受管内核界面和上游站点功能开关；K-12 限制与 DeepSeek 官方模型校验共同生效。
- 安装向导优先连接配置的数据库，只在数据库不存在时连接 postgres 创建数据库；保留桌面 CORS 配置测试。
- 纳入 `238_opencode_go_platform.sql` 与 `238_purge_unlimited_user_platform_quotas.sql`；迁移按完整文件名记录，相同编号不会覆盖。

## 升级行为

- 更新桌面安装包至 `v0.2.36` 可同时获得新界面及内置 `v0.2.5` 内核；独立内核更新不替换旧桌面前端。
- 首次启动新内核执行数据库迁移，包括 OpenCode 平台约束和清理三档限额均为空的记录；无记录继续代表不限额。
- 升级前备份数据库；二进制回滚不能撤销已执行迁移。
- 本次保留成本算法 `1.6.0`、成本扩展 `1.1.1` 和既有服务连接配置。

## 验证结果

- 发布门禁包含 Go 后端测试、前端全量测试、ESLint、Web/桌面生产构建和 Rust 生命周期测试。
- 会话合并使用上游客户端测试与本地实际路由/Pinia 回归共同验证，补充状态 0 和 408 的保留会话覆盖。
- 签名安装包和稳定内核由 GitHub Actions 发布；发布后重新下载并核对 SHA-256、安装器更新签名、内核身份和两个公开更新入口。
- 最终通过情况与下载校验记录见 `docs/2026-09-16_development-core-v0.2.5-desktop-v0.2.36-release-report.md`。

## 回滚说明

- 保留内核双槽验证、健康检查和手动回滚；仅回退内核不能还原数据库迁移。
- 需要完整回退到旧内核时，应配合升级前数据库备份恢复，并考虑旧桌面对新增平台和站点开关的支持差异。
