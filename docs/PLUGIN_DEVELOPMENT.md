# 插件对接指南

面向自行开发、签名、安装和维护 Sub2API 插件的开发者。以仓库内可运行示例为起点，不需要修改宿主前端即可提供插件配置页面。

> 本指南对应插件扩展支线。桌面独立测试版 **0.2.39-plugin.1**，内核基线 **0.2.5**，扩展构建 **1.2.0-plugin.1**。旧正式版即使内核版本相同，也不代表已包含未合并的 v2 实现。当前实际注册的 v2 能力只有 `request.preprocess.v1`。
>
> 普通进程不是 OS 沙箱；签名证明来源，不保证代码安全。process 模式只安装可信插件，需要文件、网络和资源限制时启用 v2 container 模式。不要在正式数据库上试验新插件。

## 1. 选择接口

| 需求 | 接口 | 当前边界 |
| --- | --- | --- |
| 请求准入、限制生成参数、调整 instructions | v2 `request.preprocess.v1` | OpenAI OAuth / API Key；Responses、Responses Compact、Chat Completions |
| 自行实现 HTTP/TLS 上游传输 | v1 `openai.oauth.outbound_transport.v1` | OpenAI OAuth；插件负责网络传输 |
| 新 Provider、响应处理、业务事件订阅、后台任务 | 后续能力 | 尚未注册，单改清单不会增加功能 |

新开发者优先从 v2 开始。Hook 在**账号选择和请求准备完成后、发出 HTTP 前**执行。认证、账号选择、模型、计费和重试决策仍由核心负责。公开 SDK 在 [`backend/pkg/pluginapi`](../backend/pkg/pluginapi/README.md)，插件不依赖宿主内部 Repository 或数据库连接。

## 2. 构建签名示例包

克隆仓库并切到包含本指南的支线，需要 Git 和 `backend/go.mod` 指定的 Go 工具链（本版 Go 1.27.0）。生成代码已提交，仅改 proto 时需要 protoc。示例 UI 是静态 HTML/JS，无需 Node 构建。

以下 PowerShell 命令从仓库根目录执行，得到 **0.1.0 / Windows x64 / API Key** 示例：

```powershell
cd backend
New-Item -ItemType Directory -Force dist/plugin-demo | Out-Null
$keyPrefix = Join-Path $env:LOCALAPPDATA 'Sub2API-PluginKeys/publisher-demo'
go run ./pkg/pluginapi/tools/keygen -out $keyPrefix
go build -ldflags "-X main.pluginVersion=0.1.0 -X main.accountType=apikey" -o dist/plugin-demo/preprocess.exe ./pkg/pluginapi/examples/preprocess
go run ./pkg/pluginapi/examples/preprocess/pack -binary dist/plugin-demo/preprocess.exe -version 0.1.0 -account-type apikey -target windows-amd64 -signing-key "$keyPrefix.private" -key-id publisher-demo -out dist/plugin-demo/request-policy-0.1.0-windows-amd64.s2plugin
Get-FileHash dist/plugin-demo/request-policy-0.1.0-windows-amd64.s2plugin -Algorithm SHA256
```

密钥生成器和打包器拒绝覆盖已有文件。已有密钥时跳过 keygen；重新打包使用新文件名。私钥不打印、不进入包，需限制目录访问并备份，不能提交源码或发送给部署者。

默认示例作用域是 OAuth；上述命令同时将运行时和清单切换为 API Key。开发自己的插件时修改 [`main.go`](../backend/pkg/pluginapi/examples/preprocess/main.go) 的 `GetInfo` 和 [`manifest.source.json`](../backend/pkg/pluginapi/examples/preprocess/manifest.source.json)，保持 ID、版本、能力、权限、超时和失败策略完全一致。默认 ID 为 `example.request-policy`。

`.s2plugin` 是插件包，须在管理页上传；`x64-setup.exe` 是宿主安装器。不要混淆二者。

