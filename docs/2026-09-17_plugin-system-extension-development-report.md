# 插件扩展开发记录

日期：2026-09-17。分支：`codex/plugin-generic-interface`。

后续保护传输、Preview `.2`/`.3` 和配置页修复记录见 [2026-09-18 阶段开发日志](2026-09-18_plugin-development-phase-report.md)。本文保留首版通用扩展的历史验收范围。

本文记录基于 [扩展分析报告](2026-09-17_plugin-system-extension-analysis-report.md) 落地的代码与验收证据。已完成报告建议的第一种能力 `request.preprocess.v1` 的前后端、运行时、权限隔离及端到端闭环。报告列举的 Provider、响应处理、事件订阅和后台任务仍是后续能力方向，不属于本次已实现能力。

## 已落地的行为

管理员可安装 v2 签名包、查看能力/权限/超时/失败策略、配置、测试和启停。清单版本、进程身份和运行时权限必须一致。未知能力可安装并展示不兼容，不能启用或执行配置诊断。

宿主可同时管理 v1 传输和 v2 预处理进程。当前 v2 注册能力为 OpenAI OAuth/API Key 的请求预处理，支持平台/账号类型、账号/用户/认证分组交集、账号灰度与优先级。v1 保持单传输独占；v2 允许多个插件，以优先级和安装 ID 确定首个匹配者，故障时不转交低优先级插件。

预处理发生在上游 HTTP 请求准备完毕后，只向插件发送白名单元数据及显式授权的请求体。允许修改生成参数和 instructions；账号选择、模型、流式模式、计费档位、消息、工具和认证信息不受插件修改。拒绝和失败关闭返回终止性扩展错误，避免按上游认证错误处理或自动切换账号。具体白名单见 [v2 协议](../backend/pkg/pluginapi/v2/README.md)。

每个能力实例默认 32 并发，可配置 1–256；超时允许缩短但不能超过清单声明和 5 秒上限，连续 3 次失败后熔断 10 秒。调用方取消不计入插件故障。管理接口暴露调用、失败、拒绝、并发和熔断状态；这些计数属于当前实例。

现已加入版本记录、热升级和回滚：候选进程先完成身份/配置/健康检查，再由数据库事务保存旧快照并切换；旧进程排空已有请求。最近 5 个快照提供 24 小时回滚窗口，恢复旧签名包及其加密配置。新迁移 `239_plugin_version_history.sql` 只增加历史表，保留原安装和绑定字段。

路由策略使用 `240_plugin_capability_routing.sql` 增加持久化字段。编辑接口携带版本戳以拒绝过期覆盖，运行中可更新；用户和认证分组由认证中间件冻结，客户端身份字段和调度分组覆盖不能改变此身份。

v2 已新增可选容器隔离：低权限 UID/GID 65532、零有效 capabilities、无网络、只读根和程序文件、最小环境变量、cgroup 内存/CPU/pids 限制，以及宿主租约丢失后的自动终止。普通进程仍为兼容默认值，管理页明确区分实际隔离模式；v1 不由该容器运行器处理。部署方式见 [隔离说明](../backend/pkg/pluginapi/docs/sandbox.md)。

Host API 已接入 multiplex 双向 RPC，提供受控日志、固定指标、已应用配置、按实例/能力/别名授权的秘密读取，以及有界异步事件。秘密使用 AES-256-GCM 及用途/身份/别名/到期时间绑定载荷；过期清理，列表不回显，写入请求体不进入审计日志。管理员可在页面授予和撤销；详情见 [Host API 边界](../backend/pkg/pluginapi/docs/host-api.md)。

## 验证证据

以下命令在 `backend` 目录运行，前端命令在 `frontend` 目录运行。

