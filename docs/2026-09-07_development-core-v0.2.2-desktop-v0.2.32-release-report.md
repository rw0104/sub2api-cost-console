# 内核 v0.2.2 与桌面 v0.2.32 发布记录

记录日期：2026-09-07（America/Los_Angeles）。

桌面 `v0.2.32` 与稳定内核 `v0.2.2` 已正式发布。发布源码 CI、Windows 内核验证和签名安装包工作流全部成功；重新下载的产物通过全部 SHA-256 校验，安装器更新签名与可信注释签名通过客户端内置公钥验证。GitHub 最新正式版和两个公开更新入口均已核对。

本版包含数据库列重命名迁移：原有模型列表配置将作为请求准入白名单使用。启用白名单的分组会拒绝名单外模型，升级前应核对分组配置。二进制回滚不会撤销数据库迁移，需要回退旧内核时应配合升级前的数据库备份恢复。

## 源码与版本

| 项目 | 值 |
| --- | --- |
| 桌面版本 | `0.2.32` |
| 兼容内核 | `0.2.2` |
| 官方上游提交 | `5485f368b29d05adb95a00f71801c7c23d8f48af` |
| 发布源码 | `caf6fa12406a6f7526edd33d80f45c698a15d78f` |
| 成本扩展 / 算法 | `1.1.1` / `1.6.0` |
| 必需能力 | `account_cost_loss_ledger.v1`、`account_economics_sampling.v1` |

以已集成的官方 v0.2.1 源码树为基线创建 v0.2.2 增量并合并，合并提交 `61aaacb88` 涉及 260 个文件。解决 README、账号处理器、Grok 依赖注入、Wire 生成代码和前端构建脚本共五处冲突：保留桌面用户指南及成本服务，加入上游运行配置，并在 Web/桌面构建中运行词条完整性检查。

功能变化包括分组模型白名单、简单模式分组管理、推理强度拒绝策略、DeepSeek/GLM/Gemini 价格修复，以及 Codex 路由、WebSocket 会话、Claude 身份与 Astra 推理兼容修复。详细说明见 [桌面发布说明](../frontend/DESKTOP_RELEASE_NOTES.md)。

## 测试与构建

首轮本地前端测试：288 个文件中 287 个通过，2,081 项测试中 2,080 项通过。唯一失败为新增 Codex 清单测试缺失分组页新增的认证状态依赖；同时发现该测试仍使用更名前的模型列表接口与字段。修复测试配置后，Codex 清单与分组页面的 2 个文件、9 项测试全部通过，连续编辑断言保持不变。ESLint 与中英文词条完整性 3 项检查通过。

本地 Web 生产构建通过，耗时约 1 分 41 秒（Vite 阶段）；现有大分块与 Browserslist 数据过期提示不阻断构建。修改后的分组测试文件也单独通过 ESLint。

云端验证使用发布提交 `caf6fa12406a6f7526edd33d80f45c698a15d78f`：

