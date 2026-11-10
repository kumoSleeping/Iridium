# Bot 规范

> 旧文档：写的是 0.1.x（v0.1.40 前）的做法，只当待删清单用。0.2 里没有 Bot 这个概念。

`k_` 开头的文档：每个字都对过代码。

## 1. Bot 是什么

Iridium 的终端只有两种：面板（网页，passkey 登录）和 Bot（程序，凭 TOKEN）。代码里 Bot 叫 `channel`。

Bot 按作用分两种，类型和叫法不分：

- **用 Iridium 的**：发消息、收事件、开会话。例：QQ Bot（`iridium-qq`）、脚本、以 Bot 身份接入另一个 Iridium 的 `ir pair`。
- **提供机器的**：执行端。把自己那台机器交给 Iridium，这边在那里起 agent、读写文件。例：dsh 插件、`ir join`。

每个 Bot 有唯一的名字（最多 12 字，查重不分大小写）、固定的 ID、头像、用量。请求带 `Authorization: Bearer <TOKEN>`。

### 和面板一样的

接口是同一套，面板能调的 Bot 大多也能调：开会话、发消息、答提问、传文件、看会话列表等。全部接口见附录。

### 只有面板能做的

Bot 调这些回 403：

- 终端：看终端列表，发邀请和 TOKEN，删终端，设 Bot 的跟随，改别的终端的设备标识。
- 确认：看和答“需要你”里的确认（配对申请、TOKEN、凭据）。
- 设置：看和改设置、公网开关，检查和执行更新，官方 Bot 的安装和接入。
- 执行命令：在会话所在的机器或执行端上跑命令（`ir sh`）。
- 凭据：删除，设谁能用；取值（只有本机的 `ir run` 能取）。
- 便签、配色、组的排序、文件夹的摆放、工作区的修改和拆分。
- 看别的终端的用量。
- 把一个 Bot 拉进会话（网页会话菜单）。

### Claude

终端能不能用 Claude，在建立时定下，之后不改。Bot 只有这两种情况有：本人在本机或在带 Claude 的面板上用 `ir token --owner` 建的；配对申请时，带 Claude 的面板打开了“带 Claude”。没有 Claude 的 Bot 看得到 Claude 的会话，但不能开、不能发消息、不能答。

## 2. QQ Bot 发什么、收什么

QQ Bot（`iridium-qq`）是用 Iridium 的那种。每个主人的私聊是一个聊天，键 `c2c:<openid>`。只处理主人的私聊。

### 发给 Iridium

| 请求 | 什么时候 |
|---|---|
| `POST /api/chat/{聊天}/input` | 主人发来文字、语音（带 QQ 的转写，接在后面 `[语音转写] …`）、附件，菜单发的命令也是消息。不带 `sender` |
| `POST /api/chat/{聊天}/uploads`，`PUT /api/uploads/{id}`，`POST /api/uploads/{id}/complete` | 主人发来图片、文件：先从 QQ 下载，传进会话，拿 `ref` 放进 `input`。没选会话（409）时不传，消息里写“（附件 … 没传：先选会话）” |
| `POST /api/answer` | 点卡片按钮，或直接回选项名、`1A`、`答1：…`、`拒绝：理由` |
| `GET /api/chat/{聊天}/files/{ref}` | 回复里带文件：取来发到 QQ，最多 20MB |
| `GET /api/chat/{聊天}/commands` | 连上时取命令，只留 Iridium 的（最多 20 个），设 QQ 的“/”面板。用第一个主人的聊天 |
| `GET /api/me` | 连上时取终端名（网页上改过名就写回自己的配置）；`setup` 时查 TOKEN 还认不认 |
| `PUT /api/me/profile` | 连上时把 Bot 在 QQ 上的头像设成终端头像；每次运行最多一次，图和 TOKEN 都没变就不传 |
| `GET /api/events` | 一直连着，断了重连，从上次的序号接着收 |

### 收到并处理的事件

