# Iridium 接口

Iridium 自己的接口都在 `/v1/internal/` 下：Satori 规范的 internal 路由（advanced/internal.md），Satori 的标准方法、事件和对应关系见 `docs/satori.md`。带了 `Satori-Platform`、`Satori-User-ID` 就必须是调用者自己的（否则 403 `no_such_login`）。两个入口，路径相同：

- HTTP：`127.0.0.1:7810`（配置 `listen`），给网页和应用，前面是 Passkey 门（`docs/gate.md`）。没配 `public.origin` 时只认本机的主机名（`localhost`、回环地址和配置 `hosts`），非 GET 请求带了别的 `Origin` 就拒绝，请求都算主机（带 TOKEN 的算那个应用）。配了以后每个请求都要网页登录或 TOKEN。
- unix socket：`$XDG_RUNTIME_DIR/iridium/ir.sock`（0600），给主机上的 `ir`，不经过门，算主机。`ir` 认出是 agent 在调（环境里有 `CLAUDECODE`、`CODEX_THREAD_ID`、`IRIDIUM_SESSION`，或者没有终端）时带头 `X-Iridium-Agent: 1`，这时不回任何秘密（见“需要”）。`ir` 还带 `X-Iridium-Session`（它所在的会话：`IRIDIUM_SESSION` 或 agent 自己的会话变量）；那个会话是别的终端经 Iridium 开的，这个请求就算那个终端的：有没有 Claude 照它，记它的用量和来源（agent 在某个应用开的会话里调 `ir`，等于那个应用自己做）。

来源：每条交给 agent 的消息开头是 `[终端名] `（`/` 开头的命令除外），名字取发送时终端的名字。库里记下这条输入是哪个终端（ID）发的、用的什么名字；读记录时服务端按 Iridium 自己写进去的输入认（Claude：发消息时给的 uuid；没有 ID 的 agent：`store.SourceByText` 比对输入记录），去掉前缀，消息块带 `from`（名字）和 `fromTerminal`（终端 ID，取头像用）。用量行按终端 ID 记，改名不丢。经 unix socket 的 `ir api message.create`、`ir new` 只要带着它所在的会话（`X-Iridium-Session` 头，取自 `IRIDIUM_SESSION`、`CLAUDE_CODE_SESSION_ID` 等环境变量，或正文的 `caller`），前缀就是 `[ir:<会话 ID>] `，`from` 也是它，页面画成那个会话的来信（名字：会话改过的名字，否则“Claude · 工作区名或文件夹名”）；有没有 `X-Iridium-Agent` 都一样。终端（`fromTerminal`，记用量的）照旧：开那个会话的终端，否则本机。网页和 TOKEN 来的请求不认 `caller`。

类型都在 `internal/api`。`go generate ./internal/api` 用 tygo 生成 `web/src/api/types.gen.ts`，前端只用它。生成的文件进库，改了 Go 的类型要一起重新生成提交；`scripts/check-types.sh`（CI、发布和 preflight 都跑）重新生成一遍，对不上就失败。

出错时状态码非 2xx，正文 `{"code": "…", "message": "…"}`（决定 8）：401 只表示 TOKEN 或登录无效；其余拒绝一律 403 加原因码（`pair_closed`、`login_pending`、`no_permission`、`too_many_pending`…）；422 东西不在或不对（`no_such_session`、`no_such_need`、`no_such_app`、`no_such_file`、`no_such_path`、`no_such_note`、`no_such_secret`、`no_such_input`、`no_such_image`、`not_here`（这台没开这项）、`bad_name`…）；400 请求不对；404 只表示没有这个接口（另外头像没设、网页的页面不在也是 404）；409 agent 没在运行或状态不对；429 今天的用量到上限；504 agent 没按时回。

## 请求

会话的开、列、读、发、改名、归档、置顶、颜色、位置、拖出来，排队消息的修改和撤回，回答提问，都走 Satori（`docs/satori.md`：`channel.create`、`channel.list`、`message.list`、`message.get`、`message.create`、`channel.update`、`message.update`、`message.delete`、`/v1/internal/button`）；对应的旧路由 2026-11-11 删掉。

| 请求 | 正文 → 回答 | 说明 |
|---|---|---|
| `GET /v1/internal/sessions/{id}` | → `SessionState` | 打开的会话的状态，和携带状态 `session:<id>` 同一份 |
| `GET /v1/internal/sessions/{id}/image?at=<at>&n=<n>` | → 图片 | 工具结果（`Result.images`，`at` 是结果的）给模型的第 `n` 张图，取自 agent 的记录，不读文件现在的样子：Claude、pi 和 MCP 工具的在结果里，Codex 的 view_image 在脚本的输出里（脚本只看了一张、只交出一张，当时没有别的调用、也没有后台还在跑的脚本时，才认得出是哪张；认不出的不列进 `images`），dsh 的 read_image 在它的附件库（经通道，远端经执行端）。只给 PNG、JPEG、GIF、WebP，按内容的 ETag。没有这张 422 `no_such_image`，不在了 410，所在机器没连上 409，脚本还没跑完 425（稍后再问） |
| `POST /v1/internal/sessions/{id}/interrupt` | → `{ok}` | 停掉当前回合（Esc），agent 留着。队列暂停 |
| `POST /v1/internal/sessions/{id}/stop` | → `{ok}` | 停掉 agent 进程（关 stdin，写完记录自己退出）。会话还在，下一条消息再拉起。队列暂停 |
| `POST /v1/internal/sessions/{id}/release` | `Release{toolUseId, dismiss?}` → `{ok}` | Claude auto 模式拦下一步后，临时切换至 manual（见“临时切换至 manual”）；`dismiss` 只拿掉它的卡。Claude 会话要有 Claude，agent 调用一律 403 |
| `GET /v1/internal/agents` | → `AgentInfo[]` | 能开的 agent：审批模式（`modes`、`defaultMode`、`modeSwitch`：now / next turn / restart）、有没有上下文分项、有没有额度窗口、能不能开 ultracode（`ultracode`）、程序装没装（`missing`：PATH 上找不到；这时 `channel.create` 立刻 400，不等 agent 自更新那 30 秒，网页把它画淡，点了只提示未安装） |
| `GET /v1/internal/settings` | → `Settings` | 设置的配置项：空闲回收、崩溃续跑、额度续跑（这三项设置页不放，只改配置文件或经接口）、隐藏的额度条、各 agent 新会话的审批模式、推送总开关。管理权 |
| `PUT /v1/internal/settings` | `SettingsPatch` → `Settings` | 只改给了的项，写进配置文件，马上生效。面板权 |

## 消息页

- 一页最多 20 条消息（你的话、agent 的话），中间的块跟着走，最多 200 块。
- `start`：这页第一行的字节位置，往回翻用 `before=<start>`。`end`：这页覆盖到哪，取新的用 `after=<end>`。`more`：前面还有。
- 块（`Block.kind`）：`user`、`assistant`、`thinking`、`tool`（同页的结果在 `result` 里）、`result`（调用在别的页，按 `id` 拼）、`system`（`event`：`compact`、`api_error`、`local_command`、`interrupted`、`unreadable`、`iridium`）。`iridium`：Iridium 自己写给 agent 的话（恢复提示、“继续”），`text` 是原文，页面画成居中一行淡字。
- 工具参数、结果、思考、系统行超过 2000 字只给开头，`length` 是全长，全文按 `at` 取。你的话和 agent 的话不截。

## 携带状态

网页（和主机自己的页面）在 Satori 的 `/v1/events` 上要：IDENTIFY 带 `_watch`，或之后发 op 6 WATCH `{keys}`，每次取代上一次，最多 16 个键；服务端用 op 7 STATE `{type, data}` 推，`type` 是下面的 `state` 或 `draft`。换了打开的会话就重发 WATCH。只给网页的登录和主机的连接，应用的连接发 WATCH 不理。

