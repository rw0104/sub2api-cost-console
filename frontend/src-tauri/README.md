# Sub2API Cost Console（桌面版）

这是成本作战台的 Windows 桌面壳，前端仍复用 Sub2API 的鉴权和管理 API。

## 运行

1. 先启动 Sub2API 后端，桌面版默认地址为 `http://127.0.0.1:18765`。启动后端时设置 `SERVER_PORT=18765`（或在 `config.yaml` 中设置 `server.port: 18765`）。如果后端启用了 CORS 白名单，请把桌面 WebView 来源加入 `cors.allowed_origins`：

   ```yaml
   cors:
     allowed_origins:
       - http://tauri.localhost
       - tauri://localhost
     allow_credentials: true
   ```
2. 在 `frontend` 目录执行：

   ```powershell
   corepack pnpm@9 install --frozen-lockfile
   corepack pnpm@9 desktop:dev
   ```

3. 登录后会进入「成本作战台」，也可以使用 `Ctrl/Cmd + 1/2/3` 切换三个工作区。

桌面模式默认请求 `http://127.0.0.1:18765/api/v1`。如果后端在其他地址，可在启动前设置 `VITE_API_BASE_URL`，或在运行环境的 `localStorage` 写入 `sub2api.desktop.backendUrl`。

## 发布构建

```powershell
corepack pnpm@9 desktop:build
```

可执行文件输出到 `frontend/src-tauri/target/release/sub2api-cost-console.exe`。
