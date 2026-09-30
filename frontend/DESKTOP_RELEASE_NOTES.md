# Sub2API Cost Console v0.3.17

## 主要更新

- 新增 OpenAI **GPT-6.1 Sol**（`gpt-6.1-sol`）模型支持：进入模型库与模型广场、`/v1/models` 与 Codex 模型清单、管理端模型白名单选择器，客户端可直接发现并选用。
- 补齐 `gpt-6.1-sol` 的计费与能力：$2/百万输入、$0.10/百万缓存输入、$10/百万输出，约 105 万 token 上下文，文本+图像输入；Codex 清单给出 reasoning 档位、Fast 服务档与上下文窗口，避免被兜底成旧模型计费。
- 上游兼容内核保持 Sub2API v0.2.10 不变，本轮为宿主本地扩展新增，不改上游基线。
- 保留 Windows 原生一键启动、原应用身份、托盘恢复和 Header Probe 观测显示修复；桌面与稳定内核按同一源码发布。

## 当前兼容内核

- 桌面：`v0.3.17`
- 上游 Sub2API：`v0.2.10`
- 上游提交：`2f3fed2fdb0787141294cec81487a5df30426f7f`
- 本地扩展：`v1.3.9`
- 成本算法：`v1.6.1`
- 必需能力：`account_cost_loss_ledger.v1`、`account_economics_sampling.v1`、`plugin_extensions.v2`、`openai.oauth.protection_transport.v1`、`openai.oauth.request_header_probe.v1`、`plugin_publisher_trust.v1`

## 技术变更

- 后端模型库 `DefaultModels` 新增 `gpt-6.1-sol`，并新增家族识别（拼写归一含紧凑写法 `gpt-6.1sol`），使其被认定为 GPT-6 推理家族，继承 reasoning 档位、Fast 服务档与图像输入能力。
- 定价目录与静态兜底同时新增 `gpt-6.1-sol`：动态目录命中专属价，目录缺失时回退到专属静态价而非 `gpt-5.4`，杜绝隐蔽错误计费；缓存输入价为 GPT-6 Sol 的一半（$0.10/百万）。
- Codex 模型清单为 `gpt-6.1-sol` 下发约 105 万 token 的上下文模板，实时账号元数据仍为准。
- 前端模型白名单选择器纳入 `gpt-6.1-sol`。
- 成本扩展提升至 1.3.9；成本算法和六项必需能力保持不变，使用新名称发布内核包，避免覆盖稳定通道已引用的包。
- 本轮未新增数据库迁移；改动通过后端单元测试、前端测试与桌面契约验证。

## 升级行为

- 可在“版本与更新”中升级，或退出程序后使用本安装器原位升级；应用身份、数据库连接和已有业务配置保留。
- 桌面、兼容内核或扩展版本变化后的首次启动会停用旧插件一次，保留配置、已签名原包、发布者信任及路由作用域；确认兼容性后重新启用。同版本普通重启不会重复停用。
- 独立内核更新会校验版本、上游提交、扩展、能力清单和归档 SHA-256；缺少成本能力或来源不匹配时拒绝安装，不自动降级。
- `gpt-6.1-sol` 的部分子项价格（缓存写入、批处理、Flex/Priority 档）暂沿用 GPT-6 Sol 口径，待 OpenAI 官方模型页最终确认后校准。

## 验证结果

- 正式发布以本版本同一源码提交通过完整后端 CI、前端测试、Rust 测试、生产构建和发布契约为前提；发布后下载验收结果见 [RELEASE_VERIFICATION.json](https://github.com/rw0104/sub2api-cost-console/releases/download/v0.3.17/RELEASE_VERIFICATION.json)。
- 后端门禁覆盖 `go test -tags=unit ./...`、`go test -tags=integration ./...` 和 `golangci-lint run --timeout=30m ./...`，包括新增的 GPT-6.1 Sol 模型识别、归一化与定价回归。
- 发布契约门禁核对内核源码基线、扩展版本、算法版本、六项必需能力、二进制 `--version`、内核包 SHA-256 和桌面更新签名。
- 发布后需实际下载验证安装器更新签名、内核包与 SDK 的 SHA-256、manifest/二进制版本/源码 SHA，以及公开更新和 QQ 二维码地址。

## 回滚说明

- 本版不新增数据库迁移。需要回退时可使用 v0.3.16 桌面及其兼容内核，并核对实际启用的内核版本；独立内核更新不会因桌面回退自动撤销。
- 回退后 `gpt-6.1-sol` 将不再出现在模型库，历史已记录用量保持原样。v0.3.12 及更早桌面还可能存在已修复的托盘事件丢失问题。
- Tauri updater 的 `.sig` 用于自动更新验签，不代表 Windows Authenticode 发行者签名。

## 用户交流

欢迎加入 QQ 用户交流群 **960663114**，交流安装使用、插件配置和运维经验。[查看 README 交流群入口](https://github.com/rw0104/sub2api-cost-console#community)。

<img src="https://raw.githubusercontent.com/rw0104/sub2api-cost-console/v0.3.17/assets/qq-community-qrcode.jpg" alt="Sub2API Cost Console QQ 用户交流群二维码，群号 960663114" width="320" />