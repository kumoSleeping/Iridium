# 0.2 改版进度

每一步做完在这里写一段：做了什么、和计划不一样的地方、留给后面的。

## P1 安全网（不改行为）

做了什么：
- 配置：`config.legacy` 列出旧字段（`pairClosed`、`chatDir`、`update.manual`）。`Config` 里没有这个字段时，解码前把它从文件里去掉，其余未知字段照旧报错。所以 S1/S4 删字段时，旧配置文件照样能读。`Update.Manual` 已经从结构里删掉，改由这张表处理。测试用 `internal/config/testdata/v0.1.41.json`。
- 库：要升 schema 时（版本 > 0 且低于当前），先 `VACUUM INTO` 复制成 `<db>.v<N>.bak`（0600，先写临时文件再改名）。已有同名备份就保留。
- 更新：`Apply` 把换下的二进制留成 `<exe>.prev`（即 `iridium.prev`），先试硬链接，不行就复制。留不下就不更新。
- 推送：`/api/push/*` 全部加上 `panelOnly`，TOKEN 和 agent 的 `ir` 回 403。撤销终端时 `DeleteTerminalPush`。
- CI：新增 `scripts/check-types.sh`，重新生成 `types.gen.ts` 并比对；ci.yml、release.yml、preflight.sh 都跑。修了 api.md 里“生成的文件不进库”的说法；exec.md、plan.md 改指 `docs/next/k_bot.md`。

和计划不一样的地方：
- 推送用的是现有的 `panelOnly`，没有按决定 32 改到 `/api/web/push/*`。路由改名放到 S1/S2。
- 旧字段表顺带收了 `chatDir`（S4 删聊天层时会去掉）。

留给后面的：
- 按范围没做：执行端只认 401（exec-mini、shell-bot、dsh 插件）、QQ 桥自更新，以及 S4 会删掉的聊天层 `masked` 和插件 SSE 的 `Track`。
- 回滚目前只能手动：把 `.bak` 和 `iridium.prev` 放回去。没有命令。
- `.bak` 不会自动清理，每升一次 schema 多一个。

## S1a 模型：应用、ir_web 的登录、主机（存储、身份、CLI 管理）

做了什么：
- 迁移 26（`internal/store/apps.go`）：
  - 旧 `logins`（面板的 cookie）改名 `web_sessions`，列 `terminal` 改为 `login`。
  - 新表 `apps`：channel 行迁来，ID、`token_hash` 不变。`type` 只用来显示，有执行端记录的记为 `computer`。
  - 新表 `logins`：主键 `(app, id)`，`approved_at` 为空即待批，另有 `last_active`，`perms` 是占位列。面板变成 `ir_web` 的登录，ID 不变，`approved_at` 取创建时间。`terminals` 表删除。
  - 邀请加 `perms` 列：`--owner` 的邀请换成 Claude 的 Rules JSON，`owner` 列去掉。
  - 设备放进 `apps.device` / `logins.device`，`devices` 表只留主机的。
  - `push_subscriptions` 只留 `local` 和 ir_web 登录的。
- 夹具：
  - `store/fixture_test.go`：`openAt(path, n)` 建任意版本的库。`makeFixture` 造一个 v25 合成库，覆盖终端、权限、邀请、cookie、设备、跟随、执行端、用量、openers、inputs、secret_uses、推送、聊天层三张表、布局各表、便签和凭据。
  - `TestMigrateToApps` 验证迁移后：TOKEN 照常认证，浏览器 cookie 照常登录，用量和权限沿用，推送已清理。
  - 迁移 5 的测试也改用 `openAt`。
- gate：
  - `Identity` 改为 `App`、`Login`、`Name`、`Session` 四个字段，加方法 `Host()`、`Web()`、`ID()`（存储 ID）。
  - `HostID = "local"`，显示为“主机”；`WebApp = "ir_web"`。
  - 名字按范围唯一：应用之间不重名，ir_web 的登录和邀请之间不重名。保留名加 `host`、`主机`、`ir_web`；单个 ASCII 字母不能用（错误码 `letter`）。
  - `Find` 认应用名、`ir_web/登录名`、ID，以及主机的各个名字。
  - `Invite.Owner` 改为 `Invite.Perms`（服务端的 JSON），`Joined` 带 `Perms`。
