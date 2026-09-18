# 插件配置页与多插件布局修复

## 结论

配置页空白来自宿主桌面版。插件包在宿主同源下可以显示，前端却把 `/api/v1/plugin-ui/...` 直接交给 Tauri 资源域名；改为后端地址后，原 CSP `frame-ancestors 'self'` 仍会拒绝桌面资源域名。宿主还把 iframe 的 load 事件当成完成，掩盖了空白错误页。

本次只修改宿主，账号保护插件包保持原字节，SHA-256 为 `10f85bc420f61f88c8a211f0caf06d7390ad009fd7f8b20332c458bc564a29d7`。

## 修复

- 配置地址使用宿主统一 API URL 解析器。
- 插件资源只允许宿主同源和 Tauri 固定资源域名嵌入，其他域名仍被 CSP 拒绝；iframe 继续仅允许脚本。
- 校验 ready 消息后显示配置；超时提供错误提示和重试；关闭后丢弃迟到会话。
- 插件列表改为紧凑记录，默认隐藏详细权限、运行信息和维护操作，支持名称/ID/发布者搜索及状态筛选。
- 配置弹窗收窄并限制在可视高度内，长内容在 iframe 内滚动。

## 验证

回归测试先复现相对 iframe URL 和错误 load 就绪行为，再验证修复；同时覆盖伪造 ready、超时、重试、迟到会话和多插件筛选。

`TestPluginUIBrowserFixture` 使用临时安装目录和真实签名包，运行生产 `ServeUIAsset`。Chromium 在同源、Tauri 域名和未知域名下加载未修改的插件 UI：旧响应头在 Tauri 域名下被明确报告 CSP 拒绝；新响应头下同源和 Tauri 均 ready，未知域名仍被拒绝。合成浏览器允许本地网络访问以测试 iframe/CSP 边界，不访问现有用户数据库。

复现命令和产物：

```powershell
# backend 目录，使用新的 fixture 文件名
$env:SUB2API_PLUGIN_UI_BROWSER_FIXTURE = Join-Path (Get-Location) 'dist/plugin-ui-browser.json'
$env:SUB2API_ACCOUNT_PROTECTION_PACKAGE = (Resolve-Path ../plugins/account-protection/dist/account-protection-1.0.0.s2plugin).Path
go test ./internal/handler/admin -run '^TestPluginUIBrowserFixture$' -v -count=1 -timeout=9m

# fixture ready 后，在 frontend 目录执行
python scripts/plugin-ui-embedding.py --fixture ../backend/dist/plugin-ui-browser.json --output release-assets/plugin-ui-embedding
```

桌面打包使用既有隔离通道，版本为 `0.2.39-plugin.3`。正式版安装标识、端口和数据目录不变；本次未自动安装或替换用户正在运行的程序。

## 交付验收

- 前端插件页 15 项回归通过；API 上传编码 2 项、语言键检查 3 项通过；TypeScript 检查通过。
- 宿主插件 handler 和安全响应头相关测试通过。
- 编译后的实际前端在 Tauri 来源的浏览器测试通过：12 个合成插件的紧凑列表、搜索、独立详情、未修改插件的配置加载/保存消息、移动端无横向溢出、加载超时与重试；0 页面脚本错误。管理 API 使用合成数据，插件资源使用生产 handler。
- `node scripts/verify-plugin-preview.mjs --working-tree` 验证安装器独立名称/标识、真实 sidecar 版本及编译进内核的数据库隔离。
- 安装器：`frontend/release-assets/plugin-preview-0.2.39-plugin.3/Sub2API Plugin Preview_0.2.39-plugin.3_x64-setup.exe`，31,270,997 字节，SHA-256 `0631b9ee36351cbe53470a47773c60165b20b7b01d38a01f030e63d2b294e6dd`。
- 浏览器截图与检查记录：`frontend/release-assets/plugin-management-ui/`；新旧嵌入策略对比：`frontend/release-assets/plugin-ui-embedding-legacy/` 与 `plugin-ui-embedding-fixed/`。
