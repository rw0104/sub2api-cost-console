<div align="center">

<img src="assets/cost-console-logo.png" alt="Sub2API Cost Console Logo" width="144" />

# Sub2API Cost Console

**面向 Sub2API 的 Windows 桌面成本运营控制台**

[![Windows](https://img.shields.io/badge/Windows-10%2F11-0078D4?logo=windows)](https://github.com/rw0104/sub2api-cost-console/releases/latest)
[![Tauri](https://img.shields.io/badge/Tauri-2-24C8D8?logo=tauri)](https://tauri.app/)
[![Vue](https://img.shields.io/badge/Vue-3-4FC08D?logo=vuedotjs)](https://vuejs.org/)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/License-LGPL--3.0--or--later-blue)](LICENSE)

[下载 Windows 版本](https://github.com/rw0104/sub2api-cost-console/releases/latest) · [完整开发文档](COST_CONSOLE_DEVELOPMENT.md) · [上游项目](https://github.com/Wei-Shaw/sub2api)

</div>

> [!IMPORTANT]
> **这是社区维护的非官方衍生项目。** 本仓库基于 [Wei-Shaw/sub2api](https://github.com/Wei-Shaw/sub2api) 开发，不代表上游作者或维护者发布的官方桌面版本。Sub2API 原始项目、原始代码及其版权归上游作者和贡献者所有；本仓库保留上游的 LGPL-3.0-or-later 许可证和版权声明。

## 项目简介

Sub2API Cost Console 是带本地 Go 内核的原生 Windows 桌面程序，不是把管理页单独部署成网站。它将 Sub2API 的网关、账号管理和真实 `usage_logs` 运营数据整合到同一个桌面窗口，重点解决“账号加入后花了多少钱、具体模型产生了多少 API 价值、哪个上游最稳定、号池还能使用多久”这类运营问题。

桌面程序使用 Tauri 2 承载 Vue 3 界面，并管理本地 Go 内核。首次启动时会检查 `127.0.0.1:18765`：如果已有兼容的 Sub2API 服务则直接连接，否则启动安装包内置的受管内核。受管配置和业务数据保存在 Windows 用户应用数据目录，升级桌面程序时不会覆盖。

本项目适合：

- 维护多个 Codex、Grok 或其他上游账号的个人及团队。
- 需要把采购成本、API 产出、请求质量和账号状态放在同一个界面的运维人员。
- 需要对成本算法、软件版本和内核更新进行可追溯管理的部署场景。

成本分析不只面向 Codex 和 Grok。只要请求经过 Sub2API 并产生真实用量记录，控制台就会按实际模型、渠道、账号、入站接口和上游接口归集；OpenAI、Anthropic/Claude、DeepSeek、Gemini、xAI/Grok 以及 OpenAI 兼容中转站都使用同一套可追溯统计链路。无法返回 Token 或真实计费信息的中转站会明确显示数据缺口，不会根据账号名称猜测成本。

## 核心能力

### 三个成本运营面板

| 工作区 | 主要用途 | 关键指标 |
| --- | --- | --- |
| 资产与实时成本 | 查看全局资产、消耗速度和可用时间 | 采样速率、滚动速率、实时成本、滚动成本、余额、TTFT、成功率 |
| 上游质量与排行 | 比较账号质量并定位异常上游 | 综合评分、失败率、切号恢复、产出、Token、探测成本、账号状态 |
| OAuth 号池成本 | 衡量不同套餐号池的投入产出 | 采购成本、平均单价、实时/预期成本、API 美元产出、请求数、Token |

支持 `Ctrl/Cmd + 1/2/3` 快速切换工作区。面板数据按周期自动刷新，敏感账号信息在展示和截图中应当脱敏。

### 号码加入即开始计算成本

系统默认使用账号的 `created_at` 作为成本起算时间。因此，号码或账号写入 Sub2API 后即开始累计采购成本，不需要额外启动计时器。

```text
hourly_rate = amount / billing_cycle_hours
elapsed_hours = max(0, now - started_at) / 3,600,000
accrued_cost = hourly_rate × elapsed_hours
```

支持 `hourly`、`daily`、`weekly`、`monthly` 和 `one_time` 五种计费周期。自定义成本配置写入 `account.extra.cost_profile`，不会覆盖账号已有的 `extra` 数据。默认月成本、套餐识别优先级、汇率和边界条件参见[成本模型文档](COST_CONSOLE_DEVELOPMENT.md#6-成本模型)。

每项指标的实测来源、推导公式、当前号池与历史数据边界参见[成本数据真实性与口径](docs/COST_DATA_PROVENANCE.md)；三层模型身份、不一致筛选和成本快照边界参见[上游响应模型审计与成本归因](docs/UPSTREAM_RESPONSE_MODEL_AUDIT.md)。

### 模型、渠道与实时价格目录

- 模型和渠道只从真实路由与 `usage_logs` 展示，不预置“只有 Codex / Grok”的假筛选项。
- 每条请求区分“用户请求模型、实际发往上游模型、上游响应声明模型”；可筛选仅看不一致，识别中转站自行替换或降级模型。响应声明只用于审计和分组，历史成本仍使用请求发生时的价格快照。
- 官方按量 API 使用模型输入、输出、缓存创建和缓存命中 Token 分别核算；DeepSeek、Claude 等无需填写月费成本档案。
- OpenAI 兼容中转站返回完整 usage 时，按实际上游模型、Token 和账号/渠道自定义价格核算；缺少 usage 或真实模型时明确标记数据缺口。
- 默认远程价格源为 LiteLLM 社区聚合目录，后端自动下载间隔为 24 小时；离线时使用本地缓存和随安装包发布的兜底目录。生产结算可用渠道合同价覆盖目录价，历史记录保留请求发生时的价格快照。
- 美元/人民币汇率使用 12 小时本地缓存，并显示来源与时间；网络源和有效缓存均不可用时回退到内置参考值。

### 历史统计与可观察性

- 观察窗口支持最近 1 分钟、5 分钟、30 分钟、1 小时、6 小时、24 小时、7 天、1 个月和本地自然日。
- 趋势图使用真实零桶、自适应采样粒度和滚动均值，短窗口无请求时会明确显示空窗，不伪造数据。
- 账号删除后不再级联删除历史 `usage_logs`；排行榜只显示现有账号，历史成本、请求和模型统计仍可追溯。
- K-12 账号可配置模型白名单；遇到 `Selected model is at capacity` 时进入账号故障转移，而不是停止整条请求链路。

### 可追溯的三版本体系

| 版本 | 当前值 | 作用 |
| --- | --- | --- |
| Desktop | `0.2.20` | Tauri 2 桌面壳、标题栏安全区、现代选择器和安装结构；上游内核与成本扩展独立更新、能力握手和自动安全暂存 |
| Core | `0.1.173` | 当前桌面包绑定的 Sub2API 上游基线（上游提交 `29009f0b2ea14edf3b11ae2564fb617ff91a03b4`） |
| Algorithm | `1.6.0` | 持久化经济采样、稳定区间速率、单位经济性与预测置信度；Token 单价未调整 |

每条成本档案记录其算法版本。旧数据如果没有版本会显示为 `legacy-unversioned`，不会被静默归类为当前算法。

> 版本边界：`Desktop` 检查本仓库的签名 Release；`Core` 显示当前实际运行的 Sub2API 版本和真实提交号，并检查公开的 `Wei-Shaw/sub2api` Release；`Algorithm` 是本项目成本规则版本，不冒充上游版本。

### 双通道签名更新

- **桌面整包通道**：从 `rw0104/sub2api-cost-console` 的公开 Release 读取 `latest.json`，只安装与内置公钥匹配的 Tauri 签名安装包。
- **官方内核通道**：独立扫描公开的 `Wei-Shaw/sub2api` Release，桌面壳不变时可只切换 Go 内核。
- **统一更新入口**：启动约 2 秒后检查一次，此后每 6 小时检查；用户也可从右下角“版本与更新”手动触发。
- **用户确认安装**：发现新版后由用户点击下载，避免工作中被强制重启。
- **完整性校验**：只接受固定上游仓库的 Windows x64 资产，校验上游 `checksums.txt`、SHA-256、内核 `--version` 和真实提交号。
- **失败回滚**：保留上一版内核；新内核必须在 30 秒内通过健康检查，否则自动恢复上一版本。
- **内置内核恢复**：活动内核与安装包内置内核按“版本＋提交＋SHA-256”比较；身份不同时明确让用户选择恢复或保留，恢复过程安全停止、校验、启用并在失败时回滚，不要求手工删除 `core\active`。
- **明确完成反馈**：内核恢复到 100% 后显示“内核已完成更新”，并确认健康检查已经通过，不再停留在“正在执行健康检查”。
- **Windows 窗口与托盘**：成本中心全屏按钮和 `F11` 可正常切换；最小化或关闭主窗口会收进系统托盘，单击托盘图标恢复，右键菜单可明确退出后台服务。
- **网络自适应**：更新器跟随 Windows 系统代理；受管内核按显式环境变量、系统手动代理、TUN/直连的实际状态启动，不写死代理端口。
- **统计兼容**：如果官方上游内核不支持分钟级 Dashboard 参数，控制台从真实 `usage_logs` 聚合精确窗口；超过安全行数上限时明确报错，不显示不完整结果。

更新程序不会对源码目录执行 `git pull`，不会把 GitHub Token 写入客户端，也不会执行两个固定更新来源之外的资产。

## 系统架构

```mermaid
flowchart LR
    Operator["Windows 运维人员"] --> Desktop["Tauri 2 桌面程序"]
    Desktop --> UI["Vue 3 成本控制台"]
    Desktop --> Core["受管 Go 内核 :18765"]
    UI --> API["Sub2API 管理 API"]
    Core --> PG["PostgreSQL"]
    Core --> Redis["Redis / Valkey"]
    Core --> Providers["Codex / Grok / 其他上游"]
    Upstream["Wei-Shaw/sub2api Releases"] -->|官方 checksums + SHA-256| Core
    Release["本项目 GitHub Releases"] -->|Tauri 签名 + latest.json| Desktop
```

| 层级 | 技术 | 职责 |
| --- | --- | --- |
| 桌面宿主 | Tauri 2、Rust、WebView2 | 窗口、受管内核生命周期、更新、回滚 |
| 前端 | Vue 3、TypeScript、Pinia、Chart.js | 三套面板、成本模型、图表和版本面板 |
| 后端 | Go、Gin、Ent | 鉴权、账号、调度、统计和 API 网关 |
| 数据层 | PostgreSQL、Redis/Valkey | 持久化、缓存和运行状态 |
| 发布 | GitHub Actions、Tauri NSIS、上游 Release API | 签名桌面安装包；官方上游内核扫描、校验和回滚 |

## 安装与使用

### 使用 Windows 安装包

1. 打开 [Releases](https://github.com/rw0104/sub2api-cost-console/releases/latest)。
2. 下载最新的 Windows NSIS 安装包，并使用同一 Release 中的 `INSTALLER_SHA256SUMS.txt` 核对文件。
3. 安装向导允许选择安装目录，并在完成页选择是否创建桌面快捷方式；安装范围为当前 Windows 用户，不要求管理员权限。
4. 安装并启动 Sub2API Cost Console。
5. 首次启动先选择数据服务方式：
   - **快速安装（推荐新用户）**：需要 Docker Desktop 已安装且引擎正在运行。程序自动创建固定版本的 PostgreSQL 与 Valkey 容器、随机密码和独立数据卷，只映射到 `127.0.0.1:15432` 与 `127.0.0.1:16379`。
   - **高级连接**：连接已有的本地、Docker、NAS、服务器或云端 PostgreSQL 15+ 与 Redis 7+/Valkey。已经有数据库容器的用户应选择此模式，程序不会覆盖或删除现有数据。
6. 如果未检测到可用 Docker、Docker 未启动、保留端口冲突或发现同名容器，快速安装会停止并明确引导到高级连接；程序不会静默安装 Windows 服务或不受支持的 Valkey 替代品。
7. 在首次启动向导中完成连接验证和初始管理员配置。
8. 登录后进入成本控制台；新增上游账号后，成本从账号加入时间开始累计。

快速安装下载并固定使用 `postgres:16.14-alpine` 与 `valkey/valkey:8.1.9-alpine`。Docker 镜像、容器和数据卷独立于桌面程序升级；卸载桌面程序不会自动删除业务数据。

桌面内核默认只监听：

```text
http://127.0.0.1:18765
```

如果端口已存在兼容的 Sub2API 服务，桌面端会复用该服务而不重复启动受管内核。

### 从源码运行

开发环境需要 Windows 10/11、Node.js 24、pnpm 9、Go 1.26、Rust stable、MSVC 构建工具、WebView2、PostgreSQL 15+ 和 Redis 7+。

```powershell
git clone https://github.com/rw0104/sub2api-cost-console.git
cd sub2api-cost-console/frontend
corepack pnpm@9 install --frozen-lockfile
corepack pnpm@9 desktop:dev
```

构建 NSIS 安装包：

```powershell
cd frontend
corepack pnpm@9 desktop:build
```

主要输出位于：

```text
frontend/src-tauri/target/release/sub2api-cost-console.exe
frontend/src-tauri/target/release/bundle/nsis/
```

## 配置与数据

- 桌面 API 基址默认值：`http://127.0.0.1:18765/api/v1`。
- 服务只绑定回环地址，不应直接暴露到公网。
- Tauri WebView 来源白名单为 `http://tauri.localhost`、`https://tauri.localhost` 和 `tauri://localhost`。
- 数据库密码、Redis 密码、管理员凭据和上游 Token 不得提交到仓库。
- 受管配置位于 Windows 用户应用数据目录，不写入 `Program Files`。
- 自定义成本位于账号的 `extra.cost_profile` 中。

## 开发与测试

```powershell
cd frontend

# 类型检查
corepack pnpm@9 typecheck

# 成本模型及前端测试
corepack pnpm@9 test:run

# 代码检查
corepack pnpm@9 lint:check
```

后端测试：

```powershell
cd backend
go test ./...
```

修改以下规则时必须同步提升 `frontend/ALGORITHM_VERSION`、补充测试并在 Release Notes 中说明生效边界：

- 计费周期折算小时数。
- 人民币/美元换算规则。
- 账号开始计费时间。
- 累计成本公式和一次性费用边界。
- 账号固定订阅采购与 API 按量成本的分类规则。
- 模型目录价、渠道自定义价和账号成本倍率的优先级。

更完整的目录说明、API、打包、更新、故障排查和发布流程见 [COST_CONSOLE_DEVELOPMENT.md](COST_CONSOLE_DEVELOPMENT.md)。

## 发布流程

当前版本已恢复桌面在线更新。推送与 `tauri.conf.json` 版本一致的 `v*` 标签，或手动运行 `Desktop Release` 工作流，会生成 NSIS 安装包、Tauri 签名、`latest.json` 和 SHA-256 校验文件。客户端匿名读取公开 Release，不需要 GitHub Token。

内核更新始终直接读取官方上游：

- `https://api.github.com/repos/Wei-Shaw/sub2api/releases/latest`
- 对应 Release 的 `checksums.txt`
- 对应 Windows x64 `sub2api_<version>_windows_amd64.zip`

`createUpdaterArtifacts` 当前为 `true`。发布构建必须在 GitHub Actions 中配置 `TAURI_SIGNING_PRIVATE_KEY`，本地发布构建也必须通过同名环境变量提供私钥。Tauri 更新签名用于防止安装包被替换；它不等同于 Windows Authenticode 代码签名，如需消除 Windows 发布者警告，仍应单独配置受信任的 Authenticode 证书。

## 与上游同步

本项目保留上游 remote，并在合并后重新执行前端、后端、成本算法和桌面构建测试。

```powershell
git remote add upstream https://github.com/Wei-Shaw/sub2api.git
git fetch upstream
git merge upstream/main
```

同步冲突需要逐项检查，尤其不要直接覆盖 `frontend/src/features/cost-center/`、`frontend/src-tauri/`、自动更新工作流和成本版本文件。

## 上游、许可证与版权

### 上游项目

- 项目：[`Wei-Shaw/sub2api`](https://github.com/Wei-Shaw/sub2api)
- 说明：AI API Gateway Platform for Subscription Quota Distribution
- 许可证：[GNU Lesser General Public License v3.0 or later](LICENSE)
- 上游 README 版权声明：`Copyright (c) 2026 Wesley Liddick`

### 版权归属

- Sub2API 原始项目、原始源代码、原始文档和原始品牌资产的版权归 **Wesley Liddick、Wei-Shaw/sub2api 贡献者及各自权利人**所有。
- 本仓库新增的桌面成本控制台、成本算法扩展、更新与回滚实现、文档以及原创 Logo 的版权归 `rw0104/sub2api-cost-console` 贡献者所有。
- 本仓库不主张拥有上游项目的版权，也不暗示得到上游作者的官方背书或合作授权。
- 对上游代码的复制、修改和分发继续受 LGPL-3.0-or-later 约束；第三方依赖和品牌分别遵循其自身许可证与权利声明。

完整文本见 [LICENSE](LICENSE)，补充归属说明见 [NOTICE.md](NOTICE.md)。分发二进制或修改版本时，请同时保留许可证、版权通知、源代码获取方式以及 LGPL 要求的其他材料。

## 使用风险与免责声明

使用本项目连接第三方服务可能受到相应服务商条款、地区法律和账号政策的限制。使用者应自行阅读并遵守相关协议和法律法规，并自行承担账号限制、服务中断、数据丢失或其他风险。本项目仅按“现状”提供，不承诺适用于任何特定用途。

---

<div align="center">

Sub2API Cost Console 是基于 [Wei-Shaw/sub2api](https://github.com/Wei-Shaw/sub2api) 的社区衍生项目。

</div>
