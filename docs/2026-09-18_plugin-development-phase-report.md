# 插件扩展阶段开发日志（2026-09-17—2026-09-18）

分支：`codex/plugin-generic-interface`。阶段起点：`8feeaee70`，承接 [9 月 17 日插件扩展开发记录](2026-09-17_plugin-system-extension-development-report.md)。

本阶段新增 OpenAI OAuth 保护传输能力及配置并发保护，完成本机 Preview 公钥配置、桌面插件配置页空白修复和多插件列表调整。当前交付是独立测试版 `0.2.39-plugin.3`；没有合并到主分支，也没有发布正式安装器或更新正式升级通道。普通正式版内核即使显示 `0.2.5`，也不代表包含本支线新增能力。

## 1. 保护插件与宿主接口

原 `account-protection` 是依赖宿主服务类型的源码集合，不能直接压缩成可运行插件。现有 `request.preprocess.v1` 只允许白名单请求修改，无法承担身份头和 TLS 传输。本阶段增加独立能力 `openai.oauth.protection_transport.v1`，通过可选 `ProtectionTransport.Forward` 双向流接管已选定账号的出站 HTTP 请求。

| 模块 | 实现与边界 |
| --- | --- |
| SDK / 清单 | 新增 `TransportHandler`、`TransportClient`、受限账号元数据和流式 proto；运行时声明须与签名清单一致 |
| 权限 | 显式要求请求元数据、请求体、当前凭据转发、出站网络、保护元数据及比较基线权限；普通预处理权限不变 |
| 路由 | 复用 v2 优先级、账号/用户/分组作用域及灰度；匹配的保护传输优先于 v1 传输；命中后故障关闭 |
| 流式生命周期 | 并发计数覆盖响应流；路由超时控制等待响应头；本地拒绝不记为上游认证失败，也不触发故障熔断 |
| 请求上下文 | 元数据和比较基线绑定账号 ID；账号元数据不提供任意 `extra`、完整凭据库或数据库连接 |
| 网关接入 | Responses、透传、Chat Completions、账号测试和 WebSocket HTTP Bridge 接入；匹配保护传输的 WS 使用 HTTP Bridge |
| 配置保存 | 按安装 ID、二进制 SHA-256 和原配置密文比较更新，拒绝并发覆盖；写入失败恢复持久化快照 |
| 快照重放 | 恢复与配置测试使用已保存配置的 Apply 路径，避免重新执行配置动作或递增修订号 |
| 隔离 | 此出站能力仅支持 process；无网络容器启动时明确拒绝，不自动放开网络 |

实现位于 `backend/pkg/pluginapi/v2/transport.*`、`backend/internal/service/plugin_protection_transport.go`、`plugin_runtime*.go`、`plugin_manager.go` 和 `backend/internal/repository/plugin_config_cas.go`。协议说明见 [保护传输接口](../backend/pkg/pluginapi/docs/protection-transport.md)。

本地独立插件实现八种策略、身份投影、TLS 模板、流期间并发限制、配置策略快照/还原及比较检查。它位于已有忽略规则覆盖的 `plugins/account-protection/`，不作为本次宿主仓库源码提交。源码包、原始文件哈希和签名包仍保存在本机；本次没有把私钥或插件二进制加入 Git。

**语义检查的实际边界：** 当前普通 Responses、透传和 Chat Completions 路径会在构造出站请求前刷新比较基线，WS 在准备 HTTP Bridge 请求后采集基线。因此不能将现有验证描述成“原始入站请求经过宿主全部转换后仍完整保真”的端到端保证。账号保护设置也使用插件配置覆盖及快照，未将原账号数据库服务逐项原样迁移。

## 2. 本机测试版交付与信任配置

前期给出的普通 `sub2api.exe` 是后端程序，不能代替本项目的桌面安装器。正确交付使用既有 Tauri/NSIS 流程生成 `Sub2API Plugin Preview`，保留独立应用 ID `com.sub2api.plugin-preview`、内核端口 `19765`、PostgreSQL `25432`、Valkey `26379` 及独立数据目录。

| 测试版 | 本阶段变化 |
| --- | --- |
| `0.2.39-plugin.1` | 已有通用扩展测试安装器；早期产物缺少本地保护插件公钥，上传会被拒绝 |
| `0.2.39-plugin.2` | 预览模式默认公钥映射加入 `local-account-protection-v1`，用于本机配套插件安装 |
| `0.2.39-plugin.3` | 修复配置页桌面加载、就绪状态和多插件布局；沿用独立测试数据与安装标识 |

