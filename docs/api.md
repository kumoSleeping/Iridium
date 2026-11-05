# Iridium 接口

一套接口，两个入口：

- HTTP：`127.0.0.1:7810`（配置 `listen`），给网页，前面是 Passkey 门（`docs/gate.md`）。没配 `public.origin` 时只认本机的主机名（`localhost`、回环地址和配置 `hosts`），非 GET 请求带了别的 `Origin` 就拒绝，请求都算内置终端“本机”（带插件 TOKEN 的算那个插件）。配了以后每个请求都要面板登录或 TOKEN。
- unix socket：`$XDG_RUNTIME_DIR/iridium/ir.sock`（0600），给本机 `ir`，不经过门，算“本机”。`ir` 认出是 agent 在调（环境里有 `CLAUDECODE`、`CODEX_THREAD_ID`、`IRIDIUM_SESSION`，或者没有终端）时带头 `X-Iridium-Agent: 1`（和 `X-Iridium-Session`），这时不回任何秘密（见“确认”）。

来源：每条交给 agent 的消息开头是 `[终端名] `（`/` 开头的命令除外），名字取发送时终端的名字。库里记下这条输入是哪个终端（ID）发的、用的什么名字；读记录时服务端按 Iridium 自己写进去的输入认（Claude：发消息时给的 uuid；没有 ID 的 agent：`store.SourceByText` 比对输入记录），去掉前缀，消息块带 `from`（名字）和 `fromTerminal`（终端 ID，取头像用）。用量行按终端 ID 记，改名不丢。

类型都在 `internal/api`。`go generate ./internal/api` 用 tygo 生成 `web/src/api/types.gen.ts`，前端只用它（生成的文件不进库）。

出错时状态码非 2xx，正文 `{"error": "…"}`：400 请求不对，403 权限表不许，404 没有这个会话或位置（看不到的会话也是 404），409 agent 没在运行或状态不对，429 今天的用量到上限了，504 agent 没按时回。名字不行时 409 另带 `nameError`（`empty`、`too_long`、`invalid`、`reserved`、`taken`）。

## 请求

| 请求 | 正文 → 回答 | 说明 |
|---|---|---|
| `GET /api/sessions` | → `Session[]` | 会话列表（现在只有托管的；自动发现以后并进来）。置顶在前，再按最近 |
| `POST /api/sessions` | `NewSession` → `{id}` | 在 `cwd` 开一个托管会话，带 `text` 就作为第一条消息。`agent` 现在只有 `claude` |
| `GET /api/sessions/{id}` | → `SessionState` | 打开的会话的状态，和事件流里的同一份 |
| `GET /api/sessions/{id}/messages` | → `MessagePage` | 不带参数：最新一页。`before=<start>`：往回一页。`after=<end>`：之后新写的 |
| `GET /api/sessions/{id}/block?at=<at>` | → `{text}` | 截短的块（工具参数、结果、思考、系统行）的全文 |
| `POST /api/sessions/{id}/send` | `{text}` → `{input}` | 发消息。agent 没在跑就先拉起（有记录就 `--resume`）。回合进行中发的并进当前回合 |
| `POST /api/sessions/{id}/interrupt` | → `{ok}` | 停掉当前回合（Esc），agent 留着 |
| `POST /api/sessions/{id}/stop` | → `{ok}` | 停掉 agent 进程（关 stdin，写完记录自己退出）。会话还在，下一条消息再拉起 |
| `POST /api/sessions/{id}/answer` | `Answer` → `{ok}` | 回答提问：审批 `allow`（不许时可带 `message`）；选择题 `answers`（问题原文 → 选项 `label`）；计划确认 `allow` |
| `GET /api/events?sub=…` | SSE | 见下 |
| `GET /api/health` | → `{ok}` | |

## 消息页

