# Sub2API Cost Console v0.3.15

## 主要更新

- 接入上游 Sub2API v0.2.9，更新图片计费、长上下文账号成本、自动用卡调度、模型目录与协议转换。
- 修复桌面插件配置保存的 CORS 预检：允许 `If-Match`、`X-Plugin-Revision`，并让前端读取插件版本与操作 ID 响应头。
- 渠道图片价格留空时继承模型目录价；图片输出价格显式填 `0` 时仍免费。账号统计中的长上下文成本按账号开关计算，分组开关继续决定客户售价。
- 保留 Windows 原生一键启动、原应用身份，以及 v0.3.13 的托盘恢复和 Header Probe 观测显示修复；桌面和稳定内核按同一源码发布。

## 当前兼容内核

- 桌面：`v0.3.15`
- 上游 Sub2API：`v0.2.9`
- 上游提交：`4c00df2e0183e2c70b7fa8ba45914205e36aad0c`
- 本地扩展：`v1.3.7`
- 成本算法：`v1.6.1`
- 必需能力：`account_cost_loss_ledger.v1`、`account_economics_sampling.v1`、`plugin_extensions.v2`、`openai.oauth.protection_transport.v1`、`openai.oauth.request_header_probe.v1`、`plugin_publisher_trust.v1`

## 技术变更

- 统一普通渠道价和 token 区间价的图片价格继承规则。保留宿主已有的推理等级倍率、旧版 max 倍率兼容、Opus priority 缓存创建计费与模型目录价格修正。
- 账号统计价格继续遵循自定义规则、客户计费、模型目录和默认公式的优先级。模型目录成本使用账号长上下文开关，继续携带同一次请求的计费时间、服务等级和推理等级。
- 自动用卡增加查询失败退避、调度通知冷却和新鲜无卡状态复用；调度缓存纳入自动用卡配置及状态。额度快照存在明确未来重置时间时继续执行暂停规则。
- 混合账号模型目录保留普通账号映射别名，由透传账号补充默认模型；分组白名单支持任意位置的 `*`，模型广场展示视频独立倍率。
- 接入客户端取消状态、工具输入与推理内容转换、Responses/Chat Completions 回退等上游修复。保留插件策略拒绝、插件不可用和已发送请求失败的错误语义，避免错误重试或重复计费。
- 内核扩展提升至 1.3.7；成本算法和六项必需能力保持不变，使用新名称发布内核包，避免覆盖稳定通道已引用的包。
- 宿主 CORS 允许插件配置保存携带版本头，并暴露 `ETag`、`X-Plugin-Revision`、`X-Plugin-Operation-ID` 和 `Server-Timing`；来源白名单与 `If-Match` 版本保护保持不变。

## 升级行为

- 可在“版本与更新”中升级，或退出程序后使用本安装器原位升级；应用身份、数据库连接和已有业务配置保留。
- 桌面、兼容内核或扩展版本变化后的首次启动会停用旧插件一次，保留配置、已签名原包、发布者信任及路由作用域；确认兼容性后重新启用。同版本普通重启不会重复停用。
- 请核对渠道图片价卡：留空现在继承目录价，需要图片输出免费时显式填写 `0`。长上下文账号成本修正仅作用于升级后的用量记录。
- 不新增数据库迁移，不重写历史计费或账号成本账本。连接外部服务时，需单独升级实际处理请求的兼容内核。

## 验证结果

- 正式发布以本版本同一源码提交通过以下质量门禁为前提；发布后下载验收结果见 [RELEASE_VERIFICATION.json](https://github.com/rw0104/sub2api-cost-console/releases/download/v0.3.15/RELEASE_VERIFICATION.json)。
- 后端门禁：完整 `go test -tags=unit ./...`、`go test -tags=integration ./...` 和 `golangci-lint run --timeout=30m ./...`，同时覆盖计费、长上下文、自动用卡、模型目录与插件传输回归。
- 桌面与前端门禁：Rust 测试、前端测试、TypeScript、ESLint、i18n、生产构建和桌面安装器打包。交互桌面及 opt-in 测试的执行或跳过状态单独记录。
- 发布契约门禁：公开 SDK/示例开发包、内核源码基线、扩展版本、算法版本与六项必需能力；桌面和稳定内核必须绑定同一源码 SHA 的完整 CI。
- 发布后需实际下载验证安装器更新签名、内核包与 SDK 的 SHA-256、manifest/二进制版本/源码 SHA，以及公开更新和 QQ 二维码地址。

## 回滚说明

- 本版不新增数据库迁移。需要回退时可使用 v0.3.14 桌面及其兼容内核，并核对实际启用的内核版本；独立内核更新不会因桌面回退自动撤销。
- 回退内核将恢复旧的图片价格继承、长上下文账号成本及调度行为，历史已记录用量保持原样。v0.3.12 及更早桌面还可能存在已修复的托盘事件丢失问题。
- Tauri updater 的 `.sig` 用于自动更新验签，不代表 Windows Authenticode 发行者签名。

## 用户交流

欢迎加入 QQ 用户交流群 **960663114**，交流安装使用、插件配置和运维经验。[查看 README 交流群入口](https://github.com/rw0104/sub2api-cost-console#community)。

<img src="https://raw.githubusercontent.com/rw0104/sub2api-cost-console/v0.3.15/assets/qq-community-qrcode.jpg" alt="Sub2API Cost Console QQ 用户交流群二维码，群号 960663114" width="320" />
