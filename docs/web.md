# 网页（ir_web，`internal/webapp`）

网页是内置的应用，单独一层，站在核心（`docs/gate.md`）前面，同一个进程。浏览器才需要的都在这里：Passkey、Cookie、邀请、首次设置链接、公网地址和它的 Host/Origin 检查、private 缓存、网页文件本身。

## 开和关

- 开：设了公网地址（`ir public`）。关：没设（`ir public off`），只能在主机的 CLI 上（决定 3）。不另设开关。
- 关着时：TCP 上不出登录页（`/_ir/login`、`/_ir/join` 跳回 `/`），不收 `/_ir/api/*` 的 Passkey 请求，不认 Cookie；本机浏览器是主机的页面，查过 Host（只认回环名字和 `hosts`）和 Origin 后经 `gate.AsHost` 交给核心。带 TOKEN 的照样进。
- 开着时：浏览器要 Cookie；回环也一样（cloudflared 从回环连进来）。Host 只认公网地址的主机名；带 Bearer TOKEN 的另认回环名字。

## 一个请求怎么走

| 请求 | 去向 |
|---|---|
| `/_ir/*`（头像除外） | 网页层自己答：页面、`/_ir/health`、`/_ir/api/session`、Passkey 流程、登出 |
| 带 `Authorization: Bearer` | 原样交给核心（应用、执行端 `/v1/internal/exec`、远端 `ir` `/v1/internal/cli`、配对、`/v1/events`），公网模式下响应改 private |
| 网页关着 | 查 Host/Origin 后经 `gate.AsHost` 算主机 |
| Cookie 有效 | 去掉 Cookie，设 `Authorization: Bearer <ir_web 的 TOKEN>`、`Satori-Platform: ir_web`、`Satori-User-ID: <登录 ID>`（浏览器自己带的这几个头一律替换），交给核心 |
| 都没有，路径是开放的 | 不带身份交给核心（配对、`/v1/events`）或网页文件（图标、manifest） |
| 其余 | 页面导航 302 到 `/_ir/login?next=…`，别的 401 |

`/v1/internal`、`/v1`、`/_ir/avatar/` 交给核心，别的路径是网页文件（只给主机的页面和登录了的浏览器）。

转发的请求网页层自己记着：登出、会话过期、登录被删时取消它们的 context，事件流和 WebSocket 跟着断；换公网地址时旧的那层全部取消。

## Passkey 和 Cookie

- Passkey 的 rpID 是公网地址的主机名：换域名，所有 Passkey 作废。
- 每次注册用新的随机 user handle、不带 excludeCredentials；登录用可发现凭证，不把凭证 ID 发给未登录的人；计数器回退就拒绝。
- Cookie `__Host-ir`：HttpOnly、Secure、SameSite=Strict、Path=/。空闲 12 小时、最长 7 天。库里只存哈希（`web_sessions`）。
- 非安全方法和 WebSocket 升级：Origin 必须是公网地址，`Sec-Fetch-Site` 有的话必须是 `same-origin`；本机模式 Origin 的主机要等于 Host，都没带（curl）就放行。
- 失败限速（坏邀请、验证失败）：同一客户端 15 分钟 10 次，锁 15 分钟。
- 自己的响应 `no-store`，页面 CSP 只许本站脚本、样式，禁 iframe。

## 邀请

- 新登录只能靠一次性邀请：`https://<域名>/_ir/join#<token>`，10 分钟；`#` 后面不进服务器日志。打开后起名、建 Passkey，成功才用掉邀请，同时在核心里建登录并批准（`CreateWebLogin`），再调 `OnJoin`（第一个登录是本人的，给 Claude；邀请带的权限给新登录）。
- 邀请占着的名字别的登录不能用；邀请可按 ID 或名字撤掉（`ir apps rm`）。
- 首次：开着且还没有登录时，启动会在状态目录写 `setup-token`（0600，不过期的 join 链接）；每次重启换新的，有了登录就删掉。
- 最后一个有 Passkey 的登录删不了（`MayRemove`，409 `last_passkey`）。

## 存储

`Store`：`Creds`（登录的 user handle 和 Passkey，存在 `logins` 那一行的 `user_handle`、`passkeys` 两列）、`invites`、`web_sessions`。登录那一行是核心的，Passkey 跟着它走。

## 路由（都在 `/_ir/` 下）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/_ir/login`、`/_ir/join` | 页面，网页关着时跳回 `/` |
| GET | `/_ir/gate.js`、`/_ir/gate.css` | 页面资源 |
| GET | `/_ir/health` | `{"ok":true}`，`ir public` 测通用 |
| GET | `/_ir/api/session` | `{authenticated, name, local, setup}` |
| GET | `/_ir/avatar/<id>` | 头像（核心答） |
| POST | `/_ir/api/join/check` | `{token, name?}` → `{name?, nameError?}` |
| POST | `/_ir/api/join/options`、`/verify` | 注册 Passkey，成功下发 Cookie |
| POST | `/_ir/api/login/options`、`/verify` | 登录 |
| POST | `/_ir/api/logout` | 结束本会话，断开它转发的连接 |

`/_ir/` 下的 WebSocket 一律 404。
