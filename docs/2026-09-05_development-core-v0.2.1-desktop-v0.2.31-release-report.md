# 内核 v0.2.1 与桌面 v0.2.31 发布记录

记录日期：2026-09-05（America/Los_Angeles）。

桌面 v0.2.31 和独立稳定内核 v0.2.1 均已发布。正式下载文件的全部 SHA-256 校验项通过，安装包的 Ed25519 更新签名及可信注释签名已通过客户端配置中的公钥验证。

## 版本与源码

| 项目 | 发布值 |
| --- | --- |
| 桌面版本 | `v0.2.31` |
| 兼容内核 | `v0.2.1` |
| 上游提交 | `578785ee7fb35030b094b69624efe25670a36f5f` |
| 发布源码 / Git 标签指向 | `f47542b5cb6c46bef98b44a507aba70250119f9f` |
| 成本扩展 | `1.1.1` |
| 成本算法 | `1.6.0` |
| 能力 | `account_cost_loss_ledger.v1`、`account_economics_sampling.v1` |

从已集成的官方 v0.2.0 源码树生成 v0.2.1 增量并合并，296 个文件发生变化，没有代码冲突。合并保留本地成本控制台扩展，并同步此前主分支缺失的已发布源码和同步修复。`main` 与 `v0.2.31` 标签通过一次原子推送指向相同发布源码。

本次包含 GPT-6 Astra、Codex Ultrafast、固定账号模型清单、上游请求标识和续聊兼容修复。数据库迁移增加上游请求标识及索引、最高推理强度计费倍率、分组 Codex 模型清单配置；内核二进制回滚不会撤销数据库迁移。

## 自动更新修复

旧自动任务从仍停留在 v0.1.183 的 `main` 运行，无法找到匹配的已集成 synthetic upstream 基线，因而停止。此前桌面 v0.2.28—v0.2.30 的源码和同步修复只在发布分支中。本次合并主分支文档历史后，将经过 CI 验证的完整发布历史快进推送到 `main`；当前同步任务可直接识别已集成的 v0.2.1。

## 测试记录

| 检查 | 结果 | 证据 |
| --- | --- | --- |
| 前端全量测试 | 280 个文件、2,042 项测试全部通过 | 本地 `frontend-tests.log` |
| ESLint | 通过 | 本地 `frontend-lint.log` |
| Web 生产构建 | 通过 | 本地 `web-build.log` |
| 桌面前端生产构建 | 通过 | 本地 `desktop-web-build.log` |
| Go 单元测试 | 通过 | CI 33969989692，`Unit tests` |
| Go 数据库集成测试 | 通过 | CI 33969989692，`Integration tests` |
| Go 静态检查、Shell 与前端 CI | 通过 | CI 33969989692 |
| Windows Go 默认测试 | 通过 | 内核发布任务 33970099539 |
| Rust 身份、激活、回滚与启动器测试 | 49 项全部通过 | 内核发布任务 33970099539 |
| 云端前端全量测试与 ESLint | 通过，280 个测试文件 | 内核发布任务 33970099539 |

本地日志存于 `.git/release-v0.2.31/`。本机为 8GB 内存；重复的本地 Go 单元测试因内存压力主动停止，未计为通过。后端结论以同一发布提交上的云端 CI 为准。Web 构建保留既有的大分块和 Browserslist 数据过期提示，没有阻断构建。

## 稳定内核发布与下载核验

