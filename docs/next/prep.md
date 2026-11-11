# 0.2 重构准备报告（对照 HEAD 02a9adc / v0.1.41）

除 Satori 规范外，下面的说法都在当前树上 grep 核对过。Satori 规范的副本在 `/tmp/satori-spec.3166934/`，核对的是 satorijs/docs@50fee9ef。草案是 v0.1.40 合入前一分钟写的，所以 v0.1.38–0.1.41 加的东西它都没写进去：`ir attach`、Bot 跟随面板、远端工作区、@ 位置、用量页。工作区里还有另一个会话没提交的改动：`docs/exec.md`、`scripts/dev/exec-mini.py`（+374/−156）。

---

## 1. 文档与代码的出入

k_bot.md 只拿来当“现有、要删或要迁的”清单。下表只列会让清单漏项或误导的地方，最重要的在前。

| 文档 | 原文 | 实际 | 出处 |
|---|---|---|---|
| k_bot 附录·文件 | Bot 能 `GET /api/files`、`POST /api/files/copy`、`POST …/attach` | 这三条只在 unix socket 上注册，TCP 上回 404，面板也调不到 | internal/server/routes_files.go:61-66 |
| k_bot 附录 | 号称“逐个查了注册的路由” | 漏了 `POST /api/links`（`ir attach`，只走 socket）、`PUT /api/endpoints/{id}/profile`、gate 的 `/_ir/*`（`/_ir/avatar/{id}` 凭 TOKEN 也能取） | routes_files.go:63；routes_terminals.go:29；gate/routes.go:48-104 |
| k_bot §1 Claude | Bot 只有两种情况能带 Claude | 还有第三条路：官方 Bot 扫码接入时，`extBind` 用 `ownerSays` 自动给 Claude | server/extensions.go:364,429,494 |
| k_bot §1 只有面板能做的 | 一张“Bot 回 403”的表 | 实际分三档。① `manage/manages`＝`CanManage`，不排除 agent：面板开的会话里，agent 的 `ir` 也能删终端、改便签、跑 `ir sh`。② `panelOnly`＝`!agent && (panel‖Local)`。③ 只走 socket。草案七的“agent 不直接行使管理”要从②的语义改起 | gate/gate.go:141；server/access.go:40,79-93；routes_terminals.go:56-75 |
| k_bot §1 / §2 | QQ 桥的 TOKEN 由配对得来 | `iridium-qq setup` 是在本机跑 `ir token <名> --owner --into`，所以只能装在主机上，而且带 Claude。收到 401 不删 TOKEN，只是每分钟重试 | cmd/iridium-qq/main.go:284-291；qqbridge/iridium.go:31-33；bridge.go:297,425 |
| k_bot §2 答卡片 | 能回 `1A`、`答1：…`、`拒绝：理由` | 只认选项名（多题用逗号等分隔）；`CardAnswer.Words/Message` 没用上 | qqbridge/bridge.go:634-665 |
| k_bot §4.3 | card 有 `questions[]` | 没有这个字段，题目原文放在 `summary` 里 | api/control.go:130-139；session/cards.go:37-41 |
| k_bot §4.3 | reply.files 只来自 `[文件]` 行 | 还收 `ir:<id>` 文件链接，而且这类文件不查受保护目录 | chat/files.go:79-96；files/links.go:241-279 |
| k_bot §3.2 / §4.2 | 提问卡发给跟随者；`ir bot` 发给“指着的”聊天 | 实际是三段：先给指针指着的聊天（只发排第一个）→ 再给跟随者 → 再按 reach 挑一个。另外，会话不在列表里时不发卡（v0.1.40 起） | chat/letters.go:133-161,349-352 |
| k_bot 附录 | `/api/bot` 回 403 | 回 400：“ir bot runs in a session” | routes_plugin.go:98-101 |
| k_bot §3.3 | hello 的字段 | 少写了 `arch`、`sep`、`scratch`；另外 30 秒内必须发 hello，`protocol` 必须等于 1 | remote/wire/proto.go:53-77；remote/hub.go:111-145 |
| draft 五 | “现在带 `os` 就取系统名” | 申请里给的名字合法就用它（截到 12 字）；不合法才按 os 取 windows/mac/linux，再没有是 `machine`（Bot 配对改成 `bot`）。`ir join` 默认送主机名，`ir pair` 必须带 `--name` | routes_pair.go:232-245,98-101；join/join.go:375-380 |
| draft 二·远端执行 / 三 | “执行端协议的 `run`” | 协议里没有 `run`。现在的 `ir sh` 是 hold 一个 `sh-<id>` 进程，再轮询 `GET /api/shell/{run}`；网页上主机会话的 `$` 也走同一条轮询 | session/shell.go:87-114；routes_shell.go:20-23；web/src/chat/shell.tsx:60 |
| draft 二·要删的 | “`/api/chat/*` 整个聊天层（指针、跟随、letter、Bring）” | 跟随是 `PUT /api/terminals/{id}/follows`，letter 是 `POST /api/bot`，Bring 是 `POST /api/sessions/{id}/bot`，插件事件流是不带 `sub` 的 `GET /api/events`。还有 4 张表和 4 个字段（`Terminal.Bot/Chatless/Follows`、`Session.Bots`）要删 | routes_plugin.go:49-59；routes_terminals.go:34；server.go:398-401；store.go:99,126,163,184 |
| draft 六 | 只有 pick 卡片只在内存里 | `away`（跟随者收到的提问卡）、`origin`（回合发起链）、`held` 也只在内存里，重启后 card_done 送不到、发起者也追溯不回去 | chat/chat.go:115-120；letters.go:363-399 |
| draft 六 | “命令结果一部分同步、一部分走事件流” | Iridium 自己的命令全部同步回答；走事件流的是后续结果（agent 回复、透传的斜杠命令、Bring） | chat/commands.go:20-21；letters.go:141,159,184 |
| draft 三 | `status`、`progress` 没人用 | `examples/plugin/echo_im.py:92` 处理 progress | examples/plugin/echo_im.py:92 |
| k_glossary 主机 | 本机“只代表 socket 上的 `ir`” | 没有公网地址时，所有未认证请求（包括 127.0.0.1 上的网页）都算本机。反过来，会话里 agent 跑的 `ir` 算开这个会话的终端 | gate/gate.go:392；server/access.go:79-93 |
| k_glossary 会话 TOKEN | 权限“不超过发起登录” | 没实现。`irs_` 只发给报了 `irClient` 的执行端，每次进程启动换一个，按那台机器的 Bot 身份办事；主机上的 agent 走 socket 头，按开会话的人算 | gate/gate.go:405-421；session/remote.go:128-137 |
| k_glossary 位置 | 代码里只有 `机器:路径` | 有两种。CLI 输入是 `机器名:路径`（ASCII）。内部分组和布局存的是 `机器ID:路径`：workspaces、folder_order/pins、group_order、session_at、notes.root、hold meta 都用它 | cli/client.go:238；remote/fs.go:65-71 |
| k_glossary 需要 | 提问“按跟随送达”，申请在 confirms | 有指针时，提问整条发给指着的聊天。confirms 里不止配对：还有 invite、token、secret-set、secret-use | chat.go:449-467；api/terminals.go:149 |
| k_glossary 用词 | `channel`＝会话，`login`＝登录 | 代码里这两个词已经有别的意思：`gate.KindChannel`＝TOKEN 终端；`logins` 表和 `Terminal.Logins` 是面板的 cookie 会话 | gate/store.go:20；store/terminals.go:46-54 |
| k_glossary 分享 | 网页上叫“拉进会话” | 网页文案是“把 X 切到此会话” | web/src/i18n/zh.ts:64,280 |
| 仓库文档 | `docs/exec.md:131`、`docs/plan.md:151` 引用 `docs/k_bot.md` | 仓库里没有这个文件，引用是悬空的 | HEAD 上 `git show` 可见 |
| docs/api.md:10 | `types.gen.ts` 不进库 | 它在库里（`git ls-files` 能看到），CI 也不查它是不是最新 | — |
| docs/exec.md:131 | （k_bot §5 说“提及即授权”还在） | v0.1.40 已经删掉 | — |

