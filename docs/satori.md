# Satori with ir

应用用 Satori 协议和 Iridium 说话。规范版本：satorijs/docs@50fee9ef（副本 `~/kumo/satori-spec-50fee9ef/`）。频道见 `docs/channels.md`。实现在 `internal/satori`，server 侧的接线在 `internal/server/satori.go`。定下的事见 `docs/next/decisions.md` 1、25–31，计划见 `docs/next/draft.md` 三。

## 对应

| Satori | Iridium |
| --- | --- |
| `platform`（`Satori-Platform`） | 应用 ID |
| `login.user.id`（`Satori-User-ID`） | 登录 ID（应用里的），名字是登录的名字 |
| `login.sn` | 每条链路按顺序给；HTTP 的 `login.get` 按登录的先后 |
| `login.adapter` | `iridium` |
| `channel` | 会话（它自己的频道），`id` 即会话 ID，`type` 0；无 agent 的频道也是 `type` 0，`_data.bare`；`parent_id` 是它所在的上一层：子 agent 的上级、派出它的拉起者（没被拖出来时），否则它的文件夹；会话所在的文件夹是分类频道，`type` 2，`id` 是 `folder:<分组用的路径>`（本机绝对路径，或 `机器:路径`），`_data` 是 `FolderMark`（`path`、颜色、置顶、拖到的位置） |
| `guild` | 网页的文件夹组（`id` 是组号）；不在组里的会话归 `loose`（未分组） |
| `user` | 频道里的 agent（`agent:<会话ID>`，`is_bot`，`name` 是它现在用什么跑的，`nick` 是叫它的名字，全局唯一），以及说话的应用、登录、主机；`user.get` 也认 `claude`、`codex`… |
| `message` | 频道里说的一句话（`docs/channels.md`），`id` 是它在频道里的编号；提问的卡片是 `ask:<会话>:<请求>` |

鉴权：应用用 `Authorization: Bearer <应用 TOKEN>`（过 gate）。网页也是应用（一视同仁）：浏览器凭 cookie，`Satori-Platform: ir_web`、`Satori-User-ID: <登录 ID>`；没开公网时主机自己的页面是平台 `local` 的登录 `local`。这两种连接只代表这一个登录。会话 TOKEN 403 `no_login`。`Satori-Platform` 不是本应用 ID、没有 `Satori-User-ID`：403 `no_such_login`。新登录第一次说话：403 `login_pending`，产生“登录加入”需要（和 `/v1/internal/*` 一样）；配对时带上的登录（`ir pair --login`，默认 `bot`）随应用一并批准，READY 里一开始就有。

## HTTP `POST /v1/{资源}.{方法}`