| 事件 | QQ 上 |
|---|---|
| `reply` | 发回复（Markdown，Bot 没开通就发纯文字）；`files` 逐个取来按图片或文件发 |
| `card` | 画成按钮卡片（提问、审批、计划、选择） |
| `card_done` | 忘掉这张卡片：之后回选项名不再算答它。按钮还在 QQ 上，点了 Iridium 回 404 或 410，Bot 回“这个提问已经结束。” |
| `error` | 发“出错：…” |

收到但不处理：`progress`、`status`。

### 不经 Iridium 的

- 别人发来配对码（`iridium-qq pair` 生成）：加为主人，回“已配对。之后在这里说的话交给 Iridium。”；别的消息丢掉。
- 菜单：底部菜单和“/”面板直接设到 QQ。

## 3. 所有 Bot 收发什么

Bot 和 Iridium 之间有三条路。

| 路 | 谁用 | 方向 |
|---|---|---|
| HTTP 接口 | 所有 Bot | Bot 请求，Iridium 回答 |
| 事件流 `GET /api/events` | 用 Iridium 的 | Iridium 推给 Bot |
| WebSocket `/api/exec` | 提供机器的 | 两边都发请求，也都发事件 |

### 3.1 HTTP 接口

除了第 1 节“只有面板能做的”，面板能调的 Bot 都能调。用 Iridium 的 Bot 主要用聊天这一层：

- 发消息：`POST /api/chat/{聊天}/input`。普通文字进指针指着的会话；Iridium 的命令（`/new`、`/clear`、`/use`、`/leave`、`/ls`、`/history`、`/stop`、`/model`、`/status`、`/usage`、`/context`）在这里执行，结果在回答里（`reply` 或 `card`），不走事件流；别的 `/` 开头原样交给 agent。
- 传附件：`POST /api/chat/{聊天}/uploads`，`PUT /api/uploads/{id}`，`POST /api/uploads/{id}/complete`。
- 答卡片：`POST /api/answer`。
- 查：指针 `GET /api/chat/{聊天}`，命令 `GET /api/chat/{聊天}/commands`，文件 `GET /api/chat/{聊天}/files/{ref}`。
- 自己：`GET /api/me`，`PUT /api/me/profile`（头像）。

### 3.2 事件流（用 Iridium 的）

带 `Last-Event-ID`（或 `?after=序号`）从那条之后接着收，都不带从现在起；空闲时 15 秒一次心跳；事件存 24 小时。每条带 `chat`、`session`。

| 分类 | 事件 | 发给谁 |
|---|---|---|
| 会话的回合 | `progress`（回合中的话）、`reply`（最终回复，带文件）、`error`（agent 出错退出）、`status`（状态灯） | 指着这个会话的聊天 |
| 需要你 | `card`（`permission`、`question`、`plan`）、`card_done` | 指着的聊天；没有就发给跟随发起这一回合的面板的 Bot，和发起这一回合的 Bot |
| 会话找你（`ir bot`） | 有聊天指着：`reply`；没有：`card`（`pick`：进入会话、关闭） | 指着的，或挑出来的聊天 |
| 网页把 Bot 拉进会话 | `reply`，同 `/use` | 那个 Bot 的聊天 |

### 3.3 WebSocket（提供机器的）

执行端带 TOKEN 连 `/api/exec`。每条是一个 JSON 帧：请求 `{id, method, params}`，回答 `{id, result}` 或 `{id, error: {code, message}}`，事件 `{event, params}`。错误码：`not_found`、`exists`、`denied`、`bad_request`、`unsupported`、`failed`。

执行端发给 Iridium：

| 种类 | 名字 |
|---|---|
| 请求 | `hello`：连上先发，报协议版本、程序、系统、家目录、环境、agent、手上的进程、`noShell`、`agentShell`、`irClient`、`caps`；回 `{name}`（终端名） |
| 事件 | `out`（进程输出）、`exit`（进程结束）、`held.changed`（DSH Desktop 里会话的状态变了，只有 dsh 插件） |

Iridium 发给执行端（都是请求）：