## 3. 配置公钥并安装

向部署者交付 `.public` 公钥、签名包、版本说明和 SHA-256，不交付私钥。以下命令输出配置片段，合并到宿主 `config.yaml` 的既有 `plugins` 节点：

```powershell
$publicKey = (Get-Content "$keyPrefix.public" -Raw).Trim()
Write-Output "plugins:"
Write-Output "  allow_unsigned: false"
Write-Output "  trusted_publishers:"
Write-Output "    publisher-demo: '$publicKey'"
```

不要覆盖整个配置文件或创建重复 YAML 键。桌面测试版配置在 `%APPDATA%\com.sub2api.plugin-preview\backend\config.yaml`，不要编辑正式版 `com.sub2api.cost-console` 目录。修改宿主配置后，从测试版托盘退出并重启。

1. 首次打开独立测试版，启动 Docker Desktop 后选择快速安装，创建独立管理员。测试版使用 PostgreSQL `25432`、Valkey `26379`、数据库 `sub2api_plugin_preview`，不能复用正式库。
2. 系统设置中显示“插件管理”菜单。此开关只影响菜单，不启停运行时。
3. 点击“安装插件”上传签名包。启用 step-up 时先完成 TOTP，页面会先做轻量授权检查再发文件。
4. 确认签名、兼容性、权限和作用域；安装后默认停用。
5. 打开配置，将 `max_output_tokens` 设为 `12`，保存并测试。
6. 在“路由策略”填写专用测试账号、用户、分组 ID，再启用。先小范围验证，不要直接覆盖全部流量。
7. 用匹配分组的 API Key 请求测试模型，确认上游参数改变。将 `deny_model` 设为该模型，验证 403、无上游调用、无扣费。

未签名包仅限独立开发环境临时设置 `allow_unsigned: true`，不能把它复制到生产配置。

## 4. 实现 v2 运行时

复用完整的 [请求策略示例](../backend/pkg/pluginapi/examples/preprocess/README.md)，入口调用 `pluginv2.Serve(handler)`。业务使用 [`contract.go`](../backend/pkg/pluginapi/v2/contract.go)，不要自行拼 wire 消息，也不要向标准输出写日志，标准输出用于进程握手。

| `ExtensionHandler` 方法 | 要求 |
| --- | --- |
| `GetInfo` | 与签名清单一致的 ID、版本、协议 2 和能力数组 |
| `Health` | 快速返回，不做长时间网络工作 |
| `ValidateConfig` | 严格解析，拒绝未知字段和额外 JSON 值，返回规范化对象 |
| `ApplyConfig` | 原子切换，失败保留旧配置 |
| `TestConfig` | 有界诊断，不泄漏秘密或请求正文 |
| `Preprocess` | 遵守 context 截止时间和取消，返回明确决策及受限补丁 |

SDK 尚未独立发布为可 `go get` 的版本，且此 fork 保留原上游 module 名。不要把本支线提交号传给 `go get github.com/Wei-Shaw/sub2api`。独立项目请固定一份本仓库源码，使用本地 replace。下面从宿主仓库根目录执行，在相邻目录创建一个新的插件工程（该目录应尚不存在）：

```powershell
$sdkBackend = (Resolve-Path backend).Path
New-Item -ItemType Directory ../my-sub2api-plugin | Out-Null
Copy-Item "$sdkBackend/pkg/pluginapi/examples/preprocess/main.go" ../my-sub2api-plugin/main.go
Set-Location ../my-sub2api-plugin
go mod init example.com/my-sub2api-plugin
go mod edit '-go=1.27.0'
go mod edit -replace "github.com/Wei-Shaw/sub2api=$sdkBackend"
go mod tidy
go build -o preprocess.exe .
```