---

## 2. 改动面

LOC 是各区影响图的估计，已经粗略去掉重叠。

### 2.0 Bot 清单与去向（全部删除或映射，不保留 Bot 专有行为）

| 现有 | 去向 |
|---|---|
| `gate.KindChannel` / `api.TerminalChannel`、`Kind` 决定权限 | 改为应用；`Kind` 只剩显示用的类型 |
| `Terminal.Bot/Chatless/Follows`、`Session.Bots`、Here 头像 | 删 |
| `bot-pair`、`makePairedBot` 的 `-2` 后缀、`/api/exec/pair` | 合进 `/api/pair`，重名走“更新配对” |
| `ir bot`、`POST /api/bot`、`--to` | 并入 `ir send 应用/登录` |
| `POST /api/sessions/{id}/bot`（Bring）、“把 X 切到此会话” | 改为分享 |
| `bot_follows`、`PUT …/follows`、跟随 UI | 删 |
| 官方 Bot 的 `/api/extensions`、`SyncExtensions`、`iridium-qq bind`、`ir token --owner --into` | 改为收录；QQ 桥走配对、自己更新 |
| 文档“Bot 接入”“官方 Bot”、GLOSSARY 的 Bot、irhelp 里的 bot | 重写 |

### 2.1 接入 + 模型（应用/登录/主机）
- **重写**：
  - gate：store.go 266、admin.go 385、gate.go 765（改约 220）、routes.go 485（约 130）、names.go、profile.go、web/gate.js。
  - server：routes_pair.go 279、routes_terminals.go 563、access.go 394（约 160）、confirms.go 167。
  - 其它：api/terminals.go 291、store/terminals.go 368。
  - CLI：cli/admin.go 668（约 180）。
  - QQ：cmd/iridium-qq/main.go 850（约 380）。
  - 网页：terminals.tsx 465 改成 apps.tsx（约 650），confirms.tsx 352 改成 needs.tsx（约 420）。
