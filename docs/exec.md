# 执行端

另一台机器（Windows、Mac、Linux）跑一个**执行端**：替这边启动 agent 进程、转发它的输入输出、读写文件。会话还是这边托管，网页、驱动、记录解析、卡片全部照旧。第一个实现是 dsh 插件 `dsh-remote-iridium`（2026-11-08 定）；第二个是 Iridium 二进制自己的 `ir join <地址>`：Windows 只做这个，Mac、Linux 是完整节点，接入别的 Iridium 时也用它（用户，2026-11-08）。

## 和节点的区别

| | 节点（`plan.md`“节点”） | 执行端 |
|---|---|---|
| 对方装 | 完整的 `ir` | 实现本协议的任何程序 |
| 会话归 | 各自托管，对等 | 这边托管 |
| 双方说的 | `ir new/send/cp/files` | 进程、按行收发、偏移续传、退出码、文件 |
| agent 协议由谁讲 | 对方的 Iridium | 这边现有的驱动 |

执行端不懂会话、历史、审批，也不懂 agent 的协议。这边断开时，那边的回合照样跑完；断开期间不能发话、不能答审批。要离线也能用，装完整节点。

## 镜像

托管进程（`internal/hold`）只用文件：`in` 管道、追加写的 `out.log`、`exit`。执行端在它那边原样维护这几样，这边的托管目录里放一份**按字节一致的镜像**：

- `out.log`：这边从镜像的大小开始要，执行端从那个偏移量推，只推整行。断线、这边重启都从镜像大小接着要，不丢不重。
- 输入：这边照旧写 `in` 管道；读管道的是这边的转发，按 `in.log` 的偏移量做序号发过去，执行端按序号去重。
- `exit`：执行端报了才写。
- agent 的对话记录：也是追加写的文件，镜像在托管目录的 `remote/` 下，按大小和开头 4KB 的哈希核对，对不上（改写过、迁移过格式）就整份重取。

所以会话这一层只换“谁启动托管进程”（`platform.Launcher`）和“记录在哪”，读写都还是本地文件。记录以那边的磁盘为准，镜像只是缓存。

## 连接

- 执行端主动连 `wss://<Iridium>/api/exec`，头 `Authorization: Bearer <TOKEN>`。执行端就是一个 Bot：用 Bot 的 TOKEN 接入，终端类型也显示“Bot”，名字就是机器名（≤12 字符）。Claude（用户，2026-11-08）：批准时勾了“本人的机器”的执行端才开 Claude，用那台机器上本人的登录、原样跑（不加 `--bare`），等于 `--owner`；这个勾只出现在带 Claude 的面板上。
- 配对（用户，2026-11-08）：插件里只有服务器地址，没有 TOKEN，人人同一个包。第一次不带 TOKEN 连 `/api/exec/pair`（门外唯一不用登录的口，什么也不给），报机器名、系统、装了哪些 agent，两边显示同一个 4 位配对码；网页弹确认卡片“home-pc 请求接入（Windows · dsh 0.2.2）”，带 Passkey 的面板点允许后，TOKEN 经这条连接发给执行端，它存起来以后自己用。限速，同时最多几个待批，10 分钟没人管作废。插件和 `ir join` 用同一个口：`POST /api/exec/pair` 带握手内容（同 `hello`），回 `{code, request}`；再轮询 `GET /api/exec/pair/<request>`，等到允许（拿 TOKEN）或拒绝。
- 接入是单向的（用户，2026-11-08）：批准的一方能用申请方的 agent 和文件；反过来要对方也申请一次。
- 一个 TOKEN 同时只有一条连接，新连上的顶掉旧的。
- 断了执行端自己重连，退避 1 秒起、最多 30 秒。断开期间它启动的进程照跑，输出照写盘。
- 心跳（用户，2026-11-08）：两边每 25 秒发一次事件 `{"event":"ping"}`（不用回），免得 Cloudflare 这类代理把闲着的连接切掉（约 100 秒）；对方发过 ping、之后 70 秒什么都没收到，就断开重连。从不发 ping 的旧版对方不因为安静被断。这边还用 WebSocket 的 ping 量往返时间，网页上分组名后的灯：绿，慢（超过 300 ms）或刚断开为黄，断开超过 10 秒为红。断开 10 秒内连回来，网页不显示“已断开”；`ir sh` 遇到刚断开的机器先等最多 10 秒。
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
- `env`：执行端自己的环境。这边以它为底，按驱动的规则增删，启动时整份发过去。
- `scratch`：执行端自己的一个目录，这边往里写启动用的文件（dsh 的 `--patch`）。
- `agents`：装了哪些 agent、怎么启动（`argv` 是程序部分，驱动接着加参数）。dsh 直接给 `node` 和 `bin.js`，不经 `.cmd`；`scope` 是它的 `@deepseek-ai` 包目录（这边从里面读预设），`home` 是 `DSH_HOME`。dsh 低于 0.2.2 不给开。
- `holds`：它手上全部进程（`{hold, outSize, inSeq, exit}`，`inSeq` 是写进去的最后一行的序号）。这边据此对账：哪些还在跑、输入从哪儿续；这边起过、它却没有的，算进程没了。
- `noShell`：执行端的主人选了只启动自己的 agent（`ir join --no-shell`，dsh 插件配置 `shell: false`）。这时 `hold.start` 的 `argv` 必须以某个 agent 的 `argv` 开头，否则报 `denied`；网页上这台机器的会话不能执行命令（`docs/api.md`“命令”）。
- `agentShell`：这边的 agent 也能用 `ir sh` 在那台机器上执行命令，不只是网页上的人（`ir join --agent-shell`，dsh 插件配置 `agentShell: true`）。默认没有。

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

- 允许的目录由执行端自己配，先解析符号链接再比；它自己的配置、TOKEN 和 `~/.ssh` 一律 `denied`。
- 大文件分块，同 `plan.md`“文件传输”。

## 可选

- `fs.watch {path}` 和事件 `changed {path, size}`：文件变了就报，合并着报。没有它这边定时 `fs.stat`。
- 余额：这边要读那边 DeepSeek 的密钥才能查；不做就不显示那台机器的余额。

## 网页（用户，2026-11-08）

- 不做“添加 harness”：执行端连上就自动出现，终端列表里是一个 Bot，新会话的 agent 里多出它带的 agent。
- 新会话选 agent：别的机器上的 agent 在 logo 右下角带那个 Bot 的头像（插件上传，默认按系统），旁边写机器名，以区分本机和多台机器；没连上的画淡，点了提示“未连接”。
- 会话列表同样在 agent logo 右下角带那个头像；断开时状态灯显示“已断开”。

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

## dsh 插件

一个 cordis 插件，npm 包 `dsh-remote-iridium`，每次发版由 release.yml 用同一版本号发到 npm（Trusted Publishing，不用 TOKEN）；在 `dsh web` 的“添加插件”里填包名即可装（或 `dsh plugin --profile web add dsh-remote-iridium`），跟着 `dsh web` 常驻：连这边、实现上面的必需部分。它不碰 dsh 自己的会话，每个会话照旧是一个 `dsh --profile sdk --patch …` 子进程（预设和系统提示按进程，`internal/agent/dsh/preset.go`）。

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
9. （已做，`internal/join`）`ir join <地址>`：二进制的执行端模式，复用 `internal/remote/executor`，配对、存 TOKEN、常驻（见“平台差异”）、找 agent（npm 的 `.cmd` 垫片解析成 `node` + 入口 js）。Windows 先做。

## 暂不做

发现那边在终端里自己开的会话（扫进程）；那边的 Codex、pi（Claude 先做）；目录传输。