CI 中也检出同一宿主提交，并更新 replace 到该目录；不要依赖开发机绝对路径。配置 UI、清单和打包器可继续复用宿主示例，通过打包器的 `-source` 指向自己的清单/UI 目录。其他语言除实现 [proto](../backend/pkg/pluginapi/v2/extension.proto) 外，还要兼容 go-plugin 握手、mTLS 和 broker，不是仅暴露普通 gRPC 服务。

### 权限与修改白名单

| 权限 | 范围 |
| --- | --- |
| `request.metadata.read`（必需） | request_id、校验后的 trace_id、截止时间、平台、账号类型/ID、认证用户/分组、方法、路径、主机名；仅 Content-Type/Accept 头 |
| `request.body.read` | 最多 4 MiB JSON 请求体和模型名，可能包含用户输入 |
| `request.mutate` | `x-sub2api-extension-*` 头；配合 body.read 修改生成参数 |

请求体允许修改：`temperature`、`top_p`、`max_tokens`、`max_output_tokens`、`presence_penalty`、`frequency_penalty`、`seed`、`stop`、`instructions`。设置 `BodyChanged=true`，提供完整新 JSON 对象，保留其余字段。宿主检查类型、范围、大小，非法补丁不会部分生效。

禁止修改 `model`、`stream`、`service_tier`、messages/input、tools、身份、URL 或 HTTP 方法。上下文不提供 Authorization、Cookie、代理密码、数据库连接或宿主环境变量。用户/分组来自认证，不采信客户端伪造值。

### 决策与故障

- `pass` 使用原请求；`modify` 验证后应用补丁，插件自身不发送该上游请求。
- `deny` 始终返回 403，不发送上游、不因此切换账号。
- `error`、超时、进程退出、非法结果或熔断：`fail_closed` 返回 503，`fail_open` 使用原请求。客户端取消始终终止。
- 清单超时 1–5000ms，管理员只能缩短；并发默认 32，可设 1–256；连续 3 次故障熔断 10 秒。
- 插件原因文本不直接回显客户端；宿主日志记录决策、关联 ID 和耗时，不记录提示词或秘密。

## 5. 清单、UI 和 Host API

### 清单与签名

维护 `manifest.source.json`，由打包器填充平台运行时、文件 SHA-256 和签名。签名覆盖最终 `manifest.json` 原始字节，签名后不能重新格式化。

v2 使用 `schema_version=2`、`plugin_protocol=2`、`extension_api=1`、`ui_bridge=1`；v1 保留 `transport_api=1`，不要混填。`requires.sub2api` 对比**内核版本**，不是桌面版本。`tested_sub2api_versions` 只写实测版本，范围内未声明测试时管理员需确认。清单与 GetInfo 不一致会拒绝启动；未知能力只能显示不兼容。

参考 [v2 Schema](../backend/pkg/pluginapi/v2/manifest.schema.json) 和 [包格式](../backend/pkg/pluginapi/docs/package-format.md)。

### 配置 UI

入口为包内 `ui/index.html`，复用 [`ui/app.js`](../backend/pkg/pluginapi/examples/preprocess/ui/app.js)。流程：ready → config.load → 表单 → config.save → config.test。iframe 只有 `allow-scripts`，无管理员 Token、Cookie、localStorage 和同源权限；不要使用 CDN 或远程脚本。

每条消息校验 `event.source`、来源标识、Bridge Token 和 `request_id`，卸载页面时清理待决请求。配置由宿主加密保存；运行中保存先 Validate/Apply，数据库失败时恢复旧配置。详见 [UI Bridge](../backend/pkg/pluginapi/docs/ui-bridge.md)。

### 可选 Host API

实现 `HostAware.SetHost(HostClient)` 并声明对应权限。支持 `host.log`、`host.metric`、`host.config.read`、`secrets.broker`、`events.publish`；固定代码、指标和限额见 [Host API](../backend/pkg/pluginapi/docs/host-api.md) 与 [host-aware 示例](../backend/pkg/pluginapi/examples/host-aware/README.md)。

