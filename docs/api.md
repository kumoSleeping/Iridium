# Iridium 接口

一套接口，两个入口：

- HTTP：`127.0.0.1:7810`（配置 `listen`），给网页，前面是 Passkey 门（`docs/gate.md`）。没配 `public.origin` 时只认本机的主机名（`localhost`、回环地址和配置 `hosts`），非 GET 请求带了别的 `Origin` 就拒绝，请求都算内置终端“本机”（带 Bot TOKEN 的算那个 Bot）。配了以后每个请求都要面板登录或 TOKEN。
- unix socket：`$XDG_RUNTIME_DIR/iridium/ir.sock`（0600），给本机 `ir`，不经过门，算“本机”。`ir` 认出是 agent 在调（环境里有 `CLAUDECODE`、`CODEX_THREAD_ID`、`IRIDIUM_SESSION`，或者没有终端）时带头 `X-Iridium-Agent: 1`，这时不回任何秘密（见“确认”）。`ir` 还带 `X-Iridium-Session`（它所在的会话：`IRIDIUM_SESSION` 或 agent 自己的会话变量）；那个会话是别的终端经 Iridium 开的，这个请求就算那个终端的：有没有 Claude 照它，记它的用量和来源（agent 在 Bot 的会话里调 `ir`，等于 Bot 自己做）。

来源：每条交给 agent 的消息开头是 `[终端名] `（`/` 开头的命令除外），名字取发送时终端的名字。库里记下这条输入是哪个终端（ID）发的、用的什么名字；读记录时服务端按 Iridium 自己写进去的输入认（Claude：发消息时给的 uuid；没有 ID 的 agent：`store.SourceByText` 比对输入记录），去掉前缀，消息块带 `from`（名字）和 `fromTerminal`（终端 ID，取头像用）。用量行按终端 ID 记，改名不丢。经 unix socket 的 `ir send`、`ir new` 只要带着它所在的会话（`X-Iridium-Session` 头，取自 `IRIDIUM_SESSION`、`CLAUDE_CODE_SESSION_ID` 等环境变量，或正文的 `caller`），前缀就是 `[ir:<会话 ID>] `，`from` 也是它，页面画成那个会话的来信（名字：会话改过的名字，否则“Claude · 工作区名或文件夹名”）；有没有 `X-Iridium-Agent` 都一样。终端（`fromTerminal`，记用量的）照旧：开那个会话的终端，否则本机。网页和 TOKEN 来的请求不认 `caller`。

类型都在 `internal/api`。`go generate ./internal/api` 用 tygo 生成 `web/src/api/types.gen.ts`，前端只用它（生成的文件不进库）。

出错时状态码非 2xx，正文 `{"error": "…"}`：400 请求不对，403 不许（没有 Claude 的终端用 Claude 会话、没管理权的管理），404 没有这个会话或位置，409 agent 没在运行或状态不对，429 今天的用量到上限了，504 agent 没按时回。名字不行时 409 另带 `nameError`（`empty`、`too_long`、`invalid`、`reserved`、`taken`）。

## 请求

| 请求 | 正文 → 回答 | 说明 |
|---|---|---|
| `GET /api/sessions` | → `Session[]` | 会话列表：托管的加上磁盘上发现的（见“工作区和目录”）。置顶在前，再按最近 |
| `POST /api/sessions` | `NewSession` → `{id}` | 在 `cwd` 开一个托管会话，带 `text` 就作为第一条消息。`agent`：`claude`、`codex`、`pi`；`model` 可带强度（Codex `gpt-6-luna:low`，pi `provider/id:off`）；`mode` 取 `GET /api/agents` 列的模式。带 `workspace` 就放进那个工作区。本机会话带 `at`（`机器:路径`，远端组的“+”开的协作者）：按那个远端文件夹归组和显示，跟着它进外层组，不按自己的目录 |
| `GET /api/sessions/{id}` | → `SessionState` | 打开的会话的状态，和事件流里的同一份 |
| `GET /api/sessions/{id}/messages` | → `MessagePage` | 不带参数：最新一页。`before=<start>`：往回一页。`after=<end>`：之后新写的 |
| `GET /api/sessions/{id}/block?at=<at>` | → `{text}` | 截短的块（工具参数、结果、思考、系统行）的全文 |
| `GET /api/sessions/{id}/image?at=<at>&n=<n>` | → 图片 | 工具结果（`Result.images`，`at` 是结果的）给模型的第 `n` 张图，取自 agent 的记录，不读文件现在的样子：Claude、pi 和 MCP 工具的在结果里，Codex 的 view_image 在脚本的输出里（脚本只看了一张、只交出一张时才认得出是哪张），dsh 的 read_image 在它的附件库（经通道，远端经执行端）。只给 PNG、JPEG、GIF、WebP，按内容的 ETag。没有 404，不在了 410，所在机器没连上 409，脚本还没跑完 425（稍后再问） |
| `POST /api/sessions/{id}/send` | `{text, steer?}` → `{input, queued?, command?, session?, desktop?}` | 发消息。agent 没在跑就先拉起（有记录就 `--resume`）。回合进行中发的进待发队列（`queued`），`steer` 则并进当前回合。`/` 开头的命令从不插话，排到回合结束。`/clear`（`/reset`、`/new`，后面带字、带附件也算）由 Iridium 做：同处开新会话（模型、模式、强度照旧），旧的空闲时归档，回新会话的 `session`。`command`：agent 自己执行、记录里不会有这句（Codex、pi 的 `/compact`）。见“待发队列”。在别处运行的回 409；别的机器上 DSH Desktop 开着的 dsh 会话，消息交给 Desktop 里它的 agent（`desktop`，没有 `input`，见 `docs/exec.md`“远端 DSH”） |
| `POST /api/sessions/{id}/interrupt` | → `{ok}` | 停掉当前回合（Esc），agent 留着。队列暂停 |
| `POST /api/sessions/{id}/stop` | → `{ok}` | 停掉 agent 进程（关 stdin，写完记录自己退出）。会话还在，下一条消息再拉起。队列暂停 |
| `POST /api/sessions/{id}/answer` | `Answer` → `{ok}` | 回答提问：审批 `allow`（不许时可带 `message`；`always` 见“会话设置”）；选择题 `answers`（问题原文 → 选项 `label`）；计划确认 `allow` |
| `POST /api/sessions/{id}/release` | `Release{toolUseId, dismiss?}` → `{ok}` | Claude auto 模式拦下一步后，临时切换至 manual（见“临时切换至 manual”）；`dismiss` 只拿掉它的卡。Claude 会话要有 Claude，agent 调用一律 403 |
| `GET /api/events?sub=…` | SSE | 见下。不带 `sub`：Bot 的事件流，见“Bot 接入” |
| `GET /api/agents` | → `AgentInfo[]` | 能开的 agent：审批模式（`modes`、`defaultMode`、`modeSwitch`：now / next turn / restart）、有没有上下文分项、有没有额度窗口、能不能开 ultracode（`ultracode`）、agent 起来之前会话就有的附加功能（`features`，按 id：Claude 是 `claude.ultracode`）、程序装没装（`missing`：PATH 上找不到；这时 `POST /api/sessions` 立刻 400，不等 agent 自更新那 30 秒，网页把它画淡，点了只提示未安装） |
| `GET /api/health` | → `{ok}` | |
| `GET /api/settings` | → `Settings` | 设置的配置项：空闲回收、崩溃续跑、额度续跑（这三项设置页不放，只改配置文件或经接口）、隐藏的额度条、各 agent 新会话的审批模式、推送总开关。管理权 |
| `PUT /api/settings` | `SettingsPatch` → `Settings` | 只改给了的项，写进配置文件，马上生效。面板权 |

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
| `workspaces` | `Workspace[]` |
| `colors` | `FolderColors` |
| `session:<id>` | `SessionState`：状态、提问（带 `card`）、后台任务、模型（`model` 在用的，`pick` 设的）、审批模式、思考强度（`effort` 设的，`effortNow` agent 说在用的）、可选的模型/模式/强度、`ultracode`（只 Claude）、`features`（附加功能，见“会话设置”）、待发队列、上下文总数、等额度到几点、`tail`（块带 `files`）、`end`、`proc`、出错原因、`next`（Claude 自己清空了：对话接着在这个会话里，页面跟过去）、`commands`（给输入框的命令面板：人人都有的 `clear`、`compact`、`context`、`usage`、`model`、`effort`、`mode`，不带说明，页面按语言写；再接 agent 握手报的） |
| `context:<id>` | `Context`：上下文用了多少、分项。回合结束、压缩后，回合中调用工具时最多 30 秒一次更新；存在托管目录，没进程时也有 |
| `quota` | `Quota`：按 agent + 账号（Claude：邮箱和套餐）+ 机器（`machine`，执行端的会话是那台机器的登录）的额度窗口、读到的时间、`live` |
| `confirms` | `Confirms`：等面板确认的事（只有面板能订） |
| `update` | `UpdateState`：有没有新版本、更新到哪一步 |
| `notes` | `Notes`：全部便签和便签夹（只给面板） |