| Evidence | Finding | Path |
| --- | --- | --- |
| `go test ./pkg/pluginapi/... -count=1` 通过 | v1/v2 公共契约可以共同编译，消息与业务类型无命名冲突 | `backend/pkg/pluginapi/v2/wire/`、`adapter.go` |
| `go test ./internal/service -run '^TestPluginExtensionProcessIntegration$' -count=1 -v` 通过 | 真实子进程完成签名包安装、配置、改写、拒绝、超时及退出验证 | `backend/internal/service/plugin_extension_integration_test.go` |
| 同一进程集成测试中的双管理器场景通过 | 独立目录恢复签名包、同步配置、重启已退出进程、跨实例停用有效 | `plugin_manager.go`、`plugin_extension_repository_test.go` |
| `TestPluginExtensionProcessIntegration/hot_upgrade_and_rollback` 通过 | 老进程有未完成调用时切到新进程，另一个实例跟随升级，回滚恢复旧配置，非法包保留旧进程 | `plugin_versions.go`、`plugin_extension_integration_test.go` |
| `go test -tags integration ./internal/repository -run '^TestPluginRepository' -count=1 -v -timeout=10m` 通过 | PostgreSQL 中 v2 字段完整保存、能力作用域共存与冲突回滚有效 | `backend/internal/repository/plugin_repo_integration_test.go` |
| `TestPluginRepositoryVersionSwapSnapshotsAndRejectsStaleState` 通过 | 快照包/配置完整保存、跨插件隔离、过期不可回滚，并发配置更新使旧快照切换失败 | `backend/internal/repository/plugin_versions_integration_test.go` |
| `TestPluginRepositoryRoutingPolicyRoundTripAndCAS` 通过 | 作用域 ID、优先级、灰度、并发和超时完整持久化；过期版本戳不能覆盖 | `backend/internal/repository/plugin_versions_integration_test.go` |
| `TestPluginRouting*` 及真实进程的策略同步场景通过 | 稳定优先级/身份交集、超时覆盖、调低并发不重置在途计数、多实例同步有效 | `backend/internal/service/plugin_routing_policy_test.go` |
| `go test -tags unit ./internal/server/middleware -run TestAPIKeyAuth -count=1` 通过 | 认证流程回归通过；伪造请求身份和 fallback 工作分组不能改变插件主体 | `backend/internal/server/middleware/plugin_principal_test.go` |
| `TestContainerIsolationIntegration` 在 Windows 和 Linux 宿主进程中通过 | UID/GID 65532、补充组清空、有效 capabilities=0、仅 lo、只读文件及宿主环境不可达，内核限额实测，超额分配产生 OOM 事件 | `backend/internal/pluginruntime/container_test.go` |
| `TestContainerIsolationIntegration/host_lease_loss` 通过 | 停止宿主续租后监督进程主动结束插件，没有遗留容器 | `backend/cmd/plugin-sandbox/main.go` |
| `TestPluginExtensionContainerProcessIntegration` 通过 | 签名安装、RPC、预处理、双实例、热升级/回滚和路由同步在真实隔离运行器内有效 | `backend/internal/service/plugin_extension_integration_test.go` |
| `TestPluginHostAPIProcessIntegration`、`TestPluginHostAPIContainerIntegration` 通过 | 两种运行器中双向回调、配置读取、获授权秘密读取、撤销、指标和事件有效 | `backend/internal/service/plugin_host_integration_test.go` |
| `TestPluginRepositorySecretGrantsEncryptScopeExpireAndRevoke` 通过 | 实际 AES 加密、无明文回显、跨实例/能力隔离、过期密文清理、撤销和数量上限有效 | `backend/internal/repository/plugin_secrets_integration_test.go` |
| Host API 边界与操作审计回归通过 | 未声明权限拒绝，任意日志正文/指标名拒绝，队列有背压，密文不可跨用途/别名搬用，秘密/配置请求体不进入审计 | `plugin_host_services_test.go`、`audit_log_test.go` |
| 管理接口测试通过 | HTTP 列表返回 v2 能力/权限/超时/策略，不包含配置密文或插件包内容 | `backend/internal/handler/admin/plugin_handler_extension_test.go` |
| 插件和 OpenAI transport 针对性回归通过 | v1 路由与已有请求已发送保护未被新 Hook 改写 | `plugin_security_regression_test.go`、`plugin_manager_routing_test.go` |
| `TestPluginV1ProcessCompatibility` 通过 | 无外部插件包依赖，原 v1 SDK 子进程完成分块响应、取消、已发送错误返回与健康检查 | `plugin_v1_process_test.go`、`testdata/v1transport/main.go` |
| `go test -tags plugin_e2e ./cmd/server -run '^TestPluginProductionE2E$' -count=1 -v -timeout=22m` 带浏览器等待的完整测试通过 | 生产依赖图、认证、TOTP、管理 API、签名安装、真实进程和数据库计费接通；403/503 不发上游、不扣费、不禁用账号，升级/回滚/停用有效 | `backend/cmd/server/plugin_e2e_test.go` |
| 同一 E2E 的 24 请求 / 8 并发场景通过 | 上游次数、用量记录与余额一致；最后一次本机合成上游 p50=629ms、p95=1048ms，不代表生产容量 | `plugin_e2e_test.go` 的 `bounded concurrent traffic has exact once billing` |
| `frontend/scripts/plugin-e2e.py` 通过，`page_errors=[]` | 真实 Chromium 完成登录/TOTP、上传前验证、签名安装、启用、iframe 配置测试、作用域保存、热升级、回滚和停用 | `backend/dist/plugin-browser-evidence/result.json` 与 6 组截图/可访问树 |
| 插件页、语言键和真实 Axios multipart 转换测试通过（共 15 个测试） | 能力/灰度、升级/回滚、路由、隔离、秘密授权/撤销、输入清空、上传前二次验证和文件编码有效 | `frontend/src/views/admin/__tests__/PluginsView.spec.ts`、`frontend/src/api/admin/__tests__/plugins.spec.ts` |
| `pnpm run typecheck` 与修改文件 ESLint 检查通过 | 前端类型和静态规则检查通过 | `frontend/src/views/admin/PluginsView.vue` |

