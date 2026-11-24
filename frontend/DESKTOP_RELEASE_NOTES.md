# Sub2API Cost Console v0.4.11

## 主要更新

- 接入中心手动读取模型目录时使用 Codex CLI 0.172.0 的客户端版本。达到本地 API Key 额度上限时，Codex 0.172.0 及更新的稳定版可直接提示密钥已达支出上限，旧版客户端继续收到原有的配额错误码。
- 合并 Sub2API v0.2.24 和 v0.2.25。Codex 通过 HTTP Responses 传入的 VS Code 工作区上下文和已批准的计划可随请求转发。模型目录会根据实际账号和协议显示音频输入能力。
- Codex 压缩上下文时，流中的上游过载会转换为客户端可重试的错误。OpenAI 透传模式保留加密内容无效、上下文超长等错误的结构化字段。
- 管理员可将上游分组名称与描述同步到账号，并在 API Key 账号的请求头覆写中引用用户、密钥、分组或请求 ID。错误详情中可查看该次请求实际上报的身份头。

## 当前兼容内核

| 组件 | 版本 |
| --- | --- |
| Windows x64 桌面安装包 | 0.4.11 |
| 兼容内核 | `v0.2.25` |
| 上游源码 | `348997267d756eab5729bb6b78a275a15d18796a` |
| 宿主扩展 | 1.3.30 |
| 成本算法 | 1.7.0 |

## 技术变更

网关根据上游错误生成 `response.failed` 流事件时，会在 `response.error.headers` 中传递合法的 `Retry-After`，供客户端判断重试等待时间。该处理覆盖 HTTP/SSE 与 WebSocket 转 HTTP 的 Responses 请求。

Codex 在对话中通过 `configuration_update` 调整推理强度时，网关按当前轮次的设置执行分组上限和映射，并分别记录请求值与转发值。转发到 Chat Completions 或 Anthropic 时，网关将当前设置转换为对应协议的推理参数。

HTTP 请求和 Responses WebSocket 帧现在会拒绝含重复字段的 JSON，包括嵌套对象。接入方自行拼装请求时，应确保每个对象内的字段名唯一。文献检索返回 403 时，网关会按该次请求处理错误，避免误停可处理其他请求的 OpenAI OAuth 账号。 [上游 v0.2.24 更新](https://github.com/Wei-Shaw/sub2api/releases/tag/v0.2.24) · [上游 v0.2.25 更新](https://github.com/Wei-Shaw/sub2api/releases/tag/v0.2.25)

## 升级行为

Windows 10 / 11 x64 用户可在「版本与更新」中升级，也可退出控制台后安装 v0.4.11 的 `_x64-setup.exe`。桌面模式首次启动新版本时会停用已安装插件并保留配置。管理员可在检查兼容性后重新启用插件。Windows 原生一键启动继续沿用「API 接入中心」中选定的桌面应用或 CLI。 [v0.4.11 下载页](https://github.com/rw0104/sub2api-cost-console/releases/tag/v0.4.11)

## 验证结果

已核对 Tauri、Cargo 和更新说明中的桌面版本，以及上游 v0.2.25 标签指向的完整提交。内核来源、发布产物校验和后端 CI 门禁的 15 项脚本测试通过。

在 Apple 芯片的 macOS 上，核对官方 SHA-256 后运行 Codex CLI 0.172.0。客户端使用「API 接入中心」的 TOML 模板和临时测试密钥访问本机模拟服务，成功读取带 `client_version=0.172.0` 的模型目录并收到 Responses 流式回复。模拟服务返回 HTTP 429 和 `api_key_spend_limit_exceeded` 后，客户端显示 “Your API key has reached its spend limit.” 并结束本轮，模拟服务仅收到一次推理请求。 [Codex 0.172.0 官方发布](https://github.com/openai/codex/releases/tag/rust-v0.172.0)

## 回滚说明

若需回退整个桌面版本，退出控制台后安装 v0.4.10 的 Windows x64 安装包。若仅需回退内核，在「版本与更新」中选择已保留的 v0.2.23 / 扩展 1.3.29 回滚槽位。回滚后检查插件启用状态。 [v0.4.10 发布页](https://github.com/rw0104/sub2api-cost-console/releases/tag/v0.4.10)

QQ 用户交流群 **960663114**。 [插件开发指南](https://github.com/rw0104/sub2api-cost-console/blob/v0.4.11/docs/PLUGIN_DEVELOPMENT.md)
