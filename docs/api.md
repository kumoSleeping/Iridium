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
| `GET /api/sessions` | → `Session[]` | 会话列表：托管的加上磁盘上发现的（见“工作区和目录”）。置顶在前，再按最近 |
| `POST /api/sessions` | `NewSession` → `{id}` | 在 `cwd` 开一个托管会话，带 `text` 就作为第一条消息。`agent` 现在只有 `claude`。带 `workspace` 就放进那个工作区 |
| `GET /api/sessions/{id}` | → `SessionState` | 打开的会话的状态，和事件流里的同一份 |
| `GET /api/sessions/{id}/messages` | → `MessagePage` | 不带参数：最新一页。`before=<start>`：往回一页。`after=<end>`：之后新写的 |
| `GET /api/sessions/{id}/block?at=<at>` | → `{text}` | 截短的块（工具参数、结果、思考、系统行）的全文 |
| `POST /api/sessions/{id}/send` | `{text}` → `{input}` | 发消息。agent 没在跑就先拉起（有记录就 `--resume`）。回合进行中发的并进当前回合。在别处运行的回 409 |
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
| `workspaces` | `Workspace[]` |
| `session:<id>` | `SessionState`：状态、提问、后台任务、模型、审批模式、`end`、`proc`、出错原因 |
| `context:<id>` | `Context`：上下文用了多少、分项。回合结束和压缩后更新 |
| `quota` | `Quota`：各 agent 的额度窗口和读到的时间 |

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

`idle` 无灯，`starting` 启动中，`running` 运行中，`compacting` 压缩中，`waiting` 需要你（有提问），`error` 上次进程出错退出（原因在 `SessionState.error`），`elsewhere` 在别处运行（只读）。`live` 说 agent 进程在不在跑。

## 工作区和目录（自动发现）

会话列表以磁盘为准（`internal/discover`），启动时建索引放内存，inotify 加定时扫（目录每分钟、`/proc` 每 3 秒）跟上：

- 来源：Claude `~/.claude/projects/<目录>/<id>.jsonl`（`<id>/subagents/` 和 `agent-*.jsonl` 是 Agent 工具的子任务，不列）；Codex `$CODEX_HOME/sessions/YYYY/MM/DD/rollout-*.jsonl`（`source.subagent` 的不列，标题用 `session_index.jsonl` 的线程名）；pi `~/.pi/agent/sessions/*/*.jsonl`（标题用最后一个 `session_info` 名字）；dsh `~/.dsh/sessions/--<键>--/<id>/session.v<N>.jsonl.zstd`（现在只看路径和时间，目录由键反推）。标题、最近一句话只读文件开头 8MB 和末尾 4MB。
- 在跑的：`~/.claude/sessions/<pid>.json`（核对 `procStart`），`/proc` 里本人的 codex、pi、dsh 进程（打开的记录文件或锁、命令行；pi 用工作目录里进程起来后最新写的那份）。环境变量只读本人进程的，只取下面几个，不记日志。
- 拉起者：子进程环境里依次看 `IRIDIUM_SESSION`、`CLAUDE_CODE_SESSION_ID`、`CODEX_THREAD_ID`、`CODEX_SESSION_ID`、`DSH_SESSION_ID`、`PI_SESSION_ID`；都没有就往上找第一个只跑一个会话的 agent 进程。`IRIDIUM_SESSION` 等于自己的 ID 是 Iridium 托管的，不算在别处。
- 首次发现归类一次（库里记下，以后不再动）：拉起者的工作区 → 同目录或最近上层目录已有会话所在的工作区 → 未归类（`workspace` 为 0）。手动移过的永远不再自动动。新建工作区时，那个目录及以下未归类、没被手动移过的会话进新工作区。
- 状态：`hosted` 托管；`elsewhere` 在别处运行，只读（能看状态和消息页，发消息回 409）；其余是磁盘上空闲的。别处退出后，发消息就接成托管会话（`--resume`），前提是没有别的进程在用（Claude：`sessions/` 里没有活着的同 ID 进程；dsh：`session.lock` 能拿到 flock；Codex：写锁）。现在只能接 Claude 的。
- 列表变了（新会话、记录长了、进程起停、改了覆盖层）攒 200ms 发一次 `list` 整份，变了的会话另发 `session:<id>`。

| 请求 | 正文 → 回答 | 说明 |
|---|---|---|
| `GET /api/workspaces` | → `Workspace[]` | 和事件流 `workspaces` 同一份 |
| `POST /api/workspaces` | `NewWorkspace` → `Workspace` | 用一个文件夹建（要在选择器能去的范围里）；`name` 不给就用文件夹名。同一文件夹已有工作区回 409 |
| `PATCH /api/workspaces/{id}` | `{name}` → `{ok}` | 改名 |
| `DELETE /api/workspaces/{id}` | → `{ok}` | 删掉；里面的会话变成未归类 |
| `PATCH /api/sessions/{id}` | `SessionPatch` → `{ok}` | 只改覆盖层，不碰 agent 的文件：`name`（空串去掉）、`pinned`、`archived`、`workspace`（移动，0 为未归类；移过就不再自动归类） |
| `GET /api/folders?path=` | → `Folders` | 选择器：只列文件夹，不列隐藏的，按名字排；`path` 为空或 `~` 是家目录。`parent` 为空表示到顶了 |
| `POST /api/folders` | `NewFolder` → `{path}` | 在 `path` 里新建文件夹 `name`（不能以 `.` 开头、不能带 `/`）。已有回 409 |

- 选择器默认只能在家目录里（配置 `folderRoots` 可改），先解析符号链接再比；`~/.ssh`、`~/.gnupg`、`~/.claude`、`~/.codex`、`~/.pi`、`~/.dsh` 和 Iridium 的状态、配置目录一律不给（403）。不存在 404。
