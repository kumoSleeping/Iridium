# 执行端

另一台机器（Windows、Mac、Linux）跑一个**执行端**：替这边启动 agent 进程、转发它的输入输出、读写文件。会话还是这边托管，网页、驱动、记录解析、卡片全部照旧。第一个实现是 dsh 插件 `dsh-remote-iridium`（2026-11-08 定）；第二个是 Iridium 二进制自己的 `ir join <地址>`（Linux、Mac；Windows 版不再发布，用 dsh 插件）。不做节点：别的机器都以执行端接入，见 `plan.md`“别的机器”（用户，2026-11-08）。

执行端不懂会话、历史、审批，也不懂 agent 的协议：会话归这边托管，agent 的协议由这边现有的驱动讲。这边断开时，那边的回合照样跑完；断开期间不能发话、不能答审批。

## 镜像

托管进程（`internal/hold`）只用文件：`in` 管道、追加写的 `out.log`、`exit`。执行端在它那边原样维护这几样，这边的托管目录里放一份**按字节一致的镜像**：

- `out.log`：这边从镜像的大小开始要，执行端从那个偏移量推，只推整行。断线、这边重启都从镜像大小接着要，不丢不重。
- 输入：这边照旧写 `in` 管道；读管道的是这边的转发，按 `in.log` 的偏移量做序号发过去，执行端按序号去重。
- `exit`：执行端报了才写。
- agent 的对话记录：也是追加写的文件，镜像在托管目录的 `remote/` 下，按大小和开头 4KB 的哈希核对，对不上（改写过、迁移过格式）就整份重取。

所以会话这一层只换“谁启动托管进程”（`platform.Launcher`）和“记录在哪”，读写都还是本地文件。记录以那边的磁盘为准，镜像只是缓存。

## 连接

- 执行端主动连 `wss://<Iridium>/api/exec`（dsh 插件只接受 https，或到本机、内网地址的 http：公网的明文 http 拒绝），头 `Authorization: Bearer <TOKEN>`。执行端就是一个 Bot（提供机器的那种，见 `plan.md`“交互终端”）：用 Bot 的 TOKEN 接入，终端类型也显示“Bot”，名字就是机器名（≤12 字符）。Claude（用户，2026-11-08）：批准时勾了“本人的机器”的执行端才开 Claude，用那台机器上本人的登录、原样跑（不加 `--bare`），等于 `--owner`；这个勾只出现在带 Claude 的面板上。
- 配对（用户，2026-11-08）：插件里只有服务器地址，没有 TOKEN，人人同一个包。第一次不带 TOKEN 连 `/api/exec/pair`（门外唯一不用登录的口，什么也不给），报机器名、系统、装了哪些 agent，两边显示同一个 4 位配对码；网页弹确认卡片“home-pc 请求接入（Windows · dsh 0.2.2）”，带 Passkey 的面板点允许后，TOKEN 经这条连接发给执行端，它存起来以后自己用。限速，同时最多几个待批，10 分钟没人管作废。插件和 `ir join` 用同一个口：`POST /api/exec/pair` 带握手内容（同 `hello` 的 `name`、`os`、`arch`、`program`、`agents`、`noShell`、`agentShell`；卡片据此写“可执行命令”“可执行命令（含 agent）”或“仅 agent”），回 `{code, request}`；再轮询 `GET /api/exec/pair/<request>`，等到允许（拿 TOKEN）或拒绝。普通 Bot 用同样的 `POST /api/pair`（`ir pair`），见 `api.md`。设置里可停止接收申请，对方收到 403。
- 接入是单向的（用户，2026-11-08）：批准的一方能用申请方的 agent 和文件；反过来要对方也申请一次。
- 一个 TOKEN 同时只有一条连接，新连上的顶掉旧的。
- 断了执行端自己重连，退避 1 秒起、最多 30 秒。断开期间它启动的进程照跑，输出照写盘。
- 心跳（用户，2026-11-08）：两边每 25 秒发一次事件 `{"event":"ping"}`（不用回），免得 Cloudflare 这类代理把闲着的连接切掉（约 100 秒）；对方发过 ping、之后 70 秒什么都没收到，就断开重连。从不发 ping 的旧版对方不因为安静被断。这边还用 WebSocket 的 ping 量往返时间，网页上机器名后的灯（顶上的切换和它的菜单里）：绿，慢（超过 300 ms）或刚断开为黄，断开超过 10 秒为红。断开 10 秒内连回来，网页不显示“已断开”；`ir sh` 遇到刚断开的机器先等最多 10 秒。
- 多个 Iridium（用户，2026-11-08）：执行端的 TOKEN 和托管进程按 Iridium 分开放，目录名是它的主机和端口（`ir.kumo.ltd`、`127.0.0.1-7811`，`ir join`、dsh 插件、开发脚本都照这个）。换到另一个 Iridium 再换回来，原来的 TOKEN 还在，不用重新配对；那边的 Bot 被移除了（连接被拒，401/403）才删掉 TOKEN 重新申请。同一个 Iridium 只跑一份（加锁）：同一个 TOKEN 的新连接会顶掉旧的，两份会互相顶。Bot 被移除时，这边立刻断开它的连接（`Hub.Drop`），不等它下次连。
- 不睡（用户，2026-11-08）：macOS 上执行端手上有进程在跑时，每 30 秒起一次 `caffeinate -i -t 60`，挡住闲置睡眠（挡不住合盖）；都跑完了就照常睡。
- 帧是 JSON 文本，一帧一个：
  - 请求 `{"id":7,"method":"hold.start","params":{…}}`，两边都能发。
  - 回答 `{"id":7,"result":{…}}` 或 `{"id":7,"error":{"code":"denied","message":"…"}}`。
  - 事件 `{"event":"out","params":{…}}`，只由执行端发（心跳的 `ping` 除外）。
- 错误码：`not_found`、`exists`、`denied`、`bad_request`、`unsupported`、`failed`。

## 握手（必需）

执行端连上先发 `hello`（请求），这边回 `{"name": "<终端名>"}`：

