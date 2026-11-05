# Iridium 接口

一套接口，两个入口：

- HTTP：`127.0.0.1:7810`（配置 `listen`），给网页，前面是 Passkey 门（`docs/gate.md`）。没配 `public.origin` 时只认本机的主机名（`localhost`、回环地址和配置 `hosts`），非 GET 请求带了别的 `Origin` 就拒绝，请求都算内置终端“本机”（带插件 TOKEN 的算那个插件）。配了以后每个请求都要面板登录或 TOKEN。
- unix socket：`$XDG_RUNTIME_DIR/iridium/ir.sock`（0600），给本机 `ir`，不经过门，算“本机”。`ir` 认出是 agent 在调（环境里有 `CLAUDECODE`、`CODEX_THREAD_ID`、`IRIDIUM_SESSION`，或者没有终端）时带头 `X-Iridium-Agent: 1`，这时不回任何秘密（见“确认”）。`ir` 还带 `X-Iridium-Session`（它所在的会话：`IRIDIUM_SESSION` 或 agent 自己的会话变量）；那个会话是别的终端经 Iridium 开的，这个请求就算那个终端的：用它的权限表，记它的用量和来源（agent 在插件的会话里调 `ir`，等于插件自己做）。

来源：每条交给 agent 的消息开头是 `[终端名] `（`/` 开头的命令除外），名字取发送时终端的名字。库里记下这条输入是哪个终端（ID）发的、用的什么名字；读记录时服务端按 Iridium 自己写进去的输入认（Claude：发消息时给的 uuid；没有 ID 的 agent：`store.SourceByText` 比对输入记录），去掉前缀，消息块带 `from`（名字）和 `fromTerminal`（终端 ID，取头像用）。用量行按终端 ID 记，改名不丢。agent 以“本机”调 `ir send`、`ir new`（带 `X-Iridium-Agent` 和它所在的会话）时前缀是 `[ir:<会话 ID>] `，`from` 也是它，页面画成那个会话的来信；终端仍是本机。

类型都在 `internal/api`。`go generate ./internal/api` 用 tygo 生成 `web/src/api/types.gen.ts`，前端只用它（生成的文件不进库）。

出错时状态码非 2xx，正文 `{"error": "…"}`：400 请求不对，403 权限表不许，404 没有这个会话或位置（看不到的会话也是 404），409 agent 没在运行或状态不对，429 今天的用量到上限了，504 agent 没按时回。名字不行时 409 另带 `nameError`（`empty`、`too_long`、`invalid`、`reserved`、`taken`）。

## 请求