- 一页最多 20 条消息（你的话、agent 的话），中间的块跟着走，最多 200 块。
- `start`：这页第一行的字节位置，往回翻用 `before=<start>`。`end`：这页覆盖到哪，取新的用 `after=<end>`。`more`：前面还有。
- 块（`Block.kind`）：`user`、`assistant`、`thinking`、`tool`（同页的结果在 `result` 里）、`result`（调用在别的页，按 `id` 拼）、`system`（`event`：`compact`、`api_error`、`local_command`、`interrupted`、`unreadable`）。
- 工具参数、结果、思考、系统行超过 2000 字只给开头，`length` 是全长，全文按 `at` 取。你的话和 agent 的话不截。

## 事件流（携带）

每个页面一条：`GET /api/events?sub=list&sub=session:<id>&sub=context:<id>&sub=quota`。换了打开的会话就关掉重开。

订阅的东西：

| 键 | 值 |
|---|---|
| `list` | `Session[]` |
| `session:<id>` | `SessionState`：状态、提问、后台任务、模型、审批模式、`end`、`proc`、出错原因 |
| `context:<id>` | `Context`：上下文用了多少、分项。回合结束和压缩后更新 |
| `quota` | `Quota`：各 agent 的额度窗口和读到的时间 |
| `confirms` | `Confirms`：等面板确认的事（只有面板能订） |
| `update` | `UpdateState`：有没有新版本、更新到哪一步 |

`list` 按订阅者的权限表过滤；看不到的会话订 `session:`、`context:` 回 404。终端被撤销、面板退出、`public.origin` 改了，流就断开。

事件：

- `state`：`{key, value}`，一样东西的整份。不存在时 `value` 是 `null`。
- `draft`：`{session, proc, n, text}`，agent 正在打的字，接在第 `n` 段草稿后面。
- 每 15 秒一行注释（`: ping`）保活。

规则：

- 开流（含断线重连）先发每样的整份；之后变了再发整份，不发补丁。同一样 250ms 内最多一次，发的是最新的。
- 读得慢时中间的状态直接丢掉，只送最新的。
- 草稿每 100ms 攒一包，只拼接不丢。

页面怎么用：

- 打开会话：取最新一页，记下 `end`。`session:<id>` 的 `end` 比自己的大，就 `after=<自己的 end>` 取新块。
- 草稿：同一 `proc`、同一 `n` 的 `text` 接起来；`n` 变了，上一段封口（记录里马上会有这段话）；`proc` 变了，丢掉所有草稿。记录里来了 agent 的话，就换掉最早封口的那段。重连后清掉草稿，照 `end` 取新块。

## 状态灯（`Status`）

`idle` 无灯，`starting` 启动中，`running` 运行中，`compacting` 压缩中，`waiting` 需要你（有提问），`error` 上次进程出错退出（原因在 `SessionState.error`），`elsewhere` 在别处运行（自动发现做好后才有）。`live` 说 agent 进程在不在跑。

## 交互终端

“本机”是内置终端（ID `local`），不在库里，权限全开，不能改、不能撤销。其余的是门里登记的面板（`panel`）和通道（`channel`），表在 SQLite（`terminals`、`invites`、`logins`、`permissions`）。

谁能做什么：

- **面板权**（批准新终端、放宽权限、看到 TOKEN 和邀请链接、答确认）：面板；本机 `ir` 由人在终端里跑时；没配 `public.origin` 时的本机网页。
- **管理**（列出、改名和头像、收窄权限、撤销）：面板和本机，包括 agent 调的 `ir`。
- 通道什么都不管，只能改自己的名字和头像。