```json
{"protocol": 1, "program": "dsh-iridium 0.1.0",
 "os": "windows", "arch": "x64", "home": "C:\\Users\\kumo", "sep": "\\",
 "env": ["PATH=…", "…"],
 "agents": [{"agent": "dsh", "argv": ["C:\\…\\node.exe", "C:\\…\\dsh\\lib\\bin.js"],
             "scope": "C:\\…\\dsh\\node_modules\\@deepseek-ai", "version": "0.2.2-rc.1",
             "home": "C:\\Users\\kumo\\.dsh"}],
 "holds": [{"hold": "h_…", "outSize": 48211, "exit": null}]}
```

- `os`：`windows`、`darwin`、`linux`。`sep`：路径分隔符。
- `env`：执行端自己的环境。这边以它为底，按驱动的规则增删，启动时整份发过去。密钥不出那台机器（2026-11-08）：名字像密钥的（含 `KEY`、`TOKEN`、`SECRET`、`PASSW`、`CREDENTIAL`、`COOKIE`、`AUTH`、`SESSION`、`PRIVATE`，或以 `DEEPSEEK_`、`ANTHROPIC_`、`OPENAI_`、`AWS_`、`AZURE_`、`GOOGLE_`、`GH_`、`GITHUB_`、`NPM_`、`HF_` 开头）值换成占位 `(secret kept at the executor)`；`hold.start` 的 `env` 里值仍是占位的，执行端换回本机的值（本机没有就去掉）。这边设的值照用。执行端自己的 `IRIDIUM_SESSION`、`IRIDIUM_SOCKET`、`IRIDIUM_CONFIG`、`IRIDIUM_URL`、`IRIDIUM_TOKEN`（给它本机 Iridium 的）不报，子进程只拿到这边设的。旧执行端照旧报全值。
- `scratch`：执行端自己的一个目录，这边往里写启动用的文件（dsh 的 `--patch`）。
- `agents`：装了哪些 agent、怎么启动（`argv` 是程序部分，驱动接着加参数）。dsh 直接给 `node` 和 `bin.js`，不经 `.cmd`；`scope` 是它的 `@deepseek-ai` 包目录（这边从里面读预设），`home` 是 `DSH_HOME`。dsh 低于 0.2.2 不给开。Codex 给 `codex` 本身（npm 装的给 `node` 和 `codex.js`），不给 `home`：这边看那边环境里的 `CODEX_HOME`。
- `holds`：它手上全部进程（`{hold, outSize, inSeq, exit}`，`inSeq` 是写进去的最后一行的序号）。这边据此对账：哪些还在跑、输入从哪儿续；这边起过、它却没有的，算进程没了。
- `noShell`：执行端的主人选了只启动自己的 agent（`ir join --no-shell`，dsh 插件配置 `shell: false`）。这时 `hold.start` 的 `argv` 必须以某个 agent 的 `argv` 开头，否则报 `denied`；网页上这台机器的会话不能执行命令（`docs/api.md`“命令”）。
- `agentShell`：这边的 agent 也能用 `ir sh` 在那台机器上执行命令，不只是网页上的人（`ir join --agent-shell`，dsh 插件配置 `agentShell: true`）。默认没有。
- `caps`（2026-11-09）：会讲的可选部分。`held`：`held.list`、`held.send`；`held.features`：`held.get`、`held.set` 和事件 `held.changed`（见“可选”“附加功能”）。dsh 插件在 dsh 有 `agents` 服务时报这两项。没有的（旧插件、`ir join`）照旧：`dsh.open`、`dsh.prompt`，没有附加功能。

**接入就是让这边能在那台机器上以那个账户运行程序。** `hold.start` 的 `argv` 由这边定，执行端不加限制（`noShell` 除外）；`roots` 只管文件读写，管不住进程。所以同不同意由那台机器的主人决定：装插件、跑 `ir join` 就是同意；不想让这边执行命令，就开 `noShell`。

## 进程（必需）

`hold` 是这边定的 ID（会话 ID 加进程序号），执行端照用。

| 请求 | 参数 → 回答 | 要求 |
|---|---|---|
| `hold.start` | `{hold, argv, env, cwd, stopGraceMs}` → `{pid}` | 幂等：已有就回现状。程序起不来报 `exit`，码 127 |
| `hold.write` | `{hold, seq, line}` → `{outSize}` | 写一行进 stdin（补 `\n`）。`seq` 不大于已写的就跳过 |
| `hold.tail` | `{hold, from}` → `{}` | 从字节偏移 `from` 推 `out` 事件，跟着往下推；进程结束且推完后推 `exit`。再发一次换起点（旧的停） |
| `hold.stop` | `{hold}` → `{}` | 关 stdin，等 `stopGraceMs`，还在就结束整棵进程树 |
| `hold.kill` | `{hold}` → `{}` | 立刻结束整棵进程树 |
| `hold.forget` | `{hold}` → `{}` | 删它的文件。这边拿到 `exit` 并写进镜像之后才发 |

| 事件 | 参数 |
|---|---|
| `out` | `{hold, off, data}`：`off` 是 `data` 第一个字节在 `out.log` 的偏移，`data` 是若干整行（UTF-8 文本） |
| `exit` | `{hold, from, code, signal, errTail}`：同 `hold.Exit`，`errTail` 是 stderr 的最后约 2KB（显示错误用）；进程被执行端自己的退出带走时 `from` 是 `holder` |

- stdout 原样追加到它那边的 `out.log`，stderr 另存（这边要时用 `fs.read` 读）。
- 执行端自己退出时，它启动的进程跟着结束（管道断了）。这边当作崩溃，照“崩溃续跑”接上。

## 文件（必需）

路径都是那边的绝对路径，这边当不透明字符串，只用握手给的 `sep` 拼接。

