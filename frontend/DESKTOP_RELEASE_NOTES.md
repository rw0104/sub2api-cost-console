# Sub2API Cost Console v0.3.13

## 主要更新

- 修复 Windows 最小化到托盘后，单击、双击或右键无法唤回主界面的严重体验问题。
- 修复 Header Probe 被套用目标值格式的问题：成功观测显示 `780 B · 33 块`，明确缺少请求头时显示“尚未观测到请求头”。
- 保留 Windows 原生一键启动、原应用身份、现有数据库、配置与插件包；桌面和稳定内核同步发布。

## 当前兼容内核

- 桌面：`v0.3.13`
- 上游 Sub2API：`v0.2.8`
- 上游提交：`fd80b08c90b55edcad5b00171b53f08721d30da1`
- 本地扩展：`v1.3.5`
- 成本算法：`v1.6.1`
- 必需能力：`account_cost_loss_ledger.v1`、`account_economics_sampling.v1`、`plugin_extensions.v2`、`openai.oauth.protection_transport.v1`、`openai.oauth.request_header_probe.v1`、`plugin_publisher_trust.v1`

## 技术变更

- 本机 Windows 在托盘图标位于溢出区域时可返回 `S_FALSE` 和有效矩形。原 `tray-icon` 依赖只接受 `S_OK`，在通知生成前丢弃鼠标事件。本版加入带原始许可证及来源记录的限定依赖补丁，接受成功 HRESULT 与正面积矩形；失败状态和无效矩形仍被拒绝。
- 明确处理 Windows 托盘双击事件。窗口恢复顺序经新旧对照验证可工作，本版未将调整该顺序当成根因修复。
- Header Probe 依据插件能力识别只读摘要，处理 `header_present`、缺失观测字段和故障优先级；保留旧保护传输的目标值比较格式。
- 内核扩展提升至 1.3.5，使网页端也获得内嵌前端修复，并避免覆盖稳定通道已发布的 1.3.4 同名包。

## 升级行为

- 托盘无法展开时，可再次启动已安装的 Sub2API Cost Console，通过现有单实例入口唤回主窗口，然后在“版本与更新”中升级桌面，或退出程序后使用本安装器原位升级。
- 托盘修复位于桌面程序，需升级至 v0.3.13；只更新内核不会替换旧桌面壳。
- Header Probe 1.0.1 无需重装。桌面升级后如插件按既有升级规则停用一次，检查后重新启用即可，配置与包会保留。
- 不新增数据库迁移，不重写历史计费或账号成本账本。

## 验证结果

- 修复前真实 Windows 托盘回归在 `S_FALSE` 下失败，修复后连续 5 轮单击/双击及最小化/关闭恢复通过，10 次查询均实际覆盖 `S_FALSE`。
- 本地 Rust 常规测试 71 项通过；需要交互桌面/WebView2 的原生托盘测试已单独显式执行。会启动既有 Docker 数据容器的 opt-in 测试未执行。
- AccountsView 32 项回归通过，其中新增 22 项徽标渲染测试；i18n、TypeScript、ESLint 及桌面前端生产构建通过。
- 正式发布要求同一源码提交的完整后端 unit/integration/lint CI，以及桌面 Rust、全部前端、SDK 和内核发布契约检查通过。发布后另下载验证签名、SHA-256 和公开更新地址。

## 回滚说明

- v0.3.12 及更早桌面仍可能存在本次托盘事件丢失问题；回退时需考虑此限制。
- Tauri updater 的 `.sig` 用于自动更新验签，不代表 Windows Authenticode 发行者签名。

## 用户交流

欢迎加入 QQ 用户交流群 **960663114**，交流安装使用、插件配置和运维经验。[查看 README 交流群入口](https://github.com/rw0104/sub2api-cost-console#community)。

<img src="https://raw.githubusercontent.com/rw0104/sub2api-cost-console/v0.3.13/assets/qq-community-qrcode.jpg" alt="Sub2API Cost Console QQ 用户交流群二维码，群号 960663114" width="320" />