- `message.create {channel_id, content}`：发进会话（插入正在跑的回合），发送者显示为 `应用·登录`。`<img>`/`<file>` 的 `src` 是本登录在这个会话 `upload.create` 得到的 `internal:` 地址时，作为附件；`data:` 地址存成上传的文件再作附件；其余元素按意思转成文字。顶层的 `<message>` 各发一条；合并转发 `<message forward>` 不拆，和前后的话是一条，照原样记下（`docs/channels.md`“元素”）。回 `[Message]`，`id` 是这条输入的 ID，拿去 `message.get` 能取到它（进了记录取记录里那块，还在排队取排队里的）。
- `message.get`、`message.list`：频道里说的话，一句一条，不含思考和工具调用。`message.list` 是 BidiList，翻页标记是消息的编号；`limit` 默认 50，至多 500；没有 `next` 时只能 `before`。多一个 `_data {start, end, more}`：这页第一条和最后一条的编号、前面还有没有。`_data.q` 给词时只回有这些词的消息（`docs/channels.md`“搜索”）。`message.get` 的 `message_id` 也认输入的 ID 和提问的卡片。
- `POST /v1/internal/block.list`、`block.get`（参数同上）：一个会话自己的记录，一块一条消息，内容见下面“记录”；翻页标记是记录里行的偏移，`limit` 按块数、在行首截，可能略多。`_data {start, end, more}`：这页从哪行起、读时记录到哪、前面还有没有。网页读记录走它。agent 也能读别的会话的（只读，打码）。
- `channel.get`、`channel.list {guild_id?, next?, _data?: {all}}`（不给 `guild_id` 是全部；先列文件夹，再列会话；不列归档和子 agent，`_data.all` 都列）、`guild.get`、`guild.list`（只列范围里的）、`guild.member.list {guild_id, next?}`、`guild.member.get {guild_id, user_id}`（组里各会话的 agent：`user` 是 `agent:<会话>`，带 `nick` 和 `joined_at`；再加本应用的登录。`_data.channel_id` 只列那个频道的成员，`joined_at` 是加入频道的时间，2026-11-13）、`login.get`、`user.get`。`channel.get`、`channel.list` 的每个 channel 带 `_data`：会话本身（同 `GET /v1/internal/sessions` 的一项，名字、标题、最后一句打码）；`guild.get`、`guild.list` 的 guild 带 `_data`：文件夹组（`path`、`only`），`loose` 没有。网页拿不到携带状态 `guilds` 时用 `guild.list` 的 `_data`。
- `channel.create {guild_id?, data?: {name, parent_id}, _data: {agent, cwd, text?, model?, mode?, effort?, preset?}}`（2026-11-11）：开一个会话；不带 `_data.agent` 是建一个无 agent 的频道（`_data.cwd` 是它列在哪个文件夹，2026-11-13）。`guild_id` 是文件夹组（`self` 自己的；不给或 `loose` 按目录归组），`data.parent_id` 是拉起者（不给是发话的会话自己，`ir` 开的就挂在自己下面），`data.name` 是名字；`_data` 是 agent 怎么起。回这个会话的 Channel（同 `channel.get`），随后照常有 `channel-added`。`ir api channel.create` 先印 ID，带 `text` 等 agent 接了再返回，`--wait` 等到这一回合结束。
- `channel.update {channel_id, data?: {name, parent_id}, _data?: {archived, pinned, color, order, workspace}}` 改名，`_data.archived` 归档或恢复（挂在它下面的一起），`pinned` 置顶，`color` 颜色标签（`red`…`purple`，空串去掉），`order` 拖到的位置（毫秒，0 去掉），`workspace` 手动放进那个组（0 未归类）；`data.parent_id` 设成 `null` 或它自己的文件夹，就从拉起者下面拿出来（此后算人开的），别的值 422 `parent_fixed`。文件夹（`folder:…`）只收 `_data.color`（本机的文件夹）、`pinned`、`order`，只有面板和本机能改。
- `message.update {channel_id, message_id, content | _data.text}`（2026-11-11）：改还在排队的一条（`message_id` 是 `message.create` 回的 ID，或频道里的编号）；已经交给 agent 的 422 `no_such_message`。多 agent 或无 agent 的频道里，改的是它叫起的每个 agent 队列里的那条。
- `message.delete {channel_id, message_id}`：撤回频道里的一条（`message_id` 是编号，也认输入的 ID 和 `b:<会话>:<块>`），所有人那里都没有了（`docs/channels.md`“撤回”）；还在排队的副本一并撤下。提问和 Iridium 自己的行 422 `no_such_message`；不是频道成员 403。还没进频道的输入照旧只从队列撤下。
- `reaction.create {channel_id, message_id, emoji_id}`、`reaction.delete {…, user_id?}`（不给是自己的）、`reaction.clear {channel_id, message_id, emoji_id?}`、`reaction.list {channel_id, message_id, emoji_id}`（`List<User>`）：贴表情（`docs/channels.md`“表情”）。只收一个 Unicode 表情（也认 `emoji`），否则 400。`message.list`、`message.get` 的每条带 `_data.reactions`：`[{emoji, count, users, me}]`；`block.list` 里是频道消息的块带 `_data {seq, reactions}`。
- `block.list` 带 `_data.blocks: true` 时，回答的 `_data.blocks` 另给这一页 Iridium 自己的块（`api.Block`，打码），`ir` 的 `--wait` 靠它读回复。
- `channel.delete {channel_id}` 归档（agent 停下，记录不删）。要能用这个会话。agent 改的名不盖人起的（409 `named`）。
- `upload.create`：一个 multipart，带字段 `channel_id`，每个文件回 `internal:{应用}/{登录}/files/{会话}/{ref}`。网页用分块的 `/v1/internal/uploads`。
- `POST /v1/meta`：本应用已批准的登录，`proxy_urls` 为空。`/v1/meta/*`（webhook）不做。
- `GET /v1/proxy/internal:…`：只取本应用登录的文件。不是地址 400，不是本应用的登录 404，别的地址 403。
- 不支持的标准接口照规范 404。

