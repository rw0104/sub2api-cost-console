# Sub2API Cost Console v0.3.11

## 主要更新

- 修复插件健康探测被宿主提前取消的问题。此前插件可能约每分钟被重新启动，导致请求头观测计数和内存摘要反复清空。
- 修复 Windows 桌面内核及插件启动时弹出控制台窗口的问题，保留 Windows 原生一键启动。
- 接入只读 OpenAI OAuth Header Probe：补齐请求上下文、请求关联标识和模型传递，并在账号管理页展示观测摘要。
- 隔离账号 OAuth 认证失败，统一成本中心和相关报表的时间窗选择与使用量统计。

## 当前兼容内核

- 桌面版本：`v0.3.11`
- 上游 Sub2API：`v0.2.8`
- 上游提交：`fd80b08c90b55edcad5b00171b53f08721d30da1`
- 本地扩展：`v1.3.3`
- 成本算法：`v1.6.1`
- 必需能力：`account_cost_loss_ledger.v1`、`account_economics_sampling.v1`、`plugin_extensions.v2`、`openai.oauth.protection_transport.v1`、`openai.oauth.request_header_probe.v1`、`plugin_publisher_trust.v1`
- 本次安装包从发布提交重新构建宿主内核；内核版本信息中的 upstream commit 表示上游基线，宿主源码以本次 GitHub tag 和 workflow commit 为准。

## 技术变更

- 异步 readiness 探测使用管理器生命周期，由探测自身持有 5 秒超时。移除调用方返回时的立即取消，同时保留管理器停机取消、30 秒采样、单实例在途限制和连续 3 次失败摘除策略。
- Windows sidecar 使用 GUI subsystem；宿主启动插件时设置 HideWindow 与 CREATE_NO_WINDOW。
- 请求头探针仅接收宿主派生的存在性、长度、解析结果和块数，不获取完整状态头或凭据，不改变或阻断上游请求。
- 保持应用 ID `com.sub2api.cost-console`、主程序名、18765 端口及原有 backend/core 数据目录。沿用同版本内置内核 SHA-256 不同即安全替换的升级机制。

## 升级行为

- 已安装正式桌面的用户可使用此安装器或桌面更新升级至 v0.3.11。手动安装前请退出桌面窗口和托盘实例，沿用原安装目录并保留应用数据。
- 宿主版本变化后，插件可能按既有升级规则停用一次；原插件包和配置保留，检查后重新启用即可。已有 Header Probe 1.0.1 无需重新打包或上传。
- 本版本附带修复后的桌面内核。连接独立外部后端的用户，需要另外更新实际运行的宿主服务。
- 本次发布桌面安装包和 updater 元数据；独立 `core-stable` 通道不在此次发布范围。

## 验证结果

- readiness 定向回归覆盖 v1/v2 两种协议：原实现稳定复现提前取消，最终修复的 6 个子测试通过；管理器停止仍可取消在途探测。
- 桌面 Rust 全量检查 66 项通过、0 失败，1 项需要真实 Docker 环境的恢复测试跳过；能力契约测试按当前必需清单逐项验证缺失能力，包括新增 Header Probe。
- 相关 service、pluginruntime、pluginapi/v2、repository 四个包的 184 个测试/子测试通过，0 失败；4 项需要额外容器或包环境的可选测试跳过。
- 对 Header Probe 1.0.1 二进制的独立临时副本进行真实进程验证：合成头记录非零长度和 10 块；无头请求仍增加计数；跨 4 轮健康采样保留同一实例和 6 次累计观测。
- 发布工作流执行 Rust 生命周期测试、全部前端测试、lint 和 SDK 开发包验证，再生成安装器、Tauri updater 签名及 SHA-256 清单。
- 请求本身不带状态头时，`0/— B · 0/— 块` 是合法观测结果；应结合请求计数、最近观测时间和 runtime 是否稳定判断插件状态。
- v2 当前支持预处理、OAuth 保护传输和 Header Probe。仅启用预处理/探针时，原生 WebSocket 入口仍可能绕过 HTTP hook；通用 Provider Adapter、事件消费、后台 Worker 及标准 Go SDK 的账号元数据调用面尚未全部接入，不能将本版本描述为所有 v2 场景均完成验收。

## 回滚说明

- 建议升级前保留数据库及 `%APPDATA%\com.sub2api.cost-console` 配置备份。安装器沿用现有数据目录，本次修复不新增数据库迁移。
- 若需回退，应退出桌面后使用已知版本安装器，并保留数据；旧宿主仍包含本次修复前的健康检查问题，可能再次清空插件的内存观测记录。
- 发布的 `.sig` 用于 Tauri updater 验签，不代表 Windows Authenticode 发行者代码签名。