进程集成测试通过本机测试上游执行真实请求。数据库测试使用测试框架创建的 PostgreSQL/Redis 容器，不修改现有开发服务。没有执行生产启用或部署。

端到端复现步骤见 [测试指南](../backend/pkg/pluginapi/docs/testing.md)。浏览器完成截图 SHA-256：`5A908D8789B623C08C1791B201C6FC191716C6C634B9DD78E9331C62C0D1D276`，路径 `backend/dist/plugin-browser-evidence/06-complete.png`。生成物位于忽略的 `dist/`，测试源码与步骤保存在版本控制范围内。验收后浏览器、Vite、插件进程和测试数据库容器均已清理；现有开发容器未修改。

调用路径为：OpenAI 请求准备 → `PreprocessOpenAI` → 能力路由和权限校验 → v2 gRPC 子进程 → 校验决策和原子应用白名单修改 → 现有 v1 传输或内置 HTTP → 核心响应/用量/计费处理。插件拒绝和失败关闭在发送上游前终止。

## 示例交付物

- [示例源码、配置 UI 与构建说明](../backend/pkg/pluginapi/examples/preprocess/README.md)。
- 当前开发包：`backend/dist/plugin-v2/request-policy-host02-windows-amd64.s2plugin`，Windows amd64，未签名；仅适用于允许未签名包的本地测试宿主。
- 示例包 SHA-256：`F55F6294A9EE2681395A4AC3BC395525B1089848BC93EADEAA6282980A2AE473`。
- Linux amd64 开发包：`backend/dist/plugin-v2/request-policy-host02-linux-amd64.s2plugin`，适用于容器模式，未签名；SHA-256 为 `3F9E980E066228551C74819108DEA2EC299522282306430E12C4AE0012056DB6`。
- [锁定生成器版本的生成脚本](../backend/pkg/pluginapi/v2/generate.ps1)，对应 protoc 28.3 / protoc-gen-go v1.36.11 / protoc-gen-go-grpc v1.6.2。

