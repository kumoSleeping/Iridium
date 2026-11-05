# Iridium 接口

一套接口，两个入口：

- HTTP：`127.0.0.1:7810`（配置 `listen`），给网页。只认本机的主机名（`localhost`、`127.0.0.1`、`::1` 和配置 `hosts`）；非 GET 请求带了别的 `Origin` 就拒绝。
- unix socket：`$XDG_RUNTIME_DIR/iridium/ir.sock`（0600），给本机 `ir`。

来源名：HTTP 来的消息记作 `网页`，socket 来的记作 `本机`（交互终端做好后换成终端名）。agent 看到的消息开头是 `[来源名] `（`/` 开头的命令除外），读记录时服务端去掉它，放进消息块的 `from`。

类型都在 `internal/api`。`go generate ./internal/api` 用 tygo 生成 `web/src/api/types.gen.ts`，前端只用它。

出错时状态码非 2xx，正文 `{"error": "…"}`：400 请求不对，404 没有这个会话或位置，409 agent 没在运行，504 agent 没按时回。

## 请求

| 请求 | 正文 → 回答 | 说明 |
|---|---|---|
| `GET /api/sessions` | → `Session[]` | 会话列表（现在只有托管的；自动发现以后并进来）。置顶在前，再按最近 |
| `POST /api/sessions` | `NewSession` → `{id}` | 在 `cwd` 开一个托管会话，带 `text` 就作为第一条消息。`agent` 现在只有 `claude` |
| `GET /api/sessions/{id}` | → `SessionState` | 打开的会话的状态，和事件流里的同一份 |
| `GET /api/sessions/{id}/messages` | → `MessagePage` | 不带参数：最新一页。`before=<start>`：往回一页。`after=<end>`：之后新写的 |
| `GET /api/sessions/{id}/block?at=<at>` | → `{text}` | 截短的块（工具参数、结果、思考、系统行）的全文 |
| `POST /api/sessions/{id}/send` | `{text, steer?}` → `{input, queued?}` | 发消息。agent 没在跑就先拉起（有记录就 `--resume`）。回合进行中发的进待发队列（`queued`），`steer` 则并进当前回合。见“待发队列” |
| `POST /api/sessions/{id}/interrupt` | → `{ok}` | 停掉当前回合（Esc），agent 留着。队列暂停 |
| `POST /api/sessions/{id}/stop` | → `{ok}` | 停掉 agent 进程（关 stdin，写完记录自己退出）。会话还在，下一条消息再拉起。队列暂停 |
| `POST /api/sessions/{id}/answer` | `Answer` → `{ok}` | 回答提问：审批 `allow`（不许时可带 `message`；`always` 见“会话设置”）；选择题 `answers`（问题原文 → 选项 `label`）；计划确认 `allow` |
| `GET /api/events?sub=…` | SSE | 见下。不带 `sub`：插件的事件流，见“插件” |
| `GET /api/health` | → `{ok}` | |

## 消息页

- 一页最多 20 条消息（你的话、agent 的话），中间的块跟着走，最多 200 块。
- `start`：这页第一行的字节位置，往回翻用 `before=<start>`。`end`：这页覆盖到哪，取新的用 `after=<end>`。`more`：前面还有。
- 块（`Block.kind`）：`user`、`assistant`、`thinking`、`tool`（同页的结果在 `result` 里）、`result`（调用在别的页，按 `id` 拼）、`system`（`event`：`compact`、`api_error`、`local_command`、`interrupted`、`unreadable`、`iridium`）。`iridium`：Iridium 自己写给 agent 的话（恢复提示、“继续”），`text` 是原文，页面画成居中一行淡字。
- 工具参数、结果、思考、系统行超过 2000 字只给开头，`length` 是全长，全文按 `at` 取。你的话和 agent 的话不截。

## 事件流（携带）

每个页面一条：`GET /api/events?sub=list&sub=session:<id>&sub=context:<id>&sub=quota`。换了打开的会话就关掉重开。