| 键 | 值 |
|---|---|
| `list` | `Session[]` |
| `guilds` | `Guilds`：文件夹组（按拖成的顺序，没拖过的随后）和文件夹的标记（`FolderMark`：颜色、置顶、拖到的位置），2026-11-11 取代 `workspaces`、`places`、`colors` |
| `session:<id>` | `SessionState`：状态、提问（带 `card`）、后台任务、模型（`model` 在用的，`pick` 设的）、审批模式、思考强度（`effort` 设的，`effortNow` agent 说在用的）、可选的模型/模式/强度、`ultracode`（只 Claude）、`attached`（挂在页面上的元素，见“会话设置”）、待发队列、上下文总数、`tokens`（会话累计的输入：未缓存、读缓存、写缓存，和用量同一批记录；页面在上下文弹窗里算缓存命中率）、等额度到几点、`tail`（块带 `files`）、`end`、`proc`、出错原因、`next`（Claude 自己清空了：对话接着在这个会话里，页面跟过去）、`commands`（给输入框的命令面板：人人都有的 `clear`、`compact`、`context`、`usage`、`model`、`effort`、`mode`，不带说明，页面按语言写；再接 agent 握手报的） |
| `context:<id>` | `Context`：上下文用了多少、分项。回合结束、压缩后，回合中调用工具时最多 30 秒一次更新；存在托管目录，没进程时也有 |
| `quota` | `Quota`：按 agent + 账号（Claude：邮箱和套餐）+ 机器（`machine`，执行端的会话是那台机器的登录）的额度窗口、读到的时间、`live` |
| `needs` | `Needs`：等人处理的需要（见“需要”） |
| `pairing` | `PairWindow`：配对窗口开到几点 |
| `update` | `UpdateState`：有没有新版本、更新到哪一步 |
| `terminals` | 一个计数：应用或机器变了，重新取 `GET /v1/internal/apps` |
| `notes` | `Notes`：全部便签和便签夹 |

不认识的键、不给这个连接的键（`needs`、`pairing`、`notes` 只给网页的登录和主机）不让 WATCH 失败：先为每个发一条 `state`，`{key, value: null, ignored: "unknown" | "forbidden"}`，其余照常。

`list` 和会话按权限范围给。应用被移除、网页登录退出、`public.origin` 改了，连接就断开。值里的凭据打码（见“凭据”）。

事件：

- `state`：`{key, value}`，一样东西的整份。不存在时 `value` 是 `null`。
- `draft`：`{session, proc, n, text, done?}`，agent 正在打的字，接在第 `n` 段草稿后面。`done: true`：这段写完了，不会再有字（开始调工具、写下一段），页面去掉光标；它的话随后在记录里来。

规则：

- WATCH（含断线重连）先发每样的整份；之后变了再发整份，不发补丁。同一样 250ms 内最多一次，发的是最新的。
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
  - 归档（`channel.update` 的 `_data.archived: true`）：去掉标记，记下记录当时的长度；之后长过这个长度（有了新活动）就自动回来。托管的另外像空闲回收那样停掉 agent；它退出时写的不算活动（退出后重记长度）。恢复（`archived: false`）：设上标记。没有按时间自动归档。
  - 旧版的归档标记迁移成“没标记”。
- 归组（2026-11-08：组就是文件夹，`Workspace` 是组，不能手建）：会话第一次显示时（第一次活动）做一次，记在库里，不给归档里的做：拉起者的组（组的文件夹包含它的目录时；子代理总跟父会话）→ 包含它目录的最外层的组 → 用它的目录新建一个组。家目录和 `/` 的组（另一台机器不在线时，按路径认它的家目录）只收正好在那里的会话。之后每次归组都把落在别的组文件夹里的组并进最外层那个（子目录先建了组、父目录后建的，比如执行端的会话一次全看到）：自动归进去的会话移过去，空了的组删掉，有手动放进去的会话的组留着。在未归类（`workspace` 为 0）且不是你手动放过去的也再归一次；只有没目录的、远端 Desktop 里不在任何工作区的（`loose`）留在 0。自动归进别的机器的组的（执行端重新配对换了 ID；以前跨机器跟着拉起者归的）按自己的目录重归，子代理除外，它跟父会话（2026-11-10）。`channel.create` 开的托管会话当场归组，不等记录出现：拉起者的组包含它的目录就进那个组，否则按自己的目录（跨机器不再跟拉起者，2026-11-10）。手动移过的（`channel.update` 的 `_data.workspace`）不再自动动。
- 人开的和挂着的（2026-11-10，`exec.md`“远端工作区”）：`Session.own` 由服务端算，是“人开的”的唯一依据。有 `launcher` 的：手动放过（拖出来、`detach`）才算；没有的：托管、且经创建开的（`openers` 表有它：网页“+”、终端里的 `ir new`、应用开的、`/clear`）。子代理、接管的（发消息接过来的）、发现的都不算。有 `launcher`、不是子代理、不是 `own` 的挂在拉起者下面（页面画）。
- 推送只给页面会列的会话：本机的、`own` 的、有 `launcher` 的（挂着的；拉起者不在、归档了、是子代理或没列出就当人开的）、所在组列出来的（组里有没归档的人开会话，或本机的会话），子代理随父会话。远端没人开过的组里的会话不推送。服务端和页面用同一套规则（`session.standOf` 与 `tree.ts` 的 `standing`，共用 `internal/session/testdata/standing.json`，2026-11-10）。
- 状态：`hosted` 托管；`elsewhere` 在别处运行，只读（能看状态和消息页，发消息回 409）；其余是磁盘上空闲的。别处退出后，发消息就接成托管会话（`--resume`），前提是没有别的进程在用（Claude：`sessions/` 里没有活着的同 ID 进程；dsh：`session.lock` 能拿到 flock；Codex：写锁）。现在只能接 Claude 的。
- 列表变了（新会话、记录长了、进程起停、改了覆盖层）攒 200ms 发一次 `list` 整份，变了的会话另发 `session:<id>`。

| 请求 | 正文 → 回答 | 说明 |
|---|---|---|
| `PATCH /v1/internal/guilds/{id}` | `GuildPatch{name?, position?, split?}` → `{ok}` | 文件夹组：`name` 改名；`position` 移到第几个（0 最前，和 `guilds` 的顺序一致；拖动或菜单上移下移）；`split` 按子文件夹拆开：组只留它文件夹里直接的会话，子文件夹里的按各自文件夹重新归组（子文件夹的子文件夹仍进它的组），以后也不再收子文件夹（`flat`），从拉起者下面拖出来的仍按手动放进自己目录的组。只有面板和本机 |
| `GET /v1/internal/folders?path=` | → `Folders` | 选择器：只列文件夹，不列隐藏的，按名字排；`path` 为空或 `~` 是家目录。`parent` 为空表示到顶了 |
| `POST /v1/internal/folders` | `NewFolder` → `{path}` | 在 `path` 里新建文件夹 `name`（不能以 `.` 开头、不能带 `/`）。已有回 409 |
| `GET /v1/internal/machines/{id}/dsh/workspaces` | → `DshWorkspace[]` | 那台机器上 dsh 的工作区（DSH Desktop 侧边栏那份，按它的顺序，文件夹已不在的不列），读不懂时用它会话所在的文件夹代替，最近的在前；路径用 `/`。`sessions` 是其中列出的会话数（不算子代理、这边归档的、不同步的）。新会话选文件夹时列在最上面，见 `docs/exec.md`“远端 DSH” |

- 选择器默认只能在家目录里（配置 `folderRoots` 可改），先解析符号链接再比；`~/.ssh`、`~/.gnupg`、`~/.claude`、`~/.codex`、`~/.pi`、`~/.dsh` 和 Iridium 的状态、配置目录一律不给（403）。不存在 422 `no_such_path`。

## 便签

面板和本机的，同颜色标签。每次改动，携带状态 `notes` 整份重发。