| 请求 | 参数 → 回答 |
|---|---|
| `fs.stat` | `{path}` → `{size, mtime, type, head?}`；`type`：`file`、`dir`、`link`、`other`；带 `"head": 4096` 时回开头那么多字节的 sha256 |
| `fs.list` | `{path}` → `{entries: [{name, size, mtime, type}]}`，只列一层 |
| `fs.read` | `{path, offset, length}` → `{data}`，`data` 是 base64，一次最多 4MB |
| `fs.write` | `{path, data, sha256, replace}` → `{}`；先写临时文件，核对后原子改名；`replace` 为假且已有就 `exists` |
| `fs.mkdir` | `{path}` → `{}`，连父目录一起建 |

- 允许的目录由执行端自己配，先解析符号链接再比。一律 `denied`（读写都是）：TOKEN、`~/.ssh`；dsh 家目录的 `.credentials.yaml`、`.env`、`profiles/`（含插件配置）；Claude 的 `.credentials.json`、Codex 的 `auth.json`、pi 的 `~/.pi/agent/auth.json`；`~/.aws`、`~/.config/gh`、`~/.gnupg`、`~/.netrc`、`~/.npmrc`、`~/.git-credentials`、`~/.docker/config.json`。对话记录等其余文件照常。
- 大文件分块，同 `plan.md`“文件传输”。

## 可选

- `fs.watch {path}` 和事件 `changed {path, size}`：文件变了就报，合并着报。没有它这边定时 `fs.stat`。
- 余额：这边要读那边 DeepSeek 的密钥才能查；不做，那台机器上的会话不查余额（这边不拿它的环境、不读本机同名路径）。
- 额度窗口：照常问那边的 agent 进程（Codex `account/rateLimits/read`），是那台机器的登录的，可能是另一个账号（Codex 不报账号），所以按 agent、账号和机器分开记，不和这边的混。
- dsh 插件另有这些（见“远端 DSH”“附加功能”），别的执行端回 `unsupported`。`held.*` 照握手的 `caps` 用，没有就用旧名；插件新旧两个名字都答（给旧版 Iridium）。
  - `held.list {}` → `{sessions, running, unsynced, unsyncedUngrouped}`（旧名 `dsh.open`）：插件所在的 dsh 进程（DSH Desktop）里此刻打开着的会话（Desktop 载入了它、拿着写锁，下称“Desktop 拿着”），其中 agent 正在干活的，插件设置里不同步到这边的工作区（文件夹或名字），以及未分组的会话是否也不同步。
  - `held.send {session, text, steer?}` → `{}`（旧名 `dsh.prompt`）：把消息交给 Desktop 里这个会话的 agent（没有就 `not_found`）。
  - `held.get {session, lang}` → `{held, model?, effort?, owner?, features?}`：会话在那边此刻的样子。`held`：Desktop 拿着它；只有这时才给 `model`（`供应商/模型`）和 `effort`（此刻跑的档位：有附加功能管着强度时是它的，否则是会话的模型选择里的）。`owner`：此刻管着强度的那一项的 id，`effort` 是它的档位；这边起的会话（见“附加功能”）没拿着也给。`features`：插件给它的附加功能，拿不拿着都给（没拿着的不可用，带原因），文字按 `lang`（`zh`、`en`，这边设置的语言）。
  - `held.set {session, lang, model | effort | feature: {id, on, option?}}` → 同 `held.get`，改后的：每次只改一样，只对 Desktop 拿着的会话（否则 `not_found`）；这边起的、会话进程连着插件的，只改功能和强度（强度只在有一项管着时，否则 `unsupported`，这边自己改）。模型、强度经 dsh 的 `sessionController.selectModel`（同 Desktop 的模型选择）：换模型时新模型有原来的档位就保留；强度有附加功能管着时交给它（`setEffort`）。功能交给提供它的插件。审批模式不经这里。
  - 事件 `held.changed {session, live}`：`live` 同 `held.get` 的回答，见“附加功能”。
  - `dsh.attach {session, cwd}` → `{}`，把会话登记到 `cwd` 对应的工作区（没有就新建），Desktop 侧边栏里就有它。
  - `dsh.presets {}` → `{presets: [{id, name?, description?, plugins, broken?}], default}`，插件所在 dsh 进程的 Agent 预设注册表（`agentPresets` 的 `list()`、`defaultId`，`plugins` 是 `readDocument` 给的子插件行 YAML）：这边按它列预设、生成 patch，自建和改过的预设也能用；不支持时退回读包里的预设文件。
  - `dsh.bundles {}` → `{bundles: [{name, version?, patches}]}`：插件所在 profile 的 bundle（`package.json` 的 `dsh.profile.bundles`，按顺序），去掉 dsh 自己的（`@deepseek-ai/`）和本插件，`patches` 是它 `dsh.bundle.patch` 列的文件的原文。用于第三方 bundle 的预设（见“dsh 插件”）；不支持时那种预设退回按进程摊平。
  - `dsh.session {}` → `{patch}`（2026-11-09）：插件写的一个 patch，把它的 `session.js` 装进这边在那台机器上起的每个 dsh 会话进程，带上连回插件的本地地址和口令（`bridge.js`：Unix socket 或 Windows 命名管道）。口令在里面，这边只拿路径，不读（同 `dsh.llm`）。不支持时会话里就没有插件这部分。
  - `dsh.llm {}` → `{patch}`：插件把 Desktop 此刻生效的模型设置（基础包里的 `dsh-llm*` 行和 `agent-default-model`，照它的配置和开关，`!!js` 原样）写成那台机器上的一个补丁，只回路径；这边起 dsh 会话和模型探测时多带 `--patch <它>`，sdk 进程就有了 Desktop 的供应商、模型和默认模型。里面可能有密钥：文件只在那台机器上，执行端不对外读。

## 网页（用户，2026-11-08）

- 不做“添加 harness”：执行端连上就自动出现，终端列表里是一个 Bot，新会话的 agent 里多出它带的 agent。
- 新会话选 agent：别的机器上的 agent 在 logo 右下角带那个 Bot 的头像（插件上传，默认按系统），旁边写机器名，以区分本机和多台机器；没连上的画淡，点了提示“未连接”。
- 会话列表（用户，2026-11-10）：远端组名后带那个头像，组里会话不再带；不在远端组里的远端会话（挂在拉起者下面的）logo 右下角一朵云、名字后面带头像，头像按字形中线对齐。断开时状态灯显示“已断开”。