示例包限制输出 token，可拒绝指定模型。开发配置 `delay_ms` 可用于复现截止时间行为；包内带配置页面和 UI Bridge 交互。

`host02` 包已将宿主兼容范围扩至 `<0.3.0`，适用于当前 0.2.5 宿主；旧的无 `host02` 文件名开发包仍保留，但其 `<0.2.0` 清单不能安装到当前宿主。打包器新增 `-account-type apikey`，须与构建时 `-X main.accountType=apikey` 一致。

## 端到端发现并修复的问题

1. 插件拒绝/超时已写出 JSON，但未设置核心的终止响应标记，外层又追加 SSE 错误事件。新增 `MarkResponseCommitted`，回归同时检查完整 JSON、HTTP 状态、无上游调用和计费不变。
2. 大包上传先触发后端 403，Vite/浏览器出现 `ECONNRESET` / `ERR_CONNECTION_ABORTED`，无法弹出二次验证。新增受同一管理员及 step-up 中间件保护的 `/admin/plugins/authorize-upload` 小请求；安装和升级先验证后发文件，实际上传仍再次校验权限。
3. 升级请求继承默认 JSON 头，Axios 会将 FormData 转为 JSON。改为显式 multipart；新测试使用真实 Axios transform 验证文件没有被序列化丢失。
4. 示例清单 `<0.2.0` 与当前宿主 0.2.5 不兼容。更新范围并重新构建 Windows/Linux 开发包。

## 对分析报告的交付范围审计

| 分析报告条目 | 当前状态与剩余工作 |
| --- | --- |
| 阶段一：协议、清单、首个场景、示例 | 已完成真实生成代码、清单验证、能力边界、SDK、可运行且兼容当前宿主的示例 |
| 阶段二：通用运行时、能力健康/并发/熔断 | 首个能力的双协议运行、优先级、账号/用户/认证分组范围、灰度、可配置并发及超时已实现 |
| 热升级：先启动新包、切路由、保留回滚窗口 | 已实现相同能力/权限/作用域契约内的在线升级、24 小时/最近 5 个快照、管理接口及页面回滚 |
| 阶段三：核心 Hook、业务一致性 | 按报告阶段三建议接在请求准备后、HTTP 发出前；认证/模型/账号选择保持核心控制，完整计费与失败路径已验收 |
| 数据库和管理 API | 清单/绑定、版本回滚、细粒度作用域与路由策略均已实现并验证 |
| 管理界面 | 能力、权限、统计、灰度、路由策略、版本切换、回滚、Host API 和秘密授权已实现；真实浏览器生命周期通过，秘密表单另有组件/后台/数据库/进程联动测试 |
| Host API | 已实现按实例与能力授权的日志、指标、配置、短期秘密读取、有界事件及管理员授权页面；读取窗口不替代外部凭据自身过期/撤销 |
| 阶段四：权限与部署隔离 | v2 容器模式已实现并实测低权限、文件/网络、CPU/内存/pids 限制和异常退出清理；普通进程模式明确不提供这些保证 |
| 阶段五：验证与推广 | 真实进程、双实例恢复、PostgreSQL、管理 UI→数据库→进程、核心计费、并发回归、故障回滚已验证；生产灰度及真实流量容量测试属于部署验收，未擅自执行 |
| 后续 Provider/响应/事件/后台能力 | 报告明确建议第一版先选一种能力，当前没有注册这些后续能力；也未加入可更改模型/账号选择的选择前 Hook |

本次交付是首个通用扩展能力的可运行版本，不是任意核心代码注入平台。生产发布前应启用签名和容器模式、构建匹配平台的沙箱镜像，并在目标部署上做单租户/账号灰度和真实负载评估。原生普通进程模式不提供 OS 沙箱；Windows 原生 Job Object、其他架构及其他容器平台未在本次宣称支持。
