# 桌面 v0.2.33 启动恢复与进程修复

日期：2026-09-07（America/Los_Angeles）。兼容内核保持 `v0.2.2`，成本扩展 `1.1.1`，成本算法 `1.6.0`。

桌面 `v0.2.33` 已正式发布并成为 GitHub latest。发布源码 CI 和签名安装包工作流全部成功；正式下载的安装器通过 SHA-256、Ed25519 更新签名与可信注释签名校验，公开自动更新入口与已验证的清单一致。

本次修复位于桌面启动器，必须升级桌面安装包。仅安装独立内核更新不能修复旧桌面的故障恢复与重复实例问题。从内核 v0.2.1 升级仍需注意 v0.2.2 的模型白名单数据库迁移。

## 原因与用户行为变化

| 问题 | 核实结果 | 修复后的行为 |
| --- | --- | --- |
| Docker 打开后仍连接失败 | Docker 引擎运行，但本产品 PostgreSQL、Valkey 容器均停止 | 检查配置中的服务地址；验证已有容器归属和端口后恢复容器，等待就绪再启动内核 |
| 错误原因不清楚 | 启动页主要显示连续退出提示和原始英文连接错误 | 显示 Docker 或具体数据服务的中文原因与恢复步骤，技术日志默认折叠 |
| 点击重启仍失败 | 原按钮仅调用 start；已有子进程时直接返回，启动超时也未清理子进程 | 手动重试执行停止、等待进程退出、取消旧任务、重新检测和启动 |
| 重复进程 | 本机同时存在两个旧桌面主进程；程序没有单实例插件 | 重复打开唤回已有窗口；并发启动只保留一个启动尝试 |
| 残留进程与旧任务干扰 | 旧代日志没有过滤，进程退出未立即使健康检查失效 | 按代次过滤日志、检查和延迟重启；Windows Job Object 清理受管内核 |

`v0.2.31`（内核 v0.2.1）与 `v0.2.32`（内核 v0.2.2）之间，相关桌面启动代码没有变化。使用隔离配置启动 v0.2.2 内核，重现了数据库端口拒绝连接后退出的错误，退出码为 1。

## 实现范围

- `startup_dependencies.rs`：只读取配置中的连接地址，保留 PostgreSQL/Redis 默认端口，识别 Docker 未就绪、服务未就绪和配置问题；仅启动已存在、管理标记为 true 且端口绑定匹配的本产品容器，不重建容器或修改数据卷、密码。
- `desktop_runtime.rs`：启动预留、代次失效、停止等待和更新互斥；后台等待依赖恢复，避免反复创建内核；验证 `/setup/status` 响应格式；依赖不可用时保留待验证内核而非误回滚。
- `managed_child.rs`：Windows 进程监管任务使用 `KILL_ON_JOB_CLOSE`，实际进程句柄用于等待退出，避免仅依赖端口是否关闭。
- `main.rs`、`desktop_shell.rs`：优先注册单实例插件，第二次启动显示、恢复并聚焦已有窗口。
- `DesktopBackendGate.vue`：明确原因和操作，技术详情折叠，同一窗口内重试，忽略重试前的慢请求，事件订阅失败时继续轮询。
- 桌面发布工作流增加 Rust 生命周期、前端全量与 ESLint 检查，覆盖没有独立内核发布的桌面修复。

## 本地验证

| 验证 | 结果 |
| --- | --- |
| v0.2.2 缺失数据库的隔离复现 | 退出码 1，重现 acquire migrations lock connection / connection refused |
| 修复前启动页回归测试 | 2 项失败，缺少明确的依赖状态和恢复按钮 |
| 修复后启动页与更新器测试 | 2 个文件、10 项通过，包含慢请求与事件通道异常 |
| Rust 全量测试 | 60 项通过；涉及真实 Docker 服务的 1 项按设计默认忽略 |
| 本机 Docker 恢复测试 | 显式启用后 1 项通过；两个原有容器恢复，15432/16379 端口就绪 |
| 数据卷保护 | 恢复前后容器挂载信息逐字节一致 |
| Windows 子进程测试 | 未监听端口的进程可被停止并等待退出；监管句柄关闭后进程退出 |
| 隔离桌面端到端测试 | 单实例、内核健康、强制终止桌面后的子进程与端口清理均通过 |
| 桌面前端生产构建 | 通过；保留现有大分块与 Browserslist 过期提示 |

