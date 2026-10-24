# Sub2API v0.1.183 内核与桌面 v0.2.27 发布记录

> 结论：Git 更新、主分支合并、桌面 Release 和 `core-stable` 发布均已完成。
>
> 记录时间：2026-08-29（America/Los_Angeles）。文中的 GitHub 时间均为 UTC。

## 发布结果

本次发布将兼容内核从 `0.1.182` 升级到 `0.1.183`，将 Windows 桌面端从 `0.2.26` 升级到 `0.2.27`。发布过程修复了自动内核同步时 `forceNextAccount` 未定义的合并问题，并保留成本扩展、经济采样、内核身份校验和回滚能力。

| 项目 | 最终值 | 状态 |
| --- | --- | --- |
| 发布合并提交 | `b89af607d8bceb37f70e1042a8bfa5673b65af2c` | 已进入 `main` |
| 合并请求 | [PR #25](https://github.com/rw0104/sub2api-cost-console/pull/25) | 已合并 |
| 上游内核 | `v0.1.183` | 已集成 |
| 上游提交 | `e8cb019fabf8b55199436229044cbf9aa7a82564` | 已固定 |
| 成本扩展 | `1.1.1` | 已保留 |
| 成本算法 | `1.6.0` | 已保留 |
| 桌面版本 | `v0.2.27` | 已发布 |
| 桌面 Release | [v0.2.27](https://github.com/rw0104/sub2api-cost-console/releases/tag/v0.2.27) | 正式版 |
| 兼容内核通道 | [core-stable](https://github.com/rw0104/sub2api-cost-console/releases/tag/core-stable) | 已更新 |

## 核查 Git 更新

### 核查主分支

PR #25 的发布合并提交已经进入远端 `main`。后续文档提交可以让 `main` 继续前进，但发布合并提交必须仍是其祖先：

```powershell
gh pr view 25 --repo rw0104/sub2api-cost-console `
  --json state,mergedAt,mergeCommit,url
gh api "repos/rw0104/sub2api-cost-console/compare/b89af607d8bceb37f70e1042a8bfa5673b65af2c...main" `
  --jq '.status'
```

预期关键结果：

```text
release merge: b89af607d8bceb37f70e1042a8bfa5673b65af2c
PR #25: MERGED
merged_at: 2026-08-29T08:25:26Z
compare status: identical 或 ahead
```

远端主分支中的版本文件已核对：

```text
backend/cmd/server/VERSION          0.1.183
frontend/CORE_VERSION              0.1.183
frontend/UPSTREAM_SUB2API_COMMIT   e8cb019fabf8b55199436229044cbf9aa7a82564
frontend/src-tauri/tauri.conf.json 0.2.27
```

### 核查代码树一致性

本机直接推送 Git 时网络连接多次被重置，因此发布分支通过 GitHub Git Data API 创建。创建后比较了远端发布提交与本地已验证提交的 tree SHA：

```text
local tree:  b096db5802dd11540a5e99df86e8326aaf377347
remote tree: b096db5802dd11540a5e99df86e8326aaf377347
```

两者一致，说明远端发布分支内容与本地验证内容完全相同。随后 PR #25 经 GitHub CI 验证并合并到 `main`。

## 核查发布产物

### 桌面 Release

桌面发布 workflow [33242717852](https://github.com/rw0104/sub2api-cost-console/actions/runs/33242717852) 于 2026-08-29T08:28:02Z 前后完成资产上传，Release 不是草稿，也不是预发布版本。

| 资产 | 大小 | SHA-256 |
| --- | ---: | --- |
| `Sub2API.Cost.Console_0.2.27_x64-setup.exe` | 30,550,264 bytes | `de7c09d769b12a3c587eff7c5c1785849c1e3d002f2d14aca5c60b01d2395436` |
| `Sub2API.Cost.Console_0.2.27_x64-setup.exe.sig` | 436 bytes | `6686e3ee68600188fa56ebef6f73c92dc9dc66ebf39fc97e996b9faeefb1dc7e` |
| `latest.json` | 6,751 bytes | `e0006069b5149f86dd50fd15dc170a016897738449b6761bae42268f17e55b0a` |
| `DESKTOP_RELEASE_NOTES.md` | 5,260 bytes | `ea7f9500c3c77056e37db66ba2f80b55d4fd4f98f59954988e6cca9c6c55a053` |

桌面 updater 清单声明：

```text
version: 0.2.27
platform: windows-x86_64
installer: Sub2API.Cost.Console_0.2.27_x64-setup.exe
```

### 兼容内核通道

兼容内核 workflow [33243122152](https://github.com/rw0104/sub2api-cost-console/actions/runs/33243122152) 于 2026-08-29T08:42:30Z 完成，所有发布步骤通过，包括：

- 上游版本发现和重复合并检查。
- 后端完整合同测试。
- Web 资产和托管内核构建。
- 桌面激活、身份与回滚合同测试。
- 前端与 updater 合同测试。
- 内核身份认证、发布前稳定通道复查和 `core-stable` 上传。

`core-latest.json` 的最终身份：

```json
{
  "channel": "stable",
  "version": "0.1.183",
  "algorithm_version": "1.6.0",
  "extension_version": "1.1.1",
  "upstream_commit": "e8cb019fabf8b55199436229044cbf9aa7a82564"
}
```

稳定内核资产：

```text
name:   sub2api-core_0.1.183_1.1.1_windows_x86_64.zip
size:   35,865,877 bytes
sha256: 74d05bc3833a9405f68e7869efa6ef12ca835caed4306cf0bb3cc95d6983fe35
```

## 主要代码变更

上游 `v0.1.183` 的集成重点包括：

- OpenAI OAuth 5 小时和 7 天配额耗尽识别。
- `session-id` 请求头参与粘性会话。
- 模型容量溢出时临时切换账号，不迁移持久绑定。
- Kimi 并发限制 403 使用临时冷却并保留故障转移。
- OpenAI custom tool 和 tool search 恢复正确的项目 ID 前缀。
- Antigravity 兼容模式最大 token 限制为 64,000。
- 邮箱换绑别名占用检测与事务级并发保护。
- 渠道监控 V2 Composite 聚合条件修复。

本地兼容层在 `backend/internal/service/openai_gateway_upstream_errors.go` 中继续保留模型容量和 K12 账号切换策略。合并修复让 `newOpenAIAccountFailoverErrorWithClassificationHeaders` 接收并向下传递 `forceNextAccount`，避免变量落入错误作用域。

## 验证记录

### 本地验证

```powershell
Set-Location backend
$env:GOTOOLCHAIN = 'go1.27.0'
$env:GOMAXPROCS = '2'
go test -p 1 ./...

Set-Location ../frontend
corepack pnpm@9 lint:check
corepack pnpm@9 build
node scripts/prepare-desktop-release.mjs --validate-notes-only
corepack pnpm@9 exec vitest run src/views/admin/__tests__/AccountsView.selectAllResults.spec.ts
```

结果：

- Go 全量测试通过。
- ESLint 通过。
- 前端生产构建通过。
- 桌面发布说明校验通过。
- 定向 Vitest 通过，2/2 tests。
- 首次全量 Vitest 的 270 个测试文件和 1,926 个断言全部通过，但并行运行结束时出现一个已有的异步 mock 未处理 rejection；定向复跑通过，GitHub 前端检查也通过。

### GitHub 验证

| Workflow | Run | 结果 |
| --- | --- | --- |
| CI（PR） | [33242686168](https://github.com/rw0104/sub2api-cost-console/actions/runs/33242686168) | 通过 |
| CI（分支 push） | [33242717842](https://github.com/rw0104/sub2api-cost-console/actions/runs/33242717842) | 通过 |
| Security Scan | [33242717851](https://github.com/rw0104/sub2api-cost-console/actions/runs/33242717851) | 通过 |
| Desktop Release | [33242717852](https://github.com/rw0104/sub2api-cost-console/actions/runs/33242717852) | 通过 |
| Compatible Core Auto Update | [33243122152](https://github.com/rw0104/sub2api-cost-console/actions/runs/33243122152) | 通过 |

## Evidence -> Finding -> Path

### E-001

- title: 远端主分支已包含发布合并
- observed_at: 2026-08-29
- source_type: command
- source_ref: GitHub refs API 和 PR #25
- content_hash: n/a
- artifact_path: n/a
- repro_command: `gh api repos/rw0104/sub2api-cost-console/git/ref/heads/main --jq '.object.sha'`
- raw_excerpt: `b89af607d8bceb37f70e1042a8bfa5673b65af2c`
- linked_workitem: n/a
- supersedes: none

### E-002

- title: 桌面 v0.2.27 已发布完整签名资产
- observed_at: 2026-08-29
- source_type: command
- source_ref: GitHub Release `v0.2.27`
- content_hash: `de7c09d769b12a3c587eff7c5c1785849c1e3d002f2d14aca5c60b01d2395436`
- artifact_path: GitHub Release asset `Sub2API.Cost.Console_0.2.27_x64-setup.exe`
- repro_command: `gh release view v0.2.27 --repo rw0104/sub2api-cost-console --json assets,url`
- raw_excerpt: `isDraft=false, isPrerelease=false, installer size=30550264`
- linked_workitem: n/a
- supersedes: none

### E-003

- title: core-stable 已发布兼容内核 0.1.183
- observed_at: 2026-08-29
- source_type: file
- source_ref: `core-stable/core-latest.json`
- content_hash: `6dc8147059c5a1395edcfcc48977a53f3814489ddf94116fc84be235c3eb6cd2`
- artifact_path: GitHub Release asset `core-latest.json`
- repro_command: `gh release download core-stable --repo rw0104/sub2api-cost-console --pattern core-latest.json`
- raw_excerpt: `version=0.1.183, extension_version=1.1.1, channel=stable`
- linked_workitem: n/a
- supersedes: none

### F-001

- title: Git、桌面发布和兼容内核更新已完成
- severity: info
- category: other
- status: validated
- evidence_ids: [E-001, E-002, E-003]
- location: GitHub repository `rw0104/sub2api-cost-console`
- impact: 用户可通过桌面自动更新或 NSIS 安装包升级到 v0.2.27，客户端可从稳定通道获取兼容内核 0.1.183。
- confidence: high
- repro_steps:
  1. 查询远端 `main` 和 PR #25 状态。
  2. 查询 `v0.2.27` Release 资产和 updater 清单。
  3. 下载并读取 `core-stable/core-latest.json`。
- remediation: n/a
- optional_attack:

### P-001

- title: 从上游内核到可更新桌面客户端的发布路径
- path_type: callflow
- start: 上游 Sub2API `v0.1.183`
- goal: 桌面 `v0.2.27` 和稳定内核 `0.1.183` 可供客户端下载
- steps:
  1. action: 合并上游增量并修复兼容层冲突 - evidence: E-001 - finding: F-001
  2. action: GitHub CI 和安全扫描验证 PR #25 - evidence: E-001 - finding: F-001
  3. action: 构建、签名并发布桌面安装包 - evidence: E-002 - finding: F-001
  4. action: 完整验证并更新 `core-stable` - evidence: E-003 - finding: F-001
- residual_risks: 全量 Vitest 并行运行曾出现一个测试 mock 的未处理 rejection；定向复跑和 GitHub 前端检查均通过，建议后续单独清理该测试隔离问题。

## 回滚与复核

桌面端和兼容内核使用独立回滚路径：

- 桌面端需要回退时，可重新安装上一版正式 Release。
- 新内核首次激活或健康检查失败时，客户端自动恢复上一版内核槽位。
- 不应手工覆盖 `core-stable/core-latest.json`；必须通过兼容内核 workflow 重新验证和发布。

发布后复核命令：

```powershell
gh api repos/rw0104/sub2api-cost-console/git/ref/heads/main --jq '.object.sha'
gh release view v0.2.27 --repo rw0104/sub2api-cost-console
gh release view core-stable --repo rw0104/sub2api-cost-console
gh run view 33243122152 --repo rw0104/sub2api-cost-console `
  --json status,conclusion,url
```
