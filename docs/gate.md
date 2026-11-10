# Passkey 门（`internal/gate`）

照 Rhodium 网关（`rhodium-gateway`）的做法，改成 Go 的中间件，并登记应用和网页的登录。叫法见 `GLOSSARY.md`。

## 两种模式

- **只听本机**（没配 `public.origin`）：不登录。只查 Host（回环名字）和 Origin，请求都算主机。带 TOKEN 的请求照样认成那个应用。
- **上公网**（配了 `public.origin`）：所有网页请求都要网页登录的 Cookie 或应用的 TOKEN，回环也一样（cloudflared 从回环连进来）。Host 只认 `public.origin` 的主机名；带 Bearer TOKEN 的请求另认回环名字（主机上的应用），TOKEN 照样要对。

`public.origin` 必须是 https（开发时可用 `http://localhost:端口`）。它的主机名就是 Passkey 的 rpID：换域名，所有 Passkey 作废。

## 应用和登录

- 门里一张表：`Kind` 为 `panel` 的是网页（`ir_web`）的登录（Passkey），为 `channel` 的是应用（TOKEN）。存储上分成 `apps` 和 `logins` 两张表。主机 ID `local`，显示“主机”。
- `Identity{App, Login, Name, Session}`：`Host()` 主机，`Web()` 网页的登录，`ID()` 存权限、用量的 ID。别的应用的登录由请求头 `Satori-User-ID`（或 `X-Iridium-Login`）指明，服务端管批准。
- 名字：1–12 个字，字母、数字、空格和 `- _ .`；全角 ASCII 转半角，空格合并；不分大小写查重，应用之间、网页的登录和邀请之间各自不重名。保留 `host`、`主机`、`本机`、`ir_web`、`local`、`未知`、`unknown`，单个 ASCII 字母不能用（`C:` 是盘符）。`[ ] / : ·` 不收，因为名字要放进 `[名字]`、`应用/登录`、`应用名:路径`。
- 新的网页登录只能靠一次性邀请：`https://<域名>/_ir/join#<token>`，10 分钟。`#` 后面不进服务器日志；页面读到后立刻从地址栏去掉。打开后起名（边打边查重）、建 Passkey，成功才用掉邀请。
- 首次：上公网且还没有登录时，启动会在状态目录写 `setup-token`（0600，一条不过期的 join 链接）；每次重启换新的，有了登录就删掉。
- Claude（只给本人的）：登录加入后门调 `OnJoin(Joined{Endpoint, Perms, First})`。`First`：第一个登录；`Perms`：邀请带的权限（`ir invite --claude`）。
- 应用只经配对进来（`docs/api.md`“配对”）：`CreateApp(名字, 类型)` 给 TOKEN（`ir_` 开头），`RenewToken(id)` 换新 TOKEN（更新配对），旧连接断开。请求带 `Authorization: Bearer <TOKEN>`，不查 Origin。
- 每次注册用新的随机 user handle、不带 excludeCredentials（同步型 Passkey 提供方会互相覆盖或挡住新设备，Rhodium 踩过）。登录用可发现凭证，不把凭证 ID 发给未登录的人。计数器回退就拒绝。
- 通道：`CreateToken(名字)` 建通道终端并给 TOKEN（`ir_` 开头）；对已有通道再调一次就换 TOKEN，旧的连接断开。请求带 `Authorization: Bearer <TOKEN>`，不查 Origin（TOKEN 不是浏览器自动带的）。
- 名字和头像：`UpdateProfile`。头像收 PNG/JPEG/WebP，≤2MB，服务端居中裁方、缩到 128×128（不透明存 JPEG，透明存 PNG），照 JPEG 的 EXIF 方向转正。地址 `/_ir/avatar/<id>?v=<版本>`，带版本时可永久缓存（private）。

## 会话和防护

- Cookie `__Host-ir`：HttpOnly、Secure、SameSite=Strict、Path=/。空闲 12 小时、最长 7 天。库里只存哈希。
- 失败限速：同一客户端 15 分钟内失败 10 次，锁 15 分钟（坏邀请、坏 TOKEN、验证失败）。客户端地址只在对端是回环时才信 `CF-Connecting-IP`（可改成别的头）；IPv6 按 /64 算。
- POST 等非安全方法和 WebSocket 升级：Origin 必须是 `public.origin`，`Sec-Fetch-Site` 有的话必须是 `same-origin`。本机模式：Origin 的主机要等于 Host；都没带（curl）就放行。
- 门自己的响应 `no-store`，页面 CSP 只许本站脚本、样式（`require-trusted-types-for 'script'`），禁 iframe。
- 公网模式下，经过门的应用响应 `Cache-Control` 一律改成 `private`（去掉 `public`、`s-maxage`）：Cloudflare 默认按扩展名缓存，否则别人可能从边缘拿到登录后才能看的文件。
- 日志不写 TOKEN、Cookie、邀请链接；只写事件、终端 ID、名字、客户端地址。

