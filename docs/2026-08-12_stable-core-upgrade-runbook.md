# 稳定内核升级流程

## 目标

桌面客户端只从 `core-stable` 安装经过兼容验证和人工晋升的内核。上游新版本不会由定时任务直接合并到 `main`，也不会直接覆盖稳定通道。

## 通道与职责

| 通道 | 触发方式 | 写入目标 | 客户端可见 | 用途 |
| --- | --- | --- | --- | --- |
| 上游发现 | 每 6 小时 | 无 | 否 | 只比较 `frontend/CORE_VERSION` 与 Sub2API 最新 Release |
| 候选 | 自动 | `automation/core-<version>`、PR、不可变 prerelease tag | 否 | 合并、前后端与桌面全量测试、构建候选二进制与清单 |
| 稳定 | 人工 workflow dispatch + `core-stable` Environment 审批 | `core-stable` Release | 是 | 复验候选身份和哈希后晋升完全相同的二进制 |

## 自动候选阶段

工作流：`.github/workflows/core-sync.yml`

1. 读取最新官方 Sub2API Release。
2. 若该上游提交与扩展版本的不可变候选已存在，成功结束且不产生变更；若 `main` 已是该内核但候选缺失，则仅补建候选，不创建空 PR。
3. 在临时候选提交上执行非快进合并。
4. 只允许两个已审核的机械冲突策略：
   - `README.md` 保留成本控制台版本；
   - `backend/cmd/server/VERSION` 使用新上游版本。
5. 任意其他冲突立即停止，创建或复用阻断 Issue，稳定通道保持不变。
6. 写入 `CORE_VERSION`、`UPSTREAM_SUB2API_COMMIT`，运行：
   - 后端全量 Go 测试；
   - Tauri/Rust 激活、身份校验和回滚测试；
   - 桌面内核升级 UI 测试；
   - 前端生产构建；
   - sidecar `--version` 身份校验；
   - SHA-256 与不可变候选清单生成。
7. 推送 `automation/core-<version>` 并创建 PR。定时任务不会写 `main`。
8. 发布 `core-candidate-v<core>-ext<extension>-<upstream-commit>` prerelease，标签绑定上游提交且已存在时拒绝覆盖；后续定时任务等待人工审核，不重复生成候选。客户端不会读取这个通道。

## 审核门禁

合并候选 PR 前必须确认：

- PR 只包含预期的上游变化和本项目兼容扩展；
- 数据库迁移是向前兼容的，不删除现有字段或事实账本；
- 成本能力 `account_cost_loss_ledger.v1`、`account_economics_sampling.v1` 仍存在；
- `CORE_VERSION`、`backend/cmd/server/VERSION`、`UPSTREAM_SUB2API_COMMIT` 指向同一上游发行版；
- 后端、前端、Rust、构建检查全部通过；
- 在隔离数据目录启动候选内核，验证 `/health`、管理员登录、账号列表、一次只读成本快照和受控退出；
- 连续观察至少一个自动采样周期，没有 CPU、内存、日志量或请求延迟异常。

## 人工晋升

工作流：`.github/workflows/core-promote.yml`

1. 先合并候选 PR，使 `main` 成为已审核事实来源。
2. 手动运行 `Compatible Core Promote`，输入不可变候选 tag。
3. `core-stable` Environment 应配置 required reviewers；没有批准就不能写稳定 Release。
4. 普通晋升重新验证：候选版本、上游提交、扩展版本、算法版本、能力集合、清单与归档 SHA-256、二进制 `--version` 身份必须与 `main` 一致。
5. 任务只重写稳定通道 URL 和晋升元数据，上传的是候选阶段已经验证过的同一份 zip，不重新编译。

## 客户端激活与回滚

客户端继续使用已有的双槽机制：

1. 下载到 `pending`，验证 manifest、能力、归档哈希和二进制身份。
2. 不在下载时强制重启；下次安全启动前保留当前 `active`。
3. 激活时先复制当前内核到 `previous`，再替换 `active`。
4. 新内核启动后必须通过健康检查；失败则恢复 `previous` 并上报 `core-update-rollback`。
5. 稳定通道故障时，停止新的晋升；客户端已有版本不会因远端候选失败而变化。
6. 兼容更新器拒绝远端 SemVer 降级，因此不要靠改写稳定清单强制全局回退：启动失败会自动恢复客户端 `previous`；已成功启动但出现业务退化时，先停止晋升并指导受影响客户端使用“恢复上一版内核”，再发布版本号更高的前向修复候选。

## 发布后观察

晋升后至少观察一个业务高峰窗口：

- 桌面启动成功率、自动回滚事件和核心启动耗时；
- `/health` 失败、数据库迁移错误和端口占用错误；
- 请求成功率、429/402、TTFT P95 与总耗时 P95；
- `usage_logs` 写入、成本快照、经济采样连续性；
- CPU、常驻内存与日志增长率。

任何一项显著退化都停止后续晋升并回退到上一已验证候选。候选构建失败、PR 冲突或测试失败本身不会影响当前稳定客户端。

## 本次基线

- 原稳定内核：`0.1.173`
- 新候选基线：`0.1.176`
- 成本算法：`1.6.0`
- 成本扩展：`1.1.1`

本次实际合并只出现 `README.md` 与 `backend/cmd/server/VERSION` 两个预期冲突；这正是旧定时任务连续失败的原因，也是新流程将冲突策略显式化、同时禁止直接写稳定通道的依据。
