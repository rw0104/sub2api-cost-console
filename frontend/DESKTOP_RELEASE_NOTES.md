# Sub2API Cost Console v0.4.7

## 主要更新

### 上游 v0.2.20 与 Codex CLI 0.167 兼容

- 合入上游 v0.2.20：非流式 Responses 正确返回 incomplete/cancelled，兼容缺少 Content-Type 的 SSE；允许空 instructions，并按客户端原值恢复响应回显。
- 修复 WebSocket HTTP 桥接的 Codex 指纹身份与逐轮首响统计。只从重放副本清理输出专属 token 统计，保留原始响应、客户端本轮输入、输出预算和工具选择。
- Codex 稳定版兜底及目录请求更新为 0.167.0；管理员覆写和稳定版自动同步仍优先，不默认追随 alpha。
- Terra Codex 目录对齐默认 500,000、最大 872,000 上下文；API 模型总窗口维持独立口径。保持 API Key/订阅路径的能力差异与 effort 校验。
- Terra 订阅长上下文计费沿用宿主既有账户配置，未采用缺少明确官方依据的上游新增隐式豁免。保留 Sol 订阅豁免、Ultrafast 6 倍和成本算法 1.7.0。

### 管理 MCP、权限与图片能力

- 增加 OAuth 管理 MCP、授权确认和连接撤销页面，支持 PKCE、权限范围、只读查询和管理员写操作二次验证。默认关闭，配置有效 HTTPS MCP_PUBLIC_URL 后启用。
- 补全管理员密钥的统计、OpenAI OAuth 等权限；全权兼容模式需要明确确认。宿主精确时间窗口统计同样校验账号资源范围。
- 增加 GPT Image 2.6 原生图片请求、复杂度与编辑参数及 1K/2K/4K 尺寸路由。Images、Responses 和 Lite 按各自协议能力处理，不混同独立图片与托管生图。
- 同步上游注册邮箱黑名单、使用记录导出及相关供应商修复。

### 调度恢复与资源边界

- 显式刷新/重置配额后，仅在可用额度证据完整且账号代际未变时恢复旧额度冷却。迟到查询不清除新 429、认证错误、过载、临时封禁或手动停用。
- 等待并发名额后重新读取账号状态，失效则释放名额重选。重选共享原始等待截止时间；该预算不限制正常模型执行。
- 增加请求体共享内存预算、上传/下游写入停滞超时、有界上游 drain、流事件及后台任务限制，并包含上游后续 failover 和异步图片预算修复。
- 新增不含正文的 HTTP 诊断：请求/input/tools/instructions 的 UTF-8 JSON 字节数、可确定的原生上游尝试次数及末次请求体积。插件接管时不伪造内部尝试数，持久化遵守访问日志设置。

### Windows 原生一键启动与配置导出

- 显示 ChatGPT/Codex 实际产品、版本、安装来源和发行通道，区分桌面随附 CLI 与 npm/PATH CLI。MSIX 包标识不代替实际产品显示名。
- 客户端探测隐藏窗口、有时间和输出上限、后台执行；探测失败不禁用已有启动能力，不写官方客户端登录配置。
- 密钥弹窗、WebSocket 配置、CC Switch 和原生启动器统一使用 sub2api provider。默认 Sol，导出实时联网搜索、模型目录和图片鉴权；CC Switch 保留用户全局功能开关。

## 当前兼容内核

- 桌面：v0.4.7
- 上游 Sub2API：v0.2.20
- 上游源码：dab829a4e9b7d1cc307189aea8b0319eb29e29d0
- 宿主扩展：1.3.26
- 成本算法：1.7.0
- 必需能力：account_cost_loss_ledger.v1、account_economics_sampling.v1、plugin_extensions.v2、openai.oauth.protection_transport.v1、openai.oauth.request_header_probe.v1、plugin_publisher_trust.v1。

## 技术变更

- 新增数据库迁移 249_mcp_oauth.sql，自动创建 MCP 授权存储，不修改已应用迁移。
- 配额恢复使用数据库原子条件更新并刷新调度快照；普通查询保持只读。
- 同时保留插件 revision/operation 头及 MCP 协议/发现头；依赖装配与退出清理覆盖宿主经济服务、通知队列和会话缓存。
- 前端升级 vue-i18n 11，保留桌面/Web 独立构建目录及已有依赖安全修正。
- cockpit-tools 仅用于行为与兼容用例参考，未复制其限制许可源码。

## 升级行为

- 可从“版本与更新”升级，也可退出后运行安装器原位升级；沿用程序身份、数据目录和端口。
- 实际资源配置键为 gateway.request_body_memory_budget_bytes（默认 1 GiB）、gateway.request_body_read_timeout_seconds 和 gateway.downstream_write_timeout_seconds（默认各 120 秒）。请求体预算不是进程总内存上限，应按部署内存配置；预算不足返回 503 与 Retry-After。
- 客户端断开后的上游读取有总时限，无法收集无限延迟的 usage。
- 旧 provider ID 创建的 Codex 会话要继续执行时，应使用原 provider；更换 sub2api 不会自动迁移会话身份。
- 桌面/内核/扩展身份变化后的首次启动，按既有规则停用旧插件一次，保留配置、签名原包、发布者信任及作用域，确认兼容后重新启用。
- 重置卡已兑换但可用额度尚未确认时，保留当前限额并提示刷新核验，不把调度恢复写成成功或鼓励重复兑换。

## 验证结果

发布须通过最终同一源码 SHA 的完整后端 unit/integration/lint、插件契约/生命周期/作用域/V1/V2/race、前端检查、Rust 桌面测试及安装包构建。具体执行结果以对应 GitHub Actions 和发布后校验记录为准。

协议回归使用受控上游及合成账号，不代表所有真实套餐均开通模型、图片或管理权限。安装器、更新签名、稳定内核、manifest 和 SDK 在发布后重新下载核对。

## 回滚说明

- 可用现有回滚槽位回到桌面 v0.4.6 对应 core 0.2.19 / extension 1.3.25。保留新增 MCP 表，不手工删表或修改迁移校验值；旧版不提供管理 MCP。
- 稳定内核通道不以低版本覆盖高版本，也不允许同名 ZIP 更换内容；发布问题向前修复并递增扩展版本。
- Tauri .sig 用于自动更新验签，不等同于 Windows Authenticode 发行者签名。

## 用户交流

QQ 用户交流群：**960663114**。[源码和用户文档](https://github.com/rw0104/sub2api-cost-console)。