## 远端工作区（用户，2026-11-10）

取代 11-08、11-09 的“远端组全部列出”“看哪台”和远端组里的本机协调者。（服务端已做：`Session.own`、`detach`、归档连带、不推送、DSH 工作区的会话数，见 `api.md`；页面未实现）

- 默认只列本机。远端组（`机器:路径`）里有人开的、没归档的会话才列；不另记状态。人开的：网页“+”开的、你在终端里 `ir new --machine` 开的、从拉起者下面拖出来的；agent 开的不算。列出来的组里会话全列。
- 不做“添加”键：在哪开会话，哪就显示。顶上的“+”选了远端机器，列它的 DSH 工作区（名字、路径、会话数，不标显示与否），点一行就在那里开 agent，这个组随之列出，里面原有的会话一起出来；没有 dsh 的机器用“其他文件夹…”。不想看了，组右键“归档这里的 N 个会话”。插件里关了“同步到 Iridium”的工作区不在这个列表里。
- 列出来的组：里面的会话全列，在那台机器上直接用 DSH 开的也算（约 30 秒内出现，“同步 DSH”立刻）。
- 不列的：没列的组，会话列表不列、跳转框搜不到、不推送；Desktop 里不在任何工作区的会话也不列。只是这边不列，照常扫描、托管。
- 组的“+”只开那台机器的 agent；不在文件夹里开别的机器的 agent，没有本机协调者挂在远端组里（API 的 `at` 删掉）。
- 派出的会话：会话里用 `ir new` 开的（本机、远端都算）一律挂在拉起者（`launcher`）下面，不管它所在的组列没列；链式拉起挂在最上面那个下面。从列表里拖出来的按自己的目录归组，算人开的，以后不再挂回去。拉起者那一行收起时写子会话数和汇总状态。拉起者归档，挂着的一起归档，恢复时一起恢复；拖出来的不跟。
- 权限：配对就是授权，没有按文件夹、按提及给的授权；能用哪些位置由终端范围管（`docs/k_bot.md` 第 5 节）。远端会话回头用 `ir` 操作这边，算在那个执行端 Bot 名下。`ir sh` 仍要 `agentShell`，`noShell` 照旧。
- 迁移：用 `at` 开的旧协调者回到它本机目录的组；组的 `hidden` 列不再用。

## 平台差异

| | Windows | Mac | Linux |
|---|---|---|---|
| 整棵进程树 | Job Object（或 `taskkill /T /F`） | 新进程组，`kill(-pgid)`，先 TERM 后 KILL | 同 Mac |
| 常驻 | 插件跟着 `dsh web`；`ir join` 用登录时运行的计划任务（不用系统服务：SYSTEM 账户拿不到本人的 PATH 和 agent 登录） | 插件同左；`ir join` 用 launchd 用户代理 | 插件同左；`ir join` 用 systemd 用户单元 |
| 换行 | 只认 `\n`，行尾的 `\r` 去掉 | `\n` | `\n` |
| dsh 记录的目录名 | `C:\a\b` → `C-a-b`（`/ \ :` 换成 `-`） | `/Users/a` → `Users-a` | 同 Mac |
| 编码 | UTF-8，不带 BOM | UTF-8 | UTF-8 |

这边的路径处理不能用 `filepath`（它只懂本机）：远端路径一律按握手的 `sep` 处理。

## 开发测试用的 Bot

`scripts/dev/shell-bot.mjs`：开发者自己的 Mac 上测 `ir sh` 用的小脚本，不进发布包。它借用 dsh 插件的执行端，配对一次后只提供命令执行（`agentShell`），不带 agent。这种 Bot 谁都能照协议写一个；不想写代码就用 `ir join <地址> --agent-shell --no-agents`，效果一样。

`scripts/dev/exec-mini.py`：只用 Python 标准库（WebSocket 也是手写）的最小执行端，证明照协议就能接入：配对、握手报本机找到的 agent（默认 pi）、进程、文件、心跳、重连；只开自己的 agent（`noShell`）。`python3 -I scripts/dev/exec-mini.py https://<Iridium> [名字] [agent…]`。要带自己的 User-Agent：Cloudflare 拒 Python 默认的（1010）。

## dsh 插件

一个 cordis 插件，npm 包 `dsh-remote-iridium`，每次发版由 release.yml 用同一版本号发到 npm（Trusted Publishing，不用 TOKEN）；在 `dsh web` 的“添加插件”里填包名即可装（或 `dsh plugin --profile web add dsh-remote-iridium`），跟着 `dsh web` 常驻：连这边、实现上面的必需部分。它不碰 dsh 自己的会话，每个会话照旧是一个 `dsh --profile sdk --patch …` 子进程（预设和系统提示按进程，`internal/agent/dsh/preset.go`）。

第三方包的预设（2026-11-09）：Desktop 的预设可以来自 web profile 里的 bundle，例如 OMD 的 `omd-ptc`、`oh-my-dsh`，行名是 `oh-my-dsh/host/tool-pwsh` 这种。sdk profile 只从 dsh 安装目录和自己的目录找包，这些行原样放进去会加载失败，被它们替换掉的基础行（shell、jobs）又已关掉，会话就没了执行工具。现在分两种（`internal/agent/dsh/modules.go`、`bound.go`）：

