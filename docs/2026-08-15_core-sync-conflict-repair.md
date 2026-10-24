# Compatible Core Auto Update 冲突修复记录

## 结论

GitHub Actions 的 `Compatible Core Auto Update` 在上游发布 `v0.1.177` 后连续两次失败，根因是合并门禁阻断了 `backend/internal/service/account_test_service.go` 的未知冲突。构建、Rust 测试、前端测试、签名和 GitHub Release 权限都没有执行到。

## 现场证据

| 项目 | 结果 |
| --- | --- |
| 上游版本 | `v0.1.177` |
| 上游提交 | `073e92d17178a1ccdb0a27017f572f10c9c7ab62` |
| 当前稳定清单 | `0.1.176` / 扩展 `1.1.1` |
| 失败运行 | [#88](https://github.com/rw0104/sub2api-cost-console/actions/runs/31888602599)、[#89](https://github.com/rw0104/sub2api-cost-console/actions/runs/31889795485) |
| 阻断 Issue | [#9](https://github.com/rw0104/sub2api-cost-console/issues/9) |
| 冲突文件 | `backend/cmd/server/VERSION`、`backend/internal/service/account_test_service.go` |

`backend/cmd/server/VERSION` 原本就在机械处理白名单中，Issue 文本列出的是全部冲突文件；真正触发 `unknown` 的文件是 `account_test_service.go`。

本项目在该文件中增加了 `rateLimitService`、429/402 处理和成本扩展逻辑；上游 `v0.1.177` 同时把 OpenAI compact 探测从 `/responses/compact` 改为带 `remote_compaction_v2` 的普通 `/responses` 流。直接选择 ours 会丢上游修复，选择 theirs 会丢成本逻辑。

## 修复内容

新增 `.github/resolve-core-merge.ps1`，从 Git index 的三方 stage 读取 ours/base/theirs，执行 `git merge-file --diff3`，并且只接受以下固定结构：

- 只有一个冲突块；
- ours 包含旧的 `compact-only mapping` 注释；
- theirs 包含 `remote compaction v2` 注释；
- 合并结果无任何冲突标记。

验证通过后，脚本保留统一的 `account.GetMappedModel` 代码，并将注释收敛为同时描述普通模型映射和 native remote compaction v2。任何未来代码形状变化都会返回失败，继续创建阻断 Issue，不会静默覆盖代码。

`.github/workflows/core-sync.yml` 仅在未知冲突集合恰好只包含该文件时调用脚本；`README.md` 和 `backend/cmd/server/VERSION` 的既有机械策略不变。

## 验证

- 使用上游 parent、`v0.1.177` 和当前成本扩展文件构造临时三方仓库，真实执行 `git merge-file --diff3`；结果只有预期注释冲突。
- 执行 resolver 后，未合并路径为空，冲突标记为空，同时检查 `rateLimitService` 与 `remote compaction v2` 均存在。
- 额外检查生成的 `testModelID` 映射语句紧邻 compact 路由分支，运行 `gofmt -d` 无格式差异；这避免了只清除冲突标记却生成无效 Go 代码。
- PowerShell 语法检查通过。
- `git diff --check` 通过。

这次修复只解决已确认的 `v0.1.177` 合并形状，不把任意业务文件加入自动覆盖白名单。下一次工作流运行仍需通过后端、桌面和前端完整门禁后，才会更新 `core-stable`。