## 路由（都在 `/_ir/` 下）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/_ir/login`、`/_ir/join` | 页面（同一个 HTML），本机模式跳回 `/` |
| GET | `/_ir/gate.js`、`/_ir/gate.css` | 页面资源 |
| GET | `/_ir/health` | `{"ok":true}`，`ir public` 测通用 |
| GET | `/_ir/api/session` | `{authenticated, name, local, setup}` |
| GET | `/_ir/avatar/<id>` | 头像，要登录 |
| POST | `/_ir/api/join/check` | `{token, name?}` → `{name?, nameError?}`；查邀请和名字 |
| POST | `/_ir/api/join/options`、`/verify` | 注册 Passkey，成功下发 Cookie |
| POST | `/_ir/api/login/options`、`/verify` | 登录 |
| POST | `/_ir/api/logout` | 结束本会话，断开它的连接 |

未登录时：浏览器打开页面 302 到 `/_ir/login?next=…`，其余 401 JSON。`/_ir/` 下的 WebSocket 一律 404。

## 接进 Iridium

已接上（`internal/server`）：

```go
g, _ := gate.New(gate.Config{PublicOrigin: cfg.Public.Origin, Store: st.Gate(), StateDir: cfg.StatePath(), Hosts: cfg.Hosts})
http.Serve(tcpListener, g.Wrap(mux))
// unix socket（本机 ir）不经过门：在 handler 外层用 gate.NewContext(ctx, 本机身份)
```

- 存储是 SQLite（`store.Store.Gate()`）；`JSONStore` 只留给门自己的测试。
- `PUT /api/public` 换 `public.origin`：用新配置开一个新门换上，旧门 `CloseAll()` 断开它放进来的长连接。
- 权限、需要、`ir` 管理命令见 `docs/api.md`“应用和登录”“权限”“需要”。

- 处理函数里 `gate.FromContext(ctx)` 拿到 `Identity`；`CanManage()` 是网页的登录或主机。
- 长连接（事件流、WebSocket）调 `g.Track(r, closeFn)`，会话结束或应用、登录被删时门会调 `closeFn`；连接自己结束时调返回的 `untrack`。
- 资料接口：`multipart/form-data`（`name`、文件 `avatar`、`removeAvatar=1`）或 JSON（`avatar` 为 base64 或 data URL）。改别人的只许网页登录和主机。

管理函数和 `ir` 命令：

| 命令 | 函数 |
|---|---|
| `ir invite [名字] [--claude]` | `CreateInvite(name, perms)` → `InviteLink{ID, Name, URL, ExpiresAt}`（URL 带 TOKEN，只给允许的那一处看） |
| 配对允许 | `CreateApp(name, type)`、`RenewToken(id)` → `EndpointInfo`、TOKEN |
| `ir apps` | `Access()` → 应用、登录（含在线连接数、会话数）和未用邀请 |
| `ir profile <名字> [--name] [--avatar]` | `UpdateProfile(ref, Profile{Name, Avatar, RemoveAvatar})` |
| `ir apps rm <名字>` | `Revoke(ref)`：应用、网页的登录或未用邀请；最后一个有 Passkey 的登录删不了（`ErrLastPasskey`） |

`ref` 可以是 ID 或名字。另有 `CheckName`、`Endpoint(id)`（含本机）。

## 存储接口

```go
type Store interface {
    Load() (State, error)  // 启动时读一次，全部放内存（量很小）
    Apply(Change) error    // 原子：先删后写（按 ID，会话按哈希）
}
```

现在用 SQLite（库的第 3 次迁移；第 5 次加 `invites.owner`，并一次性给最早的面板开 Claude）：`terminals`（头像 BLOB，passkeys 存 JSON 列，`token_hash` 唯一）、`invites`（hash 唯一）、`logins`（门的“会话”，hash 主键，按终端建索引）三张表，时间存纳秒（0 是没有）；`Apply` 一个事务，删终端时连它的 `permissions` 一起删。删终端时门自己在同一个 `Change` 里删它的登录，不用级联。库文件和 `-wal`、`-shm` 都是 0600。`JSONStore`（一个文件，0600，整文件重写）只给门的测试用。

## 反代

- Cloudflare Tunnel：`ir.<域名>` → `http://127.0.0.1:<端口>`；不要 Access，不加缓存规则。默认 `CF-Connecting-IP` 就对。
- 有公网 IP 时用 Caddy：

  ```
  ir.example.com {
      reverse_proxy 127.0.0.1:7810 {
          header_up CF-Connecting-IP {remote_host}
      }
  }
  ```

  Caddy 默认保留 Host；`header_up` 用真实地址覆盖客户端自己带的头。

## 还没做

- 只听本机时主机没有可存的资料（`ServeProfile` 回 422 `built_in`）。
- 门内部仍是一张 `Endpoint` 表（`panel`/`channel`），等别的应用的登录多起来再拆。
