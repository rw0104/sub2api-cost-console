# 桌面 v0.2.39 插件宿主正式发布记录

日期：2026-09-18。承接 [插件扩展阶段开发日志](2026-09-18_plugin-development-phase-report.md)。本次将插件宿主能力合并至主分支并发布正式 Windows x64 桌面安装器。

**已合并并正式发布，公开下载核验通过。** 最新正式版为 [v0.2.39](https://github.com/rw0104/sub2api-cost-console/releases/tag/v0.2.39)，稳定内核通道为 0.2.5 / 扩展 1.2.0 / 算法 1.6.1。最终核验时间：2026-09-18 09:11:45 UTC（02:11:45 PDT）。

## 发布范围

| 项目 | 版本 |
| --- | --- |
| 正式桌面 | 0.2.39 |
| 上游内核 | 0.2.5 |
| 上游提交 | `86f93c28ee34cc74b629dafb748bd5ac5ca8c5ea` |
| 本地扩展 | 1.2.0 |
| 成本算法 | 1.6.1 |

本次只发布主程序、宿主 SDK 和既有通用示例。本机独立插件源码、`.s2plugin`、源码交付包及签名私钥不进入 Git 或 Release。正式安装器使用 `com.sub2api.cost-console` 与原有升级通道，不使用 Plugin Preview 的应用标识或隔离端口。

## 功能与修复

- v2 通用插件能力、请求预处理、OpenAI OAuth 保护传输、Host API、细粒度路由、并发与灰度、限时升级回滚。
- 桌面配置页正确使用后端资源 URL，收敛 CSP 来源，校验 ready 信号并支持失败重试。
- 插件列表紧凑展示，支持搜索、筛选与独立详情展开。
- 配置持久化采用比较更新，失败时恢复既有快照。
- 发布前补齐原分支 CI 的资源关闭、类型断言、错误文本和未使用函数检查；Unix IPC 的 G704 标记仅针对经过规范路径验证的 `/rpc/` Unix socket，不关闭全局安全规则。
- 扩展版本从 1.1.2 升至 1.2.0，新增必需能力 `plugin_extensions.v2` 和 `openai.oauth.protection_transport.v1`，避免桌面升级后继续使用同上游版本的旧内核。保留原有两项成本能力。

## 升级与边界

数据库迁移 239–241 增加插件历史、路由字段和秘密授权。正式版不自动安装或启用私有插件，不预置信任测试插件发布者；第三方发布者仍按现有管理员信任配置验签。

同一请求只选择一个匹配的保护传输，process 模式才支持保护出站。通用发布者授权界面仍不在本次范围。主程序回滚不会自动撤销数据库结构；回退到不支持 v2 的版本前应停用相关插件并备份。

## 验证与发布进度

发布前已核对上游完整提交和已集成源码树，并通过发布说明结构校验。原分支的 15 项静态检查阻塞已修复；后续检查发现的过期配置恢复辅助函数和示例错误文本也已处理。

- 最终功能提交：`f3af2064b314095b46f0ab49cc507b09c03077e1`。
- [PR #29](https://github.com/rw0104/sub2api-cost-console/pull/29) 已于 2026-09-18 08:49:46 UTC 合并，主分支合并提交 `a6742fb000ce2cd4c88eef4cfeb33b186d59cd1f`。
- 标签 `v0.2.39` 指向上述合并提交，运行源码与已测试功能提交一致。
- 最终提交的 [PR CI 35325356591](https://github.com/rw0104/sub2api-cost-console/actions/runs/35325356591) 和 [push CI 35325352768](https://github.com/rw0104/sub2api-cost-console/actions/runs/35325352768) 均通过：后端单元、数据库集成、Go 静态检查、前端和部署脚本。
- [安全扫描 35325356559](https://github.com/rw0104/sub2api-cost-console/actions/runs/35325356559) 通过。
- 合并提交的 [main CI 35326364659](https://github.com/rw0104/sub2api-cost-console/actions/runs/35326364659) 再次全部通过，覆盖同一正式发布源码。
- 本地前端：315 个文件、2406 项测试通过，ESLint、Web/桌面构建通过。
- 本地 Rust：63 passed、0 failed、1 ignored；跳过的是原有需要启动本机 Docker 数据容器的显式测试。新增相同上游版本替换旧扩展内核用例，既有算法身份用例同步要求全部插件能力。
- 插件热升级/回滚进程测试在繁忙机器上暴露复用首次编译超时的问题，现为升级编译提供独立两分钟预算，复测通过。其它插件定向回归通过。
- 兼容内核归档只含 `sub2api.exe`，已核验版本、四项能力、归档摘要及包内二进制与构建输出一致；编译未开启 Preview 标志。
- [桌面签名发布工作流 35326430534](https://github.com/rw0104/sub2api-cost-console/actions/runs/35326430534) 已成功完成，源码为上述正式标签对应的合并提交。

本地构建、测试及重新下载校验材料保存在 `.git/release-v0.2.39/` 和 `frontend/release-assets/online-verify-v0.2.39/`（忽略目录）。现有 `.gitignore` 中用户对历史报告的改动保留，不纳入本次提交。

## 正式发布验收

| 产物 | 大小（字节） | SHA-256 |
| --- | --- | --- |
| `Sub2API.Cost.Console_0.2.39_x64-setup.exe` | 31,467,275 | `9a29bf3189c913c3419b219b14033c939e4896922faa9bbbb5ef60c27a2c7d27` |
| `sub2api-core_0.2.5_1.2.0_windows_x86_64.zip` | 36,938,239 | `9441192a9fb3dbccb2ee01247c086c6a7a185969b74fdb7e98dc45fce5607044` |

- [Windows 正式安装器](https://github.com/rw0104/sub2api-cost-console/releases/download/v0.2.39/Sub2API.Cost.Console_0.2.39_x64-setup.exe) 的 FileVersion / ProductVersion 均为 0.2.39。Release 为正式、非草稿，GitHub latest 已指向 v0.2.39。
- 从公开发布页重新下载全部安装器校验项，并对照 GitHub 资产摘要、文件大小和 SHA-256 验证通过。
- 使用桌面内置更新公钥验证安装器 Ed25519 文件签名与可信注释签名；独立 `.sig` 与 `latest.json` 内签名一致，两项 Windows 平台更新条目一致。
- 匿名桌面更新入口返回的新清单与发布附件一致，发布说明与源码按规范化换行比较一致。
- [稳定兼容内核](https://github.com/rw0104/sub2api-cost-console/releases/download/core-stable/sub2api-core_0.2.5_1.2.0_windows_x86_64.zip) 已发布。发布前重读旧稳定通道确认没有覆盖更高版本；先上传归档和校验文件，再切换 `core-latest.json`。
- 重新下载的内核归档与本机已验证产物逐字节一致。包内仅有 `sub2api.exe`，`--version`、上游提交、扩展版本和四项必需能力均符合清单，包含 gRPC 1.83.2。
- 正式发布附件仅包括安装器、更新签名、升级清单、校验文件和发布说明；未上传本机独立插件。兼容内核通道只增加主程序内核归档。
- 验证未在用户现有桌面实例或正式数据库上执行原位安装升级。Tauri 更新签名不等同于 Windows Authenticode 证书。

安装器与公开通道检查结果见本机 `frontend/release-assets/online-verify-v0.2.39/verification.json`；下载的正式安装器保留在该目录的 `desktop/` 子目录。