- **删**：server/extensions.go 588、extensions_test.go 的大部分（574）、api/extensions.go 56、web/extensions.tsx 150、`update.Updater.Extension`（update.go:354-~400）。
- **新**：收录表 catalogue.go（约 90）+ catalogue.tsx（约 90）、appdetail.tsx（约 450）。
- **不动**：internal/install（那是 daemon 自己的 unit）、web/src/install.ts（PWA 安装提示）。
- **DB**：
  - 旧 `logins`（cookie）先改名为 `web_sessions`。
  - 面板行变成 ir_web 的登录，ID 沿用。channel 行变成应用，ID 和 `token_hash` 不变。
  - `invites` 加权限列，变成登录空位。
  - `permissions` 拆成应用和登录两份。
  - `devices` 归到登录。
  - `local` 这个 ID 保留存储，只把显示改成“主机”。
- **配置**：`pairClosed` 碰上 `DisallowUnknownFields`（config.go:127），直接删字段会让旧配置文件启动失败。
- **坏的测试**：
  - gate：gate_test 898、profile_test 343。
  - store：terminals_test、owner_test 的降级列表（:25）。
  - server：routes_pair_test 327、terminals_test 423、claude_test、owner_panel_test、join_test。
  - 其它：cli/admin_test、owner_test；qqbridge 的 TestTokenRefused、TestFreeName；web 的 e2e.mjs、shoot.mjs。
- **外部使用者**：iridium-qq、dsh 插件（index.js:62-112，executor.js:24-27）、`ir join`/`ir pair`、exec-mini.py、shell-bot.mjs。

### 2.2 带 Agent + 位置 + 远端执行
- **改**：
  - 协议和执行端：wire/proto.go 353（约 90）、remote/hub.go 330（约 110）、executor.go 874（约 170）、executor.js 660（约 130）、exec-mini.py（约 70，WIP 冲突）。
  - server：machines.go 152 重写、routes_shell.go、routes_files_remote.go、folders_remote.go、discover/remote.go（`dropGone` 要加保护）。
  - session：shell.go。
  - CLI 和执行端安装：cli/shell.go 132 重写、client.go、sessions.go、join.go/resident.go（固化在 unit 里的参数）、irhelp。
  - 网页：create.tsx、sidebar.tsx、at.ts、refs.ts、mention.tsx、machines.tsx。
- **新**：remote/run.go（约 120）、`machineid_*.go`（约 80）、internal/place（约 140 + 测试 150）。
- **DB**：`machines`（迁移 15）并进应用；`noShell/agentShell` 映射成 `shell`；新增按应用的 agent 开关。位置键保持按 ID 存。
- **坏的测试**：executor/noshell_test、routes_pair_test（:61,79,197,206,269）、session/remote_test、cli_test TestParse、join_test TestResident；web 的 at、where、create、fold、logic、subfolder 测试。
- **外部使用者**：Mac 的 mac-shell（shell-bot.mjs 在 Mac 本地的 checkout 里 import executor.js，写死了 `agentShell:true`）、npm 上的 dsh 插件、远端 `ir join` 常驻进程（参数固化，二进制不自更新：internal/join 里没有 updater）。

### 2.3 权限
- **重写**：access/access.go 65 改成 Perms（约 250）+ place 包含判断（约 200）；server/access.go 约 300。
- **加检查的路由**：
  - 现在完全不查的：`PATCH /api/sessions/{id}`（routes_discover.go:92）、`GET presets/rules/image/messages`、`files/{ref}`、`/api/folders?machine`、`dsh/*`、`uploads/{upload}`、socket 上的 `links/files/copy`。
  - 现在按 `manages` 查、要改成按范围查的：便签、配色、组排序、摆放。