不认识的键、不给这个终端的键（`confirms` 只给面板）不让整条流失败：开流时先为每个发一条 `state`，`{key, value: null, ignored: "unknown" | "forbidden"}`，其余照常。

所有终端看到同样的 `list` 和会话。`Session.bots` 是聊天指着它的 Bot。终端被撤销、面板退出、`public.origin` 改了，流就断开。

事件：

- `state`：`{key, value}`，一样东西的整份。不存在时 `value` 是 `null`。
- `draft`：`{session, proc, n, text, done?}`，agent 正在打的字，接在第 `n` 段草稿后面。`done: true`：这段写完了，不会再有字（开始调工具、写下一段），页面去掉光标；它的话随后在记录里来。
- 每 15 秒一行注释（`: ping`）保活。

规则：

- 开流（含断线重连）先发每样的整份；之后变了再发整份，不发补丁。同一样 250ms 内最多一次，发的是最新的。
- 读得慢时中间的状态直接丢掉，只送最新的。
- 草稿每 100ms 攒一包，只拼接不丢。有状态要发时，攒着的草稿先发、排在它前面：带了块的状态不会抢在这段话的草稿之前。

页面怎么用：

- 打开会话：取最新一页，记下 `end`。`session:<id>` 的 `end` 比自己的大：
  - `tail.blocks` 不是 `null`，且自己的 `end` 在 `[tail.start, tail.end]` 里：直接用。位置在自己 `end` 之后的追加；`at` 已有的换掉（工具的结果来了）。`end` 记成 `tail.end`。
  - 否则（块大、块多、中间丢过状态）`after=<自己的 end>` 取新块。
  - `tail` 至少往回盖 1 秒；只装没截短的块，JSON 不超过 16KB。
- 草稿：同一 `proc`、同一 `n` 的 `text` 接起来；`n` 变了或来了 `done`，这段封口（记录里马上会有这段话）；`proc` 变了，丢掉所有草稿。只有空白的草稿封口就丢（记录不收空白）。
  - 记录里来了 agent 的话，按字找它的草稿（开头 32 个字一样），换掉它和它前面的；找不到（字先到、草稿后到），记下，等那段草稿封口时字一样就丢掉。
  - 光标只画在没封口的最后一段，会话不在忙时不画。重连后清掉草稿，照 `end` 取新块。

## Iridium 重启后

托管进程和 agent 照跑；新的 Iridium 接上每个还在跑的托管进程：

- 托管目录 `seen.json` 记着上一个 Iridium 读 `out.log` 读到哪（每秒最多写一次，退出时写一次）。那之前的输出重放，只为恢复驱动的状态；之后的（停机期间回合结束、用量、提问）照新来的处理。没有这个文件的（旧版）当全读过。
- 再发一次握手。Claude 回 `initialize` 时带 `pending_permission_requests`（还在等答复的提问，原样的 `control_request`），挂着的提问以它为准：没答的回来，带卡片，照常能答；答过的不再出现。
- 上下文（托管目录 `context.json`）和额度（状态目录 `quota.json`）是上次读到的值和读的时间（`at`）。上下文随回合读（回合结束、长回合里每隔一阵）；额度不随回合，每分钟单独读一次：每个 agent、账号和机器找一个在跑的进程问（Claude `get_usage`，Codex `account/rateLimits/read`，都不花额度），没有在跑的进程就不读（2026-11-06 定）；额度的 `live` 要等这个 agent、账号和机器又有进程在跑。
- 托管进程在停机期间已经退出的：不补读它的输出，按它的退出处理。

## 状态灯（`Status`）

`idle` 无灯，`starting` 启动中，`running` 运行中，`compacting` 压缩中，`waiting` 需要你（有提问），`error` 上次进程出错退出（原因在 `SessionState.error`），`elsewhere` 在别处运行（只读）。`live` 说 agent 进程在不在跑。

## 文件夹组和目录（自动发现）

会话列表以磁盘为准（`internal/discover`），启动时建索引放内存，inotify 加定时扫（目录每分钟、`/proc` 每 3 秒）跟上：

- 来源：Claude `~/.claude/projects/<目录>/<id>.jsonl`；Codex `$CODEX_HOME/sessions/YYYY/MM/DD/rollout-*.jsonl`（标题用 `session_index.jsonl` 的线程名）；pi `~/.pi/agent/sessions/*/*.jsonl`（标题用最后一个 `session_info` 名字）；dsh `~/.dsh/sessions/--<键>--/<id>/session.v<N>.jsonl.zstd`（现在只看路径和时间，目录由键反推）。标题、最近一句话只读文件开头 8MB 和末尾 4MB。
- 子代理也列（用户定的，2026-11-06）：`subagent: true`，`parent` 是父会话（托管的 Codex 换成 Iridium 的 ID），`launcher` 同 `parent`，所以归父会话的组。只读：发消息回 409，只有父会话能和它说话。
  - Claude：Agent 工具的子任务 `<目录>/<id>/subagents/agent-<agentId>.jsonl`（旧版是 `<目录>/agent-*.jsonl`），Workflow 跑出来的 agent 在 `<目录>/<id>/subagents/workflows/<runId>/agent-<agentId>.jsonl`（旁边的 `journal.jsonl` 不算会话），会话 ID 就是文件名 `agent-<agentId>`；首行 `isSidechain` 的 `sessionId` 是父会话；标题用旁边 `.meta.json` 的 `description`，没有就用任务的开头。
  - Codex：`session_meta.source.subagent.thread_spawn` 的 `parent_thread_id` 是父线程，标题用 `agent_path`（没有就 `agent_nickname`）。
- 在跑的：`~/.claude/sessions/<pid>.json`（核对 `procStart`），`/proc` 里本人的 codex、pi、dsh 进程（打开的记录文件或锁、命令行；pi 用工作目录里进程起来后最新写的那份）。环境变量只读本人进程的，只取下面几个，不记日志。
- 拉起者：子进程环境里依次看 `IRIDIUM_SESSION`、`CLAUDE_CODE_SESSION_ID`、`CODEX_THREAD_ID`、`CODEX_SESSION_ID`、`DSH_SESSION_ID`、`PI_SESSION_ID`；都没有就往上找第一个只跑一个会话的 agent 进程。`IRIDIUM_SESSION` 等于自己的 ID 是 Iridium 托管的，不算在别处。
- 显示标记（用户定的，2026-11-05）：会话只有带“显示”标记才在列表里正常显示；标记在库里（`shown` 表），按 agent + agent 自己的会话 ID 记（托管的 Codex 在知道线程 ID 前用 Iridium 的 ID，两个哪个有标记都算）。从不写 agent 的文件，不删记录。
  - 设上：Iridium 开的、接管的托管会话；托管会话的 agent 开始回合或收到消息；发现看到“活动”：没见过的记录且最后修改晚于 Iridium 开始看的时间（第一次启动时记在库里，重启不变），或者记录比上次看时长了。
  - 没标记的就是归档：`Session.archived` 为 `true`，页面放在底部折叠的“归档”里，能搜。第一次启动时磁盘上已有、之后没动过的都在这里。
  - 归档（`PATCH archived: true`）：去掉标记，记下记录当时的长度；之后长过这个长度（有了新活动）就自动回来。托管的另外像空闲回收那样停掉 agent；它退出时写的不算活动（退出后重记长度）。恢复（`archived: false`）：设上标记。没有按时间自动归档。
  - 旧版的归档标记迁移成“没标记”。
- 归组（2026-11-08：组就是文件夹，`Workspace` 是组，不能手建）：会话第一次显示时（第一次活动）做一次，记在库里，不给归档里的做：拉起者的组（组的文件夹包含它的目录时；子代理总跟父会话）→ 包含它目录的最外层的组 → 用它的目录新建一个组。家目录和 `/` 的组（另一台机器不在线时，按路径认它的家目录）只收正好在那里的会话。之后每次归组都把落在别的组文件夹里的组并进最外层那个（子目录先建了组、父目录后建的，比如执行端的会话一次全看到）：自动归进去的会话移过去，空了的组删掉，有手动放进去的会话的组留着；隐藏的组不参与。在未归类（`workspace` 为 0）且不是你手动放过去的也再归一次；只有没目录的留在 0。`POST /api/sessions` 开的托管会话当场归组，不等记录出现。手动移过的（`PATCH workspace`）不再自动动。
- 状态：`hosted` 托管；`elsewhere` 在别处运行，只读（能看状态和消息页，发消息回 409）；其余是磁盘上空闲的。别处退出后，发消息就接成托管会话（`--resume`），前提是没有别的进程在用（Claude：`sessions/` 里没有活着的同 ID 进程；dsh：`session.lock` 能拿到 flock；Codex：写锁）。现在只能接 Claude 的。
- 列表变了（新会话、记录长了、进程起停、改了覆盖层）攒 200ms 发一次 `list` 整份，变了的会话另发 `session:<id>`。