秘密按安装实例/能力/别名授权，最长读取窗口一小时，列表不回显。撤销只能阻止后续读取，无法召回已交付值；外部凭据仍需自己的短有效期和撤销能力。事件队列是有界、非持久化遥测，不是计费账本或可靠任务队列。

## 6. 路由、灰度和多实例

平台/账号类型来自清单。账号、认证用户、认证分组 ID 取交集，空列表表示不限；灰度按账号 ID 稳定分桶，0–100%，0 可保留进程但不接流量。

高优先级先匹配，同优先级按安装 ID 升序。只执行首个匹配插件，不是插件链，失败不改投低优先级插件。v1 同一传输作用域仍单插件独占。

多实例共享数据库和加密密钥，各自验签恢复包并同步配置/状态。统计属于当前实例，不是集群汇总。数据库无法确认启用状态时，未知的预处理路径失败关闭。

## 7. 升级和回滚

同时修改二进制和包版本。延续前面的密钥，在 `backend` 执行：

```powershell
go build -ldflags "-X main.pluginVersion=0.1.1 -X main.accountType=apikey" -o dist/plugin-demo/preprocess-0.1.1.exe ./pkg/pluginapi/examples/preprocess
go run ./pkg/pluginapi/examples/preprocess/pack -binary dist/plugin-demo/preprocess-0.1.1.exe -version 0.1.1 -account-type apikey -signing-key "$keyPrefix.private" -key-id publisher-demo -out dist/plugin-demo/request-policy-0.1.1-windows-amd64.s2plugin
```

管理页“升级”先验证新进程、配置和健康，再切新请求；旧实例最多排空 10 秒。失败保留旧版。热升级要求 ID、协议、能力、权限和声明作用域不变；增加权限应停用后重新安装，不能静默扩权。

回滚保留最近 5 个快照、24 小时窗口，恢复旧签名包和当时加密配置，保留当前路由策略。`runtime_version` 是实例实际运行版本。卸载前须停用；卸载删除本插件配置及历史。

## 8. 容器和跨平台构建

process 使用宿主 OS/架构；container 使用 Linux 二进制，即使宿主是 Windows。不要将 Windows exe 放入 Linux 沙箱。以下从 `backend` 构建 Linux amd64：

```powershell
$env:GOOS='linux'; $env:GOARCH='amd64'; $env:CGO_ENABLED='0'
go build -ldflags "-X main.accountType=apikey" -o dist/plugin-demo/preprocess-linux ./pkg/pluginapi/examples/preprocess
Remove-Item Env:GOOS; Remove-Item Env:GOARCH; Remove-Item Env:CGO_ENABLED
go run ./pkg/pluginapi/examples/preprocess/pack -binary dist/plugin-demo/preprocess-linux -account-type apikey -target linux-amd64 -signing-key "$keyPrefix.private" -key-id publisher-demo -out dist/plugin-demo/request-policy-0.1.0-linux-amd64.s2plugin
```

按 [隔离指南](../backend/pkg/pluginapi/docs/sandbox.md) 构建本地镜像并设置 `plugins.v2_sandbox.mode: container`。运行器不自动拉镜像、不失败降级。容器无网络、只读根、UID/GID 65532、有 CPU/内存/pids 限额，Host API 复用已认证 broker。切换模式需安装对应目标平台的包。

## 9. 管理 API

接口前缀 `/api/v1/admin/plugins`，需要管理员认证；写操作受 step-up 设置约束，生产建议开启。UI iframe 不直接调用这些接口，而通过 Bridge。