| 路由 | 请求 → 回复 | 说明 |
|---|---|---|
| `GET /v1/internal/notes` | → `Notes` | 全部 |
| `POST /v1/internal/notes` | `NewNote` → `Note` | `id` 页面自己给（UUID），同一个 `id` 再发不重复建；`pos` 不给就放在它那一夹最上面 |
| `PATCH /v1/internal/notes/{id}` | `NotePatch` → `Note` | 改正文、夹（0：不在夹里）、位置、颜色、完成；`session`：放进过的会话，记在 `sessions` 最后 |
| `DELETE /v1/internal/notes/{id}` | → `{ok}` | |
| `POST /v1/internal/note-folders` | `NoteFolderPatch` → `NoteFolder` | 只一层 |
| `PATCH /v1/internal/note-folders/{id}` | `NoteFolderPatch` → `{ok}` | 改名、位置 |
| `DELETE /v1/internal/note-folders/{id}` | → `{ok}` | 里面的便签移出夹 |

## 文件和上传

代码在 `internal/files`（路径检查在 `internal/pathsafe`），路由在 `internal/server/routes_files.go`。

| 请求 | 正文 → 回答 | 说明 |
|---|---|---|
| `GET /v1/internal/sessions/{id}/files/{ref}` | → 文件 | 支持 `Range`/`If-Range`；`ETag`（大小 + 修改时间）；`X-Sha256` 和 `Repr-Digest`（64MB 以下当场算，大的算过才有）。`?download` 强制下载 |
| `GET /v1/internal/sessions/{id}/files/{ref}/thumb?size=1024` | → 图片 | 缩略图，最长边默认 1024（32–4096）。png/jpeg/gif/webp；按路径 + 大小 + 修改时间缓存在状态目录 `thumbs/`；SVG 原样给 |
| `GET /v1/internal/sessions/{id}/files/{ref}/info` | → `FileRef` | 带 `sha256` |
| `POST /v1/internal/sessions/{id}/uploads` | `NewUpload` → `Upload` | 建上传任务。超过 `uploads.maxFile`（默认 2GB）是 413 |
| `PUT /v1/internal/uploads/{upload}?offset=<n>` | 块 → `Upload` | `offset` 必须等于 `received`，否则 409（正文也带 `Upload`）。一块最多 64MB；块大小客户端定（8–50MB，按测到的速度） |
| `GET /v1/internal/uploads/{upload}` | → `Upload` | 已收多少（续传从 `received` 接着发）。断在半块时，到达的部分也算 |
| `POST /v1/internal/uploads/{upload}/complete` | `{sha256}` → `Upload` | 核对 sha256，对上了原子改名，`file` 里是引用。对不上 422，已收的丢掉，从 0 重传 |
| `DELETE /v1/internal/uploads/{upload}` | → `{ok}` | 取消 |

只走 unix socket（本机 `ir`）：

| 请求 | 正文 → 回答 | 说明 |
|---|---|---|
| `POST /v1/internal/sessions/{id}/attach` | `{path}` → `FileRef` | 把本机文件当这个会话的附件（`ir api message.create -a`），不复制 |
| `POST /v1/internal/links` | `{path}` → `FileLink` | 给本机文件一个文件链接（`ir attach`），见“文件链接” |
| `GET /v1/internal/files?path=<目录>[&machine=<机器>]` | → `DirListing` | 列一层；带 `machine` 是那台机器上的目录（它自己的写法，空是家目录） |
| `POST /v1/internal/files/copy` | `{from, to, fromMachine?, toMachine?}` → NDJSON | 拷文件（`ir cp`）：每 250ms 一行 `CopyProgress`，最后一行带 `path`、`sha256` 或 `error`。由 Iridium 拷，`ir` 断了也接着拷；同样的请求再来就接上。一边在别的机器（`fromMachine`、`toMachine`）时：不接续，写到那边的先限 4MB（413），目标已有文件 409 |
| `POST /v1/internal/cli` | `CLIRun{args, cwd}` → NDJSON `CLIOutput` | 执行端上的会话里 agent 的 `ir`（只认会话 TOKEN，见 `docs/exec.md`“远端的 ir”）：这边照常跑这条 `ir`，算那台机器的 Bot、代表这个会话；输出按行流回（`out`、`err`），最后一行 `exit` |

规则：

- **引用**：`ref` 只对这个会话有效，同一路径每次一样（状态目录 `files.key` 做的 HMAC），看不出路径。只有会话记录里出现过的路径、它的上传和附件才有引用；服务器取不到就从上次读到的地方往后读记录。
- **文件链接**：`ir attach <路径>` 给文件一个随机 id（10 位小写 base32），打印 `![名字](ir:id)`（图片、音视频）或 `[名字](ir:id)`（其余）；回复把它写在正文里该放的位置。64MB 以内复制到状态目录 `links/<id>/<名字>`（原文件改了、删了照样显示）；更大的不复制，原文件变了就不再给（410）。记录在 `links/<id>.json`，同一会话同一文件没变时给同一个 id。id 不认会话：`/v1/internal/sessions/<任一会话>/files/<id>` 都能取，知道 id 才取得到。路径照样过 `pathsafe`。`Block.files` 里它的 `ref` 是 id、`path` 是 `ir:<id>`；网页在链接处显示（图片缩略图、音视频播放、其余卡片或行内下载），取不到的划掉，标“文件不可用”。只认 agent 的话，围栏代码块里的不算。执行端上的会话没有 `ir attach`。
- **认文件**（`Block.files`）：只认单独一行的 `` [文件] `路径` ``（绝对路径或 `~/`），你的话里另认 `` [附件] `路径` ``；`Write` 调用的 `file_path` 只给引用、不显示。行内的路径、链接、`![]()`、围栏代码块里的都不算。只认存在的普通文件。
- **类型**：先看扩展名，没有再嗅前 512 字节。图片、音视频、PDF、纯文本内联显示，其余下载。所有文件都带 `Content-Security-Policy: …; sandbox` 和 `nosniff`，SVG、HTML 里的脚本不会跑。
- **上传位置**：会话目录的 `.iridium/uploads/`（建目录时写一个只有 `*` 的 `.gitignore`）。会话目录是 `$HOME`、写不进或不许写时，放状态目录 `uploads/files/<会话>/`。同名不覆盖，改叫 `名 (2).扩展名`。先写 `.<id>.part`，核对后用硬链接放到位（原子、不覆盖）。上传记录在状态目录 `uploads/records/`，重启后能续传。
- **清理**：`uploads.keep`（默认 `720h`，`"0"` 不清）以前的上传连同文件（没改过的才删）和缩略图，每小时一次。
- **附件**：`Send.attachments` 是引用。每个附件在消息末尾占一行 `` [附件] `路径` ``；图片另作为图片块给 Claude（超过 5MB 或边长 8000 时给缩到 1568 的副本，一条消息的图片共 20MB 以内），最多 20 个附件。
- **路径检查**（`pathsafe.Guard`）：先解析符号链接（不存在的路径解析到最深的已有上层）再比允许的目录；写着的路径和解析后的都不能落在 Iridium 的状态和配置目录、`~/.ssh`、`~/.claude`（及 `CLAUDE_CONFIG_DIR`）、`~/.codex`（及 `CODEX_HOME`）、`~/.pi`、`~/.dsh`、`/proc` 里，状态目录的 `uploads/files` 例外。被拒是 403。
## `ir`（agent 用的部分）

- 代码在 `internal/cli`；socket 用 `IRIDIUM_SOCKET`，没有就读配置（`IRIDIUM_CONFIG` 或默认位置）。`ir api` 原样透传（见 `plan.md`“ir”），接口说明在 `internal/irhelp/api.txt`。
- 会话 ID（服务端认，2026-11-11）：`/v1/internal/sessions/{id}/…` 和 Satori 的 `channel_id` 可写 ID 的开头（唯一即可，不足 32 位才查），`self` 是调用者所在的会话（`X-Iridium-Session` 或会话 TOKEN；没有就 400）；`guild_id=self` 是它的组。几个会话同一个开头 400。改名在会话里跑算 agent 起的（`byAgent`），见 `plan.md`“会话标题”。
- 输出给 agent 看：只打最后一段回复（`new --wait` 先一行 ID）；`cp` 一行 `路径 大小 sha256`；需要你时每个提问一行 `需要你：…`。进度只在 stderr 是终端时画。
- `message.create`、`new` 不带 `--wait` 时等到 agent 接了这条消息（最多 15 秒）再返回，所以紧接着的 `ir wait` 等的是这一回合。
- 回合在跑时发的话默认插进这一回合（和网页回车一样，2026-11-06 定）；`--wait` 等到这一回合结束，打印最后一段回复（可能还在说别的事）。`_data.steer:=false` 进待发队列（只对人在终端里跑的 ir 有效；agent 的总是插进，免得要人去点；2026-11-06）：不带 `--wait` 马上返回，带 `--wait` 等它发出去、再等它那一回合（队列暂停了就退出码 2）。agent 在等额度时插话也进队列。
- 退出码：0 结束，2 需要你，1 出错（agent 出错退出、回合中被停、`ir` 自己出错）。