| 请求 | 正文 → 回答 | 说明 |
|---|---|---|
| `GET /api/workspaces` | → `Workspace[]` | 文件夹组，和事件流 `workspaces` 同一份（没有会话的也在，页面不列） |
| `PATCH /api/workspaces/{id}` | `{name?, hidden?}` → `{ok}` | 改名；隐藏或取消隐藏（隐藏的组：会话不列、不进归档、搜不到，除非其中有会话在别处运行，如 DSH Desktop 里开着；那时整组列出，菜单可取消隐藏） |
| `POST /api/workspaces/{id}/split` | → `{ok}` | 按子文件夹拆开：组只留它文件夹里直接的会话，子文件夹里的按各自文件夹重新归组（子文件夹的子文件夹仍进它的组），以后也不再收子文件夹（`flat`）。只有面板和本机 |
| `PUT /api/groups/order` | `{keys}` → `{ok}` | 组的顺序（拖动或菜单上移下移）：`keys` 是会话列表里每个组那一行的键（`w<id>`；同一个组在别的机器上的那部分是 `w<id>@<机器>`，各排各的），依次排在前，没列的随后；事件流 `places` 的 `groups`。只有面板和本机能改 |
| `PATCH /api/folders/place` | `FolderPlace` → `{ok}` | 文件夹在组里的位置：`path` 是分组用的路径（本机绝对路径，或 `机器:路径`）；`order` 同会话的，0 去掉；`pinned` 置顶（排在组根下的会话上面）。不给的字段不变；事件流 `places` 的 `order`、`pinned`。只有面板和本机能改 |
| `PUT /api/colors` | `FolderColor` → `{ok}` | 给文件夹打颜色标签（`red`、`orange`、`yellow`、`green`、`blue`、`purple`），`color` 为空就去掉；事件流 `colors`（只给面板）是全部。会话的标签用 `PATCH /api/sessions/{id}` 的 `color` |
| `PATCH /api/sessions/{id}` | `SessionPatch` → `{ok}` | 只改覆盖层，不碰 agent 的文件：`name`（空串去掉；带 `byAgent` 是会话里的 agent 起的，`ir rename` 这样发：去引号和句尾标点，不盖你起的名，盖不了回 409）、`pinned`、`archived`（归档或恢复，见上面的显示标记）、`workspace`（移动，0 为未归类；移过就不再自动归类）、`order`（拖到的位置：列表按它排，代替最后活动时间，与 `updatedAt` 同为毫秒；0 去掉） |
| `GET /api/folders?path=` | → `Folders` | 选择器：只列文件夹，不列隐藏的，按名字排；`path` 为空或 `~` 是家目录。`parent` 为空表示到顶了 |
| `POST /api/folders` | `NewFolder` → `{path}` | 在 `path` 里新建文件夹 `name`（不能以 `.` 开头、不能带 `/`）。已有回 409 |
| `GET /api/machines/{id}/dsh/workspaces` | → `DshWorkspace[]` | 那台机器上 dsh 的工作区（DSH Desktop 侧边栏那份，按它的顺序，文件夹已不在的不列），读不懂时用它会话所在的文件夹代替，最近的在前；路径用 `/`。新会话选文件夹时列在最上面，见 `docs/exec.md`“远端 DSH” |

- 选择器默认只能在家目录里（配置 `folderRoots` 可改），先解析符号链接再比；`~/.ssh`、`~/.gnupg`、`~/.claude`、`~/.codex`、`~/.pi`、`~/.dsh` 和 Iridium 的状态、配置目录一律不给（403）。不存在 404。

## 便签

面板和本机的，同颜色标签。每次改动，事件流 `notes` 整份重发。

| 路由 | 请求 → 回复 | 说明 |
|---|---|---|
| `GET /api/notes` | → `Notes` | 全部 |
| `POST /api/notes` | `NewNote` → `Note` | `id` 页面自己给（UUID），同一个 `id` 再发不重复建；`pos` 不给就放在它那一夹最上面 |
| `PATCH /api/notes/{id}` | `NotePatch` → `Note` | 改正文、夹（0：不在夹里）、位置、颜色、完成；`session`：放进过的会话，记在 `sessions` 最后 |
| `DELETE /api/notes/{id}` | → `{ok}` | |
| `POST /api/note-folders` | `NoteFolderPatch` → `NoteFolder` | 只一层 |
| `PATCH /api/note-folders/{id}` | `NoteFolderPatch` → `{ok}` | 改名、位置 |
| `DELETE /api/note-folders/{id}` | → `{ok}` | 里面的便签移出夹 |

## 文件和上传

代码在 `internal/files`（路径检查在 `internal/pathsafe`），路由在 `internal/server/routes_files.go`。

| 请求 | 正文 → 回答 | 说明 |
|---|---|---|
| `GET /api/sessions/{id}/files/{ref}` | → 文件 | 支持 `Range`/`If-Range`；`ETag`（大小 + 修改时间）；`X-Sha256` 和 `Repr-Digest`（64MB 以下当场算，大的算过才有）。`?download` 强制下载 |
| `GET /api/sessions/{id}/files/{ref}/thumb?size=1024` | → 图片 | 缩略图，最长边默认 1024（32–4096）。png/jpeg/gif/webp；按路径 + 大小 + 修改时间缓存在状态目录 `thumbs/`；SVG 原样给 |
| `GET /api/sessions/{id}/files/{ref}/info` | → `FileRef` | 带 `sha256` |
| `POST /api/sessions/{id}/uploads` | `NewUpload` → `Upload` | 建上传任务。超过 `uploads.maxFile`（默认 2GB）是 413 |
| `PUT /api/uploads/{upload}?offset=<n>` | 块 → `Upload` | `offset` 必须等于 `received`，否则 409（正文也带 `Upload`）。一块最多 64MB；块大小客户端定（8–50MB，按测到的速度） |
| `GET /api/uploads/{upload}` | → `Upload` | 已收多少（续传从 `received` 接着发）。断在半块时，到达的部分也算 |
| `POST /api/uploads/{upload}/complete` | `{sha256}` → `Upload` | 核对 sha256，对上了原子改名，`file` 里是引用。对不上 422，已收的丢掉，从 0 重传 |
| `DELETE /api/uploads/{upload}` | → `{ok}` | 取消 |

只走 unix socket（本机 `ir`）：

| 请求 | 正文 → 回答 | 说明 |
|---|---|---|
| `POST /api/sessions/{id}/attach` | `{path}` → `FileRef` | 把本机文件当这个会话的附件（`ir send -a`），不复制 |
| `GET /api/files?path=<目录>[&machine=<机器>]` | → `DirListing` | 列一层（`ir files`）；带 `machine` 是那台机器上的目录（它自己的写法，空是家目录） |
| `POST /api/files/copy` | `{from, to, fromMachine?, toMachine?}` → NDJSON | 拷文件（`ir cp`）：每 250ms 一行 `CopyProgress`，最后一行带 `path`、`sha256` 或 `error`。由 Iridium 拷，`ir` 断了也接着拷；同样的请求再来就接上。一边在别的机器（`fromMachine`、`toMachine`）时：不接续，写到那边的先限 4MB（413），目标已有文件 409 |
| `POST /api/cli` | `CLIRun{args, cwd}` → NDJSON `CLIOutput` | 执行端上的会话里 agent 的 `ir`（只认会话 TOKEN，见 `docs/exec.md`“远端的 ir”）：这边照常跑这条 `ir`，算那台机器的 Bot、代表这个会话；输出按行流回（`out`、`err`），最后一行 `exit` |

规则：

- **引用**：`ref` 只对这个会话有效，同一路径每次一样（状态目录 `files.key` 做的 HMAC），看不出路径。只有会话记录里出现过的路径、它的上传和附件才有引用；服务器取不到就从上次读到的地方往后读记录。
- **认文件**（`Block.files`）：只认单独一行的 `` [文件] `路径` ``（绝对路径或 `~/`），你的话里另认 `` [附件] `路径` ``；`Write` 调用的 `file_path` 只给引用、不显示。行内的路径、链接、`![]()`、围栏代码块里的都不算。只认存在的普通文件。
- **类型**：先看扩展名，没有再嗅前 512 字节。图片、音视频、PDF、纯文本内联显示，其余下载。所有文件都带 `Content-Security-Policy: …; sandbox` 和 `nosniff`，SVG、HTML 里的脚本不会跑。
- **上传位置**：会话目录的 `.iridium/uploads/`（建目录时写一个只有 `*` 的 `.gitignore`）。会话目录是 `$HOME`、写不进或不许写时，放状态目录 `uploads/files/<会话>/`。同名不覆盖，改叫 `名 (2).扩展名`。先写 `.<id>.part`，核对后用硬链接放到位（原子、不覆盖）。上传记录在状态目录 `uploads/records/`，重启后能续传。
- **清理**：`uploads.keep`（默认 `720h`，`"0"` 不清）以前的上传连同文件（没改过的才删）和缩略图，每小时一次。
- **附件**：`Send.attachments` 是引用。每个附件在消息末尾占一行 `` [附件] `路径` ``；图片另作为图片块给 Claude（超过 5MB 或边长 8000 时给缩到 1568 的副本，一条消息的图片共 20MB 以内），最多 20 个附件。
- **路径检查**（`pathsafe.Guard`）：先解析符号链接（不存在的路径解析到最深的已有上层）再比允许的目录；写着的路径和解析后的都不能落在 Iridium 的状态和配置目录、`~/.ssh`、`~/.claude`（及 `CLAUDE_CONFIG_DIR`）、`~/.codex`（及 `CODEX_HOME`）、`~/.pi`、`~/.dsh`、`/proc` 里，状态目录的 `uploads/files` 例外。被拒是 403。
## `ir`（agent 用的部分）