订阅的东西：

| 键 | 值 |
|---|---|
| `list` | `Session[]` |
| `session:<id>` | `SessionState`：状态、提问（带 `card`）、后台任务、模型、审批模式、思考强度、可选的模型/模式/强度、待发队列、上下文总数、等额度到几点、`tail`、`end`、`proc`、出错原因 |
| `context:<id>` | `Context`：上下文用了多少、分项。回合结束和压缩后更新；存在托管目录，没进程时也有 |
| `quota` | `Quota`：按 agent + 账号（Claude：邮箱和套餐）的额度窗口、读到的时间、`live` |

事件：

- `state`：`{key, value}`，一样东西的整份。不存在时 `value` 是 `null`。
- `draft`：`{session, proc, n, text}`，agent 正在打的字，接在第 `n` 段草稿后面。
- 每 15 秒一行注释（`: ping`）保活。

规则：

- 开流（含断线重连）先发每样的整份；之后变了再发整份，不发补丁。同一样 250ms 内最多一次，发的是最新的。
- 读得慢时中间的状态直接丢掉，只送最新的。
- 草稿每 100ms 攒一包，只拼接不丢。

页面怎么用：

- 打开会话：取最新一页，记下 `end`。`session:<id>` 的 `end` 比自己的大：
  - `tail.blocks` 不是 `null`，且自己的 `end` 在 `[tail.start, tail.end]` 里：直接用。位置在自己 `end` 之后的追加；`at` 已有的换掉（工具的结果来了）。`end` 记成 `tail.end`。
  - 否则（块大、块多、中间丢过状态）`after=<自己的 end>` 取新块。
  - `tail` 至少往回盖 1 秒；只装没截短的块，JSON 不超过 16KB。
- 草稿：同一 `proc`、同一 `n` 的 `text` 接起来；`n` 变了，上一段封口（记录里马上会有这段话）；`proc` 变了，丢掉所有草稿。记录里来了 agent 的话，就换掉最早封口的那段。重连后清掉草稿，照 `end` 取新块。

## 状态灯（`Status`）

`idle` 无灯，`starting` 启动中，`running` 运行中，`compacting` 压缩中，`waiting` 需要你（有提问），`error` 上次进程出错退出（原因在 `SessionState.error`），`elsewhere` 在别处运行（自动发现做好后才有）。`live` 说 agent 进程在不在跑。

## 待发队列

照 Rhodium 网页的待发队列（architecture §6.1）。

- 回合在跑时 `send` 的话进队列，不插话；回合正常结束，排着的一起作为下一个回合发出（第一条开回合，其余并进去）。
- 等额度期间发的也排队，额度回来时发。
- 打断、停止、进程退出或崩溃：队列暂停（`queue[].paused`）。只有“继续”或那一条的“插话”才放；别的回合结束都不放。暂停时新发的话：没有回合在跑就直接发。
- 队列存在库里（`inputs` 表），重启后还在。`SessionState.queue` 带着整份。

| 请求 | 正文 → 回答 | 说明 |
|---|---|---|
| `POST /api/sessions/{id}/queue/{input}/steer` | → `{ok}` | 这一条马上发，并进正在跑的回合（没有就开新回合） |
| `PUT /api/sessions/{id}/queue/{input}` | `{text}` → `{ok}` | 改这一条 |
| `DELETE /api/sessions/{id}/queue/{input}` | → `{ok}` | 删这一条 |
| `POST /api/sessions/{id}/continue` | → `{ok}` | 放开暂停的队列：没有回合在跑就马上发，否则等这个回合结束 |

不在队列里（已发、已删）：404。

## 会话设置

按会话记在库里：马上设给在跑的 agent，之后每次拉起都带上（Claude：`--model`、`--permission-mode`、`--effort`）。