| 请求 | 正文 → 回答 | 说明 |
|---|---|---|
| `GET /api/me` | → `Me` | 调用者自己：终端、权限表、`panel`（有没有面板权）、`public` |
| `PUT /api/me/profile` | 资料 → `EndpointInfo` | 改自己的名字、头像（见 `docs/gate.md`）；本机回 409 |
| `PUT /api/endpoints/{id}/profile` | 同上 | 改别的终端的（`id` 也可以是名字）；`/api/terminals/{id}/profile` 同 |
| `GET /api/terminals` | → `Terminals` | `ir access`：所有终端（本机在最前）、权限表、今天的用量，和未用的邀请。没有秘密 |
| `POST /api/terminals/invites` | `NewInvite` → `Invite` | `ir invite [名字]`：新面板的一次性链接（10 分钟）。要先有 `public.origin`（否则 409）。没有面板权时回 202 `Pending` |
| `POST /api/terminals/tokens` | `NewInvite` → `Token` | `ir token <名字>`：插件 TOKEN，只给这一次；已有的通道换新 TOKEN，旧的作废。没有面板权时回 202 `Pending` |
| `PUT /api/terminals/{id}/rules` | `SetRules` → `Terminal` | 改权限表。放宽要面板权；加 Claude 还要 `ownerConfirmed: true` |
| `DELETE /api/terminals/{id}` | → `{ok}` | `ir revoke`：终端（连同登录、权限、连接）或未用的邀请；最后一个有 Passkey 的面板删不了（409） |

## 权限表（`Rules`）

| 项 | 说明 |
|---|---|
| `agents` | 能用哪些 agent，也只看得到这些 agent 的会话。`claude` 另要 `claudeOwner` |
| `claudeOwner` | 面板确认过“这是我本人在用”（Claude 用户协议）：`{by: 面板 ID, at}`，服务端写，请求里带的不算。去掉 Claude 就清掉 |
| `allDirs`、`dirs`、`workspaces` | 能用哪些目录：全部，或某些目录（含下层）和工作区 |
| `seeOthers` | 看不看别的终端开的会话；不经 Iridium 开的算本机的 |
| `filesIn`、`filesOut` | 拷入、拷出（文件接口做好后才查） |
| `answer` | 答提问和审批 |
| `mustApprove` | 它开的会话一律要审批：不能用跳过审批的模式（`bypassPermissions`、`acceptEdits`、`auto`、`never`、`danger-full-access`），没给模式时 Claude 用 `default`；不能往跳过审批的会话发消息 |
| `dailyTokens` | 每天（本地时间）它起的回合用的 token 上限（输入、输出、缓存都算），0 不限；到了回 429 |
| `allCommands`、`commands` | 能发哪些 `/` 命令：全部，或列出的（如 `/compact`） |

默认：面板除 Claude 外全开；通道除 Claude 外，只看自己开的会话、必须审批。没存过表的终端用默认。

每个会话接口都查：列表（和事件流的 `list`）只给看得到的；看、读消息、取全文看不到就 404；发消息查 agent、命令、审批模式、上限；开会话查 agent、目录、模式、命令、上限，并记下是谁开的；停、中断查 agent；答提问另查 `answer`。

## 确认（agent 的 `ir` 要秘密时）

agent 调 `ir invite`、`ir token` 时，邀请链接和 TOKEN 不能进它的对话。服务端不回秘密，改记一条确认（内存里，10 分钟），网页面板在“需要你”里看到，同意后**只有点同意的那个面板**拿到链接或 TOKEN；agent 只拿到结果。认 agent 靠 `ir` 带的头，只防 TOKEN 进记录，挡不住故意读库。

| 请求 | 正文 → 回答 | 说明 |
|---|---|---|
| `GET /api/confirms` | → `Confirms` | 等确认的（面板权）。事件流 `confirms` 是同一份 |
| `POST /api/confirms/{id}` | `ConfirmAnswer` → `ConfirmReply` | 面板同意或拒绝；同意时回答里带 `invite` 或 `token`，只此一次 |
| `GET /api/confirms/{id}?wait=<秒>` | → `ConfirmState` | 结果：`pending`、`allowed`、`denied`、`expired`、`failed`，从不带秘密。`wait` 最多 60 秒，有结果就回 |

## 公网入口

| 请求 | 正文 → 回答 | 说明 |
|---|---|---|
| `GET /api/public` | → `PublicState` | `public.origin`，有没有面板注册过 Passkey |
| `PUT /api/public` | `SetPublic` → `PublicState` | 设 `public.origin`（`""` 关掉）：写进配置文件，门马上换成新的，旧门放进来的连接全断开。要面板权 |