错误体 `{code, message}`。404 只表示接口不存在；资源不存在 422 加原因码：`no_such_session`、`no_such_message`、`no_such_guild`、`no_such_user`、`no_such_file`、`no_such_ask`、`no_such_button`。

## ir

`ir api`（`internal/cli/api.go`，用法见 `plan.md`“ir”）走同样的 `/v1/`：本机 socket 上和远端会话的 `ir`（`POST /v1/internal/cli`）也挂着 Satori。

- Iridium 自己加的：`channel_id` 认 ID 开头和 `self`（`Resolver`，见 `api.md`“会话 ID”），`guild_id=self`；`message.create` 的 `_data.text` 代替 `content`（纯文字，这边转义），`_data.paths` 是主机上的文件（只认本机 socket 的 `ir`）。

- 用哪个登录说话：`POST /v1/meta` 只回一个。主机的 `ir` 是 `local/local`；agent 的是它那一回合的登录（主机开的就是 `local/local`）；远端会话的 `ir` 也是那一回合的登录，不知道时用 `local/local` 的名义。
- 能做什么不看这个名义，看真正的调用方（`Caller.Self`），和 `/v1/internal` 一样：远端会话的 `ir` 按那台电脑的权限（再交上那一回合登录的），agent 的消息总是插话，agent 的改名不盖人起的。
- 标准方法照样要 `Satori-Platform`、`Satori-User-ID`，`ir` 按 meta 带上。

## 记录

`block.list` 的一块一条消息：标准元素放 Satori 有的（文字；文件按类型 `<img>`/`<video>`/`<audio>`/`<file>`，`src` 是 `internal:` 地址），其余用带适配器前缀的元素（advanced/internal.md），前缀 `iridium`。只认标准元素的客户端看到的是人和 agent 说的话。`created_at` 是块的时间。编码在 `internal/satori/blocks.go`，网页解码在 `web/src/satori.ts`，两边用同一张表 `internal/satori/testdata/blocks.json` 测，能原样还原。

| 块 | 内容 |
| --- | --- |
| 人说的 | `<iridium:user id from terminal length/>` 加文字和文件 |
| agent 说的 | 文字和文件；被截断时前面加 `<iridium:assistant length/>` |
| 思考 | `<iridium:thinking length>文字</iridium:thinking>` |
| 工具调用 | `<iridium:tool id name length error><iridium:input>参数 JSON</iridium:input><iridium:result at time length error>结果 <iridium:image/>…</iridium:result></iridium:tool>`，写出的文件跟在后面 |
| 结果（调用在别页） | `<iridium:result id length error>结果 <iridium:image/>…</iridium:result>` |
| 系统行 | `<iridium:system event from terminal id length>文字 <iridium:compact auto before after ms/></iridium:system>` |

- `length`：文字（或参数）被截到 2000 字时全文的长度，`message.get` 给全文。
- `<iridium:image mime width height unsent/>`：结果给模型的图，第 n 个用 `/v1/internal/sessions/{id}/image?at=<结果的 at>&n=`。
- 文件元素还带 `path`、`size`、`mime`、`mtime`、`thumb`、`sha256`，图片带 `width`、`height`。没有登录的调用方不带文件。
- 文字、参数、结果里的凭据打码。
- 频道消息（`message.list`、事件）的内容：文字，文件元素，agent 的话后面可以跟 `<iridium:activity bash failed cmds read search fetch agents other files add del ms>`，里面每个改过的文件一个 `<iridium:file path add del/>`（路径只给网页和主机）。