- `NewSession.caller`、`Send.caller`：调用者会话，记成新会话的 `launcher`（页面挂在它下面，归档时一起归档）。不给时取请求所在的会话：socket 上 `X-Iridium-Session`（`ir` 从 `IRIDIUM_SESSION`、`CLAUDE_CODE_SESSION_ID`、`CODEX_THREAD_ID`… 取），远端是会话自己的 TOKEN；网页来的没有。不想挂：之后 `PATCH … detach:=true`（2026-11-11）。没给 `workspace` 时，调用者的组包含新会话的目录才进那个组，否则按自己的目录归组（2026-11-10）。

## 待发队列

照 Rhodium 网页的待发队列（architecture §6.1）。

- 回合在跑时 `send` 的话进队列，不插话；回合正常结束，排着的一起作为下一个回合发出（第一条开回合，其余并进去）。
- 等额度期间发的也排队，额度回来时发。
- 打断、停止、进程退出或崩溃：队列暂停（`queue[].paused`）。只有“继续”或那一条的“插话”才放；别的回合结束都不放。暂停时新发的话：没有回合在跑就直接发。
- 队列存在库里（`inputs` 表），重启后还在。`SessionState.queue` 带着整份。

| 请求 | 正文 → 回答 | 说明 |
|---|---|---|
| `POST /v1/internal/sessions/{id}/queue/{input}/steer` | → `{ok}` | 这一条马上发，并进正在跑的回合（没有就开新回合） |
| `POST /v1/internal/sessions/{id}/continue` | → `{ok}` | 放开暂停的队列：没有回合在跑就马上发，否则等这个回合结束 |

改这一条、删这一条：Satori 的 `message.update {channel_id, message_id, content | _data.text}`、`message.delete {channel_id, message_id}`，`message_id` 是 `message.create` 回的 ID；不在队列里（已发、已删）422 `no_such_message`。插话、继续仍是上面两条。

## 命令

在会话所在的机器和目录直接执行一行命令，不经 agent、不花 token（用户，2026-11-08）。网页上在输入框里以 `$` 开头（`$ git status`）；`!` 已用于插话。只有面板和本机能用，Bot 不能（执行端也不能，它只是命令落地的机器）；Bot 开的会话里 agent 调 `ir sh` 算那个 Bot，同样 403。

| 请求 | 正文 → 回答 | 说明 |
|---|---|---|
| `POST /v1/internal/sessions/{id}/shell` | `{line, cwd?}` → `{run?, cwd}` | `cwd` 省略为会话目录。`cd <目录>` 不执行，确认那边有这个目录后回 `{cwd}`（`~` 是家目录，相对路径接在 `cwd` 后）；网页记住它，下一条带上 |
| `GET /v1/internal/shell/{run}?from=N` | → `{data, next, done, code?, signal?, err?}` | 从字节偏移 `N` 读输出（一次至多 64KB），`next` 是下一次的 `from`。`done` 时输出已读完，`err` 是起不来时的原因 |
| `DELETE /v1/internal/shell/{run}` | → `{ok}` | 停止（连同它起的进程） |

- 每条命令一个进程：Linux、macOS 是 `/bin/sh -c`（stderr 并进 stdout），Windows 是 `cmd.exe /c`（stderr 在结束时给）。stdin 不接，读 stdin 的命令要手动停止。全屏程序（`vim`、`top`）不支持。
- 进程照托管 agent 的方式起（本机的 `platform.Launcher`，别的机器经执行端），目录是托管目录下的 `sh-<id>`；结束 10 分钟后删。
- 机器不在线：409；机器的主人关着命令行（`Machine.shell` 为假）：403 `no_shell`；目录不存在：422 `no_such_path`。

agent 用 `ir sh <机器>[:目录] [--cwd 目录] [--timeout 2m] -- 命令`（`POST /v1/internal/machines/{id}/shell`，`{id}` 是机器 ID 或名字，`cwd` 省略为那边的家目录；没有这台机器 422 `no_such_machine`）：原样打出输出，退出码就是它的；超时停掉它，退出码 124。执行端有 `run` 时请求带 `Accept: application/x-ndjson`，回 `ShellEvent` 的 NDJSON 流，边跑边出（`docs/exec.md`“命令”）；没有时照旧回 `ShellStarted` 再轮询。只在主人开了 `shell` 的机器上能用（默认关；否则 403 `no_shell`）。`ir apps` 列出应用，带电脑的另列系统、连接、agent（关掉的标“关”）、能否 `ir sh`、家目录。系统提示里只有 `ir --help` 的一行。

## 会话设置

按会话记在库里：马上设给在跑的 agent，之后每次拉起都带上（Claude：`--model`、`--permission-mode`、`--effort`，设过 ultracode 带 `--settings '{"ultracode":…}'`，关也带，压过你自己设置里的）。

| 请求 | 正文 | 说明 |
|---|---|---|
| `PUT /v1/internal/sessions/{id}/model` | `{model}` | `""` 是 agent 的默认。Claude：`set_model`。dsh（用户，2026-11-08：模型和供应商不写死）：会话状态的 `models` 是会话所在机器上 dsh 的 sdk profile 注册的全部供应商的模型，值是 `供应商/模型`，思考强度按模型，默认是 dsh 的默认模型。Iridium 在那台机器上起一次探测（`dsh --profile sdk --patch` 插一个 cordis 插件，读 `llm` 的 `listProviders`、`listModels`、`resolveModelInfo` 和 `agentDefaultModel`，打一行就退出），每台机器缓存 3 分钟，失败 1 分钟内不重试；启动时按值里的供应商初始化。以前存的不带供应商的值（`deepseek-flash`）按目录里有这个模型的供应商算。和 DSH Desktop 同步（用户，2026-11-08）：Desktop 设置里的供应商和默认模型存在它的 web profile，sdk 进程本来没有；远端有 dsh 插件时，会话和探测都多带 `--patch`，是插件按 Desktop 此刻生效的设置写的（`agent.prepare`），所以列出的和能跑的都同 Desktop。本机的 Desktop 装了插件、配对到本机的 Iridium 时也一样；否则用 sdk profile 自己的。agent 还没起来的会话（新建未发消息的、Desktop 开的只是列出来的）也有模型可选（用户，2026-11-09）：页面打开它时后台对那台机器探测一次，回来后状态里的 `models` 用那台机器的目录，`model` 是默认模型；在 Desktop 开的会话上选模型、强度或预设会先接管它（同发消息，但不启动 agent）；Desktop 拿着、这边没在跑的会话改模型、强度不接管，直接在 Desktop 里改（`held.set`，`docs/exec.md`“可选”），预设照旧接管（2026-11-09）|
| `PUT /v1/internal/sessions/{id}/mode` | `{mode}` | 审批模式，只收 `SessionState.modes` 里的。Claude：`set_permission_mode` |
| `PUT /v1/internal/sessions/{id}/effort` | `{effort}` | 思考强度，只收 `SessionState.efforts` 里的。Claude：先 `get_settings` 问 ultracode 开没开，再 `apply_flag_settings` 照原样带上 |
| `PUT /v1/internal/sessions/{id}/ultracode` | `{ultracode}` | 只 Claude（`GET /v1/internal/agents` 的 `ultracode`），别的 400。在跑时先 `get_settings`，要开而 `ultracodeAvailable` 不为真（CLI 不认 `get_settings` 也算）就 400；只发 `apply_flag_settings {ultracode}`，强度不变。没在跑时不查，只记下，下次拉起带上 |
| `PUT /v1/internal/sessions/{id}/attach/{place}` | `{content}` | 应用往会话页面上挂一段 Satori 元素（2026-11-11，`docs/exec.md`“挂元素”）：`content` 是字符串或按语言的 `{"zh": …, "en": …}`，空即去掉；`DELETE` 同路径也去掉。只限能用这个会话的应用（网页、本机、agent 的 `ir` 不行）。里面的按钮按下时，这个应用收到 `interaction/button`（`button.id` 是它自己的 id）。至多 4KB 一份、每个会话 4 段，存内存 |
| `GET /v1/internal/sessions/{id}/presets` | | `{presets: [{id, name?, description?}], default}`：会话所在那台 dsh 注册的 Agent 预设（用户，2026-11-08：不写死）。有 dsh 插件时（远端，或本机配对了的 Desktop）问 DSH Desktop 自己的注册表（`agent.presets`，含插件包和用户自建、改过的，按它的顺序，默认是它设置里选的，坏掉的不列）；没有插件的读 dsh 包里登记的补丁（`dsh-web-app` 的 `package.json` 里 `dsh.bundle.patch`）。按机器缓存 30 秒。内置的几个不带 `name`，网页用自己的文案；带 `name` 的照它写的显示。`GET /v1/internal/agents` 的 `presets` 只说这个 agent 有没有预设 |
| `PUT /v1/internal/sessions/{id}/preset` | `{preset}` | Agent 预设（只 dsh：上一行列出的，没有的 400），`""` 是 `default`。只在启动时装，也只在会话第一条消息前能改（同 dsh 自己的界面）：开始后改是 400，因为 dsh 会把新工具当成追加的延迟加载工具，PTC 那样全是延迟的，API 会拒绝。在跑时改就重启（空闲马上，否则等回合结束），重启时状态仍显示空闲。网页放在额度条的位置（dsh 没额度，用户定的，2026-11-06）。新会话用 `NewSession.preset`，`/clear` 照原样带上 |
| `GET /v1/internal/sessions/{id}/rules` | → `AllowRule[]` | “本会话一直允许”记下的规则 |
| `DELETE /v1/internal/sessions/{id}/rules` | → `{ok}` | 清掉 |

