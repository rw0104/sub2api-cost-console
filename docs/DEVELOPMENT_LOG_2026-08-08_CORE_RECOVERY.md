# Windows 遗留内核启动恢复修复记录

> 桌面版本：`0.2.12`
> 内置内核基线：`0.1.172`
> 成本算法：`1.4.0`
> 日期：`2026-08-08`

## 1. 事故现象

用户先通过桌面端升级到 `v0.2.11`，随后安装官方 `v0.1.172` 内核。点击修复后桌面程序闪退；卸载并重新安装 GitHub `v0.2.11` 安装包后仍无法打开。

现场状态同时满足以下条件：

- `127.0.0.1:18765` 仍由已经失去桌面父进程的受管内核监听；
- `core/active` 为旧内核，Windows 正在锁定其中的 EXE；
- `core/pending` 与 `state.json.pending` 保存着尚未完成的内核升级事务；
- 桌面启动阶段直接激活 `pending`，替换被锁定的 `core/active/sub2api-backend.exe` 时返回 Windows `os error 5`；
- 初始化错误被传递到 Tauri `setup`，使整个桌面进程退出；卸载器保留用户数据和仍在运行的孤儿进程，因此重装不能清除故障。

故障前的内核状态已备份到：

```text
D:\Sub2API-Recovery\before-v0.2.12-20260808-034240
```

备份只用于现场恢复审计，不随安装包发布。

## 2. 根因与安全边界

根因不是安装包损坏、目录 ACL 或杀毒软件，而是生命周期和升级事务之间缺少 Windows 文件锁恢复路径。

修复必须遵守以下边界：

1. 不手工删除 `core/active`，避免活动文件、状态文件和回滚记录失配。
2. 不因为端口被占用就结束任意进程。只有监听进程的完整可执行文件路径与本应用 `core/active/sub2api-backend.exe` 一致时，才允许停止它。
3. `pending` 激活失败不能再让 Tauri `setup` 崩溃；必须保存错误、保留待安装内核并继续打开桌面界面。
4. 内置内核恢复仍按“版本号＋提交＋SHA-256”比较身份，并沿用校验、暂存、停止、激活、健康检查和失败回滚事务。
5. 外部 Sub2API 服务占用端口时必须明确拒绝停止或替换，不扩大桌面程序的进程管理权限。

## 3. 实现

### 3.1 精确识别并停止遗留受管内核

新增 `frontend/src-tauri/src/managed_core_process.rs`。Windows 实现通过 `GetExtendedTcpTable` 获取 `18765` 的监听 PID，通过 `QueryFullProcessImageNameW` 读取完整进程路径，并与预期活动内核路径做规范化、大小写不敏感比较。

路径匹配后才调用 `TerminateProcess`，并使用 `WaitForSingleObject` 最多等待 5 秒释放 EXE 文件锁；路径不匹配时返回可见错误，绝不停止外部进程。

实机恢复进一步发现 Windows 退出期间存在两个短暂竞态：TCP 表可能仍保留已退出 PID，但 `OpenProcess` 或 `QueryFullProcessImageNameW` 已返回“拒绝访问”。最终实现不会在监督器发出停止后立刻查询 PID，而是先等待正常退出；只有端口超时未释放时才进入孤儿进程鉴权终止。进程在检查与终止之间自行退出、或 TCP 表残留行在第二次查询时消失，都视为已安全停止。若停止后的任意阶段失败，恢复分支会先还原状态、清理本次 pending、等待旧端口释放，再重新启动原内核。

该能力接入三个生命周期节点：

- 启动时发现 `pending` 且旧活动内核仍在监听；
- 桌面 updater 准备重启时发现受管内核尚未退出；
- 用户选择“恢复桌面内置内核”且监督器之外仍有遗留受管进程。

### 3.2 启动失败降级

`initialize_backend` 不再把 `activate_pending_core` 的错误直接返回给 Tauri `setup`。失败时：