## Iridium 自己的接口 `/v1/internal/*`

照规范的 internal 路由（advanced/internal.md）：Iridium 自己的接口全在 `/v1/internal/<路径>`（`docs/api.md`），给 Satori 没有的东西：工具块、待发队列、模型和模式、文件夹组、设置、应用管理……带了 `Satori-Platform`、`Satori-User-ID` 就必须是调用者自己的。主机的 `ir` 经 unix socket 走同样的路径。网页只用这里和标准方法。

`message.create` 多收一个 `_data`：`{attachments?, caller?, id?, text?, paths?}`（上传好的引用、代替 `content` 的纯文字、主机上的文件；引用哪条写在内容里：`<quote id="编号"/>`；`id` 是发送方自己定的消息 ID，小写 UUID，同一个 `id` 再发不会写第二遍，回答它当时的结果，丢了回答可以放心重发，网页每条都带），回答的 Message 也带 `_data`，是发送的结果（`api.SendReply`：`input`、`queued`、`seq` 频道里的编号、`said` 说进了多 agent 或无 agent 的频道、`unseen` agent 用 `ir` 发话时它没读过的消息、`session`、`command`、`desktop`）。回合在跑时发的一律插进这一回合（`_data.steer` 2026-11-19 去掉，带了也不看）；`queued` 只在发的是命令或 agent 在等额度时为真。

## WebSocket `/v1/events`

另有两个 Iridium 自己的信令（不是 EVENT，不存、没有 `sn`）：客户端发 `op 6` WATCH `{keys}`（或在 IDENTIFY 里带 `_watch`），说要哪些携带状态（会话列表、某个会话的整份状态、上下文…，键同 `docs/api.md`“事件流”）；服务端用 `op 7` STATE `{type: "state"|"draft", data}` 推这些状态和草稿。每次 WATCH 取代上一次。只给网页和主机的连接。

- 连上后 10 秒内 IDENTIFY。`token` 是应用 TOKEN；升级请求已带 `Authorization` 时可不带，带了必须是同一个应用。不对就关闭 4004；先发别的关闭 4000。
- READY 回本应用已批准的登录。PING 回 PONG；60 秒没有任何帧就断开（应用每 10 秒 PING）。
- 网页连接点的延迟就是 PING 到 PONG 的往返；8 秒没回 PONG 就当断了，重连。断开后发一次 `POST /v1/meta`，回 401 就去登录（浏览器看不到升级失败的状态码）。
- 事件存库，留 24 小时（最多 1 万条），重启后照样能补发。IDENTIFY 带 `sn` 就补发之后还留着的。
- 一个事件发给本应用每个看得见这个会话的登录，各带自己的 `login` 和自己的 `sn`（事件序号 × 1024 + 第几个登录），所以断在两个登录之间也能接上。`sn` 只往上涨，重启后不会比之前的小。

事件：

| 事件 | 什么时候 | `_data.kind` |
| --- | --- | --- |
| `message-created` | 有人往会话里发了话（网页、别的应用的登录、主机的 `ir`；`message.id` 同 `message.create` 回的）；主机终端里直接对 agent 打的话，读记录时补进来（作者 `terminal`） | `user` |
| `message-created` | agent 回合中间说的话（下一段来了才发）；agent 用 `ir` 在频道里发的话 | `progress` |
| `message-created` | 回合最后一段话；订阅来的 agent 回合结束时替它说进频道的那句 | `reply` |
| `message-created` | 提问、审批、计划 | `ask` |
| `message-created` | agent 进程出错 | `error` |
| `message-created` | Iridium 自己的一行（没转交、没送达） | `note` |
| `message-updated` | 提问结束（答了或撤了）：去掉按钮，加“已结束” | `ask` |
| `message-deleted` | 频道里的一条被撤回：`message.id` 是编号，`_data` 是它原来的（`kind`、`session`、`block`、`input`，没有内容），`user` 是作者，`operator` 是撤回的人 | 它原来的 |
| `reaction-added` / `reaction-removed` | 有人贴上、撤下表情：`message.id` 是编号，`user` 是谁，`emoji {id, name}` 是那个表情 | — |
| `interaction/button` | 某个登录按了按钮 | — |
| `channel-added` / `channel-updated` / `channel-removed` | 会话出现、改名或换组、归档或消失（看得见它的登录） | — |
| `login-added` / `login-removed` | 本应用的登录被批准、被移除（发给本应用的连接；网页的连接只收自己的） | — |