- API：
  - 类型：`Terminal`、`Terminals`、`TerminalKind` 换成 `App`、`Login`、`Apps`；`Me` 平铺为 `app`、`login`、`host`、`web`；`NewInvite{name, claude}`；`Token{app, token}`；`UsageTerminal` 改为 `UsageWho`。删 `Confirm.Into`、`ConfirmReply.Token`、`Settings.pairClosed`。
  - 路由：`/api/terminals*`、`/api/endpoints/{id}/profile` 换成 `GET /api/apps`、`DELETE /api/apps/{id}`、`PUT /api/apps/{id}/{profile,device,follows}`、`POST /api/invites`。删 `POST /api/terminals/tokens`。
- CLI：
  - 删 `ir access`、`ir revoke`、`ir token`。
  - 新增 `ir apps [logins [应用]] | rename <应用> <新名> | rm <应用>`，登录写成 `应用/登录`。
  - `ir invite --owner` 改为 `--claude`；`ir profile` 只管头像和设备。irhelp 同步。
- 配置删 `pairClosed`，旧文件靠 legacy 表照读。配对暂时总是开放，S1b 换成开放窗口。
- 网页只改编译需要的部分：
  - 数据层：`useApps` / `useWhoList`，`Me.host` / `Me.web`。
  - 终端页：用应用和登录拼出各行，删掉“接收申请”开关。
  - 文案：“终端管理 / 面板 / Bot / 本机”改为“应用管理 / 网页 / 应用 / 主机”。
  - `scripts/e2e.mjs` 删了两段 token 步骤；`shoot.mjs` 改为发配对申请。
- 测试：
  - 名字规则：gate `TestNormalizeName`、`TestNameScopes`，server `TestConfirmations`，cli `TestAdmin`。
  - server 用 `newApp` 直接建应用（配对是唯一入口，测试走捷径）。
  - cli 用 `pairApp` 走真实配对。

和计划不一样的地方：
- gate 内部仍是一张 `Endpoint` 表：`Kind` 为 panel 的是 ir_web 的登录，为 channel 的是应用。只在存储和对外类型上拆开，S4 有了别的应用的登录再拆。
- 权限仍在 `permissions` 表，按存储 ID（登录 ID 或应用 ID）。`logins.perms` 只占位。
- `ir_web` 不是表里的一行，是内置应用：有公网地址或已有登录时才出现在列表里。
- 邀请带 Claude 用 `--claude`。决定 9 删的是 `--owner` 这个写法，实质是邀请带的权限。
- 用量 API 的字段仍叫 `terminals` / `terminal`，查询参数仍是 `?terminal=`，等 P4/S2 加 `usage.login` 时一起改。
- 主机的设备留在 `devices` 表，因为主机不是登录。
- 迁移不改已有的、不合新规则的名字（`host`、单字母等）。这些名字按名字找不到，要用 ID 改名：`ir apps rename <ID> <新名>`。

留给 S2 的垫片：
- `Identity.CanManage()` = 主机或 ir_web 登录。`caller.panel()` = 不是 agent，且是主机或 ir_web 登录。`ownerSays` 里，ir_web 登录有 Claude 即算 `panel`。S2 换成 Perms。
- `rulesOf(id)` 读 `permissions` 表。邀请的 `perms` 在登录加入时原样写进 `permissions`。
- `Confirm.Owner`、`ConfirmAnswer.Owner` 以及 `bot-pair` / `exec-pair` 原样留给 S1b。
- `makeToken`、`tokenInto`、`errClaudeFixed` 只给 `extensions.go` 用，S1b 删扩展时一起删。
- hub 的 `terminals` 计数键名没改。

留给后面的：
- `cmd/iridium-qq setup` 还在调 `ir token --owner --into`，现在会失败（QQ 暂缓）。qqbridge 只改了编译，没删测试。
- 文档（api.md、gate.md、plan.md、exec.md、qq.md）未改，留给 S5。
- 网页的应用管理页和需要卡片留给 S1c。
- 决定 6 留给 S1b（和合并配对、更新配对、批准时改名一起做）：申请里的名字不合法仍退回系统名（mac/linux/machine），重名仍自动加 `-2`、`-3`。S1b 改成 422 `bad_name`、不再生成名字，`ir join` 默认送主机名。

