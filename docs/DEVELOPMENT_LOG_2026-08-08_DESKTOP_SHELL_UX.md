# Desktop 0.2.13：更新完成提示、全屏权限与系统托盘

> 日期：2026-08-08
>
> 桌面版本：`0.2.13`
>
> 内核基线：`Sub2API 0.1.172`
>
> 成本算法：`1.4.0`

## 1. 用户可见问题

1. 恢复桌面内置内核已经到达 100%，但文案仍显示“正在执行健康检查”，用户无法判断操作是否真正结束。
2. 成本中心右上角全屏按钮和 `F11` 调用了 Tauri `setFullscreen`，但正式包能力清单没有写权限，因此点击无效。
3. 桌面壳没有系统托盘图标。最小化后只能留在任务栏，关闭窗口会结束后台内核，无法作为本地网关持续运行。

## 2. 根因

### 2.1 完成阶段没有落到 finished

`restore_bundled_core` 在 Rust 侧完成文件切换、受管进程启动和健康检查后才返回。前端收到成功结果后却把阶段写为 `ready`，并再次显示“正在执行健康检查”。进度计算把 `ready` 映射为 100%，造成进度和文案互相矛盾。

### 2.2 Tauri 默认能力只允许读取全屏状态

`core:window:default` 包含 `allow-is-fullscreen`，但不包含会改变窗口状态的 `allow-set-fullscreen`。按钮实现本身能够获取当前窗口，失败发生在 IPC 能力校验层。

### 2.3 桌面生命周期只处理进程退出

此前 `main.rs` 只在 `RunEvent::Exit` / `ExitRequested` 时停止受管后端，没有创建 `TrayIcon`、托盘菜单或窗口关闭/最小化策略。

## 3. 实现

### 3.1 更新完成反馈

- 恢复命令成功返回后写入 `progressStage = "finished"`。
- 文案改为“内核已完成更新：内置内核 vX 已启用且健康检查通过”。
- 失败仍清除进度并进入现有错误区域，不把失败显示成完成。

### 3.2 全屏控制

- 能力清单增加 `core:window:allow-set-fullscreen`。
- 桌面和浏览器模式继续共用同一个公开按钮；桌面调用 Tauri Window API，浏览器调用 Fullscreen API。
- 捕获切换失败并显示在成本中心错误区，避免无反馈的 Promise rejection。

### 3.3 Windows 系统托盘

- Tauri 依赖启用 `tray-icon` 特性。
- 托盘使用安装包默认窗口图标和 `Sub2API Cost Console` 提示文字。
- 左键单击：显示、取消最小化并聚焦主窗口。
- 右键菜单：`显示主窗口`、`退出 Sub2API`。
- 主窗口关闭或最小化：隐藏到托盘，受管后端继续服务。
- 托盘显式退出：调用应用退出流程，由既有 `shutdown_backend` 安全停止受管后端。
- Tauri updater 的 restart 使用专用重启退出码，不依赖窗口关闭，因此不会被托盘关闭策略阻断。

## 4. TDD 记录

### 4.1 更新完成文案

先在 `DesktopUpdateCenter.spec.ts` 断言恢复后包含“内核已完成更新”且不再包含“正在执行健康检查”。旧实现稳定失败；切换为 `finished` 后通过。

### 4.2 全屏能力

Rust 测试解析正式 `capabilities/default.json`，要求包含 `core:window:allow-set-fullscreen`。旧配置稳定失败；增加最小权限后通过。

### 4.3 托盘菜单

桌面壳状态机测试要求 `tray-show` 映射到显示主窗口、`tray-quit` 映射到显式退出、未知菜单项不执行操作。空实现先失败，实现菜单映射后通过。

## 5. 发布验收

- [x] 更新中心定向组件测试通过。
- [x] 全屏能力回归测试通过。
- [x] 托盘菜单状态机测试通过。
- [x] `tray-icon` debug 编译通过。
- [x] Vue/TypeScript 桌面生产构建通过。
- [x] `cargo build --release --features custom-protocol` 通过。
- [x] 测试桌面进程启动并托管活动内核，`/health` 返回 HTTP 200。
- [x] 完整 Vitest、ESLint、类型检查、Rust 20 项测试、Clippy `-D warnings` 与 Go 全仓测试通过。
- [ ] 实机确认全屏按钮、F11、最小化到托盘、关闭到托盘、托盘恢复和显式退出。
- [ ] 提交、推送并发布 `v0.2.13`，复算 Release 安装包 SHA-256。
