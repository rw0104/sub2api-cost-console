# Sub2API Plugin Preview 0.2.39-plugin.1

独立插件测试版，不是正式版升级。上游基线 0.2.5，扩展构建 1.2.0-plugin.1。

- 安装名称：Sub2API Plugin Preview；程序：sub2api-plugin-preview.exe。
- 应用 ID：com.sub2api.plugin-preview；数据：%APPDATA%\com.sub2api.plugin-preview。
- 内核端口 19765；PostgreSQL 25432；Valkey 26379。
- 快速安装创建 sub2api-plugin-preview-postgres / sub2api-plugin-preview-valkey，使用独立数据卷。
- 不复制正式版设置、账户、密钥、数据库或缓存；不要将安装目录改为正式版目录。
- 测试内核拒绝连接正式版数据库/缓存端口，数据库名必须是 sub2api_plugin_preview。
- 桌面和内核的正式更新通道均禁用。后续测试版使用独立安装包升级。

首次使用先启动 Docker Desktop，再打开测试版并选择快速安装，创建独立测试管理员。进入“系统设置”启用“插件管理”菜单，上传签名的 .s2plugin 包。对接和公钥配置见仓库 docs/PLUGIN_DEVELOPMENT.md。

独立进程不等于 OS 沙箱。默认 process 模式仅安装可信插件；第三方不可信代码应配置 v2 container 模式。该安装包不包含 Docker、数据库镜像或沙箱镜像，不会在安装时自动部署它们。

此本地测试安装器没有 Windows Authenticode 签名。确认来源和 SHA-256 后再运行；不要用它替换正式版或接入真实业务数据。卸载测试版不会主动删除测试数据库容器/数据卷，避免误删数据。

## 本地构建与验收

构建源码：`7486d7bbf6a897dfd81ad64902a63c838a6c8629`，分支 `codex/plugin-generic-interface`，未合并。

安装器 `Sub2API Plugin Preview_0.2.39-plugin.1_x64-setup.exe`：31,252,677 字节；SHA-256 `4659444b9e0e72d657d2cff1bba7b89ff6a5635d067a86b269232614745fb5fe`。

已核对 ProductName、ProductVersion、FileVersion、独立主程序名、卸载注册表、开始菜单和应用 ID。对实际构建的 sidecar 验证了 0.2.5 / 1.2.0-plugin.1，并在没有测试环境变量的情况下确认编译进内核的保护会在连接前拒绝非测试数据库。

Rust 稳定/测试两种构建各 62 项通过、1 项需操作本机容器的测试跳过；Go 协议、密钥生成、配置及初始化隔离测试通过；前端插件、Axios、更新通道回归和生产构建通过。没有在用户机器上执行安装；既有正式版注册信息仍为 0.2.38。

对接文档的独立 Go module + 本地 replace 方式已实际构建通过。PowerShell 的 Go 版本参数使用引号 `'-go=1.27.0'`；初次依赖校验需要正常的 Go Module 网络访问，未关闭 checksum 校验。

在 `frontend` 中复现构建：`node scripts/build-plugin-preview.mjs`。构建后执行 `node scripts/verify-plugin-preview.mjs 7486d7bbf6a897dfd81ad64902a63c838a6c8629` 可核验本版输出。验证脚本不会安装应用，也不会连接用户数据库；代码变化后应使用对应的新构建源码提交号。