公钥预置代码位于 `backend/internal/config/plugin_preview.go` 与 `config.go`，通过 `PluginPreviewEnabled()` 门控；该判断包含编译标志及现有 `SUB2API_PLUGIN_PREVIEW=1` 环境开关。没有将配套插件公钥设为普通生产配置的全局默认，也没有关闭签名验证。

`verify-plugin-preview.mjs` 新增 `--working-tree` 模式，记录 Git 基线及源码文件摘要，允许如实验证未提交工作区的本地构建，避免把旧提交号当成新安装器源码证明。验收检查 NSIS 应用身份、sidecar 版本和编译进内核的数据库隔离。

## 3. 配置页空白修复

用户在 `.2` 点击“配置”得到空白窗口。对原签名包及真实宿主资源 handler 的复现确认三个宿主问题：

1. UI 会话返回 `/api/v1/plugin-ui/...` 相对地址；前端直接用作 iframe src，桌面端因此请求 Tauri 资源域名，而不是后端。
2. 改正地址后，旧 `frame-ancestors 'self'` 仍拒绝独立桌面域名；浏览器明确报出 CSP framing 拒绝。
3. 宿主把 iframe 的 load 事件当成就绪，而错误页也会触发 load，导致加载提示消失后只剩白框。

修复后，地址经过统一 API URL 解析器；插件资源响应只允许宿主同源及固定 Tauri 来源嵌入；普通管理/API 页面的防嵌入策略保持原有行为。iframe 仍仅有 `allow-scripts` 权限。

宿主收到来源窗口、`null` origin、Bridge Token 均匹配的 ready 消息后才显示配置。15 秒未就绪则提示失败并提供重试，关闭或切换会话后丢弃迟到响应。原插件包未修改。详细记录见 [配置页与多插件布局修复](2026-09-18_plugin-configuration-desktop-fix.md)。

## 4. 多插件管理界面

原页面把权限、版本范围、运行详情和灰度设置全部展开在大卡片内，插件数量增加时不便扫描。现在每个插件默认只展示名称、版本、状态、说明及配置/启停/详情操作；详细能力、权限、灰度和维护按钮按插件独立展开。

增加名称、ID、发布者搜索与状态筛选。配置弹窗收窄并限制在可视高度内。用 12 个合成插件检查桌面布局、搜索、详情展开和移动端横向溢出，同时使用原签名包的资源验证配置加载与保存消息。

## 5. 验证证据与调用路径

以下为本阶段已经取得的证据，不将合成环境或单一平台结果表述为生产全量验证。

| Evidence | Finding | Path |
| --- | --- | --- |
| 插件本地 `go test ./...` 通过 | 八种策略、身份投影、语义比较、实际 gRPC + 本地 TLS、SSE 分块及流期间并发限制有效 | 本机 `plugins/account-protection/plugin_test.go`、`tlsfingerprint/*_test.go` |
| `TestAccountProtectionSignedPackageProcess` 通过 | 真实签名包安装和子进程、配置动作/还原、Host API 配置回读、失败回滚、拒绝及 TLS 证书失败路径有效 | `backend/internal/service/plugin_protection_transport_test.go`；运行需提供本地插件包 |
| PostgreSQL `TestPluginRepositoryProtectionConfigCAS` 通过 | 同一旧配置上的两个并发写入者仅一个成功，错误二进制/过期配置不能覆盖 | `backend/internal/repository/plugin_config_cas_integration_test.go` |
| 插件页 15 项、上传编码 2 项、语言键 3 项通过；TypeScript 检查通过 | 桌面 URL、ready 校验、超时/重试、迟到会话、详情与筛选没有破坏既有管理操作 | `frontend/src/views/admin/__tests__/PluginsView.spec.ts` 等 |
| 宿主插件 handler / 安全响应头测试通过 | 相关服务端回归通过 | `backend/internal/handler/admin`、`internal/server/middleware` |
| 新旧 CSP 浏览器对比 | 同一原插件在旧策略下被 Tauri 来源阻止，新策略下同源/Tauri 可用，未知来源仍被拒绝 | `frontend/scripts/plugin-ui-embedding.py`、`backend/internal/handler/admin/plugin_ui_browser_test.go` |
| 编译后前端浏览器检查 `passed=true`、`page_errors=[]` | 12 个合成插件、原插件配置 UI、保存消息、移动端、失败重试可用 | `frontend/scripts/plugin-management-ui.py`；管理 API 为合成数据，插件资源走生产 handler |
| `.3` 构建及安装器验收通过 | 本机独立测试安装器及 sidecar 完成打包，未自动安装 | `frontend/scripts/build-plugin-preview.mjs`、`verify-plugin-preview.mjs` |