| 请求 | 正文 → 回答 | 说明 |
|---|---|---|
| `GET /api/sessions` | → `Session[]` | 会话列表：托管的加上磁盘上发现的（见“工作区和目录”）。置顶在前，再按最近 |
| `POST /api/sessions` | `NewSession` → `{id}` | 在 `cwd` 开一个托管会话，带 `text` 就作为第一条消息。`agent`：`claude`、`codex`、`pi`；`model` 可带强度（Codex `gpt-6-luna:low`，pi `provider/id:off`）；`mode` 取 `GET /api/agents` 列的模式。带 `workspace` 就放进那个工作区 |
| `GET /api/sessions/{id}` | → `SessionState` | 打开的会话的状态，和事件流里的同一份 |
| `GET /api/sessions/{id}/messages` | → `MessagePage` | 不带参数：最新一页。`before=<start>`：往回一页。`after=<end>`：之后新写的 |
| `GET /api/sessions/{id}/block?at=<at>` | → `{text}` | 截短的块（工具参数、结果、思考、系统行）的全文 |
| `POST /api/sessions/{id}/send` | `{text, steer?}` → `{input, queued?}` | 发消息。agent 没在跑就先拉起（有记录就 `--resume`）。回合进行中发的进待发队列（`queued`），`steer` 则并进当前回合。见“待发队列”。在别处运行的回 409 |
| `POST /api/sessions/{id}/interrupt` | → `{ok}` | 停掉当前回合（Esc），agent 留着。队列暂停 |
| `POST /api/sessions/{id}/stop` | → `{ok}` | 停掉 agent 进程（关 stdin，写完记录自己退出）。会话还在，下一条消息再拉起。队列暂停 |
| `POST /api/sessions/{id}/answer` | `Answer` → `{ok}` | 回答提问：审批 `allow`（不许时可带 `message`；`always` 见“会话设置”）；选择题 `answers`（问题原文 → 选项 `label`）；计划确认 `allow` |
| `GET /api/events?sub=…` | SSE | 见下。不带 `sub`：插件的事件流，见“插件” |
| `GET /api/agents` | → `AgentInfo[]` | 能开的 agent：审批模式（`modes`、`defaultMode`、`modeSwitch`：now / next turn / restart）、有没有上下文分项、有没有额度窗口 |
| `GET /api/health` | → `{ok}` | |
| `GET /api/settings` | → `Settings` | 设置页改的配置项：空闲回收、崩溃续跑、额度续跑、隐藏的额度条、各 agent 新会话的审批模式、推送总开关。管理权 |
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
| `session:<id>` | `SessionState`：状态、提问（带 `card`）、后台任务、模型、审批模式、思考强度、可选的模型/模式/强度、待发队列、上下文总数、等额度到几点、`tail`（块带 `files`）、`end`、`proc`、出错原因、`commands`（agent 自己的命令，给输入框的命令面板） |
| `context:<id>` | `Context`：上下文用了多少、分项。回合结束和压缩后更新；存在托管目录，没进程时也有 |
| `quota` | `Quota`：按 agent + 账号（Claude：邮箱和套餐）的额度窗口、读到的时间、`live` |
| `confirms` | `Confirms`：等面板确认的事（只有面板能订） |
| `update` | `UpdateState`：有没有新版本、更新到哪一步 |

不认识的键、不给这个终端的键（`confirms` 只给面板）不让整条流失败：开流时先为每个发一条 `state`，`{key, value: null, ignored: "unknown" | "forbidden"}`，其余照常。

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

- 打开会话：取最新一页，记下 `end`。`session:<id>` 的 `end` 比自己的大：
  - `tail.blocks` 不是 `null`，且自己的 `end` 在 `[tail.start, tail.end]` 里：直接用。位置在自己 `end` 之后的追加；`at` 已有的换掉（工具的结果来了）。`end` 记成 `tail.end`。
  - 否则（块大、块多、中间丢过状态）`after=<自己的 end>` 取新块。
  - `tail` 至少往回盖 1 秒；只装没截短的块，JSON 不超过 16KB。
- 草稿：同一 `proc`、同一 `n` 的 `text` 接起来；`n` 变了，上一段封口（记录里马上会有这段话）；`proc` 变了，丢掉所有草稿。记录里来了 agent 的话，就换掉最早封口的那段。重连后清掉草稿，照 `end` 取新块。

## Iridium 重启后

托管进程和 agent 照跑；新的 Iridium 接上每个还在跑的托管进程：

- 托管目录 `seen.json` 记着上一个 Iridium 读 `out.log` 读到哪（每秒最多写一次，退出时写一次）。那之前的输出重放，只为恢复驱动的状态；之后的（停机期间回合结束、用量、提问）照新来的处理。没有这个文件的（旧版）当全读过。
- 再发一次握手。Claude 回 `initialize` 时带 `pending_permission_requests`（还在等答复的提问，原样的 `control_request`），挂着的提问以它为准：没答的回来，带卡片，照常能答；答过的不再出现。
- 上下文（托管目录 `context.json`）和额度（状态目录 `quota.json`）是上次读到的值和读的时间（`at`）；额度的 `live` 要等这个 agent 和账号又有进程在跑。
- 托管进程在停机期间已经退出的：不补读它的输出，按它的退出处理。

