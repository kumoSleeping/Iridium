# Satori with ir

应用用 Satori 协议和 Iridium 说话。规范版本：satorijs/docs@50fee9ef（副本 `~/kumo/satori-spec-50fee9ef/`）。实现在 `internal/satori`，server 侧的接线在 `internal/server/satori.go`。定下的事见 `docs/next/decisions.md` 1、25–31，计划见 `docs/next/draft.md` 三。

## 对应

| Satori | Iridium |
| --- | --- |
| `platform`（`Satori-Platform`） | 应用 ID |
| `login.user.id`（`Satori-User-ID`） | 登录 ID（应用里的），名字是登录的名字 |
| `login.sn` | 每条链路按顺序给；HTTP 的 `login.get` 按登录的先后 |
| `login.adapter` | `iridium` |
| `channel` | 会话，`id` 即会话 ID，`type` 0，`parent_id` 是子 agent 的上级会话 |
| `guild` | 网页的文件夹组（`id` 是组号）；不在组里的会话归 `loose`（未分组） |
| `user` | agent（`claude`、`codex`…，`is_bot`），以及说话的应用、登录、主机 |
| `message` | 记录里的一块（人说的、agent 说的、思考、工具调用、系统行），`id` 是块的位置 `<偏移>.<序号>`；提问是 `ask:<会话>:<请求>` |

鉴权：应用用 `Authorization: Bearer <应用 TOKEN>`（过 gate）。网页也是应用（一视同仁）：浏览器凭 cookie，`Satori-Platform: ir_web`、`Satori-User-ID: <登录 ID>`；没开公网时主机自己的页面是平台 `local` 的登录 `local`。这两种连接只代表这一个登录。会话 TOKEN 403 `no_login`。`Satori-Platform` 不是本应用 ID、没有 `Satori-User-ID`：403 `no_such_login`。新登录第一次说话：403 `login_pending`，产生“登录加入”需要（和 `/api` 一样）。

## HTTP `POST /v1/{资源}.{方法}`

- `message.create {channel_id, content}`：发进会话（插入正在跑的回合），发送者显示为 `应用·登录`。`<img>`/`<file>` 的 `src` 是本登录在这个会话 `upload.create` 得到的 `internal:` 地址时，作为附件；`data:` 地址存成上传的文件再作附件；其余元素按意思转成文字。顶层的 `<message>` 各发一条。回 `[Message]`，`id` 是这条输入的 ID，拿去 `message.get` 能取到它（进了记录取记录里那块，还在排队取排队里的）。
- `message.get`、`message.list`：记录的每一块是一条消息，内容见下面“记录”。`message.list` 是 BidiList，翻页标记是记录里行的偏移；`limit` 按块数、在行首截，可能略多；没有 `next` 时只能 `before`。多一个 `_data {start, end, more}`：这页从哪行起、读时记录到哪（下次 `direction: after` 从这里取新写的）、前面还有没有。`direction: after` 读到记录末尾时不带 `next`。网页读记录也走它。
- `channel.get`、`channel.list {guild_id?, next?}`（不给 `guild_id` 是全部；不列归档和子 agent）、`guild.get`、`guild.list`（只列范围里的）、`login.get`、`user.get`。
- `channel.update {channel_id, data: {name}}` 改名；`channel.delete {channel_id}` 归档（agent 停下，记录不删）。要能用这个会话。
- `upload.create`：一个 multipart，带字段 `channel_id`，每个文件回 `internal:{应用}/{登录}/files/{会话}/{ref}`。网页仍用分块的 `/api/uploads`。
- `POST /v1/meta`：本应用已批准的登录，`proxy_urls` 为空。`/v1/meta/*`（webhook）不做。
- `GET /v1/proxy/internal:…`：只取本应用登录的文件。不是地址 400，不是本应用的登录 404，别的地址 403。
- 不支持的标准接口（`message.delete` 等）照规范 404。

错误体 `{code, message}`。404 只表示接口不存在；资源不存在 422 加原因码：`no_such_session`、`no_such_message`、`no_such_guild`、`no_such_user`、`no_such_file`、`no_such_ask`、`no_such_button`。

## 记录

一块一条消息：标准元素放 Satori 有的（文字；文件按类型 `<img>`/`<video>`/`<audio>`/`<file>`，`src` 是 `internal:` 地址），其余用带适配器前缀的元素（advanced/internal.md），前缀 `iridium`。只认标准元素的客户端看到的是人和 agent 说的话。`created_at` 是块的时间。编码在 `internal/satori/blocks.go`，网页解码在 `web/src/satori.ts`，两边用同一张表 `internal/satori/testdata/blocks.json` 测，能原样还原。

| 块 | 内容 |
| --- | --- |
| 人说的 | `<iridium:user id from terminal length/>` 加文字和文件 |
| agent 说的 | 文字和文件；被截断时前面加 `<iridium:assistant length/>` |
| 思考 | `<iridium:thinking length>文字</iridium:thinking>` |
| 工具调用 | `<iridium:tool id name length error><iridium:input>参数 JSON</iridium:input><iridium:result at time length error>结果 <iridium:image/>…</iridium:result></iridium:tool>`，写出的文件跟在后面 |
| 结果（调用在别页） | `<iridium:result id length error>结果 <iridium:image/>…</iridium:result>` |
| 系统行 | `<iridium:system event from terminal id length>文字 <iridium:compact auto before after ms/></iridium:system>` |

- `length`：文字（或参数）被截到 2000 字时全文的长度，全文用 `/v1/internal/sessions/{id}/block?at=`；`message.get` 直接给全文。
- `<iridium:image mime width height unsent/>`：结果给模型的图，第 n 个用 `/v1/internal/sessions/{id}/image?at=<结果的 at>&n=`。
- 文件元素还带 `path`、`size`、`mime`、`mtime`、`thumb`、`sha256`，图片带 `width`、`height`。没有登录的调用方不带文件。
- 文字、参数、结果里的凭据打码。`message-created` 事件照旧只发 agent 的话，内容和这条块的一样。