- [CI 34126279617](https://github.com/rw0104/sub2api-cost-console/actions/runs/34126279617)：全部成功，后端单元与数据库集成测试共 8 分 53 秒，Go 静态检查 4 分钟，Shell 21 秒，前端检查 1 分 53 秒。
- [安全扫描 34126279630](https://github.com/rw0104/sub2api-cost-console/actions/runs/34126279630)：成功。
- [稳定内核工作流 34126321482](https://github.com/rw0104/sub2api-cost-console/actions/runs/34126321482)：全部成功。Windows Go 默认测试、Web 和桌面构建、Rust 49 项测试、前端 288 个文件/2,081 项测试、ESLint 和内核身份验证均通过。

本地日志与验签脚本保存在 `.git/release-v0.2.32/`，不随 Git 分发。本机的生产数据库与当前桌面安装未执行原位升级。

## 稳定内核发布与下载核验

- `core-stable`：schema `2`，版本 `0.2.2`，扩展 `1.1.1`，算法 `1.6.0`，上游提交与发布源码配置一致。
- 归档：`sub2api-core_0.2.2_1.1.1_windows_x86_64.zip`，36,160,093 字节。
- SHA-256：`6b3bdc06cfed57bfd2298970e6c848e81bd8c3719e659bede8f7565d43dd654a`。
- 从 GitHub 重新下载后，全部 `CORE_SHA256SUMS.txt` 条目与清单哈希通过核验。
- 归档只包含 `sub2api.exe`；解压后执行 `--version`，确认 `0.2.2`、完整上游提交、成本扩展和两项必需能力。
- 核验目录：`frontend/release-assets/online-verify-v0.2.32/core/`。

## 桌面安装包与自动更新

- [桌面 Release v0.2.32](https://github.com/rw0104/sub2api-cost-console/releases/tag/v0.2.32)：正式版，非草稿、非预发布，已成为 GitHub latest。
- [Windows x64 安装包](https://github.com/rw0104/sub2api-cost-console/releases/download/v0.2.32/Sub2API.Cost.Console_0.2.32_x64-setup.exe)：30,766,003 字节，文件版本与产品版本均为 `0.2.32`。
- 安装器 SHA-256：`6b9c5ddd87b39657177a6b01cad8577e8ec5bb458d857b2d7471b5d4f04e5ff3`。
- [签名安装包工作流 34127504712](https://github.com/rw0104/sub2api-cost-console/actions/runs/34127504712)：成功，耗时 12 分 46 秒，源码为本报告中的发布提交。
- 安装器 `.sig` 与 `latest.json` 中签名一致。使用 `frontend/src-tauri/tauri.conf.json` 的 updater 公钥，按 Minisign 的 BLAKE2b-512/Ed25519 格式验证文件签名、key ID 和可信注释签名。
- `INSTALLER_SHA256SUMS.txt` 全部条目重新计算通过；GitHub 资产 digest 与实际下载文件 SHA-256 一致。
- `windows-x86_64` 与 `windows-x86_64-nsis` 更新条目使用一致的 URL 与签名。
- 公开桌面入口 `/releases/latest/download/latest.json` 和稳定内核入口 `/releases/download/core-stable/core-latest.json` 均通过匿名下载核对，与已核验的清单完全一致。
- 本地核验结果：`frontend/release-assets/online-verify-v0.2.32/verification.json`；初次安装器验签时间：`2026-09-07T13:40:49Z`。
- 发布后再次查询官方 latest，仍为本次集成的 `v0.2.2`。

## Evidence → Finding → Path

### E-001：上游身份与源码合并

- observed_at：2026-09-07
- source_type：command
- source_ref：官方 `v0.2.2` 标签与本地合并提交 `61aaacb88`
- content_hash：n/a
- artifact_path：n/a
- repro_command：`git show caf6fa124:frontend/UPSTREAM_SUB2API_COMMIT`；`git show --stat 61aaacb88`
- raw_excerpt：上游提交 `5485f368b29d05adb95a00f71801c7c23d8f48af`；260 files changed。
- linked_workitem：n/a
- supersedes：none

### E-002：分组测试依赖修复

- observed_at：2026-09-07
- source_type：log
- source_ref：`.git/release-v0.2.32/frontend-tests.log` 与 `groups-regression.log`
- content_hash：n/a
- artifact_path：n/a
- repro_command：在 `frontend` 执行 `corepack pnpm@9 exec vitest run src/views/admin/__tests__/GroupsView.codexManifest.spec.ts src/views/admin/__tests__/GroupsView.columnSettings.spec.ts`
- raw_excerpt：修复前 `getActivePinia()` 无活跃实例；修复后 `2 passed`、`9 passed`。
- linked_workitem：n/a
- supersedes：none

### E-003：发布源码的 CI 验证

- observed_at：2026-09-07
- source_type：command
- source_ref：[CI 34126279617](https://github.com/rw0104/sub2api-cost-console/actions/runs/34126279617)
- content_hash：n/a
- artifact_path：n/a
- repro_command：`gh run view 34126279617 --repo rw0104/sub2api-cost-console --json headSha,status,conclusion,jobs`
- raw_excerpt：`headSha=caf6fa12406a6f7526edd33d80f45c698a15d78f`，`conclusion=success`；Go 单元、集成、静态检查、Shell 和前端任务全部通过。
- linked_workitem：n/a
- supersedes：none

### E-004：稳定内核的测试、发布和身份核验

- observed_at：2026-09-07
- source_type：command
- source_ref：[稳定内核工作流 34126321482](https://github.com/rw0104/sub2api-cost-console/actions/runs/34126321482)
- content_hash：`6b3bdc06cfed57bfd2298970e6c848e81bd8c3719e659bede8f7565d43dd654a`
- artifact_path：`frontend/release-assets/online-verify-v0.2.32/core/sub2api-core_0.2.2_1.1.1_windows_x86_64.zip`
- repro_command：`gh run view 34126321482 --repo rw0104/sub2api-cost-console --json headSha,conclusion,jobs`；`gh release download core-stable --repo rw0104/sub2api-cost-console --pattern core-latest.json --output -`
- raw_excerpt：`success`；Rust `49 passed`；前端 `288 passed` / `2081 passed`；解压后 `--version` 中版本、提交、扩展与能力匹配。
- linked_workitem：n/a
- supersedes：none

### E-005：安装器验签与公开更新入口

- observed_at：2026-09-07T13:40:49Z
- source_type：command
- source_ref：[桌面工作流 34127504712](https://github.com/rw0104/sub2api-cost-console/actions/runs/34127504712)
- content_hash：`6b9c5ddd87b39657177a6b01cad8577e8ec5bb458d857b2d7471b5d4f04e5ff3`
- artifact_path：`frontend/release-assets/online-verify-v0.2.32/desktop/Sub2API.Cost.Console_0.2.32_x64-setup.exe`
- repro_command：`gh release view v0.2.32 --repo rw0104/sub2api-cost-console --json tagName,isDraft,isPrerelease,assets`；当前工作区执行 `node .git/release-v0.2.32/verify-publication.cjs`（脚本与下载目录为本地核验材料，不随 Git 分发）。
- raw_excerpt：`Ed25519 and trusted comment verified against configured updater public key`；全部校验项匹配；两个公开更新入口与核验清单一致。
- linked_workitem：n/a
- supersedes：none

### F-001：新增测试配置未跟随分组页依赖变化

- severity：low
- category：other
- status：validated
- evidence_ids：[E-002]
- location：`frontend/src/views/admin/__tests__/GroupsView.codexManifest.spec.ts`
- impact：全量前端测试被测试环境初始化错误阻断，不能验证连续编辑行为。
- confidence：high
- repro_steps：运行 E-002 中的命令，比较 `e01613f8b` 与 `caf6fa124`。
- remediation：补充认证状态模拟，采用新模型白名单接口和字段名称，保持业务断言。
- optional_attack：不适用。

### P-001：官方内核到兼容发布源码

- path_type：callflow
- start：官方 Sub2API v0.2.2
- goal：将官方能力与成本扩展集成到可验证的桌面发布源码。
- steps：
  1. 校验基线源码树并合并官方增量，保留本地依赖注入；evidence：E-001；finding：none。
  2. 固定版本及完整上游提交，解决测试配置不兼容并运行回归；evidence：E-002；finding：F-001。
  3. 完成同一源码提交的 CI，将 `main` 和 `v0.2.32` 标签同步到该提交；evidence：E-003；finding：none。
  4. 运行 Windows、Rust 和全量前端门禁，发布稳定内核并验证下载身份；evidence：E-004；finding：none。
  5. 发布签名安装器，核验公开文件和客户端实际更新入口；evidence：E-005；finding：none。
- residual_risks：未对用户生产数据库执行迁移或回滚；数据库兼容性结论应结合 CI 集成测试与升级前备份。

## 工作区保护

操作前将用户 `.gitignore` 保存至 `.git/release-v0.2.32/user-gitignore.backup`，发布后按字节核对一致。发布提交仅包含本次更新，用户原有 `.gitignore` 修改保持未提交。发布标签保留在已验证的源码提交上，报告以独立文档提交同步至 `main` 和本次发布分支。