## 状态灯（`Status`）

`idle` 无灯，`starting` 启动中，`running` 运行中，`compacting` 压缩中，`waiting` 需要你（有提问），`error` 上次进程出错退出（原因在 `SessionState.error`），`elsewhere` 在别处运行（只读）。`live` 说 agent 进程在不在跑。

## 工作区和目录（自动发现）

会话列表以磁盘为准（`internal/discover`），启动时建索引放内存，inotify 加定时扫（目录每分钟、`/proc` 每 3 秒）跟上：

- 来源：Claude `~/.claude/projects/<目录>/<id>.jsonl`（`<id>/subagents/` 和 `agent-*.jsonl` 是 Agent 工具的子任务，不列）；Codex `$CODEX_HOME/sessions/YYYY/MM/DD/rollout-*.jsonl`（`source.subagent` 的不列，标题用 `session_index.jsonl` 的线程名）；pi `~/.pi/agent/sessions/*/*.jsonl`（标题用最后一个 `session_info` 名字）；dsh `~/.dsh/sessions/--<键>--/<id>/session.v<N>.jsonl.zstd`（现在只看路径和时间，目录由键反推）。标题、最近一句话只读文件开头 8MB 和末尾 4MB。
- 在跑的：`~/.claude/sessions/<pid>.json`（核对 `procStart`），`/proc` 里本人的 codex、pi、dsh 进程（打开的记录文件或锁、命令行；pi 用工作目录里进程起来后最新写的那份）。环境变量只读本人进程的，只取下面几个，不记日志。
- 拉起者：子进程环境里依次看 `IRIDIUM_SESSION`、`CLAUDE_CODE_SESSION_ID`、`CODEX_THREAD_ID`、`CODEX_SESSION_ID`、`DSH_SESSION_ID`、`PI_SESSION_ID`；都没有就往上找第一个只跑一个会话的 agent 进程。`IRIDIUM_SESSION` 等于自己的 ID 是 Iridium 托管的，不算在别处。
- 显示标记（用户定的，2026-11-05）：会话只有带“显示”标记才在列表里正常显示；标记在库里（`shown` 表），按 agent + agent 自己的会话 ID 记（托管的 Codex 在知道线程 ID 前用 Iridium 的 ID，两个哪个有标记都算）。从不写 agent 的文件，不删记录。
  - 设上：Iridium 开的、接管的托管会话；托管会话的 agent 开始回合或收到消息；发现看到“活动”：没见过的记录且最后修改晚于 Iridium 开始看的时间（第一次启动时记在库里，重启不变），或者记录比上次看时长了。
  - 没标记的就是归档：`Session.archived` 为 `true`，页面放在底部折叠的“归档”里，能搜。第一次启动时磁盘上已有、之后没动过的都在这里。
  - 归档（`PATCH archived: true`）：去掉标记，记下记录当时的长度；之后长过这个长度（有了新活动）就自动回来。托管的另外像空闲回收那样停掉 agent；它退出时写的不算活动（退出后重记长度）。恢复（`archived: false`）：设上标记。没有按时间自动归档。
  - 旧版的归档标记迁移成“没标记”。
- 首次归类一次（库里记下，以后不再动），在会话第一次显示时（第一次活动）做，不给归档里的做：拉起者的工作区 → 同目录或最近上层目录已有会话所在的工作区，工作区自己的目录也算 → 未归类（`workspace` 为 0）。手动移过的永远不再自动动。新建工作区不收已有的会话：之后在那个目录里显示的按这条规则进它。
- 状态：`hosted` 托管；`elsewhere` 在别处运行，只读（能看状态和消息页，发消息回 409）；其余是磁盘上空闲的。别处退出后，发消息就接成托管会话（`--resume`），前提是没有别的进程在用（Claude：`sessions/` 里没有活着的同 ID 进程；dsh：`session.lock` 能拿到 flock；Codex：写锁）。现在只能接 Claude 的。
- 列表变了（新会话、记录长了、进程起停、改了覆盖层）攒 200ms 发一次 `list` 整份，变了的会话另发 `session:<id>`。