| 种类 | 名字 |
|---|---|
| 进程 | `hold.start`、`hold.write`、`hold.tail`、`hold.stop`、`hold.kill`、`hold.forget` |
| 文件 | `fs.stat`、`fs.list`、`fs.read`、`fs.write`、`fs.mkdir` |
| 只有 dsh 插件（别的回 `unsupported`） | `held.list`、`held.send`、`held.get`、`held.set`（旧名 `dsh.open`、`dsh.prompt`）；`dsh.attach`、`dsh.presets`、`dsh.bundles`、`dsh.llm`、`dsh.session` |

两边都每 25 秒发一次 `ping` 事件；对方发过 `ping`、之后 70 秒没动静，就断开。

执行端上跑的会话，它的 `ir` 用会话自己的 TOKEN 发 `POST /api/cli`，算那个 Bot 代这个会话发的。

## 4. 事件细节

### 4.1 聊天事件从哪来

只有 Iridium 自己跑着的会话（托管的）发事件，从记录回放出来的不发。会话里发生的事先变成通知，聊天层再把通知变成事件：

| 会话里 | 事件 |
|---|---|
| 回合开始 | 不发；清掉压着的话，记下这一回合是谁发起的 |
| agent 说完一段话 | 压着这段；之前压着的那段发 `progress` |
| agent 调用工具 | 压着的那段发 `progress`，清掉 |
| 回合结束 | `reply`：用回合的最终回复，没有就用压着的那段；auto 模式拦过步骤，末尾加“（auto 拦了 N 步）”；都没有就不发 |
| 开了一个提问或审批 | `card`。按会话规则自动允许的审批不开卡片，不发 |
| 提问或审批结束（在哪答的都算，或 agent 撤回） | `card_done` |
| agent 进程出错退出 | `error` |
| 状态灯变了 | `status` |

agent 的话里 `[标题] …` 行在通知前就去掉了。

### 4.2 发给谁

- 先找指针指着这个会话的所有聊天（可能跨多个 Bot），每个都发。
- 一个都没有时，只发 `card` 和 `card_done`，别的丢掉。`card` 发给“跟随者”：跟随这一回合发起者的 Bot；发起者自己是有过聊天的 Bot 时也算。每个 Bot 挑一个聊天（见下）。这样发出的 `card`，标题换成会话名，原标题放到 `summary` 第一行；之后的 `card_done` 发给同样这些聊天。
- 发起者：发这一回合那条消息的终端；是别的会话的 agent 用 `ir send`、`ir new` 派来的，就取派活那个会话当时那一回合的发起者。还没有回合时，取开这个会话的终端。
- 挑聊天：在这个 Bot 有过的、能用这个会话的聊天里，有连着的就只看连着的；其中最近进过这个会话的优先，都没进过就取最近说过话的。

### 4.3 每个事件带什么

都带 `chat`、`session`。

**`progress`**：`text`，agent 的一段话（Markdown 原文）。

**`reply`**：`reply{markdown, text, files}`。

- `files`：正文里 `` [文件] `绝对路径` ``（代码块外）的行，文件存在、是普通文件、不在受保护的目录（`~/.ssh`、`~/.gnupg`、Iridium 的配置和状态目录、各 agent 的家目录）里，就放进 `files`：`{name, size, type, ref}`，`type` 按扩展名猜，猜不出是 `application/octet-stream`。放进去的行从正文去掉，没放进去的留着。
- `text`：去掉代码块的 ``` 行、标题的 `#`、链接只留文字、粗体只留文字、行内代码去掉反引号。
- `ref` 签过名，只对这个 Bot 有效。

**`card`**：`card{id, kind, session, title, summary[], options[], multi[], questions[]}`。选项 `{id, text, description?, primary?, group?}`。

