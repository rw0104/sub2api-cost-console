# 桌面 v0.2.34 登录失效恢复修复

日期：2026-09-07（America/Los_Angeles）。桌面版本 `0.2.34`，兼容内核 `0.2.2`，成本扩展 `1.1.1`，成本算法 `1.6.0`。

桌面 `v0.2.34` 已正式发布，并成为 GitHub latest。最终源码 CI、签名构建、前端 2,102 项和 Rust 60 项测试通过。正式安装器的全部 SHA-256 校验项、Ed25519 更新签名与可信注释签名核验通过，公开更新入口与下载清单一致。

本次修复登录凭据失效后无法正常进入登录页、仪表盘空白和重复请求的问题。失效凭据仍需要用户重新登录；更新不改变后端鉴权规则，也不新增数据库迁移。

## 根因与修复

本机旧客户端使用桌面 v0.2.33、内核 v0.2.2，Docker、PostgreSQL 与 Valkey 服务均正常，桌面与内核各一个进程。业务接口返回 401；公告与管理确认接口在观察窗口中各重复请求 152 次。

HTTP 拦截器只删除 localStorage 的凭据，Pinia 中仍保留 token 和用户信息。桌面使用 Hash 路由，跳转登录页不会重载整个文档；路由守卫因此继续按“已登录管理员”跳回仪表盘。此外，守卫等待管理确认接口期间登录状态可能改变，等待结束后原代码仍会放行旧页面。

| 修复范围 | 结果 |
| --- | --- |
| 内存与存储同步 | 会话失效通知同步清除 Pinia 和存储状态，停止用户信息及令牌刷新计时器，然后才导航 |
| 重复跳转保护 | 并发 401 只触发一次登录导航和失效提示；正确识别桌面 Hash 路由及查询参数 |
| 新会话边界 | 新登录或会话恢复重置导航保护，避免新会话再次失效时无法跳转 |
| 刷新失败 | 已重试的请求再次返回 401 时结束会话；自动刷新收到 401 同样处理 |
| 临时网络故障 | 网络中断、408、429 和 5xx 等临时刷新失败保留凭据 |
| 异步请求隔离 | 旧用户请求不清除或重放到新用户会话；迟到的用户信息不恢复已失效状态 |
| 路由等待后的复查 | 异步管理确认或设置检查后，重新校验登录和管理员身份 |

实现使用轻量的同步通知连接 HTTP 层和 Pinia，避免 API 客户端直接导入 store 形成循环依赖。订阅随 Pinia scope 释放；保留 v0.2.33 的启动恢复、单实例和受管进程清理。

## 验证

- 修复前第一组 7 个回归均失败，覆盖缓存与内存不同步、并发跳转、重试后的 401、跨用户迟到请求和用户信息回写。
- 额外复现异步路由边界：管理确认等待期间清除登录状态，旧守卫仍进入 `/admin/dashboard`；修复后停留在 `/login`。
- 最终新增 17 项回归，结合原有鉴权与路由用例，5 个文件、99 项测试全部通过。
- 本地首轮前端全量：290 个文件、2,100 项通过；随后新增两个会话/异步导航边界用例通过定向回归。最终签名工作流在发布提交完成全量验证：290 个文件、2,102 项全部通过。
- ESLint 和最终代码 `vue-tsc --noEmit` 通过。桌面前端生产构建通过；现有大分块、Browserslist 数据过期提示保留。
- 回归使用实际 HTTP 拦截器、Pinia 和生产路由守卫。仅替换页面渲染组件及网络响应，避免复制守卫逻辑形成无法验证真实行为的测试。

本次未读取用户真实令牌，也未修改本机账户或密码。测试使用独立的浏览器测试环境和合成凭据。

## 源码与发布流程