- 代码在 `internal/cli`，走上面这套接口；socket 用 `IRIDIUM_SOCKET`，没有就读配置（`IRIDIUM_CONFIG` 或默认位置）。
- `<id>` 可只写开头（唯一即可）；`ir new`、`ir ls` 打印至少 8 位的短 ID。`ir ls` 只列有显示标记的，子代理只列还在工作的（没做完、父会话进程还在、5 分钟内写过），排在父会话下面；`ir ls --all` 连归档和做完的子代理一起列。`ir find <词>...` 在 ID、名字、标题、目录、最后一句里找（每个词都要有，不分大小写），归档的也找、标 `[归档]`。`ir archive`、`ir unarchive` 同会话列表菜单（`PATCH /api/sessions/{id}`）。`ir rename [<id>] <名>` 给会话起名，不写 id 是自己所在的；在会话里跑算 agent 起的（`byAgent`），见 `plan.md`“会话标题”。`ir read` 默认每条消息一行（截 80 字）加它的最终回复（截 300 字），最近 10 条，`-n` 改条数，`--full` 打整页原文。`机器:路径`、`机器/<id>`（机器是执行端，`plan.md`“别的机器”）除 `ir sh` 外现在报 `peer not connected`。
- 输出给 agent 看：只打最后一段回复（`new --wait` 先一行 ID）；`cp` 一行 `路径 大小 sha256`；需要你时每个提问一行 `需要你：…`。进度只在 stderr 是终端时画。
- `send`、`new` 不带 `--wait` 时等到 agent 接了这条消息（最多 15 秒）再返回，所以紧接着的 `ir wait` 等的是这一回合。
- 回合在跑时 `ir send` 的话默认插进这一回合（和网页回车一样，2026-11-06 定）；`--wait` 等到这一回合结束，打印最后一段回复（可能还在说别的事）。加 `--queue` 进待发队列（只对人在终端里跑的 ir 有效；agent 跑的 `--queue` 不管用，总是插进，免得要人去点；2026-11-06）：不带 `--wait` 马上返回，带 `--wait` 等它发出去、再等它那一回合（队列暂停了就退出码 2）。`--steer` 还认，和默认一样。agent 在等额度时插话也进队列。
- 退出码：0 结束，2 需要你，1 出错（agent 出错退出、回合中被停、`ir` 自己出错）。

- `NewSession.caller`、`Send.caller`：`ir` 从环境变量（`IRIDIUM_SESSION`、`CLAUDE_CODE_SESSION_ID`、`CODEX_THREAD_ID`…）读到的调用者会话，没给 `workspace` 时新会话进它的工作区（它在未归类时交给自动发现）。

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

## 命令

在会话所在的机器和目录直接执行一行命令，不经 agent、不花 token（用户，2026-11-08）。网页上在输入框里以 `$` 开头（`$ git status`）；`!` 已用于插话。只有面板和本机能用，Bot 不能（执行端也不能，它只是命令落地的机器）；Bot 开的会话里 agent 调 `ir sh` 算那个 Bot，同样 403。

| 请求 | 正文 → 回答 | 说明 |
|---|---|---|
| `POST /api/sessions/{id}/shell` | `{line, cwd?}` → `{run?, cwd}` | `cwd` 省略为会话目录。`cd <目录>` 不执行，确认那边有这个目录后回 `{cwd}`（`~` 是家目录，相对路径接在 `cwd` 后）；网页记住它，下一条带上 |
| `GET /api/shell/{run}?from=N` | → `{data, next, done, code?, signal?, err?}` | 从字节偏移 `N` 读输出（一次至多 64KB），`next` 是下一次的 `from`。`done` 时输出已读完，`err` 是起不来时的原因 |
| `DELETE /api/shell/{run}` | → `{ok}` | 停止（连同它起的进程） |

- 每条命令一个进程：Linux、macOS 是 `/bin/sh -c`（stderr 并进 stdout），Windows 是 `cmd.exe /c`（stderr 在结束时给）。stdin 不接，读 stdin 的命令要手动停止。全屏程序（`vim`、`top`）不支持。
- 进程照托管 agent 的方式起（本机的 `platform.Launcher`，别的机器经执行端），目录是托管目录下的 `sh-<id>`；结束 10 分钟后删。
- 机器不在线：409；机器的主人关了命令（`Machine.noShell`）：403；目录不存在：404。

agent 用 `ir sh <机器> [--cwd 目录] [--timeout 2m] -- 命令`（`POST /api/machines/{id}/shell`，`{id}` 是机器 ID 或名字，`cwd` 省略为那边的家目录）：等它结束，原样打出输出，退出码就是它的；超时停掉它，退出码 124。只在执行端声明了 `agentShell` 的机器上能用（主人的选择，默认不能；否则 403）。`ir machines` 列出机器：名字、系统、在线与否、能否 `ir sh`、家目录。系统提示里只有 `ir --help` 的一行。

## 会话设置

按会话记在库里：马上设给在跑的 agent，之后每次拉起都带上（Claude：`--model`、`--permission-mode`、`--effort`，设过 ultracode 带 `--settings '{"ultracode":…}'`，关也带，压过你自己设置里的）。

| 请求 | 正文 | 说明 |
|---|---|---|
| `PUT /api/sessions/{id}/model` | `{model}` | `""` 是 agent 的默认。Claude：`set_model`。dsh（用户，2026-11-08：模型和供应商不写死）：会话状态的 `models` 是会话所在机器上 dsh 的 sdk profile 注册的全部供应商的模型，值是 `供应商/模型`，思考强度按模型，默认是 dsh 的默认模型。Iridium 在那台机器上起一次探测（`dsh --profile sdk --patch` 插一个 cordis 插件，读 `llm` 的 `listProviders`、`listModels`、`resolveModelInfo` 和 `agentDefaultModel`，打一行就退出），每台机器缓存 3 分钟，失败 1 分钟内不重试；启动时按值里的供应商初始化。以前存的不带供应商的值（`deepseek-flash`）按目录里有这个模型的供应商算。和 DSH Desktop 同步（用户，2026-11-08）：Desktop 设置里的供应商和默认模型存在它的 web profile，sdk 进程本来没有；远端有 dsh 插件时，会话和探测都多带一个 `--patch`，是插件按 Desktop 此刻生效的设置写的（`dsh.llm`），所以列出的和能跑的都同 Desktop。本机没有 Desktop 进程，用 sdk profile 自己的。agent 还没起来的会话（新建未发消息的、Desktop 开的只是列出来的）也有模型可选（用户，2026-11-09）：页面打开它时后台对那台机器探测一次，回来后状态里的 `models` 用那台机器的目录，`model` 是默认模型；在 Desktop 开的会话上选模型、强度或预设会先接管它（同发消息，但不启动 agent）；插件有 `held.features` 时，Desktop 拿着、这边没在跑的会话改模型、强度不接管，直接在 Desktop 里改（`held.set`，`docs/exec.md`“附加功能”），预设照旧接管（2026-11-09）|
| `PUT /api/sessions/{id}/mode` | `{mode}` | 审批模式，只收 `SessionState.modes` 里的。Claude：`set_permission_mode` |
| `PUT /api/sessions/{id}/effort` | `{effort}` | 思考强度，只收 `SessionState.efforts` 里的。Claude：先 `get_settings` 问 ultracode 开没开，再 `apply_flag_settings` 照原样带上 |
| `PUT /api/sessions/{id}/ultracode` | `{ultracode}` | 只 Claude（`GET /api/agents` 的 `ultracode`），别的 400。在跑时先 `get_settings`，要开而 `ultracodeAvailable` 不为真（CLI 不认 `get_settings` 也算）就 400；只发 `apply_flag_settings {ultracode}`，强度不变。没在跑时不查，只记下，下次拉起带上 |
| `PUT /api/sessions/{id}/features/{feature}` | `{on, option?}` | 切一项附加功能（`SessionState.features` 里的 `id`），`option` 是它的选项之一。`claude.ultracode` 同上一行（`option` 不看）；别的是执行端那台机器上 DSH Desktop 里插件给的（`held.set`），只对 Desktop 拿着的会话，没拿着的 400（带插件给的原因）；不在会话最近拿到的附加功能里、选项不对的 400，不问执行端。`ir` 和 Bot 没有这个 |
| `GET /api/sessions/{id}/presets` | | `{presets: [{id, name?, description?}], default}`：会话所在那台 dsh 注册的 Agent 预设（用户，2026-11-08：不写死）。远端有 dsh 插件时问 DSH Desktop 自己的注册表（`dsh.presets`，含插件包和用户自建、改过的，按它的顺序，默认是它设置里选的，坏掉的不列）；本机和没有插件的执行端读 dsh 包里登记的补丁（`dsh-web-app` 的 `package.json` 里 `dsh.bundle.patch`）。按机器缓存 30 秒。内置的几个不带 `name`，网页用自己的文案；带 `name` 的照它写的显示。`GET /api/agents` 的 `presets` 只说这个 agent 有没有预设 |
| `PUT /api/sessions/{id}/preset` | `{preset}` | Agent 预设（只 dsh：上一行列出的，没有的 400），`""` 是 `default`。只在启动时装，也只在会话第一条消息前能改（同 dsh 自己的界面）：开始后改是 400，因为 dsh 会把新工具当成追加的延迟加载工具，PTC 那样全是延迟的，API 会拒绝。在跑时改就重启（空闲马上，否则等回合结束），重启时状态仍显示空闲。网页放在额度条的位置（dsh 没额度，用户定的，2026-11-06）。新会话用 `NewSession.preset`，`/clear` 照原样带上 |
| `GET /api/sessions/{id}/rules` | → `AllowRule[]` | “本会话一直允许”记下的规则 |
| `DELETE /api/sessions/{id}/rules` | → `{ok}` | 清掉 |