- **还要改**：
  - hub：接上 `Sub.Filter`（hub.go:136，现在从没设过）。
  - store：`SetGroupOrder` 现在整表替换，要改成合并（store/order.go:23）。
  - 凭据：routes_secrets.go 464（约 150）。
  - session：hosted.go、session.go 记发起登录。
- **DB**：`claudeOwner` 换算成 agents 列表；迁移来的应用范围设为“全部”；inputs、usage、openers 各加 `login` 列；`secrets.scope='owner'` 重新定义。
- **坏的测试**：access_test、claude_test、owner_panel_test、terminals_test、secrets_test、routes_notes/colors/discover_test、usage_test、cli owner/secrets/admin_test。还要新写一张权限矩阵测试。

### 2.4 推送 / 消息（删聊天层）
- **删**：internal/chat 共 2,302 行（非测试 2,073）、routes_plugin.go 300、store/follows.go 54、store/more.go 里聊天那段（:119-237）、api/chat.go 90、server 的 plugin_test 409。
- **留下来搬走的**：
  - `onNotice` 的回合文本逻辑（chat.go:405-468）。
  - 回复渲染（chat/files.go 174）。
  - QQ 的命令、目录卡、`/history`（约 1,300 行，搬进 qqbridge）。
- **新**：
  - 事件生产和按登录分发（约 380）。
  - “需要”队列，替代 confirms（约 220）。
  - `send`、`share`、公开的应用和登录列表、agent 自带命令接口（约 300）。
  - 发起登录在 session.Manager 里持久化。
- **改**：Web Push 改按登录投递（push.go:376,400-426 现在发给所有订阅）。
- **DB**：
  - 删 `chat_pointers`、`chat_seen`、`bot_follows`、`outbox`。删 `bot_follows` 必须和 store/terminals.go:215-217 的删除语句同一版去掉，否则所有撤销都会失败。
  - `push_subscriptions` 改按登录；Bot 订阅的行删掉。
  - `chat_pointers` 导出给 QQ 桥。
- **外部使用者**：iridium-qq 的全部接口、echo_im.py、`ir bot`、网页 `client.ts`（answer、bringBot、push、follows、confirms）。

### 2.5 Satori with ir
- **新**：internal/satori 的 proto、ws、api、message、channel、login、resource、elements、events，约 2,000 行，测试约 1,400。
- **改**：
  - gate：Wrap 要放行 `/v1/events` 的 upgrade，抽出 `AuthenticateToken`，并让 IDENTIFY 也受限流。
  - webui.go:172 现在只把 `/api/`、`/_ir/` 交给 API，要加 `/v1/`。
  - vite 代理加 `/v1`。
  - hub 抽出 sink，供 WS 使用。
- **QQ 桥**：iridium.go 269 重写，bridge.go 754（约 500）。
- **草案的 800–1,200 行**只算了 server 端的转发，没算 WS 信令、元素编解码、`BidiList`、代理、`channel-*` 差分、QQ 桥和网页。

### 2.6 网页 + 文案 + 文档
- **网页**：sidebar 1337（约 160）、said.tsx 和 files.tsx 新增分享菜单、stream.ts（换 WS 才重写，约 220）、usage.tsx 966（约 90）、route.ts 要把 `#/settings/terminals` 重定向、types.gen.ts（生成，约 600）。i18n 约 120 个键 ×2，zh/en 键一致由测试守着（logic.test.ts:511-515）。
- **文案**：irhelp.Help 正好 20 行，测试卡上限（cli_test.go:42）；session.go:48-51 的系统提示让用户“在网页上 `ir secret set`”。
- **文档**：api.md 约 300/475 行、plan.md 约 180、exec.md 约 80、gate.md、qq.md、GLOSSARY.md 全部重写；新写 satori.md。

### 2.7 合计（粗估 ±30%）

| 区域 | 删除 | 新增/改（代码） | 测试 |
|---|---|---|---|
| 接入+模型 | ~1,100 | ~3,300 | ~2,000 |
| 带 Agent+位置+run | ~200 | ~2,300（Go 1,700 / 网页 400 / 插件和脚本 250） | ~700 |
| 权限 | — | ~2,400（Go 1,800 / 网页 600） | ~1,200 |
| 推送/消息（聊天层） | ~2,600 | ~1,300 daemon + ~1,500 QQ 桥 + ~500 网页 | ~1,500 |
| Satori with ir | （和上一行是同一批删除） | ~2,300（含 hub→WS） | ~1,400 |
| 网页文案+文档 | ~150 | 网页 ~1,500 + types.gen ~600 + 文档 ~1,000 | ~300 |
| **合计** | **~4,000** | **~17,000** | **~7,000** |

---

## 3. 计划里要先定的事

标 **[B]** 的是阻塞项：不定就没法动手，或者会返工。

