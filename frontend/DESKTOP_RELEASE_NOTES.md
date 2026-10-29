# Sub2API Cost Console v0.4.5

## 主要更新

- 同步上游 Sub2API `v0.2.19` 正式版：按操作与资源范围授权的管理员 API Key 继续可用；OpenAI / Composite 分组新增“按张计费”开关（默认关闭，只对专用图片端点生效）；Gemini 同账号重试次数可通过 `gateway.max_same_account_retries_gemini` 配置；Grok 账号新增模型目录诊断。
- 合入上游多项计费修复：Gemini 以 `fileData` 返回的图片计费、渠道按次计费图片档位保留 token 用量、DeepSeek `prompt_cache_hit_tokens` 计为缓存读取、模型广场分组定价不再被图片兜底档位覆盖。DeepSeek V4 Pro 在 2026-09-14 之后恢复按 Pro 官方价计费（官方确认继续提供 Pro），不再按 Flash 价计费。
- 适配最新 Codex 客户端（CLI `0.165.0`）：Codex 0.162 起不再发送顶层 `instructions`，改为把基础提示词作为 `input` 首条 developer 消息。网关识别官方 Codex 客户端的这种请求后不再额外注入默认提示词，避免向上游发送两份不同的基础提示词并多计输入 token；其他客户端和旧请求形态保持原有补齐行为。
- 内置 Codex 客户端版本兜底更新为 `0.165.0`，桌面生成的模型目录地址使用 `client_version=0.165.0`。管理员覆写和自动同步的版本仍优先。
- 保留 v0.4.4 的冷模型目录缓存并发修复（与上游同类修复合并为同一处复查）、全部官方模型补齐、官方模型校对和 GPT-6.1 Terra 支持。
- Windows 原生一键启动、插件协议和六项必需能力与上一版保持兼容。

## 当前兼容内核

- 桌面：`v0.4.5`
- 上游 Sub2API：`v0.2.19`
- 上游提交：`dece26153621959ccd7635814f4b2d08304e681a`
- 已集成上游合并头：`dece26153621959ccd7635814f4b2d08304e681a`（即官方发布提交；不含发布后 upstream main 上尚未发布的提交）
- 本地扩展：`v1.3.24`
- 成本算法：`v1.6.1`
- 必需能力：`account_cost_loss_ledger.v1`、`account_economics_sampling.v1`、`plugin_extensions.v2`、`openai.oauth.protection_transport.v1`、`openai.oauth.request_header_probe.v1`、`plugin_publisher_trust.v1`

## 技术变更

- 以真实合并方式集成上游 `v0.2.19` 官方发布提交（合并基点 `37067e8e3`，68 个上游提交），并以 `synthetic upstream v0.2.19` 记录官方源码树，供发布流程校验上游身份。
- 双方各自加入了 GPT-6.1 Terra 支持，自动合并产生的重复定义已逐项去除：兜底价格、Codex 模型映射、别名、默认模型列表与推理档位各保留一份；价格目录合并上游的 flex/batch 缓存价并保留 `supports_vision`。模型目录缓存的新鲜度复查保留一次。
- 上游新增迁移 `248_group_image_count_billing`；`247_scoped_admin_api_keys` 与宿主已包含的版本逐字节一致。宿主插件迁移 239–247 保持原文件名，不重编号。
- Codex 请求对齐：官方 Codex 客户端（含 `gateway.force_codex_cli` 场景）请求缺少 `instructions` 且 `input` 首项为 developer 消息时，OAuth 转发、透传和 compact 路径都不再写入默认提示词。WebSocket 与消息桥接路径原本就不注入，行为不变。
- 仅支持 Chat Completions 的账号收到 GPT-6.1 Sol/Terra 工具调用请求时，改用上游的统一判定函数拒绝，错误信息显示归一化后的模型名。
- 插件协议、SDK、能力清单和应用身份保持兼容。宿主二进制变化使扩展版本递增，已发布的同名内核包不能用不同 SHA-256 覆盖。

## 升级行为

- 可在“版本与更新”中升级，也可以退出程序后使用安装器原位升级。
- 本版启动时会自动执行上游迁移 `248`，为分组增加 `image_count_billing` 列（默认 `false`，不改变现有计费）。从 v0.4.0 或更早版本升级时仍会经过迁移 246/247；迁移 246 会将已有 Gemini/Antigravity 分组中关闭的 `allow_image_generation` 批量改为开启，降级不会自动恢复，升级前应保留数据库备份。
- 上游刷新了 Gemini 默认模型列表，移除已停用的 2.0/2.5 系列和 `gemini-3-pro-preview`，连接测试默认模型改为 `gemini-3.5-flash`。仍需使用这些模型的账号，请自行在白名单或模型映射中配置。
- DeepSeek V4 Pro 默认价卡在 2026-09-14 之后按 Pro 价计费；使用分组或渠道自定义定价的不受影响。
- 官方 Codex 0.162 及以上版本经由 HTTP 访问 OAuth 账号时，上游请求前缀发生变化，升级后每个进行中的会话可能出现一次提示词缓存未命中。
- 桌面、兼容内核或扩展版本变化后的首次启动会停用旧插件一次，保留配置、已签名原包、发布者信任及路由作用域；确认兼容后重新启用。
- 兼容内核先于桌面安装器发布，校验版本、上游提交、扩展版本、能力清单和归档 SHA-256，并在发布后下载回读。

## 验证结果

- 合并后 `go build ./...`、`go vet` 及前端 `vue-tsc --noEmit` 通过；定价目录无重复键。
- Codex 请求对齐的回归在旧实现上失败、修复后通过：覆盖官方客户端、compact 路径、`force_codex_cli`、透传路径，以及第三方客户端、无 developer 前缀和已带 `instructions` 的对照场景。
- 最终发布门禁要求同一源码 SHA 通过完整后端 unit、integration、golangci-lint、插件契约/生命周期/路由范围/V1/V2/race、完整前端测试与 lint、Rust 桌面测试及生产构建。最终状态以对应 GitHub Actions 执行记录为准。
- Codex 请求对齐依据 Codex `rust-v0.165.0` 源码及上游变更说明，尚未使用真实账号抓取 0.165 请求核对。生产插件 E2E 需要独立测试数据库及 Docker；不可用时必须记录跳过原因，不能据定向测试宣称生产插件链路已完整验证。
- 安装器、Tauri 更新签名、更新清单、稳定内核和开发包须在发布后重新下载，核对版本、能力及 SHA-256。

## 回滚说明

- 本轮可回退到桌面 v0.4.4 及匹配的内核 `v0.2.18` / 扩展 `1.3.23`；回退后不包含上游 v0.2.19 变更和 Codex 请求对齐。迁移 `248` 新增的列在旧版本中不会被读取，回退前仍应备份数据库；跨越迁移 246/247 回到更早版本时，还需确认迁移兼容和数据补偿方案。
- 兼容内核稳定通道不允许降级发布，也不允许同名包更换内容。出现问题时向前修复并递增扩展版本；客户端可使用本地回滚槽位回到上一个内核。
- Tauri updater 的 `.sig` 用于自动更新验签，不代表 Windows Authenticode 发行者签名。

## 用户交流

欢迎加入 QQ 用户交流群 **960663114**，交流安装使用、插件配置和运维经验。[查看 README 交流群入口](https://github.com/rw0104/sub2api-cost-console#community)。