- 新会话的审批模式：`NewSession.mode`，没给就用配置 `modes.<agent>`，再没有就是 agent 的默认（`GET /v1/internal/agents` 的 `defaultMode`）。默认一律自动批准（用户定的，2026-11-05）：Claude `auto`；Codex `auto`（Codex 的 `approvalPolicy: never`，`never` 也认），更严的 `untrusted`、`on-request`、`granular` 照样能选；pi `auto`，不装审批扩展，选了 `ask` 才装。配置默认就写着这三项。
- 模式和强度按 agent 登记表（`GET /v1/internal/agents`）：Codex 从下一回合起生效（`turn/start` 带上）；pi 的模式只在启动时装（`modeSwitch: restart`），在跑时改了就重启它（空闲时马上，否则等这个回合结束；接着 `--session-id` 续上，队列照常发）。强度：Codex `effort`、pi `set_thinking_level`，启动时 pi 用 `--thinking`；模型自己带 `:级别` 的以模型的为准。
- 模型和强度怎么显示：
  - `models[]` 带 `resolved`（这个值实际跑的完整 id，Claude 的 `resolvedModel`）和 `efforts`（这个模型收的强度）；`noEffort` 是不收强度（Haiku 4.5）。两样都没有就是 agent 没说，用 `SessionState.efforts`。
  - `model` 是在用的模型，常是完整 id；`pick` 是设的值，空或 `default` 是 agent 的默认。默认时模型键显示具体的模型（`resolved` 对上的别名，再不行取默认项描述 ` · ` 前那段），不只写“默认”。
  - `effortNow`：Claude 握手后、换模型或强度后读 `get_settings` 的 `applied`（也拿到 `applied.model`，第一回合前就有）；Codex 取 `thread/start` 的 `reasoningEffort`；pi 取 `get_state` 的 `thinkingLevel`。没设过强度时显示它，但不记进库。
  - `ultracode`：`{on, active, available}`，`on` 是开没开（`ultracodeRequested`），`active` 是开了且生效，`available` 是这个模型能不能开（读到之前、进程没在跑时算能；CLI 不认 `get_settings` 算不能）。进程一结束，读到的就作废，下个进程再问。
  - `attached`：挂在会话页面上的元素（2026-11-11），每段 `{place, from, content}`：`content` 按语言（`""` 不分语言），`from` 是 `ir`（Claude 的 UltraCode）、`m:<机器>`（执行端的插件，如 OMD 的 Ultra）或 `a:<应用>`。`place` 现在网页只画 `effort`：第一个按钮是强度滑条右边的键，其余的在键按下时排在滑条下面一行。按钮按下：`POST /v1/internal/button {id: "att:<会话>:<from>:<按钮 id>"}`。见 `docs/exec.md`“挂元素”。
  - 面板里选了模型就收起；强度滑条和挂在 `effort` 的键（UltraCode 等）在面板底下，调了不收。