**模型与身份**
1. **[B] `login.user.id` 的格式**。草案写“`应用/登录`”，但 Satori 的 `internal:{platform}/{user.id}/{path}` 不许 `user.id` 里有 `/`；而且 login 必须带数字 `sn`，satori.js 按 `login.sn` 认 bot。建议：`platform`＝应用 ID（稳定、ASCII），`user.id`＝登录 ID；`login.sn` 每条连接分配；`应用/登录` 只在 `ir` 和网页上显示。
2. **[B] ir_web 的一个登录是什么**。建议：一个登录就是今天的一个面板（一套 passkey），ID 沿用面板 ID。同步 passkey 的几台设备算同一个登录，不拆。
3. **[B] 只用本机（没设公网地址）时，浏览器算谁**。建议照旧算主机，不建 ir_web 登录。ir_web 只在有公网地址时存在。“网页可以关”只能在主机的 CLI 上关。
4. **存储 ID**。建议：应用 ID＝旧终端 ID，主机的存储 ID 仍是 `local`，只改显示；位置键内部继续用 `ID:路径`，名字只在输入输出时换。这样布局表不用迁移，改名也不会断。
5. **保留名**。建议保留 `host`、`主机`、`本机`、`ir_web`，并禁止单个 ASCII 字母当名字。迁移前先扫一遍现有名字，冲突的手工改。
6. **新登录的名字（草案五）**。建议：申请里给的名字合法就用；不合法回 422 `bad_name`；重名只能选“更新配对”，或者批准人在卡片上改名再批。不再自动生成默认名，`ir join` 继续默认送主机名。

**接入**
7. **[B] 无界面的主机怎么开配对窗口**。QQ 桥的 setup、`ir join --install` 都没有网页可点。建议加 `ir apps open [10m]`（只有主机的人能用）和 `ir needs`/`ir allow`。
8. **[B] 401 和 403 的含义**。dsh 插件、exec-mini.py、shell-bot 现在把 403 也当成 TOKEN 被撤销。建议：只有 401 表示 TOKEN 作废；窗口关闭、登录未批、没有权限一律 403 加原因码（`pair_closed`、`login_pending`…）。0.1.x 先把客户端改好。
9. **`ir token` / `POST /api/terminals/tokens` / `--owner` / `--into`，以及 agent 申请的 token 类确认**。建议全删，配对是唯一入口。
10. **旧的 `/api/exec/pair`**。建议不留别名。保住 `token_hash` 之后，已配对的执行端不需要重新配对；旧客户端只有重新配对时才会碰到 404。
11. **更新配对可能被冒名**。建议卡片上并排显示旧应用和申请方的系统、程序、最后在线时间；旧应用在线时加警告。用量和权限沿用旧的，不采用申请里要的。
12. **登录加入**。建议：一个新的 `Satori-User-ID` 第一次 `message.create` 时触发；这条消息回 403 `login_pending`，不暂存；登录行的 `approved_at` 为 NULL 就算待批（自然持久）；每个应用最多 20 个待批。现有的 QQ 私聊在迁移时预先建成已批准的登录。
13. **QQ 端的主人和配对码**。建议 0.2 先留着，作为应用自己的前置过滤，防止陌生人刷出一堆加入申请。

**权限**
14. **[B] 凭据“只有主机能用”**。这和网页的凭据页、secret-set/secret-use 卡片、系统提示（session.go:50）都冲突。建议第一阶段改成“主机和有管理的登录”能列、设、删；“用”仍然只走主机 socket。
15. **[B] 第一阶段谁能批申请**。如果“网页的任何登录都能批”，一个被收窄的登录可以给自己放宽。建议：只有主机，以及权限为“全部”的 ir_web 登录能批；agent 的 `ir` 一律不能批。现在 `CanManage` 放行 agent，要一起修。只有主机的人或能用 Claude 的登录才能授予 Claude，并记下谁、什么时候（保留 `OwnerConfirm.By/At`）。
16. **不允许的 agent 的会话**。建议仍然能看（只读），不能开、发、答、归档、改名；只有范围才决定看不看得到。
17. **默认范围**。建议：迁移过来的应用＝全部（行为不变）；新的带 Agent 应用＝自己的家目录；新的不带 Agent 应用＝全部。登录 ⊆ 应用在检查时取交集，不在写入时裁剪。
18. **[B] 会话 TOKEN 不超过发起登录**。建议：发起登录在回合开始时定下并存进 `inputs.login`；顺着 via 链找到第一个人的登录；没有人的回合（主机 agent、自动续跑）发起者是主机。主机 agent 走 socket 头可以伪造，文档里写明“不是沙箱”。
19. **共享布局**。组排序改成局部合并；`folder_colors` 的键统一成位置形式；`root` 为空的便签只有范围为全部的能看。