复核后补的：
- 会话菜单里“把 X 切到此会话”：`App.Chat`（`""` 没有聊天、`idle` 连着但还没有对话、`on` 有对话）代替删掉的 `Bot`/`Chatless`，菜单只列有聊天层的应用，`idle` 置灰。S4 删聊天层时一起删。
- 发送者按 ID 认：`LastSaid` 加 `fromTerminal`（Codex、pi 的列表摘要没有），侧栏的“你：”和 `sourceOf` 先按 ID 找，只有没有 ID 时才按名字，名字重了优先 ir_web 的登录和主机。
- `PUT /api/public` 只给主机上人的 `ir`（决定 3），ir_web 的登录和主机的网页都回 403。
- `TestOwnerFromPanels` 恢复“主机的网页不能批 Claude”的 403 检查。

## S1b 配对窗口、更新配对、需要队列、收录表

做了什么：
- 配对：
  - 合并成 `POST /api/pair`、`GET /api/pair/{request}`，删 `/api/exec/pair` 和 `routes_pair.go`。
  - 窗口 `GET`/`PUT /api/pairing`（能批的人才能开），`ir apps open [时长]`、`ir apps close`。默认 10 分钟，最多 1 小时，关着时 403 `pair_closed`。
  - 每来源（IP，IPv6 按 /64）最多 4 个待批，总共 20 个，超了 403 `too_many_pending`。
  - 名字不合法 422 `bad_name`，不再自动起名、加后缀；`ir join` 默认送主机名。
  - 重名就是 `app_update`：允许后同一个应用换新 TOKEN，旧的作废、断开，用量和权限不变；允许时带 `name` 则改名建新应用。
  - 申请可带 `wants`（agents、scope），批准只能缩小（422 `too_wide`）；S2 才执行。
  - `ir pair`、`ir join` 遇到 `pair_closed` 一直等，只提示一次。
- 需要：`confirms.go` 改为 `needs.go`。种类 `app_join`、`app_update`、`app_change`、`login_join`、`invite`、`secret_set`、`secret_use`。
  - 路由 `GET /api/needs`、`POST /api/needs/{id}`、`GET /api/needs/{id}`，hub 键 `needs`。
  - CLI：`ir needs`、`ir allow <id>`、`ir deny <id>`。
  - 能批的（`mayApprove`）：主机上的人，或有 Claude、范围和 agent 都是全部的 ir_web 登录；agent 的 `ir` 一律不行。删应用、删登录也不让 agent 做（`admin`）。
  - `PUT /api/me/perms`：应用申请改权限，产生 `app_change`。
- 登录加入：应用的新登录第一次说话回 403 `login_pending`，产生 `login_join`；每个应用最多 20 个待批。删掉的登录下次说话重新申请。`DELETE /api/apps/{id}/logins/{login}`。
- 错误体统一 `{code, message, nameError?}`（`api.Error`，`Text()` 取 message 或 code）。gate 自己的拒绝只有 `code`。
- 删 `/api/extensions`、`extensions.go`、`update.Extension`、网页 `extensions.tsx`。收录表 `GET /api/catalogue`（`catalogue.go`，写死）。
- 网页只做到能编译、能用：类型改成 `Need`，`/api/needs`，`secret_set`/`secret_use`；配对卡片改读 `pair`，`app_update` 显示说明（在线时警告）；`app_change`、`login_join` 先用简单的允许/拒绝卡片。改名重名的提示按 `nameError` 判断，不再看 409。
- `scripts/dev/exec-mini.py`、`shell-bot.mjs`、dsh 插件改指 `/api/pair`，过期认 422；两个脚本遇到 403 等待。

和计划不一样的地方：
- 决定 28（资源不存在回 422）没有全面改：server 里还有约 25 处 404，留给 S4b/S5 一起扫。gate 的加入页重名仍回 409。
- `pairing` 的 hub 键有了，网页还没订阅（S1c 的倒计时用）。

留给后面的：
- S1c：应用管理页、新建应用（开窗口、收录表的安装命令）、需要卡片的完整样子（`app_join` 可删减权限、批准时改名，`app_change` 显示增减）。
- S2：`wants`/`perms` 真正生效；`CanManage` 等垫片照旧。
- 主目录 `scripts/dev/exec-mini.py` 有别的会话没提交的改动，合并回 main 时注意冲突。