`ir public` 在终端里跑（不是终端、或者是 agent 调的，就拒绝），一步步做到能用为止：

1. 找 `cloudflared`（`~/.local/bin`，再 `PATH`）；没有就从 GitHub 最新版下载到 `~/.local/bin`，sha256 对上 release 的 digest（或说明里列的）、能跑才放进去。
2. `HOME=<状态目录>/cloudflared cloudflared tunnel login`：打印登录链接，你在 Cloudflare 上选域名。登录凭证在 `<状态目录>/cloudflared/.cloudflared/cert.pem`，不碰 `~/.cloudflared`。没成就说缺一个托管在 Cloudflare 的域名。
3. 用凭证问 Cloudflare 这个站点的域名（问不到就让你填），问子域名（默认 `ir`）。
4. 建隧道 `iridium-<机器名>`（已有就接着用、重新取凭证），`route dns`；已有记录时问要不要覆盖（`--overwrite-dns`）。写 `config.yml`：这个主机名 → `http://127.0.0.1:<端口>`，其余 404，`ingress validate` 过了才用。
5. 写用户级单元 `iridium-cloudflared.service`（`cloudflared tunnel --no-autoupdate --config … run`），`daemon-reload`、`enable`、`restart`。机器上的 `cloudflared` 服务和 `/etc/cloudflared` 不碰。
6. `PUT /api/public`，再从外网（先问 1.1.1.1 解析）取 `https://<主机名>/_ir/health`，等到通（3 分钟）。
7. 生成第一个面板邀请，打印链接。

`ir public off`：停掉并删掉单元，清 `public.origin`，删隧道和它的文件（登录凭证留着，下次不用再登录）。DNS 记录 `cloudflared` 删不了，提示去控制台删。

## 更新

| 请求 | 正文 → 回答 | 说明 |
|---|---|---|
| `GET /api/update` | → `UpdateState` | 现在的版本、最新版本和说明、`available`、`phase`（`idle`、`checking`、`downloading`、`restarting`、`failed`）。事件流 `update` 是同一份 |
| `POST /api/update/check` | → `UpdateState` | 马上查一次 |
| `POST /api/update` | `ApplyUpdate` → `UpdateState` | `ir update`：下载、核对 sha256、跑一次 `iridium version` 看版本对不对、原子替换二进制、重启服务。没有更新的（`force` 除外）回 409 |

更新源是配置 `update.url`（`update.every` 默认一天查一次；以后加 GitHub Releases），返回：

```json
{"version": "1.2.0", "notes": "…", "url": "https://…/iridium-linux-amd64", "sha256": "<hex>",
 "platforms": {"linux-arm64": {"url": "…", "sha256": "…"}}}
```

`platforms` 里有本机的（`GOOS-GOARCH`）就用它。版本是带点的数字（可有 `v`、`-rc.1`）；`dev` 版不提示更新，`ir update --force` 照样装。重启：在 systemd 单元里就 `systemctl --user restart --no-block <单元>`，否则原地重新执行。托管进程在自己的单元里，不受影响。

## 部署

- `iridium install [-config <文件>] [-now]`：写用户级单元 `~/.config/systemd/user/iridium.service`（`iridium serve -config ~/.config/iridium/config.json`，带装的时候的 `PATH`），没有配置就写一份默认的（`127.0.0.1:7810`），建 `~/.local/state/iridium`，`daemon-reload`。不加 `-now` 不启动。没开 linger 时提醒：退出登录后服务和托管的 agent 都会停（`sudo loginctl enable-linger <用户>`）。
- `scripts/deploy.sh`：`web/package.json` 在就先 `npm ci && npm run build`；`go build`（版本取 `git describe`）装到 `~/.local/opt/iridium/iridium`，`~/.local/bin/ir` 链过去，再跑 `iridium install`；服务已在跑就重启它。`IRIDIUM_NO_SYSTEMCTL=1` 不碰 systemd。
