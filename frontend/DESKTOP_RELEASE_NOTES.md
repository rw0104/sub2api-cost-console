# Sub2API Cost Console v0.3.18

## 主要更新

- 集成上游 Sub2API **v0.2.11** 为新的兼容内核基线,带来官方 GPT-6.1 Sol 实现、Claude 额度重置兑换、计费在途预留(inflight reservation)、service tier 计费、API Key 缓存与创建限额、网关在途预留、Web 搜索、Grok 音频/多媒体、OpenAI alpha 搜索/嵌入与套餐类型徽章等上游能力。
- **GPT-6.1 Sol 收敛到上游官方实现**:撤销 v0.3.17 的本地热补丁,改用上游更完整的版本(内置官方 Codex 元数据 `codex_gpt61_sol.json`、reasoning effort 校验 `ValidateGPT61SolReasoningEffort`、ultra 推理档与更准确的模型清单)。模型库、`/v1/models`、Codex 模型清单、管理端模型白名单仍完整支持 `gpt-6.1-sol`,客户端可直接发现选用。
- 完整保留宿主本地扩展:账户成本账本、插件系统(`plugin_extensions.v2`)、CORS 与 `X-Plugin-Revision` 收口、Fast/priority 定价倍率等宿主特有逻辑不变。
- 保留 Windows 原生一键启动、原应用身份、托盘恢复与 Header Probe 观测显示修复;桌面与稳定内核按同一源码发布。

## 当前兼容内核

- 桌面:`v0.3.18`
- 上游 Sub2API:`v0.2.11`
- 上游提交:`96f4c115c9749078f90cbf210a01d39baf3f53b6`
- 本地扩展:`v1.3.10`
- 成本算法:`v1.6.1`
- 必需能力:`account_cost_loss_ledger.v1`、`account_economics_sampling.v1`、`plugin_extensions.v2`、`openai.oauth.protection_transport.v1`、`openai.oauth.request_header_probe.v1`、`plugin_publisher_trust.v1`

## 技术变更

- 兼容内核从上游 v0.2.10 升级到 v0.2.11:以官方 v0.2.11 源码树为合成基线(synthetic upstream v0.2.11),在其上重放宿主本地扩展。
- `gpt-6.1-sol` 相关代码全面取上游、撤本地:`DefaultModels`、模型拼写归一 `IsGPT61SolModelSpelling`、Codex 描述符与官方元数据、定价目录与静态兜底价、前端白名单均改用上游实现,消除与本地补丁的符号重定义与重复条目;本地新增的紧凑拼写 `gpt-6.1sol` 兼容随之下线(上游按 `gpt-6.1-sol` 及其后缀识别)。
- 上游 v0.2.11 引入模型自有的 Ultrafast 倍率与 service tier 计费改动,与宿主 Fast/priority 倍率合并保留;计费兜底价、缓存写入策略与长上下文倍率逻辑按"保留本地 + 吸收上游"逐项合并。
- 定价目录 `gpt-6.1-sol` 采用上游官方口径(含 batches / flex / priority 各档与约 105 万 token 上下文);目录缺失时回退到上游专属静态价,杜绝隐蔽错误计费。
- 成本扩展提升至 1.3.10;成本算法与六项必需能力保持不变,使用新名称发布内核包,避免覆盖稳定通道已引用的包。

## 升级行为

- 可在"版本与更新"中升级,或退出程序后使用本安装器原位升级;应用身份、数据库连接和已有业务配置保留。
- 桌面、兼容内核或扩展版本变化后的首次启动会停用旧插件一次,保留配置、已签名原包、发布者信任及路由作用域;确认兼容性后重新启用。同版本普通重启不会重复停用。
- 独立内核更新会校验版本、上游提交、扩展、能力清单和归档 SHA-256;缺少成本能力或来源不匹配时拒绝安装,不自动降级。
- 内核升级带来上游新特性(Claude 额度重置兑换、计费/网关在途预留、service tier 计费、API Key 缓存与限额等);计费与调度路径的行为变化以上游 v0.2.11 官方实现为准。

## 验证结果

- 正式发布以本版本同一源码提交通过完整后端 CI、前端测试、Rust 测试、生产构建和发布契约为前提;发布后下载验收结果见 [RELEASE_VERIFICATION.json](https://github.com/rw0104/sub2api-cost-console/releases/download/v0.3.18/RELEASE_VERIFICATION.json)。
- 后端门禁覆盖 `go test -tags=unit ./...`、`go test -tags=integration ./...` 和 `golangci-lint run --timeout=30m ./...`,含 GPT-6.1 Sol 识别、归一化与定价回归及上游 v0.2.11 新增测试。
- 发布契约门禁核对内核源码基线(合成 upstream v0.2.11 树)、扩展版本、算法版本、六项必需能力、二进制 `--version`、内核包 SHA-256 和桌面更新签名。
- 发布后需实际下载验证安装器更新签名、内核包与 SDK 的 SHA-256、manifest/二进制版本/源码 SHA,以及公开更新和 QQ 二维码地址。

## 回滚说明

- 本版不新增数据库迁移。需要回退时可使用 v0.3.17 桌面及其兼容内核(上游 v0.2.10),并核对实际启用的内核版本;独立内核更新不会因桌面回退自动撤销。
- 回退到 v0.3.17 会恢复本地 gpt-6.1-sol 热补丁(含紧凑拼写 `gpt-6.1sol`),模型仍可用;历史已记录用量保持原样。v0.3.12 及更早桌面还可能存在已修复的托盘事件丢失问题。
- Tauri updater 的 `.sig` 用于自动更新验签,不代表 Windows Authenticode 发行者签名。

## 用户交流

欢迎加入 QQ 用户交流群 **960663114**,交流安装使用、插件配置和运维经验。[查看 README 交流群入口](https://github.com/rw0104/sub2api-cost-console#community)。

<img src="https://raw.githubusercontent.com/rw0104/sub2api-cost-console/v0.3.18/assets/qq-community-qrcode.jpg" alt="Sub2API Cost Console QQ 用户交流群二维码,群号 960663114" width="320" />