## S1c 网页：应用管理、应用详情、新建应用、需要卡片

做了什么：
- `terminals.tsx` 改为 `apps.tsx`，路由改为 `#/settings/apps`，旧的 `#/settings/terminals` 也认。
- 列表：主机（不可点）、网页、其他应用一个列表，每行头像、名字、类型（电脑、Bot、插件、脚本）、在线、最后活动、今天用量；是执行端的再显示系统和 agent。
- 应用详情：
  - 头像（点了换）、改名、在线情况。
  - 权限：全部 agent 开关，或按 agent 勾选；全部位置开关，或一条条位置（`应用名:路径`）。改了才出现保存、还原。后端新增 `PUT /api/apps/{id}/perms`，只有能批的人能改（S2 才执行）。
  - 电脑：系统、带的 agent、命令行（只读，来自执行端）。
  - 登录：其他应用的登录，显示待批，可移除。
  - 跟随面板（S4 删）、删除应用。
  - 网页的详情：它的登录（改名、移除）、没用的邀请当作空位、邀请新面板。
- 新建应用：进入就开配对 10 分钟，显示倒计时，可提前关闭或再开；列出收录表的安装命令（按系统，可复制，`{origin}` 换成当前地址）和 `ir pair`；配对申请的卡片显示在这一页。hub 键 `pairing` 加进常驻订阅。
- 需要卡片：
  - `app_join` 显示申请的权限，可以点掉不给的（批准只会缩小）。
  - `app_update` 可以选“作为新应用接入”并另起名字；旧应用在线时警告。
  - `app_change` 展开显示多要（+）少要（−）的 agent 和范围。
  - `login_join` 允许或拒绝。
- 文案：“终端管理”统一为“应用管理”，卡片按钮“同意”改为“允许”；删了扩展页和旧配对菜单的键。
- `vite.config.ts` 的预取表、`build.test.ts`、`shoot.mjs` 跟着改名；截图更新了应用管理几张，新增 `desktop-app-light-zh`、`desktop-newapp-light-zh`。

和计划不一样的地方：
- 登录的权限（改权限）没做：登录的 `perms` 还只是占位，S2 一起做。
- “带的 agent 开关”没做：开关属于 S3（hello 报 agent、主机上的应用去 agent），现在只读显示。
- 截图时注意：在 agent 会话里跑测试 daemon 的 `ir`，要去掉 `IRIDIUM_SESSION`、`CLAUDECODE` 等变量并给它一个终端（`script -qec`），不然会被当成 agent 拒绝。这是对的行为。

## S3 远端执行和位置

做了什么：
- 执行端协议加 `run`（`wire.CapRun`）：请求 `run {run, line, cwd?, env?}`、`run.stop {run}`，事件 `run.out {run, data, err?}`、`run.exit {run, code, signal?, error?}`。每条命令一个新进程，不经托管进程；连接断了执行端杀掉这条连接要的命令；进程退出后留在后台占着输出的，2 秒后一起结束。`wire.Protocol` 没升，字段只加。
  - Go 执行端（`internal/remote/executor/run.go`，`ir join` 用的）和 `scripts/dev/exec-mini.py` 都实现了；dsh 插件没动，不报 `run`。
  - 这边：`remote.Machine.Run` / `remote.Run`（`internal/remote/run.go`），读得慢时最多攒 8MB，再多就停掉命令。
  - `POST /api/machines/{id}/shell` 带 `Accept: application/x-ndjson`、执行端有 `run` 时回 `api.ShellEvent` 的 NDJSON 流；`ir sh` 断开（`--timeout`）就 `run.stop`。没有 `run` 的执行端、或不带这个头，照旧起托管进程、轮询。网页的 `$` 没动。
  - `ir sh <应用>[:目录]`：位置写法也认；`host:` 报错（主机上直接执行）。没有这台机器 422 `no_such_machine`。
