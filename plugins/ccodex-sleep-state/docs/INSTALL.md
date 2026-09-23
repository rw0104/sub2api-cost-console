# 安装与运维

## 前置条件

- 推荐使用已修复插件密钥持久化的桌面 0.3.4 / 内核 0.2.7；
- 内核扩展 1.3.0，并确认已注册 `openai.oauth.protection_transport.v1`；
- OpenAI OAuth 账号；
- process 模式。当前保护传输不能运行在无网络的 container 模式；

## 安装

1. 在「插件管理」中选择「安装插件」，上传 `.s2plugin`，不要上传源码 ZIP 或 runtime 单文件。
2. 核对插件 ID、版本、权限、SHA-256 和发布者公钥指纹。
3. 首次遇到发布者时勾选信任并确认安装。包内公钥不会绕过宿主信任流程。
4. 打开配置页填写来源，点击「保存并获取节点」，再点击逐节点「测试」或「测试全部节点」。停用状态也能操作。
5. 在「请求头保护」点击「一键启用保护」，按需选个人/团队、固定使用/主备模式。插件启用且真实 OAuth 请求命中保护路由后，再刷新运行状态查看 state；停用时没有运行会话。
6. 先绑定一个测试账号、测试用户或测试分组，灰度比例从小范围开始。
7. 确认请求通过保护传输后，再逐步扩大路由范围。

## 配置

| 字段 | 默认值 | 范围/说明 |
| --- | ---: | --- |
| `enabled` | `false` | 插件策略总开关 |
| `inject_state` | `false` | 是否注入内存中的有效 state |
| `harvest_on_demand` | `false` | 缓存缺失时是否先发起有限探测 |
| `fail_closed` | `false` | 没有有效 state 时是否在上游前返回本地错误 |
| `proxy_urls` | `[]` | 最多 256 个 `http`、`https`、`socks5`、`socks5h` URL；重复项会去重，HTTP/HTTPS 支持 URL 内凭据 |
| `direct` | `false` | 是否把直连加入 route pool；没有任何代理 URL 时仍会使用直连作为安全默认路由 |
| `proxy_url` | 空 | 旧版单出口字段；解析时迁移为单元素 `proxy_urls`，新配置建议使用数组 |
| `state_ttl_seconds` | `3600` | 60–86400 秒 |
| `refresh_before_seconds` | `600` | 必须小于 state TTL |
| `cooldown_seconds` | `180` | 401/403/429 冷却，1–3600 秒 |
| `subscription_refresh_seconds` | `900` | 订阅 route pool 的惰性刷新周期，60–86400 秒；全池失败时提前重载 |
| `probe_timeout_seconds` | `25` | 1–120 秒 |
| `response_header_timeout_seconds` | `120` | 1–120 秒 |
| `max_body_bytes` | `67108864` | 1024–134217728 字节；升级保留显式旧值，仍受宿主自身请求上限约束 |
| `account_mode` | `auto` | 自动采用令牌套餐提示，或手动 `personal` / `team` / `custom` |
| `state_refresh_mode` | `on_demand` | 新配置默认拿到后固定使用；旧配置迁移保留 `standby` 主备行为 |
| `max_probes_per_round` | `6` | 1–20，整个插件进程共用一个采集槽；不是逐个扫描整份订阅 |
| `probe_round_seconds` | `20` | 1–60，首次请求整轮同步采集预算，给正式请求留出时间 |
| `egress_mode` | `state` | 正式请求沿用 state 出口，或显式 `random` / `fixed` |
| `egress_route` | 空 | 独立固定的正式出口 ID，与固定采集出口分开 |
| `pool_enabled` | `false` | 控制已用/失败节点生命周期；界面可回收、停用 |
| `zstd_window_mib` / `compact_limit_mib` | `64` | 16–128 MiB，解压窗口与压缩回复上限 |
| `state_target_length` | `292` | 必须对应完整加密封装块数；生产经验形状通常为 292 或 332 |
| `models` | Astra/Sol/Terra | 1–32 个唯一模型名 |
| `accounts` | `{}` | 最多 1000 个账号 ID 覆盖；布尔字段显式覆盖，省略字段继承全局配置 |

配置由宿主加密保存。插件只读取显式指定的代理/订阅环境变量和文件，不读取 `auth.json`、Codex `config.toml` 或 Cookie；OAuth 凭据只由宿主在出站请求中转发。宿主不自动传递终端环境变量，推荐直接填写代理和订阅 URL。

节点表显示名称、协议、延迟、HTTP 状态和安全错误码。点击「使用此节点」即可固定采集出口，正式请求默认跟随；独立出口可在其下单独选择。每轮连接诊断最多 12 秒，未完成项显示“请重试”；403/429 是目标限制访问，不等同于代理未连通。测试结果在配置页重新打开后保留，来源变化后重新获取。手动采集 state 最多使用本轮 25 秒 UI 操作预算，消耗模型额度；状态查看不采集。

运行会话只存在于处理实际请求的进程中，状态快照标有读取时间。节点池记录跨版本保存在插件 ID 目录的 `.state/routepool.json`；写入失败会报错，不能把文件损坏当作空池。详见 [核心说明](CORE.md)。

账号覆盖通过配置页的「高级 JSON 与账号覆盖」编辑。键必须是无前导零的正整数账号 ID，例如：

```json
{
  "accounts": {
    "42": {
      "enabled": true,
      "inject_state": true,
      "proxy_urls": ["socks5h://127.0.0.1:1080"],
      "direct": false,
      "models": ["gpt-6-astra"]
    }
  }
}
```

## 停用与回滚

- 先在宿主停用插件，再切换其它传输或账号路由；停用不会删除配置。
- 升级时保持插件 ID、能力、权限和作用域不变；增加权限必须停用并重新安装。
- 升级失败时宿主应保留旧进程和旧配置，使用宿主「回滚」恢复最近的签名包。
- 卸载前必须停用；卸载会删除该插件的配置和历史，内存 state 在进程退出时丢失。

## 当前限制

本版本使用 Mihomo v1.19.31 支持 Clash/Mihomo YAML、逐行 URI、Base64、AnyTLS、SS、SSR、VMess、VLESS、Trojan、Hysteria、Hysteria2 和 TUIC。「保存并获取节点」下载并解析来源；「测试全部节点」或逐节点「测试」执行 OpenAI HTTPS 连通性测试。仍不包含 Codex 配置接管、独立本地管理端口、后台 worker、API Key/relay 或生产计费逻辑。state 长度是经验校验，不是模型质量或额度保证。