**带 Agent / 远端执行**
20. **[B] `run` 的形状**。建议：执行端收 `{line, cwd, env?}`；边跑边推 `run.out`；`run.stop` 停止；断线就杀进程。server 端出 NDJSON 流，和 `/api/cli` 一样。用 cap 协商：执行端没报 `run` 时退回 hold 方式，过一个版本再删。`GET /api/shell/{run}` 留给主机网页的 `$`（草案说“先不管”，又把它列进要删的，两处矛盾）。
21. **[B] `--shell` 的语义和旧值映射**。现在是三档：全关、网页 `$`、再加 agent 的 `ir sh`。建议：`shell` 同时管远端网页 `$` 和 agent 的 `ir sh`；旧 hello 按 `agentShell && !noShell` 映射（偏收紧，和“默认关”一致）。dsh 插件 `shell` 默认是 true，升级后远端网页 `$` 会关掉，发布说明要写。`ir join` 继续接受旧参数，否则常驻进程会一直重启（参数写错退出码 2，join.go:86-93）。
22. **怎么认出“主机上的应用”**。建议：电脑标识（machine-id 等）取哈希放进 hello；对上了就去掉它的 agents，连接不拒。测试和 ir-test 要能用环境变量覆盖，因为它们和主实例在同一台机器上。
23. **机器报告放哪**。建议放在 exec 的 hello 里，exec 链路单独保留，不放 Satori 的 IDENTIFY。不升 `wire.Protocol`：一升，所有旧执行端会立刻被拒（hub.go:127）。
24. **语法冲突**：`ir send 应用/登录` 和现在的 `机器/<id>` 会话写法（cli/client.go:275）撞了。建议去掉 `机器/<id>` 这个写法。

**推送 / 消息 / Satori**
25. **[B] 网页这次换不换到 WS**。草案三说“其余照旧是我们自己的接口”。建议 0.2 里 ir_web 继续用 hub 的 SSE（`?sub=session:` 本身就是“正在看”），WS 只给应用；以后再迁。这样能省掉 stream.ts 重写和 hub sink 这块风险。
26. **[B] 临时帧和 sn**。satori.js 把每条 EVENT 的 sn 记成续传点。建议：草稿、状态、“正在看”不发成 EVENT，用自定义 op（≥6）。存库的只有 `message-created`、按钮、`channel-*`；sn＝outbox 的 seq；保存 24 小时；`OutboxAfter` 要加分页。
27. **[B] 卡片怎么答**。Satori 里 `interaction/button` 的方向反了：是平台用户点 bot 发的按钮。建议：Iridium 发 `<button type="action" id="ask:…">`，应用点了以后调 `POST /v1/sessions/{id}/answer` 或 `/api/sessions/{id}/answer`（扩展，body 用 CardAnswer）；同时支持 `type="input"`，回文字也能答。
28. **错误码**。404 只表示接口不存在；资源不存在回 422 带原因码。但规范规定：未知的 `Satori-User-ID` 回 403，代理按 400/403/404（resource.md:130-138）。这几处要单独列出来。
29. **QQ 的显示策略**（替代跟随）。建议默认显示：自己发起的回合，加上当前会话里的提问；可以按登录设置。否则网页发起的回合，提问在 QQ 上就看不到了。
30. **`progress`、`status` 去留和 `message-updated` 的来源**。建议删 `status`；`progress` 发成 `message-created`；回答过的提问发 `message-updated`。echo_im.py 重写。
31. **上传**。建议网页继续用分块的 `/api/uploads`（Cloudflare 有单请求体积上限）；`upload.create` 只收单个 multipart，额外带 `channel_id`。
32. **Web Push**。建议路由改到 `/api/web/push/*`，只给 ir_web 登录用，保留 VAPID 和两张表。现在任何 TOKEN 都能改开关、订阅自己的地址，这是安全漏洞，在 0.1.x 就修。

---

## 4. 迁移与发布

**数据库和配置**
- 一次 append 迁移：26 号起。`migrate` 在单个事务里跑（store.go:263-285），失败会整体回滚。但打开比自己新的库会被拒（:269-271），而 updater 只是把新二进制 rename 覆盖上去，不留旧版本和库备份。0.1.x 要先加两样：
  - 升 schema 前 `VACUUM INTO iridium.db.v<N>.bak`。
  - 留一份 `iridium.prev`。
- 迁移测试用合成的 v25 库，不碰两个实例的数据。
- `pairClosed`：在 0.1.x 就让它成为可忽略的旧字段。