| 请求 | 正文 → 回答 | 说明 |
|---|---|---|
| `GET /api/workspaces` | → `Workspace[]` | 和事件流 `workspaces` 同一份 |
| `POST /api/workspaces` | `NewWorkspace` → `Workspace` | 用一个文件夹建（要在选择器能去的范围里）；`name` 不给就用文件夹名。同一文件夹已有工作区回 409 |
| `PATCH /api/workspaces/{id}` | `{name}` → `{ok}` | 改名 |
| `DELETE /api/workspaces/{id}` | → `{ok}` | 删掉；里面的会话变成未归类 |
| `PATCH /api/sessions/{id}` | `SessionPatch` → `{ok}` | 只改覆盖层，不碰 agent 的文件：`name`（空串去掉）、`pinned`、`archived`（归档或恢复，见上面的显示标记）、`workspace`（移动，0 为未归类；移过就不再自动归类） |
| `GET /api/folders?path=` | → `Folders` | 选择器：只列文件夹，不列隐藏的，按名字排；`path` 为空或 `~` 是家目录。`parent` 为空表示到顶了 |
| `POST /api/folders` | `NewFolder` → `{path}` | 在 `path` 里新建文件夹 `name`（不能以 `.` 开头、不能带 `/`）。已有回 409 |

- 选择器默认只能在家目录里（配置 `folderRoots` 可改），先解析符号链接再比；`~/.ssh`、`~/.gnupg`、`~/.claude`、`~/.codex`、`~/.pi`、`~/.dsh` 和 Iridium 的状态、配置目录一律不给（403）。不存在 404。

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
| `GET /api/files?path=<目录>` | → `DirListing` | 列一层（`ir files`） |
| `POST /api/files/copy` | `{from, to}` → NDJSON | 拷文件（`ir cp`）：每 250ms 一行 `CopyProgress`，最后一行带 `path`、`sha256` 或 `error`。由 Iridium 拷，`ir` 断了也接着拷；同样的请求再来就接上 |

规则：

- **引用**：`ref` 只对这个会话有效，同一路径每次一样（状态目录 `files.key` 做的 HMAC），看不出路径。只有会话记录里出现过的路径、它的上传和附件才有引用；服务器取不到就从上次读到的地方往后读记录。
- **认文件**（`Block.files`）：你的话和 agent 的话里的 `![](路径)`、`[名](路径)`（相对会话目录；可带 `#行号`、`file://`；网址不算）、行内代码里的绝对路径或 `~/` 路径（可带 `:行:列`），以及 `Write` 调用的 `file_path`。围栏代码块里的不算。只认存在的普通文件。
- **类型**：先看扩展名，没有再嗅前 512 字节。图片、音视频、PDF、纯文本内联显示，其余下载。所有文件都带 `Content-Security-Policy: …; sandbox` 和 `nosniff`，SVG、HTML 里的脚本不会跑。
- **上传位置**：会话目录的 `.iridium/uploads/`（建目录时写一个只有 `*` 的 `.gitignore`）。会话目录是 `$HOME`、写不进或不许写时，放状态目录 `uploads/files/<会话>/`。同名不覆盖，改叫 `名 (2).扩展名`。先写 `.<id>.part`，核对后用硬链接放到位（原子、不覆盖）。上传记录在状态目录 `uploads/records/`，重启后能续传。
- **清理**：`uploads.keep`（默认 `720h`，`"0"` 不清）以前的上传连同文件（没改过的才删）和缩略图，每小时一次。
- **附件**：`Send.attachments` 是引用。每个附件在消息末尾占一行 `` [附件] `路径` ``；图片另作为图片块给 Claude（超过 5MB 或边长 8000 时给缩到 1568 的副本，一条消息的图片共 20MB 以内），最多 20 个附件。
- **路径检查**（`pathsafe.Guard`）：先解析符号链接（不存在的路径解析到最深的已有上层）再比允许的目录；写着的路径和解析后的都不能落在 Iridium 的状态和配置目录、`~/.ssh`、`~/.claude`（及 `CLAUDE_CONFIG_DIR`）、`~/.codex`（及 `CODEX_HOME`）、`~/.pi`、`~/.dsh`、`/proc` 里，状态目录的 `uploads/files` 例外。被拒是 403。
## `ir`（agent 用的部分）