## Iridium 自己的接口 `/v1/internal/*`

照规范的 internal 路由（advanced/internal.md）：`/v1/internal/<路径>` 就是 Iridium 自己的 `/api/<路径>`（方法、正文、回答都一样，`docs/api.md`），给 Satori 没有的东西：工具块、待发队列、模型和模式、文件夹组、设置、应用管理……带了 `Satori-Platform`、`Satori-User-ID` 就必须是调用者自己的。网页全部走这里和标准方法，不再用 `/api`。

`message.create` 多收一个 `_data`：`{steer?, attachments?, caller?}`（插话还是排队、上传好的引用），回答的 Message 也带 `_data`，是发送的结果（同 `POST /api/sessions/{id}/send` 的回答）。应用发的一律插话。

## WebSocket `/v1/events`

另有两个 Iridium 自己的信令（不是 EVENT，不存、没有 `sn`）：客户端发 `op 6` WATCH `{keys}`（或在 IDENTIFY 里带 `_watch`），说要哪些携带状态（会话列表、某个会话的整份状态、上下文…，键同 `docs/api.md`“事件流”）；服务端用 `op 7` STATE `{type: "state"|"draft", data}` 推这些状态和草稿。每次 WATCH 取代上一次。只给网页和主机的连接。

- 连上后 10 秒内 IDENTIFY。`token` 是应用 TOKEN；升级请求已带 `Authorization` 时可不带，带了必须是同一个应用。不对就关闭 4004；先发别的关闭 4000。
- READY 回本应用已批准的登录。PING 回 PONG；60 秒没有任何帧就断开（应用每 10 秒 PING）。
- 事件存库，留 24 小时（最多 1 万条），重启后照样能补发。IDENTIFY 带 `sn` 就补发之后还留着的。
- 一个事件发给本应用每个看得见这个会话的登录，各带自己的 `login` 和自己的 `sn`（事件序号 × 1024 + 第几个登录），所以断在两个登录之间也能接上。`sn` 只往上涨，重启后不会比之前的小。

事件：

| 事件 | 什么时候 | `_data.kind` |
| --- | --- | --- |
| `message-created` | 有人往会话里发了话（网页、别的应用的登录、主机的 `ir`；`message.id` 同 `message.create` 回的） | `user` |
| `message-created` | agent 回合中间说的话（下一段来了才发） | `progress` |
| `message-created` | 回合最后一段话 | `reply` |
| `message-created` | 提问、审批、计划 | `ask` |
| `message-created` | agent 进程出错 | `error` |
| `message-updated` | 提问结束（答了或撤了）：去掉按钮，加“已结束” | `ask` |
| `interaction/button` | 某个登录按了按钮 | — |
| `channel-added` / `channel-updated` / `channel-removed` | 会话出现、改名或换组、归档或消失（看得见它的登录） | — |
| `login-added` / `login-removed` | 本应用的登录被批准、被移除（发给本应用的连接；网页的连接只收自己的） | — |
| `internal`（`_type: ir.share`） | 有人分享给这个登录（`docs/api.md` 分享）；只发给它 | `share`，`_data.share` 是 `Share` |

`_type` 固定 `iridium`，`_data.origin` 是发起这个回合的应用或网页登录（知道时）。agent 说的话能在记录里找到时，`message.id` 就是块的位置；找不到时是 `live:…`，只能在 24 小时内用 `message.get` 取。

## 提问和按钮

提问作为一条消息发出，按钮 `id` 是 `<卡片>#<选项>`：

- `<button type="action">`：应用按下时 `POST /v1/internal/button {id}`。只需一个选择的（审批、计划、单个单选问题）立即作答；多个问题或多选的按钮累计，按 `#submit` 提交。也可以一次答完：`{id: <卡片>, choices, words, message}`（同 CardAnswer）。
- `<button type="input">`：`text` 是 `<卡片>#w<题号> `、`#deny `、`#reject `，用户接着写的话用 `message.create` 发出，就是这一题自己的回答或拒绝的理由，不进会话。

## 元素

收发都用 Satori 元素（`internal/satori/elements.go`）。Iridium 发：文字、`<img>`/`<file>`（`internal:` 地址）、`<button>`、`<p>`、`<b>`。会话用 `<sharp>`，位置和文件用 `<a>`，不用 `<at>`（会真的提醒人）；收到的 `<at>` 只当文字。

## 和规范不一样的地方

- 资源不存在回 422 而不是 404（`/api` 也一样）。
- 按钮反过来：`interaction/button` 是登录按了 Iridium 发的按钮之后发出的；按下本身走 `/v1/internal/button`。
- 没有 `guild-*`、`login-updated` 事件。`channel.update` 只能改名，`channel.delete` 是归档（不删记录）。
- 没有 META 信令、webhook、`proxy_urls`；草稿、状态等自定义 op（≥6）还没有。
- 发起登录不叫 `referrer`，放在 `_data.origin`，目前是应用 ID。服务端已按回合记下发起的登录（agent 的 `ir` 按它算权限），事件里还没带出来。
- 权限：每个登录按“登录的 ∩ 应用的”权限；范围外的会话（channel）和文件夹组（guild）看不见。
- `message.create` 里的 `<message>` 各发一条；`data:` 资源存成上传的文件；`https:` 资源不下载，转成文字。

## 还没做

- 主机终端里直接打的话（不经 Iridium）没有 `user`，也不发 `message-created`。
- 提问多选时按到一半的按钮只在内存里，重启后要从头按。
