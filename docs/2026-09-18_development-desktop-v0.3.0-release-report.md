# 桌面 v0.3.0：普通用户插件导入与发布者信任

日期：2026-09-18。桌面版本 0.3.0，内核基线 0.2.5，本地扩展 1.3.0，成本算法 1.6.1。

**已合并并正式发布，公开下载核验通过。** 最新正式版为 [v0.3.0](https://github.com/rw0104/sub2api-cost-console/releases/tag/v0.3.0)。最终核验时间：2026-09-18 11:49:23 UTC（04:49:23 PDT）。

## 问题与结果

用户在正式 0.2.39 中导入已编译插件时遇到“插件发布者密钥不受信任”。复现确认原包的签名和二进制有效，问题是正式宿主只有配置文件信任入口，旧包也没有附带首次导入所需的公钥。让每个接收者编译或编辑配置不能满足成品插件分发。

新流程为：开发者编译并签名一次 → 分享含公钥的 `.s2plugin` → 用户导入 → 查看来源与权限 → 首次确认信任并安装 → 按需配置、启用。无需接收者安装开发工具；以后同一发布者无需重复确认。

## 实现边界

- `POST /admin/plugins/inspect` 验证 ZIP/清单、签名、全量文件哈希、体积与兼容性，仅返回检查结果，不安装或执行插件。
- 确认绑定包 SHA-256 与发布者指纹；安装端重新计算并验证，不能把对某个文件的确认复用到另一个包。
- 信任表迁移 242 与安装/升级写入共用事务；同名不同公钥不能覆盖，取消及失败不留下新信任。
- 内置官方公钥与静态配置优先，默认签名校验不关闭。旧包在已有信任下保持兼容，新包公钥只是验证材料，不会自动变成信任根。
- 安装及升级仍要求管理员身份和既有 step-up；初始安装保持停用，插件业务权限、路由和启用机制不变。
- 本机私有插件与私钥只在本地保存，不进入宿主源码或正式 Release 附件。公开发布仅包含主程序及通用 SDK 示例。

## Evidence → Finding → Path

| 验证证据 | 结论 | 实现路径 |
| --- | --- | --- |
| 实际旧包在空配置下失败、配置正确公钥后成功 | 无需让接收者重编译；缺少首次导入信任流程 | `TestPluginSharePackageSignatureBaseline` |
| 包检查、篡改、公钥冲突及确认复用回归 | 公钥不能自我授权或覆盖已信任密钥 | `plugin_publishers.go`、`plugin_publishers_test.go` |
| 真实 PostgreSQL 原子性与竞争测试通过 | 安装/版本切换失败不会遗留授权，同名密钥竞争只有一个成功 | `plugin_publishers_integration_test.go`、迁移 242 |
| 页面首次确认、取消、卸载后迟到响应等测试通过 | 未确认不发送安装授权；授权与展示的文件/指纹一致 | `PluginInstallDialog.vue`、`PluginsView.spec.ts` |
| 真实浏览器与生产路由/数据库/插件子进程全流程通过 | 首装确认、签名包安装、升级复用信任、配置与回滚正常 | `TestPluginProductionE2E`、`frontend/scripts/plugin-e2e.py` |

浏览器证据保存在本机 `frontend/release-assets/plugin-publisher-e2e/`，测试日志位于 `.git/release-v0.3.0/`。测试使用临时数据库、缓存与合成账号，结束后清理；没有操作用户正式环境。

## 发布状态

功能提交 `9bf477dd5ee75ebc8d06a952103ff1f79699a0ec` 已通过两轮完整 CI（[35337929206](https://github.com/rw0104/sub2api-cost-console/actions/runs/35337929206)、[35337925825](https://github.com/rw0104/sub2api-cost-console/actions/runs/35337925825)）以及安全扫描。

- 前端全量：315 个文件、2,411 项测试通过；类型检查与 ESLint 通过。
- 本地正式 Web、Go sidecar 和桌面资源构建通过。
- Rust：64 passed、0 failed、1 ignored；跳过的是原有需要操作本机 Docker 容器的测试。
- SDK 开发包通过独立模块测试、离线编译、签名、mTLS 子进程与请求处理验证。
- 私有回合状态插件 1.0.2 已内置公钥，在无预配信任的宿主中验证检查、显式确认、安装、记住发布者、配置与启停。
- 原账号保护插件另生成仅补充公钥元数据的分享包，原清单、签名和运行程序保持一致，真实进程回归通过。两个私有插件包都未上传。

[PR #30](https://github.com/rw0104/sub2api-cost-console/pull/30) 已合并至 `main`，合并提交为 `885a09db2296a7b659a5fbb1da4f1ceca5658d31`。标签 `v0.3.0` 指向该提交，与已验证的运行源码一致。

[正式签名发布工作流 35338995598](https://github.com/rw0104/sub2api-cost-console/actions/runs/35338995598) 已成功完成。合并提交的 [main CI 35338926595](https://github.com/rw0104/sub2api-cost-console/actions/runs/35338926595) 也再次全部通过。

## 发布与下载验收

| 产物 | 大小（字节） | SHA-256 |
| --- | --- | --- |
| `Sub2API.Cost.Console_0.3.0_x64-setup.exe` | 31,457,062 | `8678c05e2c43c33c37b01c146c1b4b1834874cc3f3f4fe00717ed2d32e2e5ce4` |
| `sub2api-core_0.2.5_1.3.0_windows_x86_64.zip` | 36,942,387 | `efdf0aeee8b061f5ee2cc0aef49cada188719e903fe7b676fabb85c25863ba86` |

- [Windows 正式安装器](https://github.com/rw0104/sub2api-cost-console/releases/download/v0.3.0/Sub2API.Cost.Console_0.3.0_x64-setup.exe) 已重新下载，FileVersion / ProductVersion 均为 0.3.0。
- 安装器 SHA-256、GitHub 资产摘要、Tauri Ed25519 文件签名与可信注释签名均验证通过，独立 `.sig` 与更新清单一致。
- 两个 Windows 升级条目指向同一安装器，匿名最新版本入口已返回 v0.3.0，发布说明与源码按规范化换行比较一致。
- 稳定内核通道已更新至 0.2.5 / 扩展 1.3.0，重新下载的 ZIP 与本地已验证产物一致，内核报告全部五项必需能力。只取消了同一提交上的一次重复自动构建，定时更新仍启用，未覆盖更高版本。
- 正式 Release 自动附带 `PLUGIN_DEVELOPMENT.md`、SDK 开发包及独立 SHA-256 文件，下载后的 SDK 全量文件摘要与源码基线核验通过。
- 发布附件不包含本机私有插件。可分享私有包保存在本机，用户只发送对应 `.s2plugin` 即可，接收者不需要编译或 SDK。
- 本次没有在用户现有桌面实例或正式数据库上执行原位安装升级。Tauri 更新签名与 Windows Authenticode 证书是不同机制。

最终回执保存在 `frontend/release-assets/online-verify-v0.3.0/verification.json`，正式安装器的本机副本位于其 `desktop/` 子目录。