**TOKEN 和身份**
- `token_hash`、ID、`ir_`/`irs_` 前缀都不变，现有 QQ、dsh、`ir join` 的 TOKEN 照常能用。
- 面板变成 ir_web 登录，ID 不变；旧 `logins` 表改名并沿用，浏览器不用重新登录。

**执行端**
- **`ir join`**：远端二进制不会自己更新。所以协议版本不升，新字段只加不改，旧参数继续接受；“主机上的应用”命中时只去掉 agents，不断开。
- **dsh 插件**：npm 只在正式版发布（release.yml:58-59），用户自己更新。所以 0.1.x 就要发出“只认 401”的版本，加上 `run` cap。
- **mac-shell（唯一能进 Mac 的路）**：
  1. 0.1.x 发布带 `run` 的 executor.js。
  2. 用现在的 `ir sh mac-shell` 更新 Mac 上的 checkout，并重启 shell-bot。
  3. server 改用 `run` 时保留 hold 回退。
  4. 至少一个版本以后再删 hold 路径。

**QQ 桥**
- `SyncExtensions` 删掉以后，就没有人替它升级了。0.1.x 的最后一版要做到：
  - iridium-qq 按 daemon 的 `/api/health` 版本自己更新（复用 `update.Extension` 的下载逻辑），版本不一致时拉同版本资产并重启自己的服务。
  - 收到 401 就删 TOKEN、重新配对。
- 0.2 迁移时：现有私聊预先建成已批准登录；`chat_pointers` 导出给桥的 state.json；桥的 `LastEvent` 清零。
- `push-test.sh` 里 `iridium-qq.dist` 的分发靠扩展安装，要改成直接装。

**更新器和发布节奏**
- updater 读的是 `releases/latest`，GitHub 的预发布不算 latest；`*-*` 版本也不发 npm。所以 `v0.2.0-rc.N` 到不了主实例，可以放心在测试实例上验。
- 资产名不变，0.1.x 的 updater 能直接装上 0.2。发布说明要写清：先更新 dsh 插件，QQ 桥会自己升级，`ir join` 不用动。
- 回滚办法：`.bak` 库加上 `iridium.prev`。

**在测试实例上走一遍**
1. 停下来的 ir-test 先备份 state 目录（用 `iridium push` 的 ssh key，在 ir 账户下做）。
2. 用 `scripts/push-test.sh` 推上 0.2 分支。
3. 依次接上并验证：
   - 本机的 `ir join`：带主机标识覆盖，验证 agent 剥离和覆盖开关。
   - 树里的 dsh 插件、shell-bot、exec-mini：配对窗口、403 时等待、401 时重新配对。
   - 测试用 QQ Bot：自更新、预建登录、登录加入、按钮作答。
   - 网页的 passkey 登录：迁移后不用重新登录，权限矩阵正确。
4. 验证：ir-test 上已有的面板、TOKEN、布局、便签、用量迁移以后都对得上；撤销应用、关闭窗口、重启后续传都正常。
5. 打 `v0.2.0-rc.1`（预发布）检查发布流程，再打 `v0.2.0`。主实例由用户在设置页手动更新。

---

## 5. 动手顺序

规则：0.1.x 照常发补丁；破坏性的部分在 `next` 分支上分步做，每一步都要 `go test ./...`、`go vet ./...`、`npm run check` 全绿，推到 ir-test 验。

**0. 协调**：用 `ir ls` 找到 `docs/exec.md` 和 `exec-mini.py` 这两处 WIP 的主人，先让它们落地或搁置。

**P1 安全网（0.1.x，几乎不改行为）**
- `pairClosed` 可忽略，加一份旧配置文件的测试。
- dsh 插件、exec-mini、shell-bot 只认 401。
- 升 schema 前备份库，留 `iridium.prev`。
- 修几个漏洞：`/api/push/*` 加权限检查；撤销时调 `DeleteTerminalPush`；插件 SSE 也接 `gate.Track`；聊天的 input 和 answer 回答加 `s.masked`。
- CI 加 `types.gen.ts` 是否最新的检查。
- 修文档：api.md:10、悬空的 k_bot 引用。
- 测试：各自加单测；网页 `npm run check`。

**P2 摸底**
- 路由注册表：每条路由声明检查类别，没声明就测试失败。
- 现有权限矩阵的特征测试。
- 聊天层事件的 golden 夹具。
- `ir sh` 的 HTTP 和 CLI 特征测试。
- v25 库夹具生成器。
- 网页的特征测试：`sourceOf`、位置、`cmd.ts`。

