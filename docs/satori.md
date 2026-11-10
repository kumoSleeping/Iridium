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
| `message` | 记录里用户和 agent 的块，`id` 是块的位置 `<偏移>.<序号>`；提问是 `ask:<会话>:<请求>` |

鉴权：应用用 `Authorization: Bearer <应用 TOKEN>`（过 gate）。网页也是应用（一视同仁）：浏览器凭 cookie，`Satori-Platform: ir_web`、`Satori-User-ID: <登录 ID>`；没开公网时主机自己的页面是平台 `local` 的登录 `local`。这两种连接只代表这一个登录。会话 TOKEN 403 `no_login`。`Satori-Platform` 不是本应用 ID、没有 `Satori-User-ID`：403 `no_such_login`。新登录第一次说话：403 `login_pending`，产生“登录加入”需要（和 `/api` 一样）。

## HTTP `POST /v1/{资源}.{方法}`

- `message.create {channel_id, content}`：发进会话（插入正在跑的回合），发送者显示为 `应用·登录`。`<img>`/`<file>` 的 `src` 是本登录在这个会话 `upload.create` 得到的 `internal:` 地址时，作为附件；其余元素按意思转成文字。回 `[Message]`，`id` 是这条输入的 ID。
- `message.get`、`message.list`：`message.list` 是 BidiList，翻页标记是记录里行的偏移；`limit` 按行截，可能略多；没有 `next` 时只能 `before`。只含用户和 agent 的话（工具、思考、系统行看网页的 `/api/sessions/{id}/messages`）。
- `channel.get`、`channel.list {guild_id?, next?}`（不给 `guild_id` 是全部；不列归档和子 agent）、`guild.get`、`guild.list`、`login.get`、`user.get`。
- `upload.create`：一个 multipart，带字段 `channel_id`，每个文件回 `internal:{应用}/{登录}/files/{会话}/{ref}`。网页仍用分块的 `/api/uploads`。
- `POST /v1/meta`：本应用已批准的登录，`proxy_urls` 为空。`/v1/meta/*`（webhook）不做。
- `GET /v1/proxy/internal:…`：只取本应用登录的文件。不是地址 400，不是本应用的登录 404，别的地址 403。
- 不支持的标准接口（`message.delete` 等）照规范 404。

错误体 `{code, message}`。404 只表示接口不存在；资源不存在 422 加原因码：`no_such_session`、`no_such_message`、`no_such_guild`、`no_such_user`、`no_such_file`、`no_such_ask`、`no_such_button`。

## Iridium 自己的接口 `/v1/internal/*`

照规范的 internal 路由（advanced/internal.md）：`/v1/internal/<路径>` 就是 Iridium 自己的 `/api/<路径>`（方法、正文、回答都一样，`docs/api.md`），给 Satori 没有的东西：工具块、待发队列、模型和模式、文件夹组、设置、应用管理……带了 `Satori-Platform`、`Satori-User-ID` 就必须是调用者自己的。网页全部走这里和标准方法，不再用 `/api`。

`message.create` 多收一个 `_data`：`{steer?, attachments?, caller?}`（插话还是排队、上传好的引用），回答的 Message 也带 `_data`，是发送的结果（同 `POST /api/sessions/{id}/send` 的回答）。应用发的一律插话。

## WebSocket `/v1/events`

另有两个 Iridium 自己的信令（不是 EVENT，不存、没有 `sn`）：客户端发 `op 6` WATCH `{keys}`（或在 IDENTIFY 里带 `_watch`），说要哪些携带状态（会话列表、某个会话的整份状态、上下文…，键同 `docs/api.md`“事件流”）；服务端用 `op 7` STATE `{type: "state"|"draft", data}` 推这些状态和草稿。每次 WATCH 取代上一次。只给网页和主机的连接。

