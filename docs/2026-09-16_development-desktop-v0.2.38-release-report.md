# 桌面 v0.2.38 开发日志与发布记录

日期：2026-09-16（America/Los_Angeles）。本版将成本审计确认的五项修复打包为 Windows x64 桌面安装器，并提供对应的兼容内核。详细审计与修复依据见[成本算法与扩展审查及修复记录](2026-09-16_cost-algorithm-extension-audit-report.md)。

**已正式发布并完成下载核验。** GitHub 最新正式版为 [v0.2.38](https://github.com/rw0104/sub2api-cost-console/releases/tag/v0.2.38)，稳定内核通道为 0.2.5 / 扩展 1.1.2 / 算法 1.6.1。最终核验时间：2026-09-16 09:41:03 PDT（16:41:03 UTC）。

## 版本与改动

| 项目 | 本版 | 上一版 |
| --- | --- | --- |
| 桌面 | 0.2.38 | 0.2.37 |
| 上游内核 | 0.2.5 | 0.2.5 |
| 成本扩展 | 1.1.2 | 1.1.1 |
| 成本算法 | 1.6.1 | 1.6.0 |
| 上游提交 | `86f93c28ee34cc74b629dafb748bd5ac5ca8c5ea` | 同左 |
| 业务修复提交 | `a453b6e6e272412854ddf5ebdc72935f96bbf2d2` | — |
| 发布源码 / 标签 | `3782a805140b2a549e87446ff578c2e4ebacc617` / `v0.2.38` | — |

五项修复：

1. **重复终局与退款**：终局确认、退款和恢复按账号使用数据库事务锁。未恢复的生命周期复用首次终局；默认幂等键不再依赖 `updated_at`。测试中 730 元采购退款 30 元后，重复确认仍为 700 元。
2. **固定附加费遗漏**：API Key、Service Account、中转等按量账号显式配置的月租、手续费等固定费正常进入经济总成本；终局损失资格单独判断。
3. **当天统计错位**：成本与收入使用明确的起止时间和客户端时区，已发生费用不累计到未来。覆盖午夜、夏令时和本地月份边界。
4. **按量账号误套订阅价**：没有显式固定费的按量账号，不再因 `pro` 等套餐标记被自动计入订阅默认价。
5. **未来档案提前计费率**：到起算时间之前，累计费用和当前小时费率均为零。

成本中心顶部的“Sub2API 设置”改为“账号管理”，直接跳转 `/admin/accounts`。保留系统设置页面、Docker 恢复、受管内核双槽回滚、成本损失账本、经济采样与 Windows 原生一键启动。

## 数据兼容与实现边界

- 无新增数据库结构迁移。既有 `usage_logs`、成本档案和损失事件不批量改写；历史算法版本仍保留。
- 旧重复有效终局以首次终局冻结成本，合计相关退款；恢复在一个事务中追加各条冲销。原始事件和退款别名仍可追溯。
- Token 价格、730 小时/月折算和汇率来源未调整。
- 独立内核更新提供服务端账本与聚合修复；前端自然日参数、默认价、未来费率和按钮变化需要桌面整包更新。
- Tauri/Minisign 更新签名与 Windows Authenticode 证书不同。本次通过 GitHub Actions 中已有的更新签名密钥生成安装包签名。
- 发布验证不对当前生产数据库或本机已安装客户端执行原位升级。

## 已完成的开发验证

业务修复提交 `a453b6e6e` 已通过：

- 前端全量：314 个文件、2,390 项测试；ESLint。
- Windows Go 全量：`go test -p 2 -tags=unit ./...`，退出码 0。
- 真实 PostgreSQL 生命周期：3 项测试，包括 8 并发确认、并发退款余额、恢复后再次终局、硬删除后退款、旧重复记录合并与恢复。
- 前后端共享 8 组金额样例，验证 USD/CNY、计费类型、周期和起算边界一致。
- Web 资源、受管内核和桌面生产构建。
- Rust：60 passed、0 failed、1 ignored（需显式启用的本机 Docker 恢复测试）。
- [完整 CI 35119345806](https://github.com/rw0104/sub2api-cost-console/actions/runs/35119345806)：单元、数据库集成、Go 静态检查、前端与部署脚本通过。
- [安全扫描 35119345792](https://github.com/rw0104/sub2api-cost-console/actions/runs/35119345792)：通过。

Ollama 已有测试中的时钟精度依赖改为显式构造不同代次，保留原竞态断言，未修改该功能的生产逻辑。

## 发布步骤

发布工作流首先获取官方上游标签，验证完整提交号与已集成源码树一致；再构建嵌入 Web 资源的 Go sidecar 和桌面前端，执行 Rust、前端全量与 lint，最后生成签名 NSIS 安装器。

```powershell
# 仓库根目录
node frontend/scripts/verify-core-source.mjs upstream-v0.2.5
node frontend/scripts/prepare-desktop-release.mjs --validate-notes-only

# 查看发布状态
gh run list --repo rw0104/sub2api-cost-console --workflow desktop-release.yml --limit 3
gh release view v0.2.38 --repo rw0104/sub2api-cost-console --json tagName,isDraft,isPrerelease,assets
```

发布后重新下载安装器、签名、清单、校验文件和稳定内核，检查：

- 安装器 FileVersion / ProductVersion、文件大小与 SHA-256。
- `latest.json` 两个平台条目一致，签名与独立 `.sig` 一致。
- 使用客户端内置公钥验证安装器 Ed25519 文件签名和可信注释签名。
- 稳定内核归档、清单摘要及可执行文件版本、扩展、上游提交和必需能力。
- 匿名桌面与内核更新入口和 GitHub latest 状态。

核验日志保存在 `.git/release-v0.2.38/`，下载产物保存在 `frontend/release-assets/online-verify-v0.2.38/`。原有 `.gitignore` 用户修改保留，不进入此次提交。

## 发布验收记录

以下结果来自云端工作流和从公开发布页重新下载的产物。

- 发布源码 `3782a8051` 的[完整 CI 35121238273](https://github.com/rw0104/sub2api-cost-console/actions/runs/35121238273) 全部成功，包含后端单元与数据库集成、静态检查、前端和部署脚本。
- 同一源码的[安全扫描 35121238074](https://github.com/rw0104/sub2api-cost-console/actions/runs/35121238074) 成功。
- [桌面签名发布工作流 35121238218](https://github.com/rw0104/sub2api-cost-console/actions/runs/35121238218) 成功，源码为 `3782a8051`。
- [Windows x64 安装包](https://github.com/rw0104/sub2api-cost-console/releases/download/v0.2.38/Sub2API.Cost.Console_0.2.38_x64-setup.exe)：31,351,724 字节，SHA-256 `e2aa7fc17148afef9527aa31698807b0933cc1bbb125dc7728ce41ff6fb0fec6`。
- 重新下载后，全部安装器校验条目、Ed25519 文件签名和可信注释签名通过。发布说明与源码按统一换行比较一致；对文件的哈希与签名验证始终使用原始字节。
- 安装器 FileVersion 与 ProductVersion 均为 `0.2.38`。`windows-x86_64` 与 `windows-x86_64-nsis` 使用一致的更新 URL 和签名，`.sig` 与清单签名一致。
- [稳定内核工作流 35121239700](https://github.com/rw0104/sub2api-cost-console/actions/runs/35121239700) 成功，源码同为 `3782a8051`。
- 内核归档 `sub2api-core_0.2.5_1.1.2_windows_x86_64.zip`：36,812,940 字节，SHA-256 `f0756da259f471ea4cf8493300ae75222faf08057bce304d6028971fead08926`，仅含 `sub2api.exe`。
- 归档解压后执行 `--version`，确认 0.2.5、完整上游提交、扩展 1.1.2 和两项必需成本能力；清单算法为 1.6.1，内核构建信息确认 gRPC 1.83.2。
- `INSTALLER_SHA256SUMS.txt` 与 `CORE_SHA256SUMS.txt` 全部条目通过，下载资产的文件大小和 GitHub SHA-256 digest 一致。
- [匿名桌面更新入口](https://github.com/rw0104/sub2api-cost-console/releases/latest/download/latest.json)和[匿名稳定内核入口](https://github.com/rw0104/sub2api-cost-console/releases/download/core-stable/core-latest.json)均与已核验清单完全一致。桌面 Release 非草稿、非预发布，GitHub latest 为 v0.2.38。
- 总核验记录：`frontend/release-assets/online-verify-v0.2.38/verification.json`，时间 `2026-09-16T16:41:03.473Z`。

## Evidence → Finding → Path

- E-01：业务修复 CI、安全扫描和本地测试日志，关联上述修复提交；详见成本审计报告的原始失败与修复后结果。
- E-02：发布标签与官方上游身份。复核命令：`git rev-parse 'v0.2.38^{commit}'`、`node frontend/scripts/verify-core-source.mjs upstream-v0.2.5`。发布源码为 `3782a8051`，上游标签解引用提交为 `86f93c28ee34cc74b629dafb748bd5ac5ca8c5ea`，已集成源码树一致。
- E-03：两条发布工作流成功，公开资产重新下载并验签通过。复核命令：`gh release view v0.2.38 --repo rw0104/sub2api-cost-console --json assets`；`gh release download core-stable --repo rw0104/sub2api-cost-console --pattern core-latest.json --output -`。原始证据为上述核验目录和 `.git/release-v0.2.38/` 中的结果文件，资产 SHA-256 如发布验收记录所列。
- F-01：桌面与内核需要配套升级。服务端账本修复由扩展 1.1.2 提供，前端窗口和默认费率修复由桌面 0.2.38 提供；依据 E-01、E-02。
- P-01：核对修复源码与上游身份（E-01、E-02）→ 提交开发日志 → 推送 v0.2.38 标签 → 云端构建与门禁 → 安装器和内核上传 → 下载验签及更新入口核验（E-03），全部完成。