- 找包：patch 里包不是 `@deepseek-ai/` 的行改走 `dsh-modules.mjs`（和 patch 写在一起）：它在 agent 那台机器上按 dsh 的家目录（`DSH_HOME`，否则 `~/.dsh`）去 `profiles/*/node_modules` 找包（先 web），照 `exports` 加载。这边读不到 `profiles`（插件不给），所以找包放在那边的进程里做。
- 绑定（插件有 `dsh.bundles` 时）：预设用到的包是 Desktop 的 bundle，就照 Desktop 的组合来：预设注册表、web 的禁用、那个 bundle 自己那层（去掉其中的预设声明，预设以 `dsh.presets` 读到的为准）、这个预设的声明，再由 `dsh-preset.mjs` 在每个会话创建时绑定这个预设（`agentPresets.recompose`，子代理随父会话）。OMD 的 PTC 执行器只能在绑定的预设里用，这样 `run_code`、上下文、codegraph、workflow 都有。实测（dsh 0.2.2-rc.1、OMD 1.3.9）：`omd-ptc`、`oh-my-dsh` 都是 63 个工具，`run_code` 里跑 `git --version` 正常。仍没有的：只在 Desktop 网页进程里有的（OMD 的 deepseek-workbench、extensions、rewind 要 `webServer`），以及本插件（不进 sdk 进程，所以没有 Ultra）。
- 插件旧、没有 `dsh.bundles`，或预设不用别的 bundle：照旧按进程摊平；被第三方包的行替换的基础行改为条件关闭（那个包在才关，`!!js`，并保留基础行自己的条件），包不在就留着 dsh 自己的 shell 和 jobs。这时 OMD 预设里依赖 `omd` 服务的行（PTC 执行器、上下文、codegraph、workflow）起不来，执行退回 dsh 自带的，shell、jobs、文件工具都在。

实测（2026-11-08）：Mac（arm64，Node 25）上 dsh 0.2.2-rc.1 的 `--profile sdk` 握手、开会话、`shutdown` 都和 Linux 一样，记录写在 `sessions/--<目录>--/<会话>/`。0.1.0 没有 sdk profile，执行端握手报的版本低于 0.2.2 时不给开 dsh。Windows 待测。

## 这边要做的

已做（2026-11-08）：1、3、4、5 里的端点和“已断开”标记、7。代码：`internal/remote`（连接、转发、镜像）、`internal/remote/wire`（协议，各系统都能编）、`internal/remote/executor`（Go 写的参考执行端，测试用；dsh 插件照它做）、`internal/session/remote.go`。分组按 `机器ID:路径`（`/` 分隔）。配对口和网页的确认卡片也已做（`internal/server/routes_pair.go`）；机器列表（`GET /api/machines`、状态 `machines`，断开的也列，记在库里）、新会话里别的机器的 agent、会话的机器角标和“已断开”灯已做（`internal/server/machines.go`、`web/src/machines.tsx`）。别的机器上选目录也已做（目录选择器上方切换机器，`GET /api/folders?machine=`，路径给网页时一律用 `/`）。还没做：上传到那边、`pathsafe`。

1. `platform.Launcher` 加远端实现：`Start` 读托管目录的 `spec.json` 发 `hold.start`，起转发（管道 → `hold.write`，`out` → 镜像 `out.log`，`exit` → 镜像 `exit`）；`State` 看执行端报的 `holds`。会话按所在机器选用哪个。
2. 会话的 `meta` 记机器；目录是 `机器:路径`。分组、显示、`pathsafe` 认这种写法。
3. dsh 驱动的 `Spawn` 不再直接读本机：程序、包、家目录取握手的 `agents`；预设的 yml 用 `fs.read` 取到本地缓存再生成 patch，patch 和系统提示用 `fs.write` 写到那边。
4. 记录：找文件用 `fs.list`，读用镜像。
5. `/api/exec` 端点、TOKEN、在线状态；会话断开时状态灯显示“已断开”，不当作停止。
6. 网页：新会话选机器；列表标机器名。
7. 一个 Go 写的参考执行端（`internal/remote/executor`），测试用；回放录制的假 dsh 在 `internal/testagent/dsh.go`。
8. （已做）Claude 在执行端：驱动的 `Spawn` 按 `Remote` 走（系统提示文件写到那边），记录在那边的 `~/.claude/projects/` 用 `FindRemote` 找。只读那边的 `~/.claude`。项目目录名照 Claude 的规则（每个不是 ASCII 字母数字的 UTF-16 单元换成 `-`）；超过 200 的 Claude 会截断加哈希，这种找不到记录。没勾“本人的机器”的那台：新会话不列 Claude，接口也拒绝（403）。
9. （已做，`internal/join`）`ir join <地址>`：二进制的执行端模式，复用 `internal/remote/executor`，配对、存 TOKEN、常驻（见“平台差异”）、找 agent（npm 的 `.cmd` 垫片解析成 `node` + 入口 js）。
10. （已做）`ir cp`、`ir files` 认执行端的 `机器:路径`（`fs.read`/`fs.write`/`fs.list`），只给本机的 `ir`：读按 `MaxRead` 分块不限大小；写到那边是一次 `fs.write`，先限 4MB。`ir sh` 已做。
11. （已做）执行端上的会话里 agent 调 `ir`，见“远端的 ir”。
12. 任何 agent 都能在执行端跑（2026-11-08）：驱动不分本机和那边，程序取 `SpawnOptions.Command`（那边是握手的 `argv`），启动文件经 `LaunchFile`、系统提示经 `PromptFile`（那边写进 scratch）；agent 的不同只在注册表一行（`internal/agents`）：`FindRemote`（那边的记录在哪）、`HomeEnv`（握手给的家目录放进哪个变量）、`MinRemote`（最低版本）。已接：Claude、dsh、pi（已做，用 `scripts/dev/exec-mini.py` 在测试实例上实测：开会话、工具调用、记录镜像、执行端重启后续跑、停止）、Codex（2026-11-09，只有测试，实机待跑）。Codex 的家目录是那边的 `CODEX_HOME`，没有就 `~/.codex`；记录是 `sessions/YYYY/MM/DD/rollout-<那边本地时间>-<线程>.jsonl`，日期按那边的本地时间。线程 id 是 UUIDv7，前 48 位是毫秒时间：取它的 UTC 日期和前后各一天（任何时区的本地日期都在其中）三个目录，再看 `archived_sessions/`；不是 v7 的（旧版）从最新的日目录往回找，最多 400 个。没有线程（第一回合前）就没有记录；停了再开按线程 `thread/resume`。系统提示在这边读、经协议发过去，那边不写文件。测试：`internal/agents` 的 `TestCodexFindRemote`（三个日期、归档、旧 id、上限），`internal/session` 的 `TestRemoteCodex`（Go 参考执行端：用握手的 `argv` 和那边的 `CODEX_HOME` 启动、按线程找到记录并镜像；没有假的 Codex app-server，agent 是一个记下启动参数的 shell，回合没测）。
13. 发现那边自己开的会话：dsh 的已做（见“远端 DSH”），不用执行端新增请求；Claude、pi 还没做。