- 连上后 10 秒内 IDENTIFY。`token` 是应用 TOKEN；升级请求已带 `Authorization` 时可不带，带了必须是同一个应用。不对就关闭 4004；先发别的关闭 4000。
- READY 回本应用已批准的登录。PING 回 PONG；60 秒没有任何帧就断开（应用每 10 秒 PING）。
- 事件存在内存里 24 小时（最多 1 万条），`sn` 是序号，从启动时刻的毫秒数往上数，重启后不会比之前的小。IDENTIFY 带 `sn` 就补发之后还留着的。重启后之前的事件没有了。
- 一个事件发给本应用每个看得见这个会话的登录，各带自己的 `login`，`sn` 相同。

事件：

| 事件 | 什么时候 | `_data.kind` |
| --- | --- | --- |
| `message-created` | agent 回合中间说的话（下一段来了才发） | `progress` |
| `message-created` | 回合最后一段话 | `reply` |
| `message-created` | 提问、审批、计划 | `ask` |
| `message-created` | agent 进程出错 | `error` |
| `message-updated` | 提问结束（答了或撤了）：去掉按钮，加“已结束” | `ask` |
| `interaction/button` | 某个登录按了按钮 | — |

`_type` 固定 `iridium`，`_data.origin` 是发起这个回合的应用或网页登录（知道时）。agent 说的话能在记录里找到时，`message.id` 就是块的位置；找不到时是 `live:…`，只能在 24 小时内用 `message.get` 取。

## 提问和按钮

提问作为一条消息发出，按钮 `id` 是 `<卡片>#<选项>`：

- `<button type="action">`：应用按下时 `POST /v1/internal/button {id}`。只需一个选择的（审批、计划、单个单选问题）立即作答；多个问题或多选的按钮累计，按 `#submit` 提交。也可以一次答完：`{id: <卡片>, choices, words, message}`（同 CardAnswer）。
- `<button type="input">`：`text` 是 `<卡片>#w<题号> `、`#deny `、`#reject `，用户接着写的话用 `message.create` 发出，就是这一题自己的回答或拒绝的理由，不进会话。

## 元素

收发都用 Satori 元素（`internal/satori/elements.go`）。Iridium 发：文字、`<img>`/`<file>`（`internal:` 地址）、`<button>`、`<p>`、`<b>`。会话用 `<sharp>`，位置和文件用 `<a>`，不用 `<at>`（会真的提醒人）；收到的 `<at>` 只当文字。

## 和规范不一样的地方

- 资源不存在回 422 而不是 404。
- 按钮反过来：`interaction/button` 是登录按了 Iridium 发的按钮之后发出的；按下本身走 `/v1/internal/button`。
- 没有 `login-*`、`channel-*`、`guild-*` 事件；登录新批准后要重连才在 READY 里，但事件照发。
- 没有 META 信令、webhook、`proxy_urls`；草稿、状态等自定义 op（≥6）还没有。
- 发起登录不叫 `referrer`，放在 `_data.origin`，目前是应用 ID。服务端已按回合记下发起的登录（agent 的 `ir` 按它算权限），事件里还没带出来。
- 权限：每个登录按“登录的 ∩ 应用的”权限；范围外的会话（channel）看不见。文件夹组（guild）还没按范围过滤。

## 还没做（审计记下的）

- 消息的 `user.id`：`message.create` 回登录 ID，同一条进了记录后 `message.list`/`get` 给的是应用 ID；主机终端打的消息没有 `user`。
- `message.create` 回的 `id` 不能拿去 `message.get`；`message.get` 从 outbox 取时缺 `channel`/`guild`。
- 只有 agent 的话发 `message-created`，别人（网页、别的登录、主机终端）发的不发。
- `<img src="https:…">`、`data:` 资源不下载，转成文字；`<message>` 不拆成多条。
- `message.list` 不带 `limit` 时多给一个 `next`；同一事件给多个登录共用一个 `sn`，续传可能丢。
- `guild.*` 不按范围过滤；IDENTIFY 的 TOKEN 不计入 gate 的失败限速。
- 没有 `login-*`、`channel-*` 事件和 `channel.update`/`channel.delete`；事件和按钮只在内存里 24 小时，重启就没。