隔离桌面测试使用临时应用标识 `com.sub2api.cost-console.recovery-smoke-20260907` 和新数据目录，未使用用户生产配置或执行生产数据库迁移。首次桌面 PID 27940，内核 PID 28524；第二次启动 PID 21380 自动退出。强制终止首次桌面后，内核与 18765 监听均消失。测试窗口配置为空，不创建可见窗口。

本机已有 PostgreSQL、Valkey 容器的恢复通过正式实现 `ensure_dependencies` 执行；没有重新安装数据库。测试结束时，旧安装的两个桌面进程仍属于用户原有会话，未通过按名称批量终止。

## 发布源码与验证任务

- 发布分支：`codex/desktop-startup-recovery-v0.2.33`。
- 修复源码：`18d29447e6c81b537552361553559ea1d107b5bb`。
- [CI 34132554714](https://github.com/rw0104/sub2api-cost-console/actions/runs/34132554714)：全部通过；后端单元和数据库集成任务 10 分 11 秒，Go 静态检查 4 分钟，前端检查 1 分 44 秒，Shell 检查 12 秒。`main` 已同步到该修复提交。
- [安全扫描 34132554672](https://github.com/rw0104/sub2api-cost-console/actions/runs/34132554672)：成功。
- [桌面签名发布 34132819715](https://github.com/rw0104/sub2api-cost-console/actions/runs/34132819715)：成功，Rust 60 项通过、1 项 Docker 测试按设计忽略（已在本机显式执行通过）；前端 289 个测试文件、2,085 项测试全部通过；ESLint、Web/桌面构建与 NSIS 打包通过。
- 本地日志、复现与验签材料：`.git/desktop-recovery/`。

## 正式安装包与发布核验

- [Release v0.2.33](https://github.com/rw0104/sub2api-cost-console/releases/tag/v0.2.33)：非草稿、非预发布，发布时间 `2026-09-07T14:49:59Z`。
- [Windows x64 安装包](https://github.com/rw0104/sub2api-cost-console/releases/download/v0.2.33/Sub2API.Cost.Console_0.2.33_x64-setup.exe)：30,865,938 字节，文件版本和产品版本均为 `0.2.33`。
- SHA-256：`913e653446267d896ba00e0a97d45cfd5cf08d726424d7caf805f44c1fd28e05`。
- Git 标签 `v0.2.33` 指向 `18d29447e6c81b537552361553559ea1d107b5bb`，与 CI 和安装包工作流源码一致。
- 安装器 `.sig`、更新清单中的签名与客户端内置公钥匹配，Ed25519 文件签名与可信注释签名均验证通过；`INSTALLER_SHA256SUMS.txt` 全部条目匹配。
- `/releases/latest/download/latest.json` 已匿名下载核对，与已验证清单完全相同；两个 Windows 平台条目的 URL 和签名一致。
- 本次仅发布桌面修复，独立 `core-stable` 仍使用先前已经验证的 v0.2.2 归档。
- 核验结果：`frontend/release-assets/online-verify-v0.2.33/verification.json`，首次验签时间 `2026-09-07T14:51:06Z`。

## Evidence → Finding → Path

### E-001：现场和隔离复现

- observed_at：2026-09-07
- source_type：command
- source_ref：进程列表、Docker 容器状态与 `.git/desktop-recovery/repro-v0.2.2/`
- content_hash：n/a
- artifact_path：n/a
- repro_command：`docker ps -a --filter name=sub2api-cost --format '{{.Names}} {{.Status}}'`；使用隔离的 config.yaml 指向未监听的数据库端口，运行正式 v0.2.2 内核。
- raw_excerpt：两个旧桌面进程；Docker 引擎可用但两个数据容器均 exited；隔离内核退出码 1，数据库连接被拒绝。
- linked_workitem：n/a
- supersedes：none

### E-002：生命周期和界面回归

- observed_at：2026-09-07
- source_type：log
- source_ref：`.git/desktop-recovery/rust-full.log`、`frontend-regression.log`
- content_hash：n/a
- artifact_path：n/a
- repro_command：在 `frontend/src-tauri` 执行 `cargo test --locked --features custom-protocol`；在 `frontend` 执行 `corepack pnpm@9 exec vitest run src/features/desktop/__tests__/DesktopBackendGate.spec.ts src/features/desktop/__tests__/DesktopUpdateCenter.spec.ts`。
- raw_excerpt：Rust 60 passed；前端 10 passed。
- linked_workitem：n/a
- supersedes：none

### E-003：真实容器恢复与隔离桌面进程验证

- observed_at：2026-09-07T14:23:38Z
- source_type：log
- source_ref：`.git/desktop-recovery/docker-recovery-smoke.log`、`desktop-smoke-result.json`
- content_hash：n/a
- artifact_path：n/a
- repro_command：实际容器恢复测试需显式设置 `SUB2API_RECOVERY_SMOKE=1`，在 `frontend/src-tauri` 执行 `cargo test --locked --features custom-protocol startup_dependencies::tests::recover_existing_local_managed_containers -- --ignored --exact`；隔离桌面脚本为当前工作区 `.git/desktop-recovery/smoke-desktop.ps1`，需先按脚本使用独立应用标识构建，且 18765 端口空闲。
- raw_excerpt：Docker smoke 1 passed；volume mappings unchanged；singleInstance/health/abruptExitCleanup passed。
- linked_workitem：n/a
- supersedes：none

### E-004：正式发布和下载验签

- observed_at：2026-09-07T14:51:06Z
- source_type：command
- source_ref：[桌面签名发布 34132819715](https://github.com/rw0104/sub2api-cost-console/actions/runs/34132819715)
- content_hash：`913e653446267d896ba00e0a97d45cfd5cf08d726424d7caf805f44c1fd28e05`
- artifact_path：`frontend/release-assets/online-verify-v0.2.33/desktop/Sub2API.Cost.Console_0.2.33_x64-setup.exe`
- repro_command：`gh release view v0.2.33 --repo rw0104/sub2api-cost-console --json tagName,isDraft,isPrerelease,assets`；在保留核验材料的本机工作区执行 `node .git/desktop-recovery/verify-publication.cjs`。
- raw_excerpt：workflow success；Rust 60 passed；前端 2085 passed；Ed25519 and trusted comment verified；public desktop updater matches。
- linked_workitem：n/a
- supersedes：none

### F-001：桌面启动器缺少依赖恢复和完整的进程生命周期保护

- severity：medium
- category：other
- status：validated
- evidence_ids：[E-001, E-002, E-003]
- location：`frontend/src-tauri/src/desktop_runtime.rs`、`main.rs`、`DesktopBackendGate.vue`
- impact：用户无法理解和恢复启动故障，重复打开可能留下多份桌面进程。
- confidence：high
- repro_steps：Docker 引擎未运行或数据容器停止时启动桌面，再恢复 Docker 并点击重试；重复启动桌面；比较修复前后的 E-002/E-003。
- remediation：配置感知的依赖等待与已有容器恢复、真实停止等待、代次保护、单实例与 Windows 进程监管。
- optional_attack：不适用。

### P-001：从依赖未就绪到唯一健康内核

- path_type：callflow
- start：Docker 或配置中的数据库服务不可用。
- goal：明确指引并恢复同一个桌面会话，同时避免重复和残留内核。
- steps：
  1. 复现并识别停止的数据容器与旧桌面多实例；evidence：E-001；finding：F-001。
  2. 预留唯一启动任务，显示原因并等待或恢复依赖；evidence：E-002、E-003；finding：F-001。
  3. 停止旧进程并等待退出，只允许当前代次创建和验证内核；evidence：E-002；finding：F-001。
  4. 验证真实桌面的第二实例退出和异常退出清理；evidence：E-003；finding：F-001。
  5. 在同一提交完成云端验证、签名发布和公开下载核验；evidence：E-004；finding：F-001。
- residual_risks：未对用户生产数据库执行 v0.2.1→v0.2.2 迁移；隔离测试专用数据目录因自动审批拒绝清理而保留，测试进程已退出。

## 工作区与预防

用户原有 `.gitignore` 修改保存了操作前副本，保持未提交。修复没有修改业务计费逻辑或新增数据库迁移。此次缺陷本可由桌面故障恢复测试提前发现，因此将相关检查加入每次签名安装包发布的门禁。
