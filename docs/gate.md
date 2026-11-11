# TOKEN 门（`internal/gate`）

核心认请求只凭 TOKEN；Passkey、Cookie、邀请、公网地址是网页（ir_web）那一层的事，见 `docs/web.md`。叫法见 `GLOSSARY.md`。

## 谁是谁

TCP 上的请求先过网页层（`internal/webapp`），再到核心的 `gate.Wrap`。核心只认这几种：

- **应用的 TOKEN**：`Authorization: Bearer ir_…`。坏的 401。
- **会话 TOKEN**：`irs_…`，那台电脑上 agent 的 `ir`，代那个应用、那个会话（`Identity.Session`）。
- **登录**：应用用 `Satori-User-ID` 指明在替哪个登录说话；第一次说话要批（403 `login_pending`），见服务端 `withLogin`。网页也一样：网页层带着 ir_web 自己的 TOKEN 和登录 ID 转发；ir_web 的登录只认已有的（入邀请时就批了），别的 403 `no_such_login`。
- **主机**：unix socket（本机的 `ir`；agent 的 `ir` 照旧按会话的发起人）；或下面那条信任边。
- `Config.Open` 放行的路径（配对、`/v1/events` 由 IDENTIFY 带 TOKEN、图标和 manifest）：没 TOKEN 也进，算没人。
- 其余 401。核心不读 Cookie，不查 Host、Origin，没有登录页。

## 唯一的信任边

`gate.AsHost(r)`：只能在进程内调用，请求里的任何东西都设不了它。网页层在没设公网地址（网页关着）时，把本机浏览器的请求在查过 Host（回环名字，防 DNS rebinding）和 Origin（防 CSRF）之后经它交给核心，算主机。别处不用。

## 应用和登录

- 一张表：`Kind` 为 `channel` 的是应用（`apps`），`panel` 的是 ir_web 的登录（`logins` 里 `app = 'ir_web'`）。主机 ID `local`，显示“主机”。
- `Identity{App, Login, Name, Session}`：`Host()` 主机，`Web()` 网页的登录，`ID()` 存权限、用量的 ID。
- ir_web 自己是一行应用（ID `ir_web`，有 TOKEN 哈希），不列出、不能按名字找、不能改名或删除；TOKEN 每次启动重新生成（网页和核心在同一个进程，旧的没人用），写进状态目录 `web-token`（0600）（`webapp.AppToken` → `SetAppToken`）。Satori 的 IDENTIFY 不收它。
- 应用只经配对进来：`CreateApp`、`RenewToken`（更新配对，旧连接断开）。ir_web 的登录只经邀请：`CreateWebLogin`（网页层调）。
- 名字：1–12 个字，字母、数字、空格和 `- _ .`；全角转半角，空格合并；不分大小写查重，应用之间、网页的登录之间各自不重名（登录改名时也避开邀请占着的名字，`Config.LoginNameTaken`）。保留 `host`、`主机`、`本机`、`ir_web`、`local`、`未知`、`unknown`，单个 ASCII 字母不能用。`[ ] / : ·` 不收。
- 名字和头像：`UpdateProfile`、`ServeProfile`。头像收 PNG/JPEG/WebP，≤2MB，居中裁方、缩到 128×128。地址 `/_ir/avatar/<id>?v=<版本>`，核心给任何认出来的人，带版本时可永久缓存（private）。

## 防护

- 坏 TOKEN 计失败：同一客户端 15 分钟内 10 次，锁 15 分钟（`ByTokenFrom` 也算）。客户端地址只在对端是回环时才信 `CF-Connecting-IP`；IPv6 按 /64。
- 长连接调 `Track(r, closeFn)`：应用或登录被删时调 `closeFn`。浏览器登出由网页层取消它转发的请求。
- 日志不写 TOKEN。

## 接进 Iridium

```go
g, _ := gate.New(gate.Config{Store: st.Gate(), SessionToken: …, OnRevoke: …, Open: openPath})
tok, _ := webapp.AppToken(g, stateDir)
w, _ := webapp.New(webapp.Config{PublicOrigin: origin, Core: g, Token: tok, Store: st.Web(), …})
core := g.Wrap(withLogin(withSatori(mux)))
http.Serve(tcp, w.Handler(core, webui.Handler()))
// unix socket 不经过这两层：handler 外层用 gate.NewContext(ctx, 主机身份)
```

- `PUT /v1/internal/public` 只换网页层（新开一个 `webapp.Web`，旧的 `CloseAll()` 结束它转发的浏览器请求）；核心和应用的连接不动。
- 管理：`CreateApp`、`RenewToken`、`Access()`（应用和网页的登录）、`Find`、`Revoke`（ir_web 的登录先问网页层 `MayRemove`：最后一个有 Passkey 的不能删）、`UpdateProfile`、`CheckName`。

## 存储

```go
type Store interface {
    Load() (State, error)  // 启动时读一次，全部放内存
    Apply(Change) error    // 原子：先删后写（按 ID）
}
```

SQLite：`apps`（`token_hash` 唯一）和 `logins`（ir_web 的行）。删应用连它的登录和 `permissions` 一起删；删 ir_web 的登录，那一行里的 Passkey 跟着没了，网页层收到 `OnRevoke` 删它的网页会话。`JSONStore` 只给测试用。

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
