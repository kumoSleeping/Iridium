# Passkey 门（`internal/gate`）

照 Rhodium 网关（`rhodium-gateway`）的做法，改成 Go 的中间件，并加上交互终端登记。

## 两种模式

- **只听本机**（没配 `public.origin`）：不登录。只查 Host（回环名字）和 Origin，请求都算内置终端“本机”。带插件 TOKEN 的请求照样认成那个插件。
- **上公网**（配了 `public.origin`）：所有网页请求都要面板的会话 Cookie 或通道的 TOKEN，回环也一样（cloudflared 从回环连进来）。Host 只认 `public.origin` 的主机名；带 Bearer TOKEN 的请求另认回环名字（本机的插件），TOKEN 照样要对。

`public.origin` 必须是 https（开发时可用 `http://localhost:端口`）。它的主机名就是 Passkey 的 rpID：换域名，所有 Passkey 作废。

## 交互终端

- `kind`：`panel`（网页面板，Passkey 登录）或 `channel`（插件、节点，TOKEN）。
- 名字：1–12 个字，字母、数字、空格和 `- _ .`；全角 ASCII 转半角，空格合并；不分大小写查重；`本机`、`未知`、`local`、`unknown` 保留。`[ ] / : ·` 之类不收，因为名字要放进 `[名字]`、`[QQ·张三]`、`mac:~/dir`。内部用 16 位随机 ID，比任何名字都长，不会混。
- 新面板只能靠一次性邀请：`https://<域名>/_ir/join#<token>`，10 分钟。`#` 后面不进服务器日志；页面读到后立刻从地址栏去掉。打开后起名（边打边查重）、建 Passkey，成功才用掉邀请。邀请时定的名字只是预填，可改。
- 首次：上公网且还没有面板时，启动会在状态目录写 `setup-token`（0600，内容就是一条 join 链接，不过期）；每次重启换新的。只要有了面板（不管用哪个邀请进来的），文件和这条邀请就删掉，`ir access` 也不再列；启动时（本机模式也一样）发现有面板就清掉残留的。
- Claude（Claude 用户协议：只给本人的终端）：面板注册后门调 `OnJoin(Joined{Endpoint, Owner, First})`。`First`：注册时还没有别的面板；`Owner`：邀请是 `ir invite --owner` 生成的（`CreateInvite(名字, owner)`，存在 `invites.owner`）。两者之一，服务端就给它开 Claude。插件同理：`ir token --owner`。没 Claude 的面板不能开 Claude（规则接口只认本机由人跑的 `ir` 和带 Claude 的面板，见 `docs/api.md`），只能关。
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
- 交互终端能做什么（只有 Claude 有区别）、确认、`ir` 管理命令见 `docs/api.md`“交互终端”。

- 处理函数里 `gate.FromContext(ctx)` 拿到 `Identity{EndpointID, Name, Kind, Local}`；`CanManage()` 是面板或本机。
- 长连接（事件流、WebSocket）调 `g.Track(r, closeFn)`，会话结束或终端被撤销时门会调 `closeFn`；连接自己结束时调返回的 `untrack`。
- 资料接口：`multipart/form-data`（`name`、文件 `avatar`、`removeAvatar=1`）或 JSON（`avatar` 为 base64 或 data URL）。改别人的只许面板和本机。

管理函数和 `ir` 命令：

| 命令 | 函数 |
|---|---|
| `ir invite [名字] [--owner]` | `CreateInvite(name, owner)` → `InviteLink{ID, Name, URL, ExpiresAt}`（URL 带 TOKEN，只给确认面板看） |
| `ir token <名字> [--owner]` | `CreateToken(name)` → `EndpointInfo`、TOKEN；`--owner` 时服务端再给它开 Claude |
| `ir access` | `Access()` → 终端（含在线连接数、会话数）和未用邀请 |
| `ir profile <名字> [--name] [--avatar]` | `UpdateProfile(ref, Profile{Name, Avatar, RemoveAvatar})` |
| `ir revoke <名字>` | `Revoke(ref)`：终端或未用邀请；最后一个有 Passkey 的面板删不了（`ErrLastPasskey`） |

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

- 本机模式下“本机”没有可存的资料（`ServeProfile` 回 409 `local`）。
- 节点（`ir invite --peer`）的签名请求。
- 每个面板都有面板权，包括给别人开的面板；Claude 另算，只在建的时候定。