配置加载调用路径：插件管理页 → 创建短时 UI 会话 → 按 API 后端解析资源地址 → CSP 校验 iframe 嵌入来源 → 插件 ready → 宿主校验窗口/来源/Token → Bridge 配置读写。

保护传输调用路径：宿主认证与账号选择 → 准备请求和账号绑定基线 → v2 路由与权限校验 → 插件出站传输 → 宿主处理响应、用量和计费。保存失败路径为：候选配置验证/应用 → 数据库比较更新失败 → 读取持久化配置 → 直接 Apply 恢复。

## 6. 构建产物记录

| 产物 | 本机位置 | SHA-256 |
| --- | --- | --- |
| `.3` 桌面测试安装器，31,270,997 字节 | `frontend/release-assets/plugin-preview-0.2.39-plugin.3/Sub2API Plugin Preview_0.2.39-plugin.3_x64-setup.exe` | `0631b9ee36351cbe53470a47773c60165b20b7b01d38a01f030e63d2b294e6dd` |
| `.3` sidecar | `frontend/src-tauri/binaries/sub2api-backend-x86_64-pc-windows-msvc.exe` | `0ed72b208831378c24cbf3d4ea7fec723995fc4dca1274a20c0fc75ed8b7b074` |
| 账号保护插件 1.0.0，19,419,255 字节 | `plugins/account-protection/dist/account-protection-1.0.0.s2plugin` | `10f85bc420f61f88c8a211f0caf06d7390ad009fd7f8b20332c458bc564a29d7` |

`.3` 现有验收回执的 Git 基线为 `8feeaee70ff3936cd7cbf46a204a85ae0a3a4693`，模式为 `working-tree`，源码文件摘要为 `f05479d9636be07394951e7cac1daf8f974d363fce526d3c1e05007e734b0349`。日志和对接文档整理不改变已经交付的安装器字节；不能据此声称已重新发布正式版。

生成物、浏览器截图和安装器均按既有规则保存在忽略目录，不加入源码提交。当前测试安装器无 Windows Authenticode 签名；插件包的 Ed25519 签名是独立机制。

## 7. 复现入口

在仓库根目录进入 `frontend` 执行：

```powershell
corepack pnpm@9 exec vitest run src/views/admin/__tests__/PluginsView.spec.ts src/api/admin/__tests__/plugins.spec.ts src/i18n/__tests__/localeKeyCompleteness.spec.ts
corepack pnpm@9 exec vue-tsc --noEmit
node scripts/build-plugin-preview.mjs
node scripts/verify-plugin-preview.mjs --working-tree
```

在 `backend` 执行：

```powershell
go test ./pkg/pluginapi/... ./internal/config ./internal/service ./internal/handler/admin ./internal/server/middleware -run 'TestPlugin|TestAccountProtection|TestPreviewTrusted' -count=1 -timeout=5m
go test -tags integration ./internal/repository -run '^TestPluginRepositoryProtectionConfigCAS$' -count=1 -timeout=5m
```

真实保护包测试须设置 `SUB2API_ACCOUNT_PROTECTION_PACKAGE` 和 `SUB2API_ACCOUNT_PROTECTION_PUBLIC_KEY`；未提供时对应测试会跳过，不能把整个包测试通过当成实际安装包验证。浏览器复现命令见上面的配置页专项记录；`--keep-alive` 可在同一临时 fixture 上依次执行嵌入与完整界面检查。

## 8. 剩余工作及验收限制

- 第三方开发者自助发布者信任/授权界面尚未实现。Preview 内置一把配套公钥只解决本机测试，不是通用插件安装生态的完成证明。
- 保护插件当前范围是 OpenAI OAuth；其它 provider 的通用账号管理、TLS 模板数据库 CRUD 和原生 WSS 未按原源码完整迁移。插件配置覆盖与 HTTP Bridge 是当前实现方式。
- 本阶段未验证 Linux 运行时实际启动，只有交叉编译；未使用生产凭据验证上游效果或模型质量。
- `go test -race` 曾因本机 CGO/C 编译器条件未满足而未执行成功；已验证流式并发及 PostgreSQL 竞争行为。
- 本阶段完整后端测试曾出现 3 个既有 `backup_pg_dumper` Windows 环境失败，原因是 PATH 中缺少 `sh`；不能声称全库测试全部通过。
- 浏览器测试不等于在用户正在运行的桌面实例上自动安装验收。测试版用于本机验证；对外正式发布、主分支合并和升级通道更新均待后续明确发布工作。
