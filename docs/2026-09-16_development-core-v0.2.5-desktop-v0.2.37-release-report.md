# 内核 v0.2.5 与桌面 v0.2.37 开发与发布记录

记录日期：2026-09-16（America/Los_Angeles）。本次承接已发布的桌面 v0.2.35 / 内核 v0.2.4，同步上游 v0.2.5，并发布内置新内核的 Windows 安装包。

桌面 v0.2.37 与稳定兼容内核 v0.2.5 已正式发布。源码 CI、安全扫描、签名安装包和稳定内核四条工作流均在提交 `e28d98734` 上成功；重新下载的产物、更新签名、内核身份和两个匿名更新入口全部核验通过。最终核验时间为 2026-09-16 02:09:09 PDT（09:09:09 UTC）。

本版包含两项数据库迁移：添加 OpenCode 平台约束、清理三档限额均为 NULL 的平台配额记录。无配额记录继续表示不限额。升级前应备份数据库；内核双槽回滚不会撤销数据库迁移。安装包更新签名为 Tauri/Minisign，不能视为 Windows Authenticode 证书。

## 版本与来源

| 项目 | 值 |
| --- | --- |
| 开发分支 | `codex/core-v0.2.5-desktop-v0.2.37` |
| 桌面版本 | `0.2.37` |
| 内置兼容内核 | `0.2.5` |
| 官方上游 | [Wei-Shaw/sub2api v0.2.5](https://github.com/Wei-Shaw/sub2api/releases/tag/v0.2.5) |
| 官方上游提交 | `86f93c28ee34cc74b629dafb748bd5ac5ca8c5ea` |
| 上一版源码 | `4fe2e80db19f527ad39055159957998ba62290f0` |
| 通过首轮完整 CI 的业务源码 | `54a266a69d76bcec8100d12cb9ec171c9a54e704` |
| 最终发布源码 | `e28d987348dae21922647bf96f090d27c5e04860` |
| 上一版上游提交 | `5de5e2bed035d43591a2e10e51f420ef6a84eb98` |
| 成本扩展 / 算法 | `1.1.1` / `1.6.0` |
| 必需能力 | `account_cost_loss_ledger.v1`、`account_economics_sampling.v1` |

官方 v0.2.4 → v0.2.5 增量涉及 447 个文件。主要功能包括 OpenCode Zen / GO、多协议路由、充值/订阅三态开关、订阅和 API Key 批量操作；修复 WebSocket 连接池容量、心跳、抢占与作用域隔离，Ollama Cloud 用量重置，DeepSeek 模型校验和计价，以及 Gemini/Grok/Responses 兼容问题。详细用户说明见 [发布说明](../frontend/DESKTOP_RELEASE_NOTES.md)。

## 合并方式与冲突决策

仓库历史使用 synthetic upstream 提交记录官方源码树。直接合并官方历史可能重复引入旧改动。本次先验证 `38161fe68` 的源码树与已记录的官方 v0.2.4 提交完全一致，且它是当前分支祖先；再用官方 v0.2.5 的源码树创建以该基线为父提交的增量提交。

自动更新的 [阻塞 issue #28](https://github.com/rw0104/sub2api-cost-console/issues/28) 记录了以下 7 个冲突文件，均逐项审阅：

| 文件 | 决策与依据 |
| --- | --- |
| `README.md` | 保留本项目桌面用户指南，更新桌面/内核版本 |
| `backend/internal/service/wire.go` | 限流服务同时接入本地成本损失服务与上游 Ollama 用量探测服务 |
| `backend/cmd/server/wire_gen.go` | 用 Wire 重新生成，确保服务依赖与生命周期一致 |
| `backend/internal/service/account.go` | 保留 K-12 白名单，再执行上游 DeepSeek 默认模型名单与既有映射逻辑 |
| `backend/internal/setup/setup_test.go` | 保留桌面 CORS 两项测试，并采用上游“先连接配置数据库，仅缺库时回退 postgres”的测试 |
| `frontend/src/App.vue` | 同时保留受管内核界面、标题栏、更新中心和上游站点开关 |
| `frontend/src/api/client.ts` | 保留会话身份、跨用户重放防护和 Hash 登录恢复，融合上游临时故障状态与消息 |

会话刷新中，网络故障、状态 0/408/429/5xx 保留凭据，真正被拒绝的刷新结束会话。上游 `client.spec.ts` 与本地 `authSessionRecovery.spec.ts` 共同验证；本地表驱动测试补充 0、408 两种情况，避免只让单侧测试通过。

迁移 `238_opencode_go_platform.sql` 与 `238_purge_unlimited_user_platform_quotas.sql` 的数字前缀相同，但迁移状态按完整文件名记录，两者均需保留。原有 Windows ZIP 关闭修复、成本账本、经济采样和桌面生命周期功能继续保留。

## 发布前依赖修复

首轮 [安全扫描 35072262994](https://github.com/rw0104/sub2api-cost-console/actions/runs/35072262994) 在 gRPC `1.82.1` 检出两项符号可达漏洞，调用链涉及插件通信。核对 Go 官方漏洞库后升级到 `1.83.2`：[GO-2026-6348](https://pkg.go.dev/vuln/GO-2026-6348) 的 HTTP/2 内存耗尽问题在 `1.83.1` 修复，但 [GO-2026-6443](https://pkg.go.dev/vuln/GO-2026-6443) 在 `1.83.x` 分支需要 `1.83.2`。后者实际触发还依赖 xDS 配置，扫描可达性不代表当前配置已被利用。

`go get google.golang.org/grpc@v1.83.2` 与 `go mod tidy` 同步其必要的 OpenTelemetry / genproto、`golang.org/x/*` 依赖和校验值。依赖改变后重新验证后端，并由最终源码提交重新执行安全扫描。没有添加漏洞豁免或跳过门禁。

## 上游身份元数据修正

初次准备 v0.2.36 时，PowerShell 中未加引号的 `^{commit}` 表达式导致人工记录了上游父提交 `30ed40a56a5f4b5ab7b8dd3d685353db3a531c84`。实际合并使用了正确解引用的官方源码树 `a291b5788afa30085709a78349233656e730ac11`，但 `UPSTREAM_SUB2API_COMMIT` 和说明中的元数据不匹配。稳定内核工作流拒绝该不一致状态，既有 core-stable 未被覆盖。

发现后取消尚未发布资产的 v0.2.36 安装器任务，保留原 Git 标签历史，将最终桌面版本推进到 v0.2.37，并将元数据修正为官方注解标签解引用后的 `86f93c28ee34cc74b629dafb748bd5ac5ca8c5ea`。业务源码树不需要重新合并。

新增 `frontend/scripts/verify-core-source.mjs`，同时用于桌面和稳定内核工作流：核对官方标签解引用提交、元数据提交，以及当前历史中已集成 synthetic upstream 的完整源码树。桌面 checkout 使用完整历史，并在构建前获取对应官方标签。`node --test frontend/scripts/verify-core-source.test.mjs` 用独立临时仓库验证正确注解标签、误填父提交和未集成源码树三种情况。

## 开发与验证命令

从仓库根目录执行；Go 使用 `backend/go.mod` 指定的 1.27.0，pnpm 使用 9，Rust 使用 Windows MSVC stable。需要访问网络时使用本机实际可用代理，不将代理凭据写入仓库。

Windows 的备份进程测试需要 Git for Windows 提供的 `sh` 在 PATH 中。首轮本地 Go 测试有 3 项因为缺少 `sh` 失败，另 1 项验证码网络错误测试受到依赖下载代理影响。依赖下载完成后，测试进程清除 HTTP/HTTPS/ALL_PROXY、设置 NO_PROXY，并将已安装的 Git `bin` / `usr/bin` 加入当前进程 PATH 后重跑。不要因此放宽生产错误处理或跳过测试。

```powershell
git fetch --no-tags https://github.com/Wei-Shaw/sub2api.git 'refs/tags/v0.2.5:refs/tags/upstream-v0.2.5'
node --test frontend/scripts/verify-core-source.test.mjs
node frontend/scripts/verify-core-source.mjs upstream-v0.2.5

Push-Location backend
go run github.com/google/wire/cmd/wire ./cmd/server
go test ./...
Pop-Location

Push-Location frontend
corepack pnpm@9 install --frozen-lockfile
corepack pnpm@9 test:run
corepack pnpm@9 lint:check
corepack pnpm@9 build
node scripts/prepare-desktop-sidecar.mjs
corepack pnpm@9 build:desktop
node scripts/prepare-desktop-release.mjs --validate-notes-only
Push-Location src-tauri
cargo test --locked --features custom-protocol
Pop-Location
Pop-Location
```

Go 默认测试不等同于所有带标签的数据库集成测试；完整单元、集成、静态检查和 Shell 验证由 `backend-ci.yml` 补充。构建顺序必须先生成 Web 资源，再编译嵌入 Web 资源的 sidecar，最后构建桌面前端。

## Git 与两个发布通道

版本文件包括 `frontend/CORE_VERSION`、`frontend/UPSTREAM_SUB2API_COMMIT`、`backend/cmd/server/VERSION`，以及 `frontend/src-tauri/tauri.conf.json`、`Cargo.toml`、`Cargo.lock`。成本扩展和算法版本保持不变。

桌面通道由推送 `v0.2.37` 标签触发 `desktop-release.yml`，输出 NSIS 安装器、`.sig`、`latest.json`、`INSTALLER_SHA256SUMS.txt` 和发布说明。稳定内核通道由 `core-sync.yml` 输出兼容 ZIP、`CORE_SHA256SUMS.txt` 和 schema 2 的 `core-latest.json`。解决冲突并将源码推送到 main 后，关闭对应的阻塞 issue，再触发稳定内核工作流。

公开入口：

- [桌面更新清单](https://github.com/rw0104/sub2api-cost-console/releases/latest/download/latest.json)
- [稳定内核清单](https://github.com/rw0104/sub2api-cost-console/releases/download/core-stable/core-latest.json)
- [桌面发布页](https://github.com/rw0104/sub2api-cost-console/releases/tag/v0.2.37)

发布后应从 GitHub 重新下载产物，重算所有校验清单条目，核对安装器签名与更新清单签名一致，并使用客户端内置公钥验证 Ed25519 文件签名和可信注释签名。内核 ZIP 仅应包含预期可执行文件；解压后执行 `--version` 核对上游提交、扩展和能力，并将清单中的算法版本与 `frontend/ALGORITHM_VERSION` 核对。还需匿名获取两个公开入口，避免只验证本地产物。

前序完整测试日志保存在 `.git/release-v0.2.36/`，身份修正后的构建与核验日志保存在 `.git/release-v0.2.37/`，下载产物保存在 `frontend/release-assets/online-verify-v0.2.37/`，均不作为源码分发。用户原有 `.gitignore` 修改保留在工作区，不并入发布提交。

发布验证不对本机现用桌面或生产数据库执行原位升级；实际安装和数据迁移仍由使用者在备份后执行。

## Evidence → Finding → Path

### E-001：官方版本与合并基线

- observed_at：2026-09-16
- source_type：command
- source_ref：官方 Release API、本地 Git 源码树
- content_hash / artifact_path：n/a
- repro_command：`gh api repos/Wei-Shaw/sub2api/releases/tags/v0.2.5 --jq .tag_name`；`git show -s --format=%T 38161fe68 5de5e2bed035d43591a2e10e51f420ef6a84eb98`
- raw_excerpt：官方 `v0.2.5`，完整提交 `86f93c28ee34cc74b629dafb748bd5ac5ca8c5ea`；上一版官方树与 synthetic 基线一致。
- linked_workitem：#28
- supersedes：none

### E-002：修正后上游身份

- observed_at：2026-09-16
- source_type：command
- source_ref：`frontend/scripts/verify-core-source.mjs`、官方 v0.2.5 注解标签
- content_hash / artifact_path：n/a
- repro_command：`node frontend/scripts/verify-core-source.mjs upstream-v0.2.5`；`node --test frontend/scripts/verify-core-source.test.mjs`
- raw_excerpt：upstreamCommit=`86f93c28ee34cc74b629dafb748bd5ac5ca8c5ea`，upstreamTree=`a291b5788afa30085709a78349233656e730ac11`，syntheticBase=`24a8b036652e4a2476486304714493f6db835a82`；回归测试通过。
- linked_workitem：发布身份修正
- supersedes：v0.2.36 的错误身份元数据

### E-003：发布工作流与公开下载产物

- observed_at：2026-09-16T09:09:09Z
- source_type：command
- source_ref：[桌面发布任务](https://github.com/rw0104/sub2api-cost-console/actions/runs/35075002072)、[稳定内核任务](https://github.com/rw0104/sub2api-cost-console/actions/runs/35075002623)、公开 Release 资产
- content_hash：安装器 `d13910e8ec39abcb5ecb241884e86483186d2aa7aebc98daf810d8520b6de9dc`；内核 ZIP `649cd80a56a91e376aa82743d873cd904bead7665cf42a3cd0db1c3ab1e74106`
- artifact_path：`frontend/release-assets/online-verify-v0.2.37/desktop/Sub2API.Cost.Console_0.2.37_x64-setup.exe`、`frontend/release-assets/online-verify-v0.2.37/core/sub2api-core_0.2.5_1.1.1_windows_x86_64.zip`
- repro_command：`gh release view v0.2.37 --repo rw0104/sub2api-cost-console --json assets`；`gh release download core-stable --repo rw0104/sub2api-cost-console --pattern core-latest.json --output -`
- raw_excerpt：两个发布任务均 success，headSha=`e28d987348dae21922647bf96f090d27c5e04860`；全部校验条目、GitHub digest、安装器 Ed25519 和可信注释签名、内核身份及公开入口一致。
- linked_workitem：桌面与稳定内核发布
- supersedes：none

### F-001：需要同步桌面与内核两个发布通道

- status：validated
- evidence_ids：[E-001]
- location：`frontend/src-tauri/tauri.conf.json`、`.github/workflows/core-sync.yml`
- impact：独立内核更新不替换桌面 Vue 资源，必须同时发布新桌面版本才能提供新增管理界面。
- confidence：high

### P-001：可重现的升级发布路径

验证官方标签与源码树（E-001、E-002）→ 合并增量并审阅 7 处冲突 → 运行本地与云端门禁 → 推送 main/发布分支/桌面标签 → 分别发布安装器与兼容内核 → 下载核验哈希、更新签名、内核身份及公开入口（E-003）。

## 最终验证记录

- 前端全量：312 个文件、2,371 项测试通过；ESLint 通过；Web 生产构建通过。
- [最终源码 CI 35074996163](https://github.com/rw0104/sub2api-cost-console/actions/runs/35074996163)：提交 `e28d98734` 的后端单元、数据库集成、Go 静态检查、前端与部署脚本全部通过。
- [最终源码安全扫描 35074996166](https://github.com/rw0104/sub2api-cost-console/actions/runs/35074996166)：提交 `e28d98734` 的后端 govulncheck 与前端依赖扫描均成功。
- 最终源码 `e28d98734` 已推送到 main、发布分支和 `v0.2.37` 标签；#28 已关闭。v0.2.36 仅保留 Git 标签，无正式安装包发布。
- Windows Go 全量复测通过；v0.2.37 重新构建后的 Rust 生命周期测试 60 passed、1 ignored（需显式启用的本机 Docker 恢复测试）。
- 本地受管内核 `--version` 确认版本、完整上游提交、成本扩展和两项必需能力；`go version -m` 确认 Go 1.27.0、gRPC 1.83.2、x/net 0.58.0。
- [签名安装包工作流 35075002072](https://github.com/rw0104/sub2api-cost-console/actions/runs/35075002072) 成功，headSha 与发布源码一致。
- [Windows x64 安装包](https://github.com/rw0104/sub2api-cost-console/releases/download/v0.2.37/Sub2API.Cost.Console_0.2.37_x64-setup.exe)：31,338,728 字节，SHA-256 `d13910e8ec39abcb5ecb241884e86483186d2aa7aebc98daf810d8520b6de9dc`。重新下载后，全部安装器校验条目、Ed25519 文件签名及可信注释签名验证通过。

- [稳定内核工作流 35075002623](https://github.com/rw0104/sub2api-cost-console/actions/runs/35075002623) 成功，headSha 与发布源码一致，已更新 core-stable。
- 内核归档 `sub2api-core_0.2.5_1.1.1_windows_x86_64.zip`：36,802,422 字节，SHA-256 `649cd80a56a91e376aa82743d873cd904bead7665cf42a3cd0db1c3ab1e74106`，仅包含 `sub2api.exe`。
- 下载内核的 `--version` 确认 `0.2.5`、完整官方提交 `86f93c28ee34cc74b629dafb748bd5ac5ca8c5ea`、扩展 `1.1.1` 与两项必需能力；`go version -m` 确认包含 gRPC `1.83.2`。
- 安装器 FileVersion / ProductVersion 均为 `0.2.37`。所有 INSTALLER_SHA256SUMS / CORE_SHA256SUMS 条目、GitHub 资产 digest 和文件字节数重新核对通过。
- 匿名获取的桌面 `/releases/latest/download/latest.json` 与稳定内核 `/releases/download/core-stable/core-latest.json`，均与已核验发布清单完全一致。GitHub latest 为 `v0.2.37`。
- 发布后再次查询官方 latest 仍为 `v0.2.5`，远端标签解引用提交仍为 `86f93c28ee34cc74b629dafb748bd5ac5ca8c5ea`。
- 本地总核验记录：`frontend/release-assets/online-verify-v0.2.37/verification.json`；核验时间 `2026-09-16T09:09:09.143Z`。