| 接口 | 请求/行为 |
| --- | --- |
| `GET` 前缀本身、`GET /:id` | 清单、兼容、绑定、运行状态 |
| `POST /authorize-upload` | 轻量权限/step-up 校验，不安装文件 |
| `POST /upload` | multipart `plugin`，仍再次校验权限 |
| `GET/PUT /:id/config`、`POST /:id/test` | 配置读写、已保存配置诊断 |
| `POST /:id/enable` | `rollout_percent`、`accept_untested` |
| `POST /:id/disable`、`DELETE /:id` | 停用、卸载 |
| `PUT /:id/routing` | `policies` 与最新 `expected_updated_at`，拒绝过期覆盖 |
| `POST /:id/upgrade` | multipart `plugin`、`accept_untested`；不能把 FormData 转 JSON |
| `GET /:id/versions`、`POST /:id/rollback` | 历史；回滚传 `version_id`、`accept_untested` |
| `GET /:id/host`、`GET/PUT/DELETE /:id/secret-grants` | 遥测、秘密授权/撤销 |

字段以 [API 类型](../frontend/src/api/admin/plugins.ts) 和 [handler](../backend/internal/handler/admin/plugin_handler.go) 为准。

## 10. 验收和排错

在 `backend` 运行；最后一项需要 Docker，会自动创建并清理独立测试数据库：

```powershell
go test ./pkg/pluginapi/... -count=1
go test ./internal/service -run '^TestPluginExtensionProcessIntegration$' -count=1
go test ./internal/service -run '^TestPluginV1ProcessCompatibility$' -count=1
go test -tags plugin_e2e ./cmd/server -run '^TestPluginProductionE2E$' -count=1 -v -timeout=8m
```

真实浏览器流程、Host API 和容器验证见 [完整测试指南](../backend/pkg/pluginapi/docs/testing.md)。发布前覆盖配置边界、身份、签名/哈希、超时/取消/退出、非法补丁、计费、无插件路径、升级失败和回滚；检查包内无私钥、日志、真实测试数据或宿主凭据，再做单账号灰度。

| 问题 | 排查 |
| --- | --- |
| 签名不受信任 | key_id、Base64 公钥、宿主配置键是否一致；重启宿主 |
| 不兼容/身份失败 | 宿主是否包含 v2；版本、能力、权限、超时是否与 GetInfo 一致 |
| UI 拒绝 localStorage/网络 | sandbox 正常边界，改用包内资源和 Bridge |
| 403 extension_error | 策略 deny，不是上游凭据错误，不要切账号重试 |
| 503 extension_error | fail_closed、超时、并发、熔断、进程或绑定状态不可用 |
| 未修改请求 | 检查作用域交集、优先级、灰度、权限和白名单 |
| 容器启动失败 | 本地镜像、Docker Linux Engine、Linux 静态二进制与架构 |
| 测试版拒绝数据库 | 仅本机 25432 的 sub2api_plugin_preview 及 26379 缓存，不复制正式配置 |

### v1 维护说明

旧 TransportPlugin 继续运行，不必因 v2 重新编译。Forward 请求帧为 start/body_chunk/body_end，响应为 start/body_chunk/end 或 error。仅能确认未发送时才返回 `request_sent=false`，否则必须 true，避免重复发送/扣费。v1 获得网络传输必需的敏感数据，边界不同于 v2，当前容器运行器不接管 v1。详见 [v1 开发规范](../backend/pkg/pluginapi/docs/development.md)。

### Evidence → Finding → Path

| 验证证据 | 结论 | 实现位置 |
| --- | --- | --- |
| `TestPluginExtensionProcessIntegration` | 签名、子进程、双实例、升级回滚可运行 | `backend/internal/service/plugin_extension_integration_test.go` |
| `TestPluginProductionE2E`、`frontend/scripts/plugin-e2e.py` | UI/数据库/进程/网关闭环，拒绝不发上游不扣费 | `backend/cmd/server/plugin_e2e_test.go` |
| Host API、秘密和隔离测试 | 权限、加密、无网络与资源限额有执行证据 | [验收记录](2026-09-17_plugin-system-extension-development-report.md) |

调用路径：核心认证/账号选择 → 准备 HTTP → 能力路由 → v2 子进程 → 宿主验证决策/补丁 → v1 或内置 HTTP → 核心响应/用量/计费。插件不能跳过核心直接改账本。