- 本会话一直允许：`answer` 带 `always: true`（或卡片选 `always`）。记下工具和 `Ask.pattern`（Claude 自己建议的规则，例如 `ls *`；没有时 shell 命令取第一个词加 ` *`，别的工具不带模式＝这个工具全部）。之后这个会话里匹配的审批自动允许，不通知、不出卡片。
- 临时切换至 manual：Claude 的 auto 模式分类器拦下的一步不经 Iridium，直接回给模型（结果开头是 `Permission for this action was denied by the Claude Code auto mode classifier`）。`release` 把运行中的进程切到 `manual`（不写进会话设置，进程重启就回到原模式），不给 Claude 发消息（原来以用户名义插一条“请原样再做一次”，它可能已换了做法，2026-11-08）。之后的调用照常问；第一个问的调用（或同一调用：工具相同，参数除 `description` 外相同）做完就切回原模式（2026-11-08），一个都没问就在当时进行中的回合（空闲时为下一回合）结束时切回，期间手动换了模式就不切回（2026-11-07）。切换记在 hold 目录的 `release.json`：守护进程重启接管仍在 manual 的同一进程时接着算，新进程就删掉。拦下的每一步都在会话状态的 `denials`（`Denial{toolUseId, tool, input, reason, at}`，只记在内存里）直到放行、`dismiss` 或给这个会话发下一条消息。只有回合停在被拦的一步上（它之后本回合没有做完别的调用）才算 `waiting`（`ir wait` 不算需要你）；之后做完了别的调用就当它绕过去了，状态照常（2026-11-08）。回合结束的推送和 Bot 回复带上“auto 拦了 N 步”。
- 匹配：`*` 是任意一段字；`前缀:*` 同 `前缀*`。shell 命令里有 `;`、`&&`、`||`、`|`、`` ` ``、`$(`、换行的，一律不按模式放行。

## 设置

`GET /v1/internal/settings` 给 `Settings`：`idleReap`（`"1h"`、`"15m"`，`"0"` 是不回收）、`crashResume`、`quotaResume`、`quotaHidden`（隐藏的额度条，`<agent>:<账号>:<窗口>:<模型>`）、`modes`（agent → 新会话的审批模式，没列的用 agent 自己的）、`push`（推送总开关，和 `PUT /v1/internal/push` 是同一个）、`enterInTurn`（回合在跑时网页回车做什么：`steer` 插话，默认；`queue` 排队）、`lang`（用户的语言 `zh`/`en`，网页设置里选的；`""` 是还没告诉过，网页发现是空的就报自己的；托管 agent 下次启动时写进系统提示）、`autoUpdate`（自动更新，默认开，见“更新”）。

- `PUT /v1/internal/settings` 带 `SettingsPatch`，只改给了的项；`modes` 里给 `""` 是去掉那个 agent 的。时长不对、agent 没有这个模式回 400。
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

网页和应用用同一份：`id`、`kind`（`permission`、`question`、`plan`）、`title`、`summary`（几行）、`options`（`id`、`text`、`primary`、`group`）。

- 提问的卡片在 `SessionState.asks[].card`，`id` 是 `ask:<会话>:<requestId>`。
  - 审批：`allow`、`deny`、`always`。计划：`approve`、`reject`。
  - 选择题：每个问题一组（`group` 是问题序号），选项 `id` 是 `<问题>.<选项>`；`multi` 列出可多选的组。

回答：`POST /v1/internal/button`（Satori 的，要带登录头），`{id: <卡片>, option}` 或 `{id, choices: [...]}`（每组一个，多选组可多个）；选择题自己写的答案放 `words`（按组，空串是没写）；拒绝时可带 `message` → `{}`。没有这张卡、已答过或撤回 422 `no_such_ask`。也可以一个一个按钮按（`id` 是 `<卡片>#<选项>`，见 `docs/satori.md`）。

## 推送

Web Push，总开关在配置 `push`，默认关。VAPID 密钥存在状态目录 `vapid.json`（0600）。下面的请求只给网页的登录和主机：应用的 TOKEN 和 agent 的 `ir` 回 403。删一个登录时删掉它名下的订阅。

| 请求 | 正文 → 回答 | 说明 |
|---|---|---|
| `GET /v1/internal/web/push` | → `PushState` | 总开关、VAPID 公钥（订阅时的 `applicationServerKey`）、默认事件 `defaults`、规则 `rules` |
| `PUT /v1/internal/web/push` | `{enabled}` | 总开关，写进配置文件 |
| `PUT /v1/internal/web/push/defaults` | `PushEvents` → `PushState` | 没有规则管的地方推哪些：`needsYou`、`error`、`turnEnd`（任务完成）默认都开。你发的 `/compact` 压缩成功算一次 `turnEnd`；agent 自己在回合中途的自动压缩不推（2026-11-08） |
| `PUT /v1/internal/web/push/rules` | `PushRule` → `PushState` | 一个工作区、文件夹或会话的规则；`mode: inherit`（或空）删掉它 |
| `POST /v1/internal/web/push/subscriptions` | `PushSubscription` | 浏览器 `subscription.toJSON()`，记在这个登录名下；只收 https |
| `DELETE /v1/internal/web/push/subscriptions` | `{endpoint}` | |
| `POST /v1/internal/web/push/test` | → `{sent}` | 给自己的订阅发一条测试，不看总开关 |

`PushRule{scope, target, mode, events?}`：`scope` 是 `workspace`（`target` 是工作区 ID，`"0"` 是没归类的）、`folder`（会话目录的绝对路径，只管这一个目录）、`session`（会话 ID）；`mode` 是 `on` 或 `off`；`events` 只跟 `on`。一个会话的事件：会话、文件夹、工作区里最近的那条规则说开还是关（都没有就开）；开的话，事件取最近一条带 `events` 的 `on` 规则的，没有就用 `defaults`。

推送的内容（service worker 收到的 JSON）：`PushMessage{kind, session, title, body, tag}`，`kind` 是 `needs_you`、`error`、`turn_end`、`share`、`test`；`tag` 是会话 ID。推送服务回 404/410 的订阅删掉。

## 分享（`ir share`）

`POST /v1/internal/share`，`ShareRequest{what, session?, at?, place?, ref?, path?, note?, to}` → `ShareReply{app, login, name, pushed}`。`to` 是 `应用/登录`（网页登录是 `ir_web/登录`）。`what`：

- `session`：对方要能用这个会话（范围加 agent）。
- `reply`：会话的一块（`at`），对方要看得到会话。
- `place`：`应用名:路径`，对方的范围要有它。
- `file`：会话里的文件，`ref`，或本机会话的 `path`（随之附进会话）；对方要看得到会话，范围有这个文件。

分享的人自己也要看得到。Iridium 补上 `machine`、`title`、`from`、`fromName`、`fromSession`、`sharedAt`。送达：网页登录收到一条 `share` 需要，推送开着时再推一条（`kind: share`）；别的应用的登录收到 Satori 事件 `internal`，`_type: ir.share`，`_data: {kind: share, share}`，只发给它。错误：422 `no_such_app`、`no_such_login`、`not_usable`；400 缺东西或分享给自己。

`GET /v1/internal/share/targets?what=&session=&…` → `ShareTargets{apps: [{id, name, logins}]}`：能用它的应用和登录（不含自己），网页“分享到 ▸”用。

## 应用的接口

应用用 **Satori with ir**：见 `docs/satori.md`。官方应用不由 Iridium 安装（见“应用和登录”）。

## 应用和登录

主机（ID `local`，显示“主机”）不在表里，不可被管理。别的都是应用（表 `apps`，`type` 只用来显示），应用下面是登录（表 `logins`，主键 `(app, id)`，`approved_at` 为空即待批）。网页（`ir_web`）是内置应用：每个浏览器（一套 passkey）是它的一个登录，只在设了公网地址或已有登录时列出。每个应用、登录有设备 `device`（`computer`、`laptop`、`phone`、`none`）。

别的应用说话时用头 `Satori-User-ID`（或 `X-Iridium-Login`）指明登录；新的登录第一次说话回 403 `login_pending`，产生一条 `login_join` 需要；每个应用最多 20 个待批。

| 请求 | 正文 → 回答 | 说明 |
|---|---|---|
| `GET /v1/internal/me` | → `Me` | 调用者：`app`、`login`、`host`、`web`、`rules`、`public` |
| `PUT /v1/internal/me/profile`、`PUT /v1/internal/apps/{id}/profile` | 资料 → `EndpointInfo` | 改名字、头像（见 `docs/gate.md`）。名字不合法 422 `bad_name`，带 `nameError`（`empty`、`too_long`、`invalid`、`reserved`、`letter`、`taken`） |
| `PUT /v1/internal/me/device`、`PUT /v1/internal/apps/{id}/device` | `SetDevice` | 设备 |
| `GET /v1/internal/apps` | → `Apps` | 应用（主机在最前、网页、其余）、登录（带 `perms`）、未用的邀请。没有秘密。谁都能调：不能管理的只拿公开部分（名字、类型、头像、在线、电脑，已批准的登录；不带 `rules`、`perms`、用量、待批的登录和邀请），能管理的 `full: true`。带 Agent 的应用（执行端）带 `computer`：系统、家目录、`sep`、报的 agent（关掉的 `off: true`）、`shell`、`online`、`latency`、`seenAt`。取代 `GET /v1/internal/machines` 和 hub 键 `machines`；电脑连上、断开、延迟变了都推 `terminals` |
| `PUT /v1/internal/apps/{id}/agents` | `AppAgents` → `AppAgents` | `{off: [...]}`：它带的 agent 里关掉哪些，其余开（新报的默认开）。关掉的不能在那里启动（新建、接管、重启都拒，403），网页新会话里不列；在跑的不停。能批的人。`ir apps agents <应用> [agent=on\|off …]` |
| `DELETE /v1/internal/apps/{id}` | → `{ok}` | 删应用（连同 TOKEN、连接）或网页的登录、未用的邀请。只有人（主机、网页登录），agent 不行 |
| `DELETE /v1/internal/apps/{id}/logins/{login}` | → `{ok}` | 删一个登录；别的应用的登录下次说话重新申请 |
| `PUT /v1/internal/apps/{id}/perms` | `Perms` → `Perms` | 应用能用的，能批的人改 |
| `PUT /v1/internal/apps/{id}/logins/{login}/perms` | `Perms` → `Perms` | 登录能用的（网页的登录 `id` 写 `ir_web`），在应用的之内起作用 |
| `PUT /v1/internal/me/perms` | `Perms` | 应用申请改权限：一条 `app_change` 需要 |
| `POST /v1/internal/invites` | `NewInvite` → `Invite` | 网页的一次性邀请（10 分钟），先要公网地址。`claude: true` 加入的登录带 Claude。agent 调的回 202 `Pending` |
| `GET /v1/internal/usage?days=N&terminal=<ID、名字或 me>` | → `Usage` | 按日期、应用或登录、agent、模型合计的用量（`by=hour`、`by=weekhour` 同前）。看别人的要管理权 |

## 配对

配对是应用唯一的来路（`ir join`、`ir pair`、dsh 插件、QQ 桥都走它）。

| 请求 | 正文 → 回答 | 说明 |
|---|---|---|
| `GET /v1/internal/pairing`、`PUT /v1/internal/pairing` | `PairWindow` | 配对窗口：`{open, until}`；`PUT {open, minutes?}` 开（默认 10 分钟，最多 60）或关。能批的人。网页应用管理右上角的“开放配对”开，再点一下关。hub 键 `pairing` |
| `POST /v1/internal/pair` | `PairRequest` → `Pairing` | 不用登录。窗口关着 403 `pair_closed`（本机 socket 来的不看窗口，`ir pair` 不给地址时）；带 `login {id, name?}` 的，批准时一并建好这个登录、已批准（已有的不动），ID 不合法 422 `bad_login`；每来源（IP，IPv6 按 /64）最多 4 个待批、总共 20 个，多了 403 `too_many_pending`；名字不合法 422 `bad_name`。回配对码 `code` 和 `request` |
| `GET /v1/internal/pair/{request}` | → `PairState` | `pending`、`allowed`（带 `token`，只给一次）、`denied`、`expired`；不认识的 422 `no_such_request` |

申请带 `wants`（想要的权限），批准时只能少给（422 `too_wide`）。没带 `wants`、报了 agent 和 `home` 的（`ir join`）默认范围是自己的家目录 `应用名:家目录`（决定 17），批准时改名的跟着改；其余默认全部。`machineId` 和主机的一样就去掉它报的 agent。批准的答复可带 `agentsOff`：一开始就关掉的 agent。名字已有应用时是 `app_update`：允许即给那个应用换新 TOKEN，旧的作废、连接断开，用量和权限照旧；答复带 `name` 则作为新应用、用这个名字接入。

## 权限（`Perms`）

`{agents, scope}`，`["*"]` 为全部。`scope` 是位置 `应用名:路径`（主机写 `主机:` 或 `host:`）。

- 能用的 = 登录的 ∩ 应用的；主机全部。0.2 之前配对的应用是全部。
- 范围外的会话不存在：列表里没有，hub 的 `list`、`session:`、`context:` 过滤掉，按 ID 访问回“没有这个会话”。
- 范围内、agent 不在能用之列的会话只能看：开、发、答、停、改名、归档、改设置一律 403 `no_permission`。所有 `/v1/internal/sessions/{id}/…` 都在路由前统一查（GET 查范围，其余加查 agent）。
- Claude 另外要“本人的”（`rules.claudeOwner`：`{by, at}`），只在建应用、登录时由服务端写（配对时允许为本人的、`ir invite --claude`）。

## 需要（`Need`）

等一个人决定的事，一个队列。申请（`app_join`、`app_update`、`app_change`、`login_join`、`invite`、`secret_set`、`secret_use`）内存里存 10 分钟，主机和网页登录看得到。另有：

- `ask`：会话开着的提问、审批、计划，`id` 和 `card.id` 一样（`ask:<会话>:<请求>`），带 `session`、`card`、`origin`（发起这个回合的应用或登录）。只给能用这个会话的（范围加 agent，同 `using`）。用 `POST /v1/internal/button` 回答，不走 `POST /v1/internal/needs/{id}`。
- `share`：分享给这个网页登录的东西（`share`），只给它自己，存 24 小时；`DELETE /v1/internal/needs/{id}` 收起。

网页在应用管理和侧栏“需要你”里画成卡片（会话里的提问仍在会话里画，不重复）；`ir needs`、`ir allow <id>`、`ir deny <id>`。

| 请求 | 正文 → 回答 | 说明 |
|---|---|---|
| `GET /v1/internal/needs` | → `Needs` | 按调用方过滤（见上）；agent 的 `ir` 403。hub 键 `needs` 每条流也按它的调用方过滤 |
| `DELETE /v1/internal/needs/{id}` | → `{ok}` | 网页登录收起分享给自己的一条 |
| `POST /v1/internal/needs/{id}` | `NeedAnswer` → `NeedReply` | 允许或拒绝。只有能批的：主机上的人，或有 Claude、agent 和范围都是全部的网页登录；agent 的 `ir` 一律 403。`perms`：给的权限（不超过申请的）；`name`：配对时改名；`owner`：本人的（得 Claude）；`value`、`forSession`：凭据用 |
| `GET /v1/internal/needs/{id}?wait=<秒>` | → `NeedState` | 结果，从不带秘密 |

agent 调 `ir invite`、`ir secret set` 时秘密不进它的对话：变成一条需要，允许后链接只给点允许的那一处，agent 只拿到结果。

## 凭据

见 `docs/plan.md`“凭据”。表 `secrets`（名字、说明、范围、`always`（每次确认）、密文、谁存的、时间）、`secret_uses`（凭据、会话、终端、命令开头、时间）。值用 AES-256-GCM 加密，密钥 `<状态目录>/secrets.key`（0600，第一次用时生成）。名字：大写字母、数字、`_`，字母开头，≤64。

| 请求 | 正文 → 回答 | 说明 |
|---|---|---|
| `GET /v1/internal/secrets` | → `Secrets` | 名字、说明、范围、谁存的、用量和最近 50 次使用。从不带值。主机和网页登录 |
| `PUT /v1/internal/secrets/{name}` | `SetSecret` → `SecretInfo` | 存或换（值、说明、范围、`always`）。不带值：已有的只改说明和范围。主机和网页登录；agent 调的回 202 `Pending`（需要卡片带密码框，值由点“存”的那一处交上来，见下） |
| `DELETE /v1/internal/secrets/{name}` | → `{ok}` | 要管理权；agent 调的也行 |
| `POST /v1/internal/secrets/use` | `UseSecrets` → `SecretValues` | 只给 `ir run`，只走 unix socket：会话、名字、命令开头。查范围；要确认时回 202 `Pending`，`ir run` 等到同意后带 `confirm`（确认的 ID）再取，一个确认只能用一次。值只交给 `ir` 进程，它不打印 |

- 需要里的 `secret_set`（`Need.note`、`replace`；允许时 `NeedAnswer.value` 带值，服务端存好，谁存的记点“存”的面板；回答和 `ConfirmState` 不带值）、`secret_use`（`Need.secrets`、`command`；允许 / 本会话一直允许（`NeedAnswer.forSession`）/ 拒绝，记在内存里，会话的进程停了就忘；`owner: true`，只有带 Claude 的面板或本机 `ir` 能允许；有 `always` 的凭据不给“本会话一直允许”）。
- 要不要确认：本机由人跑的 `ir run` 直接用；agent 的，会话由本机、带 Claude 的终端开的（或不经 Iridium 开的）直接用，别的终端开的要确认；`always` 的每次都确认。范围不对直接 403，不弹确认。
- 对话记录出接口前（会话列表、会话状态、消息页、整段原文、携带状态、Satori 的消息和事件、推送；`ir api message.list`、`ir wait` 走的就是这些），已知的值（原样、URL 编码、base64）换成 `[secret:名]`；短于 6 个字符的不换（误伤太多）。
- `ir run` 自己起子进程（同目录、同环境，加上凭据），stdout/stderr 照转，按最长值的长度留尾巴再打码；退出码照子进程。

## 公网入口

| 请求 | 正文 → 回答 | 说明 |
|---|---|---|
| `GET /v1/internal/public` | → `PublicState` | `public.origin`，有没有面板注册过 Passkey |
| `PUT /v1/internal/public` | `SetPublic` → `PublicState` | 设 `public.origin`（`""` 关掉）：写进配置文件，门马上换成新的，旧门放进来的连接全断开。要面板权 |

`ir public` 在终端里跑（不是终端、或者是 agent 调的，就拒绝），一步步做到能用为止：

1. 找 `cloudflared`（`~/.local/bin`，再 `PATH`）；没有就从 GitHub 最新版下载到 `~/.local/bin`，sha256 对上 release 的 digest（或说明里列的）、能跑才放进去。
2. `HOME=<状态目录>/cloudflared cloudflared tunnel login`：打印登录链接，你在 Cloudflare 上选域名。登录凭证在 `<状态目录>/cloudflared/.cloudflared/cert.pem`，不碰 `~/.cloudflared`。没成就说缺一个托管在 Cloudflare 的域名。
3. 用凭证问 Cloudflare 这个站点的域名（问不到就让你填），问子域名（默认 `ir`）。
4. 建隧道 `iridium-<机器名>`（已有就接着用、重新取凭证），`route dns`；已有记录时问要不要覆盖（`--overwrite-dns`）。写 `config.yml`：这个主机名 → `http://127.0.0.1:<端口>`，其余 404，`ingress validate` 过了才用。
5. 写用户级单元 `iridium-cloudflared.service`（`cloudflared tunnel --no-autoupdate --config … run`），`daemon-reload`、`enable`、`restart`。机器上的 `cloudflared` 服务和 `/etc/cloudflared` 不碰。
6. `PUT /v1/internal/public`，再从外网（先问 1.1.1.1 解析）取 `https://<主机名>/_ir/health`，等到通（3 分钟）。
7. 生成第一个面板邀请，打印链接。

`ir public off`：停掉并删掉单元，清 `public.origin`，删隧道和它的文件（登录凭证留着，下次不用再登录）。DNS 记录 `cloudflared` 删不了，提示去控制台删。

## 更新

| 请求 | 正文 → 回答 | 说明 |
|---|---|---|
| `GET /v1/internal/update` | → `UpdateState` | 现在的版本、最新版本和说明、`size`（下载多大）、`available`、`auto`（查到新版就在后台下好：Release 版是，源码编译的不是）、`ready`（已下好）、`phase`（`idle`、`checking`、`downloading`（`got` 是下了多少）、`restarting`、`failed`）。携带状态 `update` 是同一份 |
| `POST /v1/internal/update/check` | → `UpdateState` | 马上查一次；`auto` 时有新版就在后台下好 |
| `POST /v1/internal/update` | `ApplyUpdate` → `UpdateState` | `ir update`、设置里的“更新”：用下好的，没有就现在下（核对 sha256、跑一次 `iridium version` 看版本对不对），原子替换二进制（换下的留在旁边，叫 `iridium.prev`）、重启服务。新版要升库时，先把库复制成 `iridium.db.v<旧版本号>.bak`。回滚：放回这两个文件。没有更新的（`force` 除外）回 409 |

更新源是配置 `update.url`：不填是 `github:kumoSleeping/Iridium`（GitHub Releases 的最新版），`off` 不查；`update.every` 默认一天查一次。Release 版（版本号是干净的 `vX.Y.Z`）查到新版就下到二进制旁边的 `.iridium-update-*`，点“更新”只换文件、重启；源码编译的（`dev`、`vX.Y.Z-dev.N+g…`）不预先下载。设置里“自动更新”（`Settings.autoUpdate`，存为配置 `update.manual` 取反，默认开）关掉后不定时查、不预先下载，打开设置也不查（`UpdateState.manual`）；“检查更新”照常查、下载、替换（2026-11-10）。守护进程启动时清掉上次剩下的。

GitHub 源：最新 Release 的 tag 是版本，正文（`<!-- iridium:assets -->` 之前，后面的大小表只给 Release 页看）是说明，资源 `iridium-<GOOS>-<GOARCH>.tar.gz`（里面的 `iridium`）或 `iridium-<GOOS>-<GOARCH>`；sha256 用资源自带的 `digest`，没有就看同一 Release 里的 `SHA256SUMS`。别的地址返回清单：

```json
{"version": "1.2.0", "notes": "…", "url": "https://…/iridium-linux-amd64", "sha256": "<hex>",
 "platforms": {"linux-arm64": {"url": "…", "sha256": "…"}}}
```

`platforms` 里有本机的（`GOOS-GOARCH`）就用它；`url` 以 `.tar.gz` 结尾的同样解包。版本是带点的数字（可有 `v`、`-rc.1`）；`dev` 版不提示更新，`ir update --force` 照样装。重启：在 systemd 单元里就 `systemctl --user restart --no-block <单元>`，否则原地重新执行。托管进程在自己的单元里，不受影响。

设置里（有面板权的才有）：“检查更新”一个键，按下即 `POST /v1/internal/update`：查到新版就下载、替换、重启，中间不再确认；旁边写状态（检查中、下载中 37%、重启中、已是最新 · 时间、更新失败）。打开设置时上次查过超过一小时就查一次（只查）。有新版时最上面一张卡：版本、大小、说明、下载进度、“更新”键。在这个页面上按了更新，新版一上线页面就刷新（有没发出的内容除外）；否则连接状态那里换成“↻ 刷新”，点了刷新。

## 部署

- `iridium install [-config <文件>] [-unit <名>] [-listen <地址>] [-env 名=值]... [-now]`：写用户级单元 `~/.config/systemd/user/iridium.service`（`iridium serve -config ~/.config/iridium/config.json`，带装的时候的 `PATH` 和 `-env` 给的变量；`$XDG_CONFIG_HOME` 设了就写在它下面），没有配置就写一份默认的（`127.0.0.1:7810`，`-listen` 换地址），把自己复制到 `~/.local/opt/iridium/iridium`（`-prefix`）、链 `~/.local/bin/ir`（`-bin`），建 `~/.local/state/iridium`，`daemon-reload`。不加 `-now` 不启动。没开 linger 时提醒：退出登录后服务和托管的 agent 都会停（`sudo loginctl enable-linger <用户>`）。
- `iridium push [-i <密钥>] [-listen <地址>] <用户@主机>`：经 ssh 把这个二进制发到另一个账号或机器（先 `uname -sm` 核对平台），放到那边的 `~/.local/opt/iridium/iridium`，跑一次 `version`；那边 `iridium.service` 已经装了就重启，没有就 `iridium install -now`（`-listen` 给第一次装的地址）。托管进程不受影响。
- `iridium node new <账号> [-listen <地址>]`：在这台机器上给一个账号装一个 Iridium，你在终端里跑：要 root 的几步（没有就 `useradd -m`、`loginctl enable-linger`、把 `~/.ssh/iridium_<账号>_ed25519` 放进那边的 `authorized_keys`）合成一次，是 root 就直接跑，在 sudo/wheel/admin 组里用 sudo，否则用 `su`（问 root 的密码）；再 `push` 过去（地址默认 127.0.0.1 上 7811 起第一个空着的端口）。不替你装 agent：那边的 agent 登录和 `ir public` 在那个账号里做。
- `scripts/deploy.sh`：`web/package.json` 在就先 `npm ci && npm run build`（`NO_WEB=1` 用现成的），`internal/webui/dist` 里没有网页就停；`go build`（版本取 `git describe`）装到 `~/.local/opt/iridium/iridium`（`PREFIX`），`~/.local/bin/ir`（`BIN`）链过去，再跑 `iridium install`（`CONFIG`、`UNIT`、`UNIT_ENV` 照传）；服务已在跑就重启它。`IRIDIUM_NO_SYSTEMCTL=1` 不碰 systemd。TCP 地址上就是网页本身（`/v1/internal/`、`/_ir/` 之外都是网页，前面同样是门）。
- `scripts/test-deploy.sh up|again|down [目录]`：不碰真的服务，把 `deploy.sh` 真跑一遍：临时的安装位置、状态、家目录和 Claude 目录，假 claude（`internal/testagent`），单元 `iridium-test-deploy-<n>` 写在 `$XDG_RUNTIME_DIR/systemd/user`（不进 `~/.config`），随机端口；托管进程的单元是 `iridium-test-hold-<n>-*`。`web/scripts/seed.mjs` 往里放会话，`e2e.mjs`、`shoot.mjs` 对着它跑。