| `kind` | `id` | `title` | `summary` | 选项 |
|---|---|---|---|---|
| `permission` | `ask:<会话>:<请求>` | 工具名 | 要做什么（最多 300 字），说明（最多 200 字） | `allow` 允许、`deny` 拒绝、`always` 本会话一直允许（后面写规则） |
| `question` | 同上 | 提问 | 每个问题一行 | 每个问题一组，`id` 是 `问题序号.选项序号`，每组第一个是主选项；`multi` 是多选的组，`questions` 是问题原文 |
| `plan` | 同上 | 计划 | 计划的前 6 行 | `approve` 批准、`reject` 拒绝 |
| `pick` | `pick:<随机>` | 看来由 | 看来由 | 选项 `id` 就是命令的参数；`-` 是关闭 |

`pick` 来自 `/use`、`/new`、`/model`、未选择会话时发的消息（这些在请求的回答里），和 `ir bot`（在事件里）。

**`card_done`**：`cardId`。

**`error`**：`text`：退出码或信号，加 stderr 的最后一段；或者 Iridium 放弃这个 agent 进程、把它停掉的原因。崩溃时 Iridium 可能接着续跑。

**`status`**：`status`，取值 `idle`、`starting`、`running`、`compacting`、`waiting`（等人答）、`error`、`elsewhere`。只在变了时发。

### 4.4 执行端的事件

| 事件 | 方向 | 带什么 |
|---|---|---|
| `out` | 执行端 → Iridium | `{hold, off, data}`：进程 stdout 的若干整行，`off` 是第一个字节在输出文件里的偏移 |
| `exit` | 执行端 → Iridium | `{hold, from, code, signal, errTail}`：`from` 是 `agent`（进程自己结束）或 `holder`（执行端没看到它结束就没了）；`errTail` 是 stderr 的末尾 |
| `held.changed` | 执行端 → Iridium | `{session, live}`：DSH Desktop 里这个会话的状态变了（模型、强度、附加功能）。只有 dsh 插件，按 `caps` |
| `ping` | 双向 | 心跳，每 25 秒 |

## 5. 范围（用户，2026-11-10）

来自会话 f7af93d5 里用户认同的部分。

- **没有文件夹授权。** Bot 接入后就是完全授权的，不再有按会话、按提及给的授权。原来 `exec.md` 的“提及即授权”删掉（它只在文档里，代码没有）。
- **终端有范围。** 面板和 Bot 建立时可以限定只能用哪些文件夹、哪些远端：侧边栏里只有这些；在别处开会话会被拦。不限定就是全部。
- **范围可以改。** 之后由有“管理”权限的连接给别的连接改（用户，2026-11-10；见 `draft.md`“管理”）。

（未实现：现在没有终端范围，所有终端都能用全部位置。）

### @

@ 在 Iridium 里只是指一个东西，不带任何发送的含义：不送达、不通知。要找谁，由 agent 自己用 `ir bot` 去找。

## 附录：全部接口，Bot 能不能调

按 `internal/server` 里注册的路由逐个查的。“能”：Bot 的 TOKEN 调得通；“Claude”：对 Claude 的会话，Bot 要有 Claude；“不能”：回 403（或 401）。

### 会话

| 接口 | Bot |
|---|---|
| `GET /api/sessions`、`GET /api/sessions/{id}`、`/messages`、`/block` | 能（凭据的值打码） |
| `GET /api/sessions/{id}/image`、`/presets`、`/rules` | 能 |
| `POST /api/sessions` 开会话 | Claude |
| `POST /api/sessions/{id}/send` 发消息 | Claude |
| `POST /api/sessions/{id}/interrupt`、`/stop`、`/answer`、`/release`、`/continue` | Claude |
| `PUT /api/sessions/{id}/model`、`/mode`、`/effort`、`/ultracode`、`/preset`、`/features/{feature}` | Claude |
| 待发队列：`POST …/queue/{input}/steer`、`PUT`、`DELETE …/queue/{input}` | Claude |
| `DELETE /api/sessions/{id}/rules` | Claude |
| `PATCH /api/sessions/{id}`：改名、归档等 | 能 |
| `POST /api/sessions/{id}/shell`、`POST /api/machines/{id}/shell`、`GET`/`DELETE /api/shell/{run}` | 不能 |
| `POST /api/sessions/{id}/bot` 把 Bot 拉进会话 | 不能 |