- hello 的 `shell`：一个开关管网页 `$` 和 agent 的 `ir sh`，默认关。旧 hello 按 `agentShell && !noShell` 算（`Hello.ShellOn`）；新执行端两套都报。`ir join --shell`；`--no-shell`、`--agent-shell`、`--no-agents` 照收（`shell = (--shell || --agent-shell) && !--no-shell`），常驻单元只写 `--shell`。配对申请同样：`PairRequest.shell`，旧的两个字段由 `cleanPair` 换成 `shell`。`api.Machine` 只剩 `shell`（去掉 `noShell`、`agentShell`），网页的卡片、应用详情、`$` 跟着改；文案少了“网页上的命令”一档。
- hello 的 `machineId`（`internal/machineid`：Linux machine-id、Mac IOPlatformUUID、Windows MachineGuid 的哈希；`IRIDIUM_MACHINE_ID` 覆盖，`none` 为没有）。等于主机自己的（`Hub.Self`），就去掉它报的 agent，连接照收。exec-mini 也报。
- 位置：`internal/place`（`Split`、`Parse`、`Format`、`Key`、`FromKey`）和 `web/src/places.ts`（`splitPlace` 等），共用 `internal/place/testdata/places.json`。按第一个冒号拆；单个 ASCII 字母、前缀里有 `/` `\` 或以 `~` 开头的不算前缀；`host`、`主机`、`本机` 是主机；输出一律带前缀，主机写 `host:`。
  - CLI：`ir new <agent> <位置> "任务"`（`--machine` 还认，帮助里去掉）；`ir cp`、`ir files` 认 `host:`；`ir ls`、`ir find`、`ir ws` 的目录一律带前缀（`host:~/ir`、`mac:/x`）。去掉 `机器/<id>` 的会话写法（决定 24）。
  - 网页：@ 里输入 `host:…`、`主机:…` 当主机的路径（`atPath`）。
- P4 顺手修：
  - `Hub.Find` 按应用现在的名字找（`Hub.NameOf`，server 用 gate 的名字），改名后旧名字找不到、也不会找到别的应用。
  - 幻影机器：`Find` 不再返回只被 `Machine(id)` 查过、从没连上过、也没有这个应用的条目；`Hub.Drop`（应用被删）把没有托管进程挂着的条目一起删掉；`session` 的命令行查机器改用 `Find`，不再凭空建条目。
  - `dropGone` 的保护：只有应用不在了才丢掉那台机器上发现的会话和镜像，`machines` 表少了一行不算（`Store.AppIDs`）。
- 测试：`remote/run_test.go`（输出、退出码、找不到程序、目录不存在、停止、断线杀进程、`shell` 关着拒绝、主机上的应用去 agent、按现名找、exec-mini 的 `run` 和只有 401 才删 TOKEN）、`server/routes_shell_test.go`（流式、托管回退、没有 `run` 的执行端、`no_shell`、`no_such_machine`、改名）、`cli/shell_test.go`（流的打印、退出码、超时 124）、`place` 和 `web/test/places.test.ts`、`discover` 的 `dropGone` 保护、`pairing_test` 的映射。

和计划不一样的地方：
- `run` 的 ID 由这边定（同 `hold`），不是执行端回的。stderr 单独标 `err`，`ir sh` 分开打到 stdout、stderr（托管方式下仍合在一起）。
- 输出里主机写 `host:`，不是 `主机:`（CLI 给 agent 看；网页的 `formatPlace` 可传别的名字）。
- 旧执行端的默认（两个旧字段都没有）现在算 `shell` 关：以前网页能在它上面 `$`，现在不能，要在那边重新 `ir join --shell`（决定 21 的映射）。
- `--no-agents` 没删：应用详情里的 agent 开关还没做，暂时没有别的办法做“只做命令行的机器”。

留给后面的：
- 没做：`machines` 并进应用（`GET /api/machines`、`machines.tsx`、`api.Machine` 仍在）；按应用的 agent 开关（API 和应用详情）。
- 网页的位置显示（会话顶上、卡片、侧栏的组名）还没换成 `formatPlace`；网页 `$` 仍走托管进程（决定 20）。
- dsh 插件（`executor.js`）和 `shell-bot.mjs` 没加 `run`、`shell`、`machineId`，它们仍报旧字段，照映射算。
- 主目录 `scripts/dev/exec-mini.py` 有别的会话没提交的大改（多 Iridium），这里改的是 next 的那份（`run`、`--shell`、`machineId`、只认 401），合并时要手工合。