- 新会话的审批模式：`NewSession.mode`，没给就用配置 `modes.<agent>`，再没有就是 agent 的默认（`GET /api/agents` 的 `defaultMode`）。默认一律自动批准（用户定的，2026-11-05）：Claude `auto`；Codex `auto`（Codex 的 `approvalPolicy: never`，`never` 也认），更严的 `untrusted`、`on-request`、`granular` 照样能选；pi `auto`，不装审批扩展，选了 `ask` 才装。配置默认就写着这三项。
- 模式和强度按 agent 登记表（`GET /api/agents`）：Codex 从下一回合起生效（`turn/start` 带上）；pi 的模式只在启动时装（`modeSwitch: restart`），在跑时改了就重启它（空闲时马上，否则等这个回合结束；接着 `--session-id` 续上，队列照常发）。强度：Codex `effort`、pi `set_thinking_level`，启动时 pi 用 `--thinking`；模型自己带 `:级别` 的以模型的为准。
- 模型和强度怎么显示：
  - `models[]` 带 `resolved`（这个值实际跑的完整 id，Claude 的 `resolvedModel`）和 `efforts`（这个模型收的强度）；`noEffort` 是不收强度（Haiku 4.5）。两样都没有就是 agent 没说，用 `SessionState.efforts`。
  - `model` 是在用的模型，常是完整 id；`pick` 是设的值，空或 `default` 是 agent 的默认。默认时模型键显示具体的模型（`resolved` 对上的别名，再不行取默认项描述 ` · ` 前那段），不只写“默认”。
  - `effortNow`：Claude 握手后、换模型或强度后读 `get_settings` 的 `applied`（也拿到 `applied.model`，第一回合前就有）；Codex 取 `thread/start` 的 `reasoningEffort`；pi 取 `get_state` 的 `thinkingLevel`。没设过强度时显示它，但不记进库。
  - `ultracode`：`{on, active, available}`，`on` 是开没开（`ultracodeRequested`），`active` 是开了且生效，`available` 是这个模型能不能开（读到之前、进程没在跑时算能；CLI 不认 `get_settings` 算不能）。进程一结束，读到的就作废，下个进程再问。
  - `features`：附加功能（2026-11-09），每项 `{id, name, on, active?, available, reason?, hint?, options?, option?, place, tone?}`：Claude 的 UltraCode（`claude.ultracode`，同 `ultracode`，那个字段先留着），以及远端 dsh 插件给的（如 `omd.ultra`：DSH Desktop 拿着的会话，和这边起的、用 OMD 预设的会话）。`place` 现在只有 `effort`：开关在强度滑条右边，开着时选项在滑条下面一行；`tone: "warm"` 是开着时的颜色。插件给的从执行端问来，缓存 10 秒，状态不等它：过期了后台去问，回来再推一次状态。Desktop 拿着的会话，`effortNow` 是它在 Desktop 里此刻跑的档位（附加功能管着强度时是它的），`model`、`effort` 没设过时也取 Desktop 的。限制见 `docs/exec.md`“附加功能”。
  - 面板里选了模型就收起；强度滑条和附加功能的开关（UltraCode 等）在面板底下，调了不收。