## 远端 DSH（用户，2026-11-08）

那台机器上 dsh 自己的工作区和会话（DSH Desktop 开的），这边照样能选、能看、能接着聊。只用现有的 `fs.list`、`fs.read`、`fs.stat`，执行端不用改。代码：`internal/dshhome`（读 dsh 家目录里的文件）、`internal/discover/remote.go`（扫描、镜像）。

- 读什么：家目录取 dsh 的 `home`（握手），没有就 `DSH_HOME`，再没有就 `~/.dsh`。
  - `storages/workspace.json`：工作区（`unit` 为 `workspace`、`version` 2；`global.workspaceIds` 是顺序，`tables.workspaces[id]` 有 `path`、`title`、`sessionIds`；`global.archivedSessionIds` 是 Desktop 里归档的会话）。
  - `storages/session_projcache/sessions/<id>.json`：每个会话一份小文件，`record.identity.cwd`、`rows.title.val`、`rows.timeContext.val.lastMessageTime`。
  - `sessions/--<目录>--/<id>/session.vN.jsonl.zstd`：记录。开头那一帧的第一行是会话头（`cwd`、`origin`、`parentSession`），读前 8KB（不够再读 64KB）就有，用来认子 agent。
  - 版本对不上的一律当没有：工作区退回用会话所在的文件夹，摘要退回用会话头。
- 扫描：每台连着、装了 dsh 的机器每 30 秒一次，连上时立刻一次；只看变了的：`sessions/` 下的目录按修改时间、摘要文件按修改时间；没有摘要的会话每 10 分钟逐个看一次。同时最多 8 个请求。结果存在状态目录 `remote-dsh/<机器>.json`，重启、机器断开时照样列出。
- 列表：全部扫描，有人开的会话的组才列（2026-11-10，见“远端工作区”）。不照本机“开始监视前就有的进归档”：第一次见到就显示，Desktop 里归档的进归档；之后 Desktop 里新归档的这边也归档，归档的有新内容再出来。显示标记的大小按那边记录文件的大小。分组照 `机器ID:路径`。
- 记录：打开时才拉（`Mirror`，增量），放在状态目录 `remote-dsh/<机器>/<会话>/`；拉过的会话，扫描看到变了就跟着拉。断开时用已拉的。
- 接着聊：发消息即接管（本机发现的 dsh 会话同样），在那台机器上 `session/resume`。插件每 5 秒报一次 Desktop 里开着哪些（`held.list`，旧插件 `dsh.open`），这些会话显示“在别处运行”。Desktop 开着（拿着写锁）的会话，消息交给 Desktop 自己的那个 agent（插件的 `held.send`，旧插件 `dsh.prompt`；`followup`／`steer`，来源 `user`，文本前带来源标记），任务在 Desktop 里接着跑，这边从镜像的记录里看（`SendReply.desktop`；`ir send --wait` 等记录里出现回复、5 秒不再变化）；Desktop 没有它的 agent 时照常在那边 `session/resume`，被写锁拒绝（`session/writer-held`，Windows 上是命名信号量，这边查不到）就报“正在另一个 dsh（DSH Desktop）里打开”。用户，2026-11-08：执行端断开一次（Desktop 重启），Desktop 回来后会自己载入这些会话并拿着写锁，之前就再也接不上。接管后这边的进程运行期间 Desktop 写不了它，停掉就放开。插件有 `held.features` 时，Desktop 拿着、这边没在跑的会话，网页上改模型、强度直接在 Desktop 里改（`held.set`），不接管；会话状态的 `effortNow` 是它在 Desktop 里此刻跑的档位（2026-11-09）。
- 进 Desktop 的工作区：这边在那台机器上开的 dsh 会话，记录一出现就请插件登记到它目录的工作区（`dsh.attach`，没有就新建），只请求一次（成功或插件不支持，记在托管目录的 `remote-filed`）。Desktop 的会话列表只随它自己进程里的会话刷新，别的进程建的会话要重新载入窗口才出现：插件在浏览器里的那一半盯着工作区列表，工作区里有会话列表没有的会话时，让会话列表重新拉一次（每个会话最多三次）。
- 分组（用户，2026-11-08；2026-11-10 改）：Desktop 里不在任何工作区的会话（它的“未分组”）不归组（`workspace` 0，`loose`），不列，除非人开的或挂在拉起者下面；以前自动放进组的留在原组。读不到工作区登记表时照文件夹分组。执行端重新配对换了机器 ID，自动归到旧 ID 分组的会话改归新 ID 的分组。
- 不同步（用户，2026-11-08）：插件在 dsh 设置里有自己的一页“Iridium”（`settings.section`，不在“插件”里；dsh web 和 DSH Desktop 共用这套设置外壳），上面是地址，下面每个工作区一个开关“同步到 Iridium”，最后一行是“未分组”（不在任何工作区里的会话，多是一次性的对话）。关掉的工作区按文件夹记在插件配置的 `unsynced`，未分组记在 `ungrouped: false`，随 `held.list`（`dsh.open`）报给这边（`unsynced`、`unsyncedUngrouped`）；这些会话（连同它们的子 agent）不列出，新会话的工作区列表里也没有，改了几秒内生效。只是这边不列，执行端照样能读那些文件。
- 隐藏：撤了（2026-11-10）：组列不列由里面有没有人开的会话定，组的 `hidden`（库、`PATCH`）删掉。
- 同步（用户，2026-11-09）：远端组右键菜单的“同步 DSH”（那台机器装了 dsh 才有，`POST /api/machines/{id}/dsh/sync`）立刻重读那台机器：工作区和会话（不等 30 秒的扫描）、模型和供应商（带 Desktop 此刻的设置重新探测，不用 3 分钟的缓存）、预设；那台机器的会话随即刷新。在跑的 agent 用的还是启动时的，下次启动才换。
- 新会话：目录选择器在装了 dsh 的远端机器上，最上面列它的工作区（`GET /api/machines/{id}/dsh/workspaces`），每行名字、路径、会话数，点一下就在那里开（2026-11-10，见“远端工作区”）。
- 插件在 Desktop 进程里用 dsh 自己的服务：`sessions.list()`、`agents.get()`、`sessionController.selectModel`（`plugins/dsh-remote-iridium/held.js`），`workspaceRegistry` 的 `resolveByPath`、`create`、`attachSession`（`dsh.js`）。旧版插件和 `ir join` 没有 `held.list`（`dsh.open`）和 `dsh.attach`：不显示 Desktop 里开着的，也不登记。