- 保留 `state.json.pending` 和 `core/pending`，不丢弃已经校验的升级包；
- 将错误写入 `state.json.last_error`；
- 继续创建后端监督器并打开桌面界面；
- 版本与更新面板展示恢复错误，供用户判断当前活动、待安装和内置内核身份。

这使单次文件锁、外部端口占用或进程查询失败都变成可恢复状态，而不是桌面程序级崩溃。

### 3.3 构建模式说明

Tauri 正式构建必须启用 `custom-protocol`，窗口才会加载安装包内的 `tauri.localhost` 前端资源。裸执行：

```powershell
cargo build --release
```

仍属于开发导航语义，会访问 `tauri.conf.json` 的 `devUrl`（当前为 `127.0.0.1:3000`）；没有 Vite 开发服务器时 WebView 会显示 `ERR_CONNECTION_REFUSED`。这不代表 `18765` 内核失败。

本次补充项目级 `custom-protocol` feature。无安装包快速回归使用：

```powershell
cd frontend/src-tauri
cargo build --release --features custom-protocol
```

正式发布仍必须使用：

```powershell
cd frontend
corepack pnpm@9 desktop:build
```

## 4. 自动化与现场验证

新增回归覆盖：

- 精确匹配活动内核路径时允许停止监听进程；
- 端口由外部可执行文件监听时拒绝停止；
- 监听进程在路径检查与终止之间退出时不误报失败；
- Windows TCP 表短暂保留已退出 PID 时不误报失败；
- `pending` 激活失败时保留升级事务并记录错误；
- 桌面更新面板展示延迟恢复错误，同时不阻塞主界面加载。

全量 Vitest 首轮还暴露了既有 `AccountsView.selectAllResults.spec.ts` 未卸载 Vue wrapper 的测试污染：上一个用例的异步表格刷新会在后续 mock 重置后继续执行并产生未处理拒绝。两个用例补充 `wrapper.unmount()` 后，目标文件与全量前端测试均通过；该变更只修复测试生命周期，不改变账号选择业务逻辑。

现场使用故障原状态验证修复程序：

1. 修复程序识别并停止孤儿内核 PID `35116`；
2. 正常激活 `pending` 的官方 `0.1.172 / 155c4949...` 内核；
3. `state.json.pending` 清空，原 `0.1.171` 转入 `previous`；
4. 新受管内核监听 `127.0.0.1:18765`；
5. `/setup/status` 返回 HTTP 200 和 `needs_setup=false`；
6. 桌面进程保持运行，不再出现 `Failed to setup app` 或 `os error 5` 闪退；
7. 从版本面板实际点击“恢复桌面内置内核”后，活动身份切换为 `0.1.172 / f934079b15e7 / def306...`；
8. 原官方内核 `0.1.172 / 155c4949... / 7d09...` 完整进入 `previous`，`pending=null`、`pending_validation=false`、`last_error=null`；
9. 新受管内核重新监听 `18765`，健康接口返回 200，界面显示恢复进度 100% 且无错误警报。

发布验收还必须使用正式 `custom-protocol` 构建启动窗口，并实际执行一次“恢复桌面内置内核”，确认提交和 SHA-256 切换、健康检查以及回滚记录均正确。

## 5. 发布检查表

- [x] Rust 单元测试和格式检查通过（18 项）。
- [x] 桌面更新中心组件测试通过（3 项）。
- [x] 前端类型、ESLint、全量 Vitest、桌面生产构建通过。
- [x] Go 全仓测试通过。
- [x] `cargo build --release --features custom-protocol` 的窗口加载内置界面。
- [x] 现场“恢复桌面内置内核”完成，状态文件和监听进程一致。
- [x] 提交并推送源码，创建 `v0.2.12` 标签。
- [x] Desktop Release 工作流成功，Release 包含 NSIS、签名、`latest.json` 和 SHA-256。