- 本会话一直允许：`answer` 带 `always: true`（或卡片选 `always`）。记下工具和 `Ask.pattern`（Claude 自己建议的规则，例如 `ls *`；没有时 shell 命令取第一个词加 ` *`，别的工具不带模式＝这个工具全部）。之后这个会话里匹配的审批自动允许，不通知、不出卡片。
- 临时切换至 manual：Claude 的 auto 模式分类器拦下的一步不经 Iridium，直接回给模型（结果开头是 `Permission for this action was denied by the Claude Code auto mode classifier`）。`release` 把运行中的进程切到 `manual`（不写进会话设置，进程重启就回到原模式），不给 Claude 发消息（原来以用户名义插一条“请原样再做一次”，它可能已换了做法，2026-11-08）。之后的调用照常问；第一个问的调用（或同一调用：工具相同，参数除 `description` 外相同）做完就切回原模式（2026-11-08），一个都没问就在当时进行中的回合（空闲时为下一回合）结束时切回，期间手动换了模式就不切回（2026-11-07）。切换记在 hold 目录的 `release.json`：守护进程重启接管仍在 manual 的同一进程时接着算，新进程就删掉。拦下的每一步都在会话状态的 `denials`（`Denial{toolUseId, tool, input, reason, at}`，只记在内存里）直到放行、`dismiss` 或给这个会话发下一条消息。只有回合停在被拦的一步上（它之后本回合没有做完别的调用）才算 `waiting`（`ir wait` 不算需要你）；之后做完了别的调用就当它绕过去了，状态照常（2026-11-08）。回合结束的推送和 Bot 回复带上“auto 拦了 N 步”。
- 匹配：`*` 是任意一段字；`前缀:*` 同 `前缀*`。shell 命令里有 `;`、`&&`、`||`、`|`、`` ` ``、`$(`、换行的，一律不按模式放行。

## 设置

`GET /api/settings` 给 `Settings`：`idleReap`（`"1h"`、`"15m"`，`"0"` 是不回收）、`crashResume`、`quotaResume`、`quotaHidden`（隐藏的额度条，`<agent>:<账号>:<窗口>:<模型>`）、`modes`（agent → 新会话的审批模式，没列的用 agent 自己的）、`push`（推送总开关，和 `PUT /api/push` 是同一个）、`enterInTurn`（回合在跑时网页回车做什么：`steer` 插话，默认；`queue` 排队）、`lang`（用户的语言 `zh`/`en`，网页设置里选的；`""` 是还没告诉过，网页发现是空的就报自己的；托管 agent 下次启动时写进系统提示）。

- `PUT /api/settings` 带 `SettingsPatch`，只改给了的项；`modes` 里给 `""` 是去掉那个 agent 的。时长不对、agent 没有这个模式回 400。
- 先读配置文件，只改这几项再写回（文件里别的照旧），同时改内存：空闲回收、续跑开关下次判断时就用新的，审批模式从下一次拉起起。
- 会话自己的设置（库里的）照样盖过这里的。

## 续跑

配置 `crashResume`、`quotaResume`，默认都开。

- 崩溃续跑（Rhodium §4.8、product §4.5）：Iridium 没要求的退出（非 0、信号、托管进程没了）算崩溃。只有 agent 已经有了这个回合（收下过它的输入，或自己开始的）才续跑，否则等你的下一条消息。
  - 用 `--resume` 拉起，Claude 另设 `CLAUDE_CODE_RESUME_INTERRUPTED_TURN=1`，它自己重跑被打断的回合。握手应答时还没有回合开始，就发恢复提示（Iridium 行）：`The last turn was interrupted. Check where things stand, then carry on.`
  - 同一会话 10 分钟内第 3 次崩溃就不再续跑（熔断），状态停在 `error`。
  - 挂着的提问作废；队列暂停。
- 额度续跑（product §4.6）：agent 报额度用完（Claude `rate_limit_event` rejected），`SessionState.quotaUntil` 记恢复时间（错开 0–60 秒）。期间发的话排队。到点：有排队的就发排队的，没有就发 Iridium 行 `Continue.`。期间 `interrupt` 或 `stop`：到点不发“继续”。agent 期间自己开了回合，等待结束。等额度只在内存里，重启就没了。

## 卡片（`Card`）

确认面板和 Bot 用同一份：`id`、`kind`（`permission`、`question`、`plan`、`pick`）、`title`、`summary`（几行）、`options`（`id`、`text`、`primary`、`group`）。

- 提问的卡片在 `SessionState.asks[].card`，`id` 是 `ask:<会话>:<requestId>`。
  - 审批：`allow`、`deny`、`always`。计划：`approve`、`reject`。
  - 选择题：每个问题一组（`group` 是问题序号），选项 `id` 是 `<问题>.<选项>`；`multi` 列出可多选的组。
- `pick`：Bot 命令（`/use`、`/model`）给的选择，只有发起的 Bot 能答，24 小时内有效。

`POST /api/answer`：`{card, option}` 或 `{card, choices: [...]}`（每组一个，多选组可多个）；选择题自己写的答案放 `words`（按组，空串是没写），顶替那一组的选择；拒绝时可带 `message` → `CardReply{ok, reply?, card?}`。已答过或撤回：410。

## 推送

Web Push，总开关在配置 `push`，默认关。VAPID 密钥存在状态目录 `vapid.json`（0600）。

| 请求 | 正文 → 回答 | 说明 |
|---|---|---|
| `GET /api/push` | → `PushState` | 总开关、VAPID 公钥（订阅时的 `applicationServerKey`）、默认事件 `defaults`、规则 `rules` |
| `PUT /api/push` | `{enabled}` | 总开关，写进配置文件 |
| `PUT /api/push/defaults` | `PushEvents` → `PushState` | 没有规则管的地方推哪些：`needsYou`、`error`、`turnEnd`（任务完成）默认都开。没有单独的压缩事件：你发的 `/compact`（dsh 的 `/压缩`）压缩成功算一次 `turnEnd`，正文“压缩完成”，它那一回合随后的空结束不再推；agent 自己在回合中途的自动压缩不推（2026-11-08） |
| `PUT /api/push/rules` | `PushRule` → `PushState` | 一个工作区、文件夹或会话的规则；`mode: inherit`（或空）删掉它 |
| `POST /api/push/subscriptions` | `PushSubscription` | 浏览器 `subscription.toJSON()`，记在这个交互终端名下；只收 https |
| `DELETE /api/push/subscriptions` | `{endpoint}` | |
| `POST /api/push/test` | → `{sent}` | 给本终端的订阅发一条测试，不看总开关 |

`PushRule{scope, target, mode, events?}`：`scope` 是 `workspace`（`target` 是工作区 ID，`"0"` 是没归类的）、`folder`（会话目录的绝对路径，只管这一个目录，不含子目录）、`session`（会话 ID）；`mode` 是 `on` 或 `off`；`events` 只跟 `on`，是这一层自己的事件，不带就用上面的。一个会话的事件：会话、文件夹、工作区里最近的那条规则说开还是关（都没有就开）；开的话，事件取最近一条带 `events` 的 `on` 规则的，没有就用 `defaults`。旧的按工作区设置迁移成工作区规则（全关的是 `off`，和默认一样的不留）。

推送的内容（service worker 收到的 JSON）：`PushMessage{kind, session, title, body, tag}`，`kind` 是 `needs_you`、`error`、`turn_end`、`test`；`tag` 是会话 ID。推送服务回 404/410 的订阅删掉。

## Bot 接入

Bot 用自己的 TOKEN（交互终端由门认，见 `gate.md`）；下面的“Bot”就是请求的交互终端。聊天键是 Bot 自己定的字符串（1–200 字节可打印字符，放进路径要 URL 编码）。

| 请求 | 正文 → 回答 | 说明 |
|---|---|---|
| `POST /api/chat/{key}/input` | `ChatInput{text, sender?}` → `ChatInputReply` | 见下 |
| `GET /api/chat/{key}` | → `ChatPointer` | 指针：当前会话，和 `/new` 用的 agent、目录 |
| `GET /api/chat/{key}/commands` | → `ChatCommand[]` | Iridium 的命令（`iridium: true`），再加当前会话 agent 自己的，拿去设平台菜单 |
| `GET /api/chat/{key}/files/{ref}` | → 文件 | 回复里的文件，支持 Range |
| `POST /api/chat/{key}/uploads` | `NewUpload` → `Upload` | 往指针指着的会话传附件（没选会话回 409），之后同网页分块 `PUT`、`complete`，拿 `file.ref` 放进 `ChatInput.attachments` |
| `POST /api/answer` | `CardAnswer` → `CardReply` | 见“卡片” |
| `GET /api/events` | SSE | 不带 `sub`：这个 Bot 的事件 |

收消息：

- 不以 `/` 开头：交给指针指着的会话，回合在跑就并进去（插话，同 IM）。没有指针（或会话没了）：不开会话、消息不发，`card` 是标题“未选择会话”的选会话卡片（同 `/use`），一个会话都没有时 `reply` 提示 `/new`。`/new` 按指针的默认（没有就 `claude` 和配置 `chatDir`，再没有是家目录）开。
- 以 `/` 开头：是 Iridium 命令就在这里做，回答直接在 `reply`（Markdown 和去掉标记的文字）或 `card` 里；不是就原样交给 agent（透传）。
- 来源标记：`[Bot 名·sender]`，`sender` 去掉 `[ ] / : ·`，最多 24 字。

命令：`/new [agent] [目录]`（目录可以是绝对路径、`~/…` 或相对指针的目录）、`/clear`（同 `send` 的 `/clear`，指针换到新会话）、`/use [ID 开头]`（不带参数：在会话里时给当前会话所在文件夹的卡片，否则给选组的卡片，见 `docs/qq.md`；文件夹卡片末尾有“回到上一级”（子文件夹才有）、“回到根目录”；在会话里时卡片最后一项是“离开当前会话”）、`/leave`（指针不指任何会话，列表上不再标这个 Bot）、`/ls`、`/history [轮数]`（最近几轮，默认 5，最多 10，一条消息）、`/stop`、`/model [值]`（不带参数给卡片）、`/status`、`/usage`、`/context`。

事件流：`GET /api/events`，带 `Last-Event-ID`（或 `?after=<序号>`）从它之后接着收；都不带就从现在起。每条 `id: <序号>`，`event:` 是类型，`data:` 是 `ChatEvent`（都带 `chat`、`session`）：

| 类型 | 带什么 |
|---|---|
| `progress` | `text`：回合中 agent 说的一段话（最后一段是 `reply`，不在这里） |
| `reply` | `reply{markdown, text, files[]}`：回合的最终回复；`files` 是回复里 `` [文件] `绝对路径` `` 行给的、存在的文件（名字、大小、类型、`ref`），这些行和 `[标题] …` 行都不在 `markdown`、`text` 里 |
| `card`、`card_done` | 提问出现（`card`）；已答或撤回（`cardId`），网页答的也发 |
| `error` | `text`：agent 进程出错退出 |
| `status` | `status`：状态灯变了 |

- 只发指针指着这个会话的聊天。例外：没有聊天指着的会话，它的提问（`card`、`card_done`）发给一个聊天（选法见下），卡片标题换成会话（`[CC] 名字`），原标题放到 `summary` 第一行；在那里答，指针不动。事件存库 24 小时（`outbox` 表），过期删掉。
- `/use` 选中会话时，回复带最近 3 轮：`# 近期消息预览`，每轮 `[发送者] 消息`（60 字）、`[CC] 回复`（160 字），过长的去掉中间、留头尾，轮间 `---`；合计不超过 1500 字，在跑的写“进行中”，一条消息发完。

会话找你（`ir bot "消息" [--to bot]`，2026-11-08）：`POST /api/bot`，`BotLetter{text, to?, caller}` → `BotReply{bot, name, chat, letter}`。只认 socket 上在会话里跑的 `ir`（`X-Iridium-Session` 或 `caller`），否则 400；`to` 是 Bot 终端的名字，没有回 404。

- 有聊天指着这个会话：当作它的 `reply` 发过去。
- 没有：选一个聊天发 `card`（`pick`）：标题是会话，`summary` 是消息，选项“进入会话”（选项 ID 是会话 ID，等于 `/use` 它）和“关闭”（`-`，什么都不做）。不点直接说话，照旧进聊天指着的会话。
- 选哪个聊天：只看连着事件流的 Bot（都没连就不限），能用这个会话的；最近有过这个会话的聊天优先，其次你最后说话的聊天（QQ 一小时内被动回复不耗额度）。记录在 `chat_seen` 表（Bot、聊天、会话、时间：发消息、跑命令、切会话时记）。一个都没有回 404。
- `ref` 只对这个 Bot 有效，签过名，重启后照样能用；`~/.ssh`、`~/.claude`、`~/.codex`、`~/.pi`、Iridium 的状态和配置目录里的文件一律不给。
- 例子：`examples/plugin/echo_im.py`（只用标准库）把 stdin/stdout 当成一个假 IM。
- `ChatInput.attachments`：上面传好的引用，路径写进消息、图片照样给 agent（同网页）；只有附件没有文字也行。

Bot 切到会话（网页会话右键菜单、聊天页 ⋯ 菜单的最底下，2026-11-08）：`POST /api/sessions/{id}/bot`，`BringBot{bot}` → `BotReply{bot, name, chat}`。只有面板和本机；`bot` 不是 Bot 终端回 404。聊天的选法同上，指针改到这个会话，并发一条 `reply`，内容同 `/use`。没有能用这个会话的聊天回 404。`Terminal.bot`：有过聊天、或此刻连着事件流的插件，菜单按它列；已在这个会话里的不列，Claude 会话不列没有 Claude 的。`Terminal.chatless`：还没有聊天的 Bot，菜单项灰着，写「尚无对话」。插件第一次有聊天、事件流连上或断开时，服务端推 `terminals` 变更，网页重取终端列表。

## 官方 Bot

Iridium 不内置、但认得名字的 Bot（现在只有 QQ Bot `qq`，二进制 `iridium-qq`，见 `docs/qq.md`）。只有人能用（面板，或本机人跑的 `ir`）；agent 回 403，用扩展自己的命令行。

| 请求 | 正文 → 回答 | 说明 |
|---|---|---|
| `GET /api/extensions` | → `Extension[]` | 装没装、正在做什么、上次的错、扩展的每个 Bot（`bots`，`iridium-qq list --json`，带终端在线数）、扫码中的二维码 |
| `POST /api/extensions/{id}/install` | → `Extension` | 从同版本的 Release 下二进制放在 Iridium 旁边（校验 sha256、跑一次）；开发版用旁边的 `<名>.dist` |
| `POST /api/extensions/{id}/bind` | → `Extension` | 开始扫码，回答带第一张二维码；扫完 Iridium 建终端、把 TOKEN 写进那个 Bot 的目录（同 `--into`；本人才带 Claude）、启动它的服务。新 Bot 多一个，扫过的 Bot 更新 |
| `DELETE /api/extensions/{id}/bind` | → `Extension` | 取消扫码 |
| `POST /api/extensions/{id}/start`、`stop`、`pair` | → `Extension` | 服务开关；新配对码（在 `bots[].pair`）。`?bot=<AppID>` 指定 Bot，start、stop 不带就是全部 |
| `DELETE /api/extensions/{id}` | → `Extension` | 停全部服务、删配置和二进制、撤销它的全部终端 |

网页只用 `install`、`bind`、取消扫码；撤销（`DELETE /api/terminals/{id}`、`ir revoke`）一个官方 Bot 的终端时，Iridium 顺带让那个 Bot `remove`（停它的服务、删它的配置；别的 Bot 和二进制留着）。

- Iridium 启动时（更新之后）把装了的扩展换成同版本，服务在跑就重启。

## 交互终端

“本机”是内置终端（ID `local`），不在库里，有 Claude，不能撤销。其余的是门里登记的面板（`panel`）和通道（`channel`），表在 SQLite（`terminals`、`invites`、`logins`、`permissions`、`devices`）。每个终端有个设备 `device`：`computer`（默认）、`laptop`、`phone` 或 `none`（不展示），消息头像右下角的小标就是它，`none` 不带小标。

谁能做什么：

- **面板权**（批准新终端、看到 TOKEN 和邀请链接、答确认）：面板；本机 `ir` 由人在终端里跑时；没配 `public.origin` 时的本机网页。
- **管理**（列出、改名和头像、撤销）：面板和本机，包括 agent 调的 `ir`。
- 通道什么都不管，只能改自己的名字和头像。

| 请求 | 正文 → 回答 | 说明 |
|---|---|---|
| `GET /api/me` | → `Me` | 调用者自己：终端（`rules.claudeOwner`：有没有 Claude）、`panel`（有没有面板权）、`public` |
| `PUT /api/me/profile` | 资料 → `EndpointInfo` | 改自己的名字、头像（见 `docs/gate.md`）；本机回 409 |
| `PUT /api/endpoints/{id}/profile` | 同上 | 改别的终端的（`id` 也可以是名字）；`/api/terminals/{id}/profile` 同 |
| `PUT /api/me/device` | `SetDevice` → `Terminal` | 改自己的设备（`computer`/`laptop`/`phone`/`none`，别的回 400）；本机也行。网页在“这个终端”弹窗里改 |
| `PUT /api/terminals/{id}/device` | 同上 | 改别的终端的（`id` 也可以是名字，`本机`/`local` 也行），要管理权；`ir profile <名字> --device phone` |
| `GET /api/terminals` | → `Terminals` | `ir access`：所有终端（本机在最前）、有没有 Claude、今天的用量，和未用的邀请。没有秘密 |
| `GET /api/usage?days=N&terminal=<ID、名字或 me>` | → `Usage` | `ir usage`：最近 N 天（默认 7，最多 366，今天在最后）按本地日期、终端、agent、模型合计的回合数、输入、输出、缓存读写 token 和费用（只有 agent 报了的）。不带 `terminal`：管理权看所有人，通道只看自己；看别的终端要管理权（否则 403）。`terminals` 给出行里还在的终端的名字和头像；`terminal` 为空的行是认不出谁发起的回合 |
| `POST /api/terminals/invites` | `NewInvite` → `Invite` | `ir invite [名字]`：新面板的一次性链接（10 分钟），`qr` 是同一链接的二维码（SVG data URL）。要先有 `public.origin`（否则 409）。没有面板权时回 202 `Pending`。`owner: true`（`--owner`）：加入的面板带 Claude。本机由人跑的 `ir`、带 Claude 的面板（有 Passkey 的，不算没配 `public.origin` 的本机网页）直接给；agent 的回 202 `Pending`，只有带 Claude 的面板或本机 `ir` 能同意；别的 403 |
| `POST /api/terminals/tokens` | `NewInvite` → `Token` | `ir token <名字>`：Bot TOKEN，只给这一次；已有的通道换新 TOKEN，旧的作废。没有面板权时回 202 `Pending`。`owner: true` 同上：新建的通道带 Claude；已有的通道换 TOKEN 时 Claude 照旧，没 Claude 的再带 `owner` 回 409（Claude 只在建的时候定）。`into`（`ir token --into 文件`）：由 Iridium 把 TOKEN 写进这个绝对路径（0600，不能落在 Iridium 和 agent 的目录里），回答和确认面板都不给 TOKEN，只给 `into` |
| `DELETE /api/terminals/{id}` | → `{ok}` | `ir revoke`：终端（连同登录、Claude、连接）或未用的邀请；最后一个有 Passkey 的面板删不了（409） |

## 能做什么（`Rules`）

所有终端都一样，什么都能做，只有 Claude 例外（用户，2026-11-08）：

- `claudeOwner`：这是你本人的终端（Claude 用户协议），能用 Claude。`{by, at}`，`by` 是 `local`（本机由人跑的 `ir` 的 `--owner`；`--owner` 邀请加入的面板也记这个）、`panel`（带 Claude 的面板开的或同意的）或 `first`（第一个面板）。只在建终端时由服务端写，之后不能改，也没有改的接口。
- 没有 Claude 的终端看得到 Claude 的会话（列表、读消息、事件流、文件），但不能开新的、发消息、停、中断、答提问、放行、换模型模式强度、动待发队列、往里传文件（403）。改名、置顶、颜色、归档谁都行。别的 agent 谁都能用。
- 不再有目录、命令、审批、每日上限、拷入拷出、看别人会话这些限制；Bot 和面板一样。旧库里这些字段还在，不读。

Bot 的聊天层（`/api/chat/…`）按 TOKEN 认终端，同一条规则：开会话（含 `/new`，默认 Claude）、发消息、`/use`、`/stop`、答提问对 Claude 会话要有 Claude。聊天开的会话记在 Bot 名下，来源标记是 `[Bot 名·发送者]`。

## 确认（agent 的 `ir` 要秘密时）

agent 调 `ir invite`、`ir token` 时，邀请链接和 TOKEN 不能进它的对话。服务端不回秘密，改记一条确认（内存里，10 分钟），网页面板在“需要你”里看到，同意后**只有点同意的那个面板**拿到链接或 TOKEN；agent 只拿到结果。认 agent 靠 `ir` 带的头，只防 TOKEN 进记录，挡不住故意读库。

| 请求 | 正文 → 回答 | 说明 |
|---|---|---|
| `GET /api/confirms` | → `Confirms` | 等确认的（面板权）。事件流 `confirms` 是同一份 |
| `POST /api/confirms/{id}` | `ConfirmAnswer` → `ConfirmReply` | 面板同意或拒绝；同意时回答里带 `invite` 或 `token`，只此一次。`owner: true` 的确认只有带 Claude 的面板或本机 `ir` 能同意（别的 403，确认继续等），谁都能拒绝 |
| `GET /api/confirms/{id}?wait=<秒>` | → `ConfirmState` | 管理权或本机 socket 上的 `ir`。结果：`pending`、`allowed`、`denied`、`expired`、`failed`，从不带秘密。`wait` 最多 60 秒，有结果就回 |

## 凭据

见 `docs/plan.md`“凭据”。表 `secrets`（名字、说明、范围、`always`（每次确认）、密文、谁存的、时间）、`secret_uses`（凭据、会话、终端、命令开头、时间）。值用 AES-256-GCM 加密，密钥 `<状态目录>/secrets.key`（0600，第一次用时生成）。名字：大写字母、数字、`_`，字母开头，≤64。

| 请求 | 正文 → 回答 | 说明 |
|---|---|---|
| `GET /api/secrets` | → `Secrets` | 名字、说明、范围、谁存的、用量和最近 50 次使用。从不带值。所有终端 |
| `PUT /api/secrets/{name}` | `SetSecret` → `SecretInfo` | 存或换（值、说明、范围、`always`）。不带值：已有的只改说明和范围。面板、本机由人跑的 `ir`、所有通道（通道只能存值和说明，范围、`always` 只有面板改）；agent 调的回 202 `Pending`（确认卡带密码框，值由点“存”的面板交上来，见下） |
| `DELETE /api/secrets/{name}` | → `{ok}` | 要管理权；agent 调的也行 |
| `POST /api/secrets/use` | `UseSecrets` → `SecretValues` | 只给 `ir run`，只走 unix socket：会话、名字、命令开头。查范围；要确认时回 202 `Pending`，`ir run` 等到同意后带 `confirm`（确认的 ID）再取，一个确认只能用一次。值只交给 `ir` 进程，它不打印 |

- 确认（`/api/confirms`）多两种：`secret-set`（`Confirm.note`、`replace`；同意时 `ConfirmAnswer.value` 带值，服务端存好，谁存的记点“存”的面板；回答和 `ConfirmState` 不带值）、`secret-use`（`Confirm.secrets`、`command`；允许 / 本会话一直允许（`ConfirmAnswer.forSession`）/ 拒绝，记在内存里，会话的进程停了就忘；`owner: true`，只有带 Claude 的面板或本机 `ir` 能允许；有 `always` 的凭据不给“本会话一直允许”）。
- 配对（`docs/exec.md`“连接”）：执行端用 `POST /api/exec/pair`，Bot 用 `POST /api/pair`（`ExecPair` → `ExecPairing`；Bot 只报 `name`、`program`），再轮询 `GET /api/exec/pair/{request}` 或 `GET /api/pair/{request}`（→ `ExecPairState`：`pending`、`allowed` 带 `token`、`denied`、`expired`；未知或已取过 404）。这是门外仅有的不用登录的口。Bot 接入只走这条路，网页不再手动发 TOKEN（用户，2026-11-08）；`ir pair <地址> --name 名字 --into 文件` 替程序申请，TOKEN 写进文件、不打印。限制放宽（用户，2026-11-08）：每个来源每小时 30 次、同时最多 4 个待批，总共最多 20 个，多了 429；设置 `pairClosed`（终端管理页底部“接收新的连接申请”）关掉时一律 403。申请变成 `exec-pair` 或 `bot-pair` 确认（`Confirm.code`、`machine`）；面板同意时 `ConfirmAnswer.owner` 说“本人的机器”或“带 Claude”（只有带 Claude 的面板或本机 `ir` 能说）。同意后新建一个 Bot，名字是申请的名字，重名加 `-2`、`-3`；已有同名的 Bot 时 `Confirm.replace` 为真，面板可带 `ConfirmAnswer.replace` 改为替换它：同一个终端（用量、Claude 照旧），新 TOKEN，旧的作废。TOKEN 只经 `GET` 给申请方一次，面板拿不到。
- 要不要确认：本机由人跑的 `ir run` 直接用；agent 的，会话由本机、带 Claude 的终端开的（或不经 Iridium 开的）直接用，别的终端开的要确认；`always` 的每次都确认。范围不对直接 403，不弹确认。
- 对话记录出接口前（会话列表、会话状态、消息页、整段原文、事件流、Bot 的聊天指针、推送；`ir read`、`ir wait` 走的就是这些），已知的值（原样、URL 编码、base64）换成 `[secret:名]`；短于 6 个字符的不换（误伤太多）。
- `ir run` 自己起子进程（同目录、同环境，加上凭据），stdout/stderr 照转，按最长值的长度留尾巴再打码；退出码照子进程。

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
| `GET /api/update` | → `UpdateState` | 现在的版本、最新版本和说明、`size`（下载多大）、`available`、`auto`（查到新版就在后台下好：Release 版是，源码编译的不是）、`ready`（已下好）、`phase`（`idle`、`checking`、`downloading`（`got` 是下了多少）、`restarting`、`failed`）。事件流 `update` 是同一份 |
| `POST /api/update/check` | → `UpdateState` | 马上查一次；`auto` 时有新版就在后台下好 |
| `POST /api/update` | `ApplyUpdate` → `UpdateState` | `ir update`、设置里的“更新”：用下好的，没有就现在下（核对 sha256、跑一次 `iridium version` 看版本对不对），原子替换二进制、重启服务。没有更新的（`force` 除外）回 409 |

更新源是配置 `update.url`：不填是 `github:kumoSleeping/Iridium`（GitHub Releases 的最新版），`off` 不查；`update.every` 默认一天查一次。Release 版（版本号是干净的 `vX.Y.Z`）查到新版就下到二进制旁边的 `.iridium-update-*`，点“更新”只换文件、重启；源码编译的（`dev`、`vX.Y.Z-dev.N+g…`）不预先下载。没有开关；旧配置里的 `update.manual` 不再读（2026-11-08）。守护进程启动时清掉上次剩下的。

GitHub 源：最新 Release 的 tag 是版本，正文（`<!-- iridium:assets -->` 之前，后面的大小表只给 Release 页看）是说明，资源 `iridium-<GOOS>-<GOARCH>.tar.gz`（里面的 `iridium`）或 `iridium-<GOOS>-<GOARCH>`；sha256 用资源自带的 `digest`，没有就看同一 Release 里的 `SHA256SUMS`。别的地址返回清单：

```json
{"version": "1.2.0", "notes": "…", "url": "https://…/iridium-linux-amd64", "sha256": "<hex>",
 "platforms": {"linux-arm64": {"url": "…", "sha256": "…"}}}
```

`platforms` 里有本机的（`GOOS-GOARCH`）就用它；`url` 以 `.tar.gz` 结尾的同样解包。版本是带点的数字（可有 `v`、`-rc.1`）；`dev` 版不提示更新，`ir update --force` 照样装。重启：在 systemd 单元里就 `systemctl --user restart --no-block <单元>`，否则原地重新执行。托管进程在自己的单元里，不受影响。

设置里（有面板权的才有）：“检查更新”一个键，按下即 `POST /api/update`：查到新版就下载、替换、重启，中间不再确认；旁边写状态（检查中、下载中 37%、重启中、已是最新 · 时间、更新失败）。打开设置时上次查过超过一小时就查一次（只查）。有新版时最上面一张卡：版本、大小、说明、下载进度、“更新”键。在这个页面上按了更新，新版一上线页面就刷新（有没发出的内容除外）；否则连接状态那里换成“↻ 刷新”，点了刷新。

## 部署

- `iridium install [-config <文件>] [-unit <名>] [-listen <地址>] [-env 名=值]... [-now]`：写用户级单元 `~/.config/systemd/user/iridium.service`（`iridium serve -config ~/.config/iridium/config.json`，带装的时候的 `PATH` 和 `-env` 给的变量；`$XDG_CONFIG_HOME` 设了就写在它下面），没有配置就写一份默认的（`127.0.0.1:7810`，`-listen` 换地址），把自己复制到 `~/.local/opt/iridium/iridium`（`-prefix`）、链 `~/.local/bin/ir`（`-bin`），建 `~/.local/state/iridium`，`daemon-reload`。不加 `-now` 不启动。没开 linger 时提醒：退出登录后服务和托管的 agent 都会停（`sudo loginctl enable-linger <用户>`）。
- `iridium push [-i <密钥>] [-listen <地址>] <用户@主机>`：经 ssh 把这个二进制发到另一个账号或机器（先 `uname -sm` 核对平台），放到那边的 `~/.local/opt/iridium/iridium`，跑一次 `version`；那边 `iridium.service` 已经装了就重启，没有就 `iridium install -now`（`-listen` 给第一次装的地址）。托管进程不受影响。
- `iridium node new <账号> [-listen <地址>]`：在这台机器上给一个账号装一个 Iridium，你在终端里跑：要 root 的几步（没有就 `useradd -m`、`loginctl enable-linger`、把 `~/.ssh/iridium_<账号>_ed25519` 放进那边的 `authorized_keys`）合成一次，是 root 就直接跑，在 sudo/wheel/admin 组里用 sudo，否则用 `su`（问 root 的密码）；再 `push` 过去（地址默认 127.0.0.1 上 7811 起第一个空着的端口）。不替你装 agent：那边的 agent 登录和 `ir public` 在那个账号里做。
- `scripts/deploy.sh`：`web/package.json` 在就先 `npm ci && npm run build`（`NO_WEB=1` 用现成的），`internal/webui/dist` 里没有网页就停；`go build`（版本取 `git describe`）装到 `~/.local/opt/iridium/iridium`（`PREFIX`），`~/.local/bin/ir`（`BIN`）链过去，再跑 `iridium install`（`CONFIG`、`UNIT`、`UNIT_ENV` 照传）；服务已在跑就重启它。`IRIDIUM_NO_SYSTEMCTL=1` 不碰 systemd。TCP 地址上就是网页本身（`/api/`、`/_ir/` 之外都是网页，前面同样是门）。
- `scripts/test-deploy.sh up|again|down [目录]`：不碰真的服务，把 `deploy.sh` 真跑一遍：临时的安装位置、状态、家目录和 Claude 目录，假 claude（`internal/testagent`），单元 `iridium-test-deploy-<n>` 写在 `$XDG_RUNTIME_DIR/systemd/user`（不进 `~/.config`），随机端口；托管进程的单元是 `iridium-test-hold-<n>-*`。`web/scripts/seed.mjs` 往里放会话，`e2e.mjs`、`shoot.mjs` 对着它跑。