- 代码在 `internal/cli`，走上面这套接口；socket 用 `IRIDIUM_SOCKET`，没有就读配置（`IRIDIUM_CONFIG` 或默认位置）。
- `<id>` 可只写开头（唯一即可）；`ir new`、`ir ls` 打印至少 8 位的短 ID。`ir ls` 只列有显示标记的，`ir ls --all` 连归档一起列。`机器:路径`、`机器/<id>` 现在报 `peer not connected`。
- 输出给 agent 看：只打最后一段回复（`new --wait` 先一行 ID）；`cp` 一行 `路径 大小 sha256`；需要你时每个提问一行 `需要你：…`。进度只在 stderr 是终端时画。
- `send`、`new` 不带 `--wait` 时等到 agent 接了这条消息（最多 15 秒）再返回，所以紧接着的 `ir wait` 等的是这一回合。
- 回合在跑时 `ir send` 的话进待发队列：不带 `--wait` 马上返回；带 `--wait` 等它发出去、再等它那一回合（队列暂停了就退出码 2）。`--steer` 插进正在跑的回合。
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

## 会话设置

按会话记在库里：马上设给在跑的 agent，之后每次拉起都带上（Claude：`--model`、`--permission-mode`、`--effort`）。

| 请求 | 正文 | 说明 |
|---|---|---|
| `PUT /api/sessions/{id}/model` | `{model}` | `""` 是 agent 的默认。Claude：`set_model` |
| `PUT /api/sessions/{id}/mode` | `{mode}` | 审批模式，只收 `SessionState.modes` 里的。Claude：`set_permission_mode` |
| `PUT /api/sessions/{id}/effort` | `{effort}` | 思考强度，只收 `SessionState.efforts` 里的。Claude：先 `get_settings` 问 ultracode 开没开，再 `apply_flag_settings` 照原样带上 |
| `GET /api/sessions/{id}/rules` | → `AllowRule[]` | “本会话一直允许”记下的规则 |
| `DELETE /api/sessions/{id}/rules` | → `{ok}` | 清掉 |