- 发布源码：`9e933e8574d76bb404d8f574b3d2044a1a86c5cb`。
- 分支：`codex/auth-session-recovery-v0.2.34`，已同步到 `main`。
- [最终提交 CI 34140028697](https://github.com/rw0104/sub2api-cost-console/actions/runs/34140028697)：全部成功，源码为 `9e933e8574d76bb404d8f574b3d2044a1a86c5cb`。后端单元与集成测试任务 9 分 55 秒，静态检查 3 分 10 秒，前端检查 1 分 47 秒，Shell 检查 12 秒。
- [安全扫描 34140028715](https://github.com/rw0104/sub2api-cost-console/actions/runs/34140028715)：成功。
- [签名安装包工作流 34140217260](https://github.com/rw0104/sub2api-cost-console/actions/runs/34140217260)：成功，耗时 26 分 23 秒。Rust 60 项通过，显式本机 Docker 恢复测试按设计忽略；前端 2,102 项、ESLint、Web/桌面生产构建与 NSIS 打包全部通过。
- 本地证据目录：`.git/auth-session-recovery/`；原始定位材料保存在 `.git/local-client-auth-check/`。
- 用户原有 `.gitignore` 修改已保留操作前副本并保持未提交。

## 安装包与公开更新核验

- [Release v0.2.34](https://github.com/rw0104/sub2api-cost-console/releases/tag/v0.2.34)：正式版，非草稿、非预发布，发布时间 `2026-09-07T16:14:23Z`。
- [Windows x64 安装包](https://github.com/rw0104/sub2api-cost-console/releases/download/v0.2.34/Sub2API.Cost.Console_0.2.34_x64-setup.exe)：30,872,950 字节，文件版本与产品版本均为 `0.2.34`。
- SHA-256：`1c6ad8a9d8aeb3661d7ac8748043afbaddda04caadf5e2f95462485aa510a74b`。
- 安装器 `.sig` 与 `latest.json` 中签名一致；按客户端配置公钥验证 Ed25519 文件签名、签名 key ID 和可信注释签名，通过全部检查。
- `INSTALLER_SHA256SUMS.txt` 全部条目重新计算一致，GitHub 资产 digest 匹配。
- `windows-x86_64` 与 `windows-x86_64-nsis` 使用一致的下载 URL 与签名；`/releases/latest/download/latest.json` 已匿名下载核对，与已验签清单完全一致。
- Git 标签 `v0.2.34` 指向 `9e933e8574d76bb404d8f574b3d2044a1a86c5cb`，与最终 CI 和安装包任务源码相同。
- 本地结果：`frontend/release-assets/online-verify-v0.2.34/verification.json`，首次验签时间 `2026-09-07T16:15:30Z`。独立内核通道继续使用先前已验证的 v0.2.2 归档。

## Evidence → Finding → Path

### E-001：401 与内存状态未清除

- observed_at：2026-09-07
- source_type：log
- source_ref：旧客户端日志与 `.git/local-client-auth-check/reproduction.log`
- content_hash：n/a
- artifact_path：n/a
- repro_command：在隔离测试环境给实际 Pinia 设置合成的过期登录状态，通过实际 HTTP 拦截器处理 401，检查存储、内存和目标路由。
- raw_excerpt：`storedTokenCleared=true`，`inMemoryAuthenticated=true`，`destination=#/login`，`routeGuardWouldReturnToDashboard=true`。
- linked_workitem：n/a
- supersedes：none

### E-002：异步路由失效边界

- observed_at：2026-09-07
- source_type：log
- source_ref：`.git/auth-session-recovery/guard-race-baseline.log`
- content_hash：n/a
- artifact_path：n/a
- repro_command：在 `frontend` 执行 `corepack pnpm@9 exec vitest run src/api/__tests__/authSessionRecovery.spec.ts -t 'rechecks authentication after'`，比较路由修复前后提交。
- raw_excerpt：修复前 `expected '/admin/dashboard' to be '/login'`；修复后通过。
- linked_workitem：n/a
- supersedes：none

### E-003：最终定向回归

- observed_at：2026-09-07
- source_type：command
- source_ref：`.git/auth-session-recovery/targeted-tests.log`
- content_hash：n/a
- artifact_path：n/a
- repro_command：在 `frontend` 执行 `corepack pnpm@9 exec vitest run src/api/__tests__/authSessionRecovery.spec.ts src/api/__tests__/client.spec.ts src/api/__tests__/tokenRefresh.spec.ts src/stores/__tests__/auth.spec.ts src/router/__tests__/guards.spec.ts`。
- raw_excerpt：`5 passed`，`99 passed`；生产路由守卫停留登录页，20 个并发 401 只触发一次跳转。
- linked_workitem：n/a
- supersedes：none

### E-004：签名构建与公开安装包验证

- observed_at：2026-09-07T16:15:30Z
- source_type：command
- source_ref：[签名安装包工作流 34140217260](https://github.com/rw0104/sub2api-cost-console/actions/runs/34140217260)
- content_hash：`1c6ad8a9d8aeb3661d7ac8748043afbaddda04caadf5e2f95462485aa510a74b`
- artifact_path：`frontend/release-assets/online-verify-v0.2.34/desktop/Sub2API.Cost.Console_0.2.34_x64-setup.exe`
- repro_command：`gh release view v0.2.34 --repo rw0104/sub2api-cost-console --json tagName,isDraft,isPrerelease,assets`；保留本地核验材料的工作区可执行 `node .git/auth-session-recovery/verify-publication.cjs`。
- raw_excerpt：workflow success；frontend 2102 passed；Rust 60 passed；Ed25519 and trusted comment verified；public updater matches。
- linked_workitem：n/a
- supersedes：none

### F-001：登录失效未同步到路由判断

- severity：medium
- category：other
- status：validated
- evidence_ids：[E-001, E-002, E-003]
- location：`frontend/src/api/client.ts`、`frontend/src/stores/auth.ts`、`frontend/src/router/index.ts`
- impact：用户看到缓存身份与空白仪表盘，业务请求重复失败，无法正常进入登录页恢复。
- confidence：high
- repro_steps：使已有会话的业务请求返回 401，检查真实内存状态与 Hash 导航；在异步守卫等待期间使会话失效。
- remediation：同步失效通知、导航去重、请求与用户信息代次保护、异步路由检查后的鉴权复查。
- optional_attack：不适用。

### P-001：从失效会话返回可用登录页

- path_type：callflow
- start：已有会话的业务接口返回 401。
- goal：安全结束失效会话，停止循环请求并进入登录页。
- steps：
  1. 确认 Docker 与内核正常，复现客户端状态不同步；evidence：E-001；finding：F-001。
  2. 同步清除状态与计时器，仅发起一次登录导航；evidence：E-003；finding：F-001。
  3. 阻止旧响应回写及旧守卫放行，保护新登录会话；evidence：E-002、E-003；finding：F-001。
  4. 完成同一源码提交的全量门禁，构建签名安装包并核验公开更新入口；evidence：E-004；finding：F-001。
- residual_risks：服务器拒绝的凭据仍需用户重新登录；测试不绕过真实账号鉴权。