## 附加功能（用户，2026-11-09）

会话上多出的开关（Feature），网页画在思考强度滑条右边：Claude 的 UltraCode（驱动的，`claude.ultracode`），以及执行端那台机器上 DSH Desktop 里别的插件给的，例如 OMD（oh-my-dsh）的 Ultra（`omd.ultra`）。OMD 不进 Iridium 的核心（用户，2026-11-09）：这边只收插件给的数据，不带认识 OMD 的代码。代码：`internal/harness/dsh/held.go`，插件的 `held.js`、`features.js`、`adapters/omd.js`。

- 形状：`{id, name, on, active?, available, reason?, hint?, options?: [{id, name}], option?, place, tone?, v?}`。这边只认 `on`（开没开）、`active`（开了且生效）、`available`（现在能不能切，不能时 `reason` 说为什么）、`place`、`tone`；名字、说明、原因、选项照转，由插件按 `held.get` 的 `lang` 写。
- 插件里的注册表：cordis 服务 `iridiumFeatures`（`features.js`）。`provide({id, get, set, ownsEffort?, effort?, setEffort?})` 注册一项，返回撤销函数；同 id 先到先得，前一个撤了后一个才顶上。每次调用有时限（读 3 秒、改 15 秒），出错或超时只让这一项不可用，错误消息作原因；`held.get` 里各项的读取和“谁管强度”同时问，整个回复 3 秒内给出。插件设置“Iridium”一页有一组开关“提供给 Iridium 的附加功能”，关掉的按 id 记在配置 `unfeatured`，不提供、也不能改；这一页在浏览器那一半，拿不到注册表，只列插件自带适配的（现在是 `omd.ultra`）和已关掉的。
- 推送：Desktop 里有变化（切了功能、选了模型、agent 载入或卸下；Desktop 拿着的会话另外每 3 秒看一次），插件对这边 10 分钟内问过的会话发 `held.changed`（Desktop 一直拿着的会话不过期），每个会话每秒最多两次。
- OMD 的适配：`adapters/omd.js` 是插件里唯一认识 OMD 的文件，一个 cordis 子插件，`inject: ['omd', 'iridiumFeatures']`：两样都在才启用，没装 OMD 时不运行也不报错，OMD 卸载或重载时连同这一项撤掉。它把 OMD 的 `ultracode`（`inspect`、`select`）译成 `omd.ultra`：名字 Ultra，暖色，子模式 Workflow、Agent Team（OMD 说能组队时才有）、自由。开着时强度归它管（滑条显示、设的都是 Ultra 的档位）；开时不给档位由 OMD 选（已开着的保留原档位），关时回到滑条的档位。哪天 OMD 自己注册进 `iridiumFeatures`，删掉这个文件即可。
- 能开的：Desktop 拿着的会话，和 Iridium 自己起的、用 OMD 预设的 dsh 会话（2026-11-09）。后者是 `dsh --profile sdk` 子进程，OMD 在那个进程里（见“dsh 插件”的绑定），所以功能也在那个进程里：插件经 `dsh.session` 把 `session.js` 装进去，它在进程里提供同样的 `iridiumFeatures`、启用同一个 `adapters/omd.js`，再经本地的桥（`bridge.js`：插件每次启动一个自己的 Unix socket 或 Windows 命名管道，带口令；`dsh.session` 每次写当前这个）连回 Desktop 里的插件；sdk 进程没有 dsh 的 `sessionController`（那是网页那一半的），`session.js` 补一个只会选模型的（`selectModel`，同 Desktop 的模型选择，但不存成默认模型），OMD 开关 Ultra 要用它；`held.get`、`held.set`、`held.changed` 对这种会话由插件转给那个进程，开关、子模式、强度归属都和 Desktop 拿着的一样（开着时滑条是 Ultra 的档位，改强度经 `held.set` 交给 OMD）。这边只认通用的形状：`available` 由插件说，`owner` 说明强度归谁。别的会话（Desktop 没拿着、非 OMD 预设、插件旧没有 `dsh.session`）照样列出这一项，但不可用（OMD 为会话记着的 Ultra 照样显示为开），原因是插件给的（“会话不在 DSH Desktop 里打开着，或没用 OMD 的预设”）。Iridium 所在机器上直接发现的 Desktop 会话没有执行端，也就没有附加功能；插件旧、握手没有 `held.features` 的，同样没有。
- 这边的限制：
  - `id` 形如 `<插件>.<名字>`（只用 `a-z0-9-`），不合的丢；同 id 取第一个；每个会话最多 4 项，多的丢（每台机器记一次日志）。`v` 大于 1 的丢；`place` 只认 `effort`（网页只有这一处），别的丢；`tone` 只认 `warm`，别的当没有。
  - 名字、选项名至多 24 字，说明、原因至多 120 字：去掉控制字符和首尾空白，长了截断。选项至多 8 个，id 只用 `a-z0-9-`；`option` 不是其中之一就当没有。
  - 切换前先核对：这一项、这个选项在会话最近一次拿到的里面，否则 400，不问执行端。
  - 每个会话拿到的缓存 10 秒。会话状态不等执行端：过期了、或语言变了就在后台 `held.get`（6 秒超时；失败的 10 秒后再问，不当作拿到过），回来再发一次会话状态，所以第一份状态可能还没有附加功能。设置里换了语言，拿到过的会话都重发一次。Desktop 拿着的会话，强度、模型以 Desktop 的为准（这边起过的会话也一样，不看这边存的设置）。名字、说明按服务器的语言（设置里的），不按各页面的。`held.changed` 每个会话 500ms 至多发一次（取最后一份）；不是这边知道的、那台机器上的 dsh 会话，不理。
  - 只有网页能切（`PUT /api/sessions/{id}/features/{feature}`，权限同改强度）；`ir` 和 Bot 不碰附加功能。