`_type` 固定 `iridium`，`_data.origin` 是发起这个回合的应用或网页登录（知道时）。`message.id` 是消息在频道里的编号（提问是卡片的 ID）。几个 agent 的频道里，每个 agent 的话都发，`user` 是说话的那个。

## 提问和按钮

提问作为一条消息发出，按钮 `id` 是 `<卡片>#<选项>`：

- `<button type="action">`：应用按下时 `POST /v1/internal/button {id}`。只需一个选择的（审批、计划、单个单选问题）立即作答；多个问题或多选的按钮累计，按 `#submit` 提交。也可以一次答完：`{id: <卡片>, choices, words, message}`（同 CardAnswer）。
- `<button type="input">`：`text` 是 `<卡片>#w<题号> `、`#deny `、`#reject `，用户接着写的话用 `message.create` 发出，就是这一题自己的回答或拒绝的理由，不进会话。

## 元素

收发都用 Satori 元素（`internal/satori/elements.go`）。Iridium 发：文字、`<img>`/`<file>`（`internal:` 地址）、`<button>`、`<p>`、`<b>`。会话用 `<sharp>`，位置和文件用 `<a>`，叫 agent 是正文里的 `@nick`（`docs/channels.md`“叫起谁”），不发 `<at>`；收到的 `<at id="agent:<会话>"/>` 转成 `@nick`（等同点名），别的 `<at>` 转成 `@名字`，收到的 `<sharp>` 转成网页的写法 `[名字](ir:session/<id>)`（2026-11-12，`plan.md`“@”）。

## 和规范不一样的地方

- 资源不存在回 422 而不是 404（`/v1/internal` 也一样）。
- 按钮反过来：`interaction/button` 是登录按了 Iridium 发的按钮之后发出的；按下本身走 `/v1/internal/button`。
- 没有 `guild-*`、`login-updated` 事件。`channel.update` 只改名字、位置和 Iridium 自己的（`_data`），`channel.delete` 是归档（不删记录）；文件夹（分类频道）由会话所在的目录生出，不能建也不能删。没有 `guild.update`：组的改名、顺序、拆开是 `PATCH /v1/internal/guilds/{id}`。
- 没有 META 信令、webhook、`proxy_urls`。
- 自定义 op：6 WATCH、7 STATE（见上），只给网页和主机的连接。
- 发起登录不叫 `referrer`，放在 `_data.origin`，目前是应用 ID。服务端已按回合记下发起的登录（agent 的 `ir` 按它算权限），事件里还没带出来。
- 权限：每个登录按“登录的 ∩ 应用的”权限；范围外的会话（channel）和文件夹组（guild）看不见。
- `message.create` 里的 `<message>` 各发一条（`<message forward>` 除外）；`data:` 资源存成上传的文件；`https:` 资源不下载，转成文字。

## 接 Koishi 等框架

Koishi（`adapter-satori`）、NoneBot（`nonebot-adapter-satori`）、satori-python 这类客户端把登录当作 bot 自己：会话是频道，agent（`claude`…）和人是频道里的用户，bot 用 `message.create` 回话，前端显示为 `应用·登录`。

```
ir pair --name koishi --env ~/koishi/.env:IR_TOKEN   # 本机；网页上允许一次，连同登录 bot
```

Koishi 的 `koishi.yml`：`adapter-satori` 的 `endpoint` 是打印出的地址（本机 `http://127.0.0.1:7810`，不带 `/v1`），`token: ${{ env.IR_TOKEN }}`。命令要带前缀（频道 `type` 0，按群聊）。

## 还没做

- 提问多选时按到一半的按钮只在内存里，重启后要从头按。
