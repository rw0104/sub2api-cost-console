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