## 通道（2026-11-09）

核心（`internal/session`、`server`、`cli`、`chat`、`remote`）不认识具体的 agent：某个 agent 独有的事放在它的通道里（`internal/harness/<agent>`），核心按会话的 agent 取通道、只问能力（`harness.Stater`、`Sender`、`Setter`、`Launcher`、`Models`、`Presets`、`Syncer`、`Retuner`、`Imager`），在 `cmd/iridium` 接线。静态的差异仍写在 `agents.Info` 的行里，驱动的差异仍是驱动的可选接口。

- dsh 的通道（`internal/harness/dsh`）：DSH Desktop 拿着的会话（`held.*`：状态、转交消息、改模型强度和附加功能）、模型目录（探测）、预设、启动时的 `dsh.llm`、`dsh.session` 补丁和 `dsh.bundles`、`dsh.attach` 归档。协议名不变，插件不用重发。
- 工具读的图（`Imager`，2026-11-09）：记录里只写 handle、不带图的，由通道读。dsh 的 `read_image` 把图存进附件库（`<home>/attachments/v1/objects/<前两位>/<sha256>`），记录里是 `sha256:<hex>`；通道按会话所在的机器读本机的或经执行端 `fs.read`，核对 sha256，最近读的在内存里留 64MB。执行端不用改。
- `Setter` 对在这边跑着的会话也问（2026-11-09）：由通道说是不是它的。dsh 的只接一种：会话进程里有一项附加功能管着强度时（`held.get` 的 `owner`），改强度交给它；别的照旧走驱动。
- Claude、Codex、pi 暂无通道：它们的差异都在 `agents.Info` 和驱动接口里。
- 新接口只加在核心真要做决定的地方（显示状态、发消息、改设置、启动、显示图片），形状按核心的决定定，不按某个 agent 的协议定；只属于 dsh 的概念留在它的通道里。
- 守护测试 `internal/harness/neutral_test.go`：核心包里出现 dsh 的名字就失败。还没搬的列在它的 `pending` 里：`/dsh/` 路由、`SendReply.desktop` 的等待、远端 dsh 会话的发现（`internal/discover/remote.go`，最大的一块，单独做）。UltraCode 的 `claude.ultracode` 也还在核心里，待驱动有 `Featurer` 后搬。

## 远端的 ir（用户，2026-11-08）

跨机器的多 agent 协同：本机的主 agent 经 @ 知道远端的位置，用 `ir new --machine` 给那边的 dsh 派活、看结果、搬文件；派出的会话挂在它下面（“远端工作区”）。那边的会话也能回过来找它。

- `ir new … --machine <机器>` 在那台机器上开（目录按那边的写法，不给就是那边的家目录）；`--workspace <组>` 放进指定的组。会话里跑的，新会话挂在拉起者下面。`ir ls --here` 只列自己组的会话；`ir ls` 里别的机器的会话目录写成 `机器名:路径`。
- 那边的 `ir`：执行端握手声明 `irClient`，这边每次在那边启动 agent 时生成一个只属于这个会话的 TOKEN（前缀 `irs_`，托管目录只存它的哈希，重启一次换一个），经 `IRIDIUM_TOKEN` 给执行端；执行端在这个会话的托管目录里放一个 `ir`（`holds/<会话>/bin/`），排在它 PATH 的最前面，地址（自己连的那个，`IRIDIUM_URL` 给了就用它）和 TOKEN 直接写在里面：agent 跑的命令不一定拿得到它的环境变量（dsh 跑命令就不带 `IRIDIUM_*`，2026-11-08 实测）。dsh 插件的是 `ir.js`（`ir`、`ir.cmd` 用插件所在的 node 跑；DSH Desktop 里是它自己的程序，带 `ELECTRON_RUN_AS_NODE`）；`ir join` 的是这个二进制本身（脚本再执行 `bin/.ir/ir` 这个链接，按名字 `ir` 进命令行模式）。
- 那边的 `ir` 只转发：把参数和当前目录发到 `POST /api/cli`，这边照常跑 `ir`，输出一行一行流回去，最后是退出码。请求算那台机器的 Bot 发的、代表这个会话：Bot 的规则照旧（没勾“本人的机器”的不能开 Claude、不能给 Claude 会话发消息），算 agent 发的（消息并进对方当前回合，不能放行 auto 模式拦下的步骤），信里写成来自这个会话。Bot 被删，它的会话 TOKEN 一起失效。
- 那边能用的：`new`（目录是那边的，`--machine` 可开到别的机器）、`send`、`wait`、`read`、`ls`、`find`、`stop`、`archive`、`unarchive`、`rename`、`ws`、`machines`。不能用：`cp`、`files`、`sh`、`run`、`secret`、`bot`、`usage` 和管理命令，这些碰的是 Iridium 所在机器的文件、命令和秘密，留给本机的会话和面板。
- 系统提示：执行端有 `ir` 的，给那边能用的命令（`irhelp.Remote`）；没有的，不提 `ir`。都不教文件链接（那边的 `ir` 没有 `attach`，文件网页取不到）。

## 暂不做

目录传输。