- [稳定内核发布任务 33970099539](https://github.com/rw0104/sub2api-cost-console/actions/runs/33970099539)：成功。
- [core-stable 通道](https://github.com/rw0104/sub2api-cost-console/releases/tag/core-stable)：`0.2.1`，schema `2`，成本扩展 `1.1.1`，算法 `1.6.0`。
- 归档：`sub2api-core_0.2.1_1.1.1_windows_x86_64.zip`。
- SHA-256：`e646d88d4706cd28b8260b11a933f5498782b6b7e404ed9109e8073dda16f8e0`。
- 从 GitHub 重新下载后计算的归档 SHA-256 与清单一致。解压后的二进制执行 `--version`，确认 `0.2.1`、完整上游提交、扩展版本和两项必需能力均匹配。
- 下载核验目录：`frontend/release-assets/online-verify-v0.2.31/core/`。

## Evidence → Finding → Path

### E-001：官方内核身份与源码合并

- observed_at：2026-09-05
- source_type：command
- source_ref：上游 Git 标签 `v0.2.1` 与发布标签 `v0.2.31`
- content_hash：n/a
- artifact_path：n/a
- repro_command：`git show v0.2.31:frontend/UPSTREAM_SUB2API_COMMIT`；`git rev-parse 'v0.2.31^{commit}'`
- raw_excerpt：上游 `578785ee7fb35030b094b69624efe25670a36f5f`；发布源码 `f47542b5cb6c46bef98b44a507aba70250119f9f`。
- linked_workitem：n/a
- supersedes：none

### E-002：自动更新失败原因

- observed_at：2026-09-05
- source_type：log
- source_ref：[旧自动更新任务 33961236255](https://github.com/rw0104/sub2api-cost-console/actions/runs/33961236255)
- content_hash：n/a
- artifact_path：n/a
- repro_command：`gh run view 33961236255 --repo rw0104/sub2api-cost-console --log-failed`
- raw_excerpt：`Unable to find the integrated synthetic upstream v0.1.183 base`
- linked_workitem：n/a
- supersedes：none

### E-003：发布提交 CI 全部通过

- observed_at：2026-09-05
- source_type：command
- source_ref：[CI 33969989692](https://github.com/rw0104/sub2api-cost-console/actions/runs/33969989692)
- content_hash：n/a
- artifact_path：n/a
- repro_command：`gh run view 33969989692 --repo rw0104/sub2api-cost-console --json headSha,conclusion,jobs`
- raw_excerpt：`headSha=f47542b5cb6c46bef98b44a507aba70250119f9f`，`conclusion=success`；单元与集成测试均成功。
- linked_workitem：n/a
- supersedes：none

### E-004：稳定内核验证和发布成功

- observed_at：2026-09-05
- source_type：command
- source_ref：[内核发布任务 33970099539](https://github.com/rw0104/sub2api-cost-console/actions/runs/33970099539)
- content_hash：`e646d88d4706cd28b8260b11a933f5498782b6b7e404ed9109e8073dda16f8e0`
- artifact_path：`frontend/release-assets/online-verify-v0.2.31/core/sub2api-core_0.2.1_1.1.1_windows_x86_64.zip`
- repro_command：`gh run view 33970099539 --repo rw0104/sub2api-cost-console --json conclusion,jobs`；`gh release download core-stable --repo rw0104/sub2api-cost-console --pattern core-latest.json --output -`
- raw_excerpt：工作流 `success`；Rust `49 passed; 0 failed`；重新下载的内核 `--version` 输出正确版本、提交与成本扩展能力。
- linked_workitem：n/a
- supersedes：none

### E-005：桌面正式发布与密码学验签

- observed_at：2026-09-05T14:18:12Z
- source_type：command
- source_ref：[桌面发布任务 33970551420](https://github.com/rw0104/sub2api-cost-console/actions/runs/33970551420)
- content_hash：`16e563f09a791192b7a6db198d148a0eaef054423f687492553580e18a306786`
- artifact_path：`frontend/release-assets/online-verify-v0.2.31/desktop/Sub2API.Cost.Console_0.2.31_x64-setup.exe`
- repro_command：`gh release view v0.2.31 --repo rw0104/sub2api-cost-console --json tagName,isDraft,isPrerelease,assets`；`gh release download v0.2.31 --repo rw0104/sub2api-cost-console --dir release-check`
- raw_excerpt：任务 `success`；安装包和清单所有校验项匹配；Ed25519 签名、签名 key ID 和可信注释验证通过；更新清单两个 Windows 平台条目一致。
- linked_workitem：n/a
- supersedes：none

### F-001：发布分支与主分支基线不一致导致自动更新失败

- severity：medium
- category：other
- status：validated
- evidence_ids：[E-001, E-002, E-003, E-004]
- location：`main` 与 `.github/workflows/core-sync.yml`
- impact：自动任务无法从实际已发布内核计算后续增量。
- confidence：high
- repro_steps：查看 E-002 失败记录，并比较旧 `main` 与 E-001 发布版本元数据。
- remediation：保留主分支已有文档历史，将通过 CI 的发布源码及同步修复快进到 `main`。
- optional_attack：不适用。

### P-001：上游版本到已验证发布源码

- path_type：callflow
- start：官方 Sub2API v0.2.1
- goal：桌面与稳定内核使用一致且可核对的源码身份。
- steps：
  1. 合并官方增量并固定版本与提交；evidence：E-001；finding：none。
  2. 对齐主分支历史与发布基线；evidence：E-002；finding：F-001。
  3. 在发布提交运行单元、集成与静态检查；evidence：E-003；finding：F-001。
  4. 完成 Windows 默认测试、Rust 与前端测试，发布并下载核验稳定内核；evidence：E-004；finding：F-001。
  5. 构建并发布桌面安装器，验证更新清单、SHA-256 与更新签名；evidence：E-005；finding：none。
- residual_risks：本次未对用户正在使用的数据库或桌面安装执行原位升级，实际安装与迁移冒烟不属于已执行的验证。

## 工作区保留

原有 `.gitignore` 和 2026-08-29 发布记录已与操作前副本逐字节核对。前者的用户改动保持未提交；后者与主分支中的文档完全相同，随主分支历史合并成为受 Git 跟踪的文件。

## 桌面发布与安装包

- [正式 Release v0.2.31](https://github.com/rw0104/sub2api-cost-console/releases/tag/v0.2.31)。
- [Windows x64 安装包](https://github.com/rw0104/sub2api-cost-console/releases/download/v0.2.31/Sub2API.Cost.Console_0.2.31_x64-setup.exe)。
- 安装包 SHA-256：`16e563f09a791192b7a6db198d148a0eaef054423f687492553580e18a306786`。
- 客户端公开自动更新入口 `/releases/latest/download/latest.json` 已直接访问核对，与完成验签的清单内容一致。
- `latest.json`：版本 `0.2.31`；`windows-x86_64` 与 `windows-x86_64-nsis` 使用一致的下载 URL 和签名。
- 安装包 `.sig` 与更新清单中的签名一致，按 Minisign 的 BLAKE2b-512 / Ed25519 格式验证文件与可信注释，公钥来自 `frontend/src-tauri/tauri.conf.json`。
- 所有 `INSTALLER_SHA256SUMS.txt` 和 `CORE_SHA256SUMS.txt` 条目均通过重新计算校验。
- 本地核验结果：`frontend/release-assets/online-verify-v0.2.31/verification.json`。验签脚本保留在本次工作区 `.git/release-v0.2.31/verify-publication.cjs`，该脚本不随 Git 提交分发。
