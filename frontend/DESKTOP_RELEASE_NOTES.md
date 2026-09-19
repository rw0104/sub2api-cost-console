# Sub2API Cost Console v0.3.1

## 主要更新

- 兼容内核从上游 Sub2API `v0.2.5` 更新到 `v0.2.7`，保留成本扩展、插件发布者信任和 OAuth 保护传输能力。
- 接入上游 Seedance（火山方舟）原生异步视频任务 API，并保留任务创建、查询、删除和用量计费链路。
- 接入上游通用插件宿主服务：插件可在隔离的命名空间使用 KV 存储；声明 OpenAI OAuth 能力的插件可按权限访问账号目录与出站身份。
- 保留 Windows 原生一键启动、桌面/内核独立更新、内核身份校验、健康检查和回滚。

## 当前兼容内核

- 上游 Sub2API：`v0.2.7`
- 上游提交：`aea725f2ea644d5592d0bbb1d63b607efa7e200a`
- 本地扩展：`v1.3.0`
- 成本算法：`v1.6.1`
- 必需能力：`account_cost_loss_ledger.v1`、`account_economics_sampling.v1`、`plugin_extensions.v2`、`openai.oauth.protection_transport.v1`、`plugin_publisher_trust.v1`

## 技术变更

- 合并上游 v0.2.7 的 Seedance 原生视频任务、Antigravity/Kimi/Codex/DeepSeek 兼容修复和插件 HostService API。
- HostService 通过 go-plugin broker 协商，旧插件未实现时优雅降级；KV 命名空间绑定插件 ID，并限制键、值、TTL 和列表大小。
- 账号目录只对清单声明 OpenAI OAuth 出站能力的插件注入，宿主仍保留本地 v2 扩展 Host API 的权限、密钥信封和沙箱策略。
- 版本元数据、上游提交、能力清单和内核 `--version` 输出在打包前一致性校验。

## 升级行为

- 使用本正式桌面安装包升级即可；启动时会用扩展 1.3.0 替换缺少成本或插件能力的旧受管内核。
- 已安装插件和数据库配置会保留。上游插件协议新增能力是可选协商，旧插件不会因未实现 HostService 而无法启动。
- 兼容内核发布到稳定通道后，桌面会自动下载并校验，在下一次安全启动切换；失败会保留当前可用版本并提供回滚。

## 验证结果

- 上游 v0.2.7 tag 与提交 `aea725f2ea644d5592d0bbb1d63b607efa7e200a` 已核对，合并冲突逐文件审查并保留本地扩展。
- Go 全仓编译门禁通过；带 `unit` 标签的 Seedance、通用 HostService、broker 往返和现有 v2 宿主测试通过。
- 前端生产构建、Tauri 启动/回滚合同和 NSIS 安装包将在本次产物构建阶段完成。

## 回滚说明

- 上游内核可通过「版本与更新」回滚到上一版；数据库迁移和本地插件配置不会因回滚二进制自动撤销。
- 如需回退桌面版本，请先备份数据库和 `%APPDATA%\\com.sub2api.cost-console` 配置目录。
- Windows 当前构建若未提供发布私钥，只能作为本地验证包使用；正式公开发布必须使用既有 updater 私钥生成 `.sig`，不得轮换公钥替代。