| 请求 | 正文 | 说明 |
|---|---|---|
| `PUT /api/sessions/{id}/model` | `{model}` | `""` 是 agent 的默认。Claude：`set_model` |
| `PUT /api/sessions/{id}/mode` | `{mode}` | 审批模式，只收 `SessionState.modes` 里的。Claude：`set_permission_mode` |
| `PUT /api/sessions/{id}/effort` | `{effort}` | 思考强度，只收 `SessionState.efforts` 里的。Claude：先 `get_settings` 问 ultracode 开没开，再 `apply_flag_settings` 照原样带上 |
| `GET /api/sessions/{id}/rules` | → `AllowRule[]` | “本会话一直允许”记下的规则 |
| `DELETE /api/sessions/{id}/rules` | → `{ok}` | 清掉 |

- 新会话的审批模式：`NewSession.mode`，没给就用配置 `modes.<agent>`，再没有就是 agent 自己的。
- 本会话一直允许：`answer` 带 `always: true`（或卡片选 `always`）。记下工具和 `Ask.pattern`（Claude 自己建议的规则，例如 `ls *`；没有时 shell 命令取第一个词加 ` *`，别的工具不带模式＝这个工具全部）。之后这个会话里匹配的审批自动允许，不通知、不出卡片。
- 匹配：`*` 是任意一段字；`前缀:*` 同 `前缀*`。shell 命令里有 `;`、`&&`、`||`、`|`、`` ` ``、`$(`、换行的，一律不按模式放行。

## 续跑

配置 `crashResume`、`quotaResume`，默认都开。

- 崩溃续跑（Rhodium §4.8、product §4.5）：Iridium 没要求的退出（非 0、信号、托管进程没了）算崩溃。只有 agent 已经有了这个回合（收下过它的输入，或自己开始的）才续跑，否则等你的下一条消息。
  - 用 `--resume` 拉起，Claude 另设 `CLAUDE_CODE_RESUME_INTERRUPTED_TURN=1`，它自己重跑被打断的回合。握手应答时还没有回合开始，就发恢复提示（Iridium 行）：`上一回合被打断，先检查现状再继续`。
  - 同一会话 10 分钟内第 3 次崩溃就不再续跑（熔断），状态停在 `error`。
  - 挂着的提问作废；队列暂停。
- 额度续跑（product §4.6）：agent 报额度用完（Claude `rate_limit_event` rejected），`SessionState.quotaUntil` 记恢复时间（错开 0–60 秒）。期间发的话排队。到点：有排队的就发排队的，没有就发 Iridium 行 `继续`。期间 `interrupt` 或 `stop`：到点不发“继续”。agent 期间自己开了回合，等待结束。等额度只在内存里，重启就没了。

## 卡片（`Card`）

确认面板和插件用同一份：`id`、`kind`（`permission`、`question`、`plan`、`pick`）、`title`、`summary`（几行）、`options`（`id`、`text`、`primary`、`group`）。

- 提问的卡片在 `SessionState.asks[].card`，`id` 是 `ask:<会话>:<requestId>`。
  - 审批：`allow`、`deny`、`always`。计划：`approve`、`reject`。
  - 选择题：每个问题一组（`group` 是问题序号），选项 `id` 是 `<问题>.<选项>`；`multi` 列出可多选的组。
- `pick`：插件命令（`/use`、`/model`、`/mode`）给的选择，只有发起的插件能答，24 小时内有效。

`POST /api/answer`：`{card, option}` 或 `{card, choices: [...]}`（每组一个，多选组可多个），拒绝时可带 `message` → `CardReply{ok, reply?, card?}`。已答过或撤回：410。

## 推送

Web Push，总开关在配置 `push`，默认关。VAPID 密钥存在状态目录 `vapid.json`（0600）。

| 请求 | 正文 → 回答 | 说明 |
|---|---|---|
| `GET /api/push` | → `PushState` | 总开关、VAPID 公钥（订阅时的 `applicationServerKey`）、各工作区的设置（`workspace: 0` 是没归类的会话） |
| `PUT /api/push` | `{enabled}` | 总开关，写进配置文件 |
| `PUT /api/push/workspaces/{ws}` | `PushEvents` | 这个工作区推哪些：`needsYou`、`error` 默认开，`turnEnd`、`compact` 默认关 |
| `POST /api/push/subscriptions` | `PushSubscription` | 浏览器 `subscription.toJSON()`，记在这个交互终端名下；只收 https |
| `DELETE /api/push/subscriptions` | `{endpoint}` | |
| `POST /api/push/test` | → `{sent}` | 给本终端的订阅发一条测试，不看总开关 |

推送的内容（service worker 收到的 JSON）：`PushMessage{kind, session, title, body, tag}`，`kind` 是 `needs_you`、`error`、`turn_end`、`compact`、`test`；`tag` 是会话 ID。推送服务回 404/410 的订阅删掉。

## 插件

插件用自己的 TOKEN（交互终端由门认，见 `gate.md`）；下面的“插件”就是请求的交互终端。聊天键是插件自己定的字符串（1–200 字节可打印字符，放进路径要 URL 编码）。

| 请求 | 正文 → 回答 | 说明 |
|---|---|---|
| `POST /api/chat/{key}/input` | `ChatInput{text, sender?}` → `ChatInputReply` | 见下 |
| `GET /api/chat/{key}` | → `ChatPointer` | 指针：当前会话，和 `/new` 用的 agent、目录 |
| `GET /api/chat/{key}/commands` | → `ChatCommand[]` | Iridium 的命令（`iridium: true`），再加当前会话 agent 自己的，拿去设平台菜单 |
| `GET /api/chat/{key}/files/{ref}` | → 文件 | 回复里的文件，支持 Range |
| `POST /api/answer` | `CardAnswer` → `CardReply` | 见“卡片” |
| `GET /api/events` | SSE | 不带 `sub`：这个插件的事件 |

收消息：

- 不以 `/` 开头：交给指针指着的会话，回合在跑就并进去（插话，同 IM）。没有指针（或会话没了）：按指针的默认（没有就 `claude` 和配置 `chatDir`，再没有是家目录）开一个。
- 以 `/` 开头：是 Iridium 命令就在这里做，回答直接在 `reply`（Markdown 和去掉标记的文字）或 `card` 里；不是就原样交给 agent（透传）。
- 来源标记：`[插件名·sender]`，`sender` 去掉 `[ ] / : ·`，最多 24 字。

命令：`/new [agent] [目录]`（目录可以是绝对路径、`~/…` 或相对指针的目录）、`/use [序号|ID 开头]`（不带参数给卡片）、`/ls`、`/stop`、`/model [值]`、`/mode [模式]`（不带参数给卡片）、`/status`、`/usage`、`/context`。

事件流：`GET /api/events`，带 `Last-Event-ID`（或 `?after=<序号>`）从它之后接着收；都不带就从现在起。每条 `id: <序号>`，`event:` 是类型，`data:` 是 `ChatEvent`（都带 `chat`、`session`）：

| 类型 | 带什么 |
|---|---|
| `progress` | `text`：回合中 agent 说的一段话（最后一段是 `reply`，不在这里） |
| `reply` | `reply{markdown, text, files[]}`：回合的最终回复；`files` 是回复里 `![](路径)`、`[名](路径)`、行内代码里存在的绝对路径（名字、大小、类型、`ref`） |
| `card`、`card_done` | 提问出现（`card`）；已答或撤回（`cardId`），网页答的也发 |
| `error` | `text`：agent 进程出错退出 |
| `status` | `status`：状态灯变了 |

- 只发指针指着这个会话的聊天。事件存库 24 小时（`outbox` 表），过期删掉。
- `ref` 只对这个插件有效，签过名，重启后照样能用；`~/.ssh`、`~/.claude`、`~/.codex`、`~/.pi`、Iridium 的状态和配置目录里的文件一律不给。
- 例子：`examples/plugin/echo_im.py`（只用标准库）把 stdin/stdout 当成一个假 IM。