### 文件

| 接口 | Bot |
|---|---|
| `GET /api/sessions/{id}/files/{ref}`、`/thumb`、`/info` | 能 |
| `POST /api/sessions/{id}/uploads`、`POST /api/sessions/{id}/attach` | Claude |
| `GET`、`PUT`、`DELETE /api/uploads/{upload}`，`POST /api/uploads/{upload}/complete` | 能 |
| `GET /api/files` 列目录、`POST /api/files/copy` 复制 | 能（受允许的目录限制） |

### 聊天层

| 接口 | Bot |
|---|---|
| `GET /api/chat/{key}`、`POST …/input`、`GET …/commands`、`GET …/files/{ref}`、`POST …/uploads` | 能（开会话、发消息同样受 Claude 限制） |
| `POST /api/answer` | 能（同上） |
| `POST /api/bot`（`ir bot`） | 不能：只认本机 socket 上会话里的 `ir` |

### 侧边栏

| 接口 | Bot |
|---|---|
| `GET /api/folders`、`POST /api/folders` 建文件夹、`GET /api/workspaces` | 能 |
| `PATCH /api/workspaces/{id}`、`POST …/split`、`PUT /api/groups/order`、`PATCH /api/folders/place`、`PUT /api/colors` | 不能 |
| `GET /api/machines`、`GET /api/machines/{id}/dsh/workspaces`、`POST /api/machines/{id}/dsh/sync` | 能 |
| 便签 `/api/notes`、`/api/note-folders` 全部 | 不能 |

### 推送和状态

| 接口 | Bot |
|---|---|
| `GET /api/events` 不带 `sub`：聊天事件 | 能 |
| `GET /api/events?sub=…`：网页的状态流 | 能；`confirms`、`colors`、`notes` 三项被忽略 |
| Web Push：`GET`/`PUT /api/push`，`PUT /api/push/defaults`、`/rules`，`POST`/`DELETE /api/push/subscriptions`，`POST /api/push/test` | 能（没有检查） |

### 终端和自己

| 接口 | Bot |
|---|---|
| `GET /api/me`，`PUT /api/me/profile`，`PUT /api/me/device` | 能 |
| `PUT /api/terminals/{id}/profile`、`/device`（别的终端的） | 不能 |
| `GET /api/usage` | 只看自己的 |
| `GET /api/terminals`、`DELETE /api/terminals/{id}`、`POST /api/terminals/invites`、`/tokens`、`PUT /api/terminals/{id}/follows` | 不能 |
| `GET /api/confirms`、`POST`/`GET /api/confirms/{id}` | 不能 |

### 凭据

| 接口 | Bot |
|---|---|
| `GET /api/secrets` 列名字和备注 | 能 |
| `PUT /api/secrets/{name}` 存值 | 能，但不能设谁能用 |
| `DELETE /api/secrets/{name}` | 不能 |
| `POST /api/secrets/use` 取值 | 不能：只认本机 socket 上的 `ir run` |

### 设置、更新、官方 Bot

| 接口 | Bot |
|---|---|
| `GET /api/health`、`GET /api/agents`、`GET /api/update`、`GET /api/extensions` | 能 |
| `GET`/`PUT /api/settings`、`GET`/`PUT /api/public`、`POST /api/update/check`、`POST /api/update` | 不能 |
| `POST`/`DELETE /api/extensions/…`：安装、接入、启停、移除 | 不能 |

### 接入和机器

| 接口 | Bot |
|---|---|
| `POST /api/pair`、`GET /api/pair/{request}`、`POST /api/exec/pair`、`GET /api/exec/pair/{request}` | 不用登录，谁都能 |
| `GET /api/exec`（WebSocket） | 只有 Bot 的 TOKEN 能连（面板、本机、会话的 TOKEN 都不行） |
| `POST /api/cli` | 只认执行端上会话的 TOKEN |