**P3 中性重构**
- `logins` 改名为 `web_sessions`。
- Go 的 `internal/place` 和网页的 places 模块。
- confirm 种类改成常量。
- 抽出配对状态机，留一个 `open?` 谓词。
- 统一的 `s.allow` 入口。
- hub：sink、`Watch`、`Filter`（直通）。
- `onNotice` 改成纯函数；回复渲染搬到 internal/files；QQ 用的辅助函数抽出来。
- extensions_test 里无关的测试搬走；`newApp` 测试 helper。
- 网页：`useApps` 门面、stream 的 Link 接口、组件拆分和改名（带重定向）。

**P4 只加不删（0.1.x）**
- 发起登录持久化：`inputs.login`、`usage.login`、`openers.login`，加 `TurnOrigin`。
- `/sessions/{id}/answer` 接受 CardAnswer，网页切过去。
- 资料路径合成一条。
- `GET /sessions/{id}/commands`。
- `run` cap 进三个执行端，然后更新 mac-shell。
- hello 加 `shell` 和机器标识字段（server 暂不使用）。
- `ir join` 接受 `--shell`。
- iridium-qq：自更新、401 删 TOKEN、换传输用的接口层。
- pick 卡片存库。
- 修 `Hub.Find` 用的是连接时的名字、幻影机器、`dropGone` 没有保护。
- 加 OpenAsks 状态。

**S1 模型+接入（next）**
- 迁移 26+：应用、ir_web 登录、主机。
- `Identity.Login`；合并的 `/api/pair`、配对窗口、更新配对、`ir apps open`。
- 收录表；删 extensions。
- 改 CLI 的 admin 命令；网页的应用管理和需要卡片。
- 测试：基于夹具的迁移测试、配对的新用例。

**S2 权限**
- Perms 换掉 Rules，`s.allow` 换语义，hub 按登录过滤，凭据和 Claude 的授予规则。
- 测试：矩阵的期望值整体翻过来。

**S3 带 Agent 和位置**
- `machines` 并进应用；agent 开关；主机标识；`shell`。
- `ir sh` 改走 `run`（保留 hold 回退）；输出一律带前缀。

**S4 消息和 Satori**
- 事件生产，加上 `/v1` 和 WS。
- 需要队列；`send`、`share`。
- 删聊天层、跟随、指针；QQ 桥改成 Satori 客户端。
- 测试：握手一致性、错误码、元素编解码 fuzz、续传。

**S5 收尾**
- irhelp、系统提示、docs、GLOSSARY；接着走 rc 和 ir-test 那套流程（第 4 节）。

---

## 6. 现在就能做的准备
- [ ] 协调 `docs/exec.md` 和 `scripts/dev/exec-mini.py` 的 WIP
- [ ] 让 `config.Load` 能读带 `pairClosed` 的旧配置，加测试
- [ ] dsh 插件 executor.js:27、exec-mini.py:94,142、shell-bot.mjs 改成只有 401 才算作废
- [ ] iridium-qq：收到 `ErrToken` 就删 TOKEN；按 daemon 版本自己更新
- [ ] 升 schema 前 `VACUUM INTO` 备份；updater 保留 `iridium.prev`
- [ ] `/api/push/*` 加权限检查；撤销时清 push 订阅；插件 SSE 接 `Track`；聊天回答加 `masked`
- [ ] CI 检查 `types.gen.ts` 是否最新；修 docs/api.md:10 和悬空的 `docs/k_bot.md` 引用
- [ ] 路由检查类别注册表和测试；现有权限矩阵的特征测试
- [ ] v25 库夹具生成器（合成数据）；聊天层事件 golden；`ir sh` 特征测试
- [ ] `logins` 改名 `web_sessions`；confirm 种类常量；`newApp` 测试 helper
- [ ] `internal/place`（Go）和网页 places 模块，共用一张夹具表
- [ ] 配对状态机抽出来；`s.allow` 统一入口；hub sink、Watch、Filter
- [ ] `onNotice` 纯函数；回复渲染搬到 internal/files；QQ 命令和目录辅助函数抽出
- [ ] `inputs.login`、`usage.login`、`openers.login` 三列和持久化的 `TurnOrigin`
- [ ] `/sessions/{id}/answer` 收 CardAnswer，网页切过去；资料路径合成一条
- [ ] 三个执行端加 `run` cap，然后更新 mac-shell；hello 加 `shell` 和机器标识；`ir join --shell`
- [ ] 修 `Hub.Find` 的旧名字、幻影机器、`dropGone` 的保护
- [ ] 用只读方式扫两个实例的终端名：单字母、host、主机、ir_web
- [ ] 把第 3 节的 [B] 项交给 kumo 拍板，写进 draft；新写 docs/satori.md，写明规范版本 50fee9ef 和扩展点
- [ ] 改 `push-test.sh`，不再依赖扩展安装来分发 iridium-qq
