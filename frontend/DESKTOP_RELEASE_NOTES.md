# Sub2API Cost Console v0.4.10

## 主要更新

- Windows 原生一键启动可分别打开 Microsoft Store 安装的 ChatGPT / Codex 桌面应用和 Codex CLI。在「API 接入中心」选择对应客户端，再点「检查客户端」，即可查看本机版本和安装来源。CLI 优先使用用户已安装的独立版本，也可使用桌面包随附的版本。
- 在 macOS 从源码运行控制台时，启动器可识别 ChatGPT.app 或 Codex.app，读取其桌面版本和内置 CLI 版本，并在所选工作目录中用“终端”打开 Codex CLI。
- 「API 接入中心」可分别复制适用于 Windows PowerShell 和 macOS 终端的密钥命令。`config.toml` 可直接保存。使用官方桌面应用时，控制台沿用应用自己的登录状态。

## 当前兼容内核

| 组件 | 版本 |
| --- | --- |
| Windows x64 桌面安装包 | 0.4.10 |
| 兼容内核 | `v0.2.23` |
| 上游源码 | `5af9afb588c4b014b3df9db7d4073af1f8ae01b6` |
| 宿主扩展 | 1.3.29 |
| 成本算法 | 1.7.0 |

## 技术变更

Windows 启动器按当前用户的 Store 注册记录查找应用，在实际安装卷上定位内置 CLI。macOS 从应用包及其 CLI 清单读取版本和入口。桌面版本、CLI 版本及预发布标记分别按实际检测结果显示。

成本面板和「使用密钥」导出的 Codex 配置移除了当前 CLI 配置规范未列出的三个根字段（`disable_response_storage`、`network_access`、`windows_wsl_setup_acknowledged`），并为模型名和网关地址生成正确的 TOML 字符串。手动读取模型目录时使用客户端协议的三段版本号 `0.171.0`。宿主扩展的打包版本递增至 1.3.29，以标识包含新版前端的内核。

## 升级行为

Windows 10 / 11 x64 用户可在「版本与更新」中升级，或退出控制台后运行 v0.4.10 的 `_x64-setup.exe`。桌面模式首次启动新版本时会停用已安装插件并保留配置，管理员可在检查兼容性后重新启用。已有 Codex 配置按需从「API 接入中心」或「使用密钥」重新导出。

macOS 的 `darwin_arm64` 和 `darwin_amd64` 压缩包分别用于在 Apple 芯片和 Intel Mac 上运行 Sub2API 后端服务。控制台的原生启动界面可在 macOS 源码构建中使用。 [v0.4.10 下载页](https://github.com/rw0104/sub2api-cost-console/releases/tag/v0.4.10)

## 验证结果

核对了 Windows Store 桌面包 `26.1117.4957.0` 的元数据，并从 macOS 实机的桌面包 `26.1117.41037` 读取并运行内置 CLI `0.171.0-alpha.13`。本轮兼容性验证通过 3,499 项前端测试和 72 项 Rust 测试，并完成 Windows 目标测试程序的交叉编译。

## 回滚说明

如需恢复 v0.4.9 的桌面启动和配置导出行为，退出控制台后安装 v0.4.9 的 Windows x64 安装包。如只需回退内核，可在「版本与更新」选择已保留的 v0.2.23 / 扩展 1.3.28 回滚槽位。回滚后请检查插件启用状态。 [v0.4.9 发布页](https://github.com/rw0104/sub2api-cost-console/releases/tag/v0.4.9)

QQ 用户交流群 **960663114**。 [插件开发指南](https://github.com/rw0104/sub2api-cost-console/blob/v0.4.10/docs/PLUGIN_DEVELOPMENT.md)