- 新会话的审批模式：`NewSession.mode`，没给就用配置 `modes.<agent>`，再没有就是 agent 的默认（`GET /api/agents` 的 `defaultMode`）。默认一律自动批准（用户定的，2026-11-05）：Claude `auto`；Codex `auto`（Codex 的 `approvalPolicy: never`，`never` 也认），更严的 `untrusted`、`on-request`、`granular` 照样能选；pi `auto`，不装审批扩展，选了 `ask` 才装。配置默认就写着这三项。
- 模式和强度按 agent 登记表（`GET /api/agents`）：Codex 从下一回合起生效（`turn/start` 带上）；pi 的模式只在启动时装（`modeSwitch: restart`），在跑时改了就重启它（空闲时马上，否则等这个回合结束；接着 `--session-id` 续上，队列照常发）。强度：Codex `effort`、pi `set_thinking_level`，启动时 pi 用 `--thinking`；模型自己带 `:级别` 的以模型的为准。
- 本会话一直允许：`answer` 带 `always: true`（或卡片选 `always`）。记下工具和 `Ask.pattern`（Claude 自己建议的规则，例如 `ls *`；没有时 shell 命令取第一个词加 ` *`，别的工具不带模式＝这个工具全部）。之后这个会话里匹配的审批自动允许，不通知、不出卡片。
- 匹配：`*` 是任意一段字；`前缀:*` 同 `前缀*`。shell 命令里有 `;`、`&&`、`||`、`|`、`` ` ``、`$(`、换行的，一律不按模式放行。

## 设置

`GET /api/settings` 给 `Settings`：`idleReap`（`"1h"`、`"15m"`，`"0"` 是不回收）、`crashResume`、`quotaResume`、`quotaHidden`（隐藏的额度条，`<agent>:<账号>:<窗口>:<模型>`）、`modes`（agent → 新会话的审批模式，没列的用 agent 自己的）、`push`（推送总开关，和 `PUT /api/push` 是同一个）。

- `PUT /api/settings` 带 `SettingsPatch`，只改给了的项；`modes` 里给 `""` 是去掉那个 agent 的。时长不对、agent 没有这个模式回 400。
- 先读配置文件，只改这几项再写回（文件里别的照旧），同时改内存：空闲回收、续跑开关下次判断时就用新的，审批模式从下一次拉起起。
- 会话自己的设置（库里的）照样盖过这里的。

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

`POST /api/answer`：`{card, option}` 或 `{card, choices: [...]}`（每组一个，多选组可多个）；选择题自己写的答案放 `words`（按组，空串是没写），顶替那一组的选择；拒绝时可带 `message` → `CardReply{ok, reply?, card?}`。已答过或撤回：410。

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
| `POST /api/terminals/invites` | `NewInvite` → `Invite` | `ir invite [名字]`：新面板的一次性链接（10 分钟），`qr` 是同一链接的二维码（SVG data URL）。要先有 `public.origin`（否则 409）。没有面板权时回 202 `Pending` |
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
| `filesIn`、`filesOut` | 拷入（上传、附件、按路径）、拷出（下载、缩略图、插件取文件） |
| `answer` | 答提问和审批 |
| `mustApprove` | 它开的会话一律要审批：不能用跳过审批的模式（`bypassPermissions`、`acceptEdits`、`auto`、`never`、`on-request`、`danger-full-access`），没给模式时用会询问的（Claude `manual`、Codex `untrusted`、pi `ask`）；不能往跳过审批的会话发消息 |
| `dailyTokens` | 每天（本地时间）它起的回合用的 token 上限（输入、输出、缓存都算），0 不限；到了回 429 |
| `allCommands`、`commands` | 能发哪些 `/` 命令：全部，或列出的（如 `/compact`） |

默认：面板除 Claude 外全开；通道除 Claude 外，只看自己开的会话、必须审批。没存过表的终端用默认。

每个会话接口都查：列表（和事件流的 `list`）只给看得到的；看、读消息、取全文看不到就 404；发消息查 agent、命令、审批模式、上限（带附件另查 `filesIn`）；开会话查 agent、目录、模式、命令、上限，并记下是谁开的；停、中断、换模型和强度、待发队列、改名置顶查 agent；换审批模式另查 `mustApprove`；答提问（含 `POST /api/answer` 的卡片）另查 `answer`。

文件：会话的文件和缩略图要 `filesOut`；上传（建任务、传块、完成、取消）要 `filesIn`；socket 上按路径的（`attach` 要 `filesIn`，`GET /api/files`、`POST /api/files/copy` 要 `filesIn`+`filesOut`）和文件夹选择器，路径都得在允许的目录里（先解析符号链接）。工作区的建、改、删只给面板和本机。

插件的聊天层（`/api/chat/…`）按 TOKEN 认终端，同一张表：开会话（含 `/new`）查 agent、目录、上限，必须审批时用会询问的模式；发消息、`/use`、`/stop`、`/model` 只对看得到的会话；Iridium 命令本身查 `commands`，`/mode` 查 `mustApprove`；`/ls`、`/use` 只列看得到的；取文件查 `filesOut`。聊天开的会话记在插件名下，来源标记是 `[插件名·发送者]`。

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
