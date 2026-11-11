# 旧接口

0.1 的 `/api/*` 在 0.2 删了。现在的接口见 `docs/api.md`（`/v1/internal/*`）和 `docs/satori.md`。这里只记还留着什么、删了什么。

## 暂未移除

装在别的机器上的程序不会自己更新，下面三条在 HTTP 上还能用，就是 `/v1/internal/` 下同名的路由（`internal/server/legacy.go`）。旧版执行端和插件都更新后删掉。

| 旧路径 | 现在 | 谁还在用 |
|---|---|---|
| `POST /api/pair`、`GET /api/pair/{请求}` | `/v1/internal/pair` | 旧版 `ir join`、dsh 插件 |
| `GET /api/exec` | `/v1/internal/exec` | 旧版执行端 |
| `POST /api/cli` | `/v1/internal/cli` | 旧版执行端上会话里的 `ir` |

其余 `/api/*` 一律 404，unix socket 上也是。

## 已删

- 其余 `/api/*`：同名搬到 `/v1/internal/*`。
- `GET /api/events`（SSE）：网页改用 `/v1/events` 的 WATCH 和 STATE。
- 聊天层（`/api/chat/*`、`/api/bot`、跟随、聊天指针）：应用改用 Satori with ir。
- `ir token`、`/api/exec/pair`：配对是唯一入口。
