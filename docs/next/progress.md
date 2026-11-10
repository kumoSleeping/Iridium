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

## S4b Satori

做了什么：
- 新包 `internal/satori`，说明在 `docs/satori.md`：
  - `elements.go`：元素的解析和编码（转义、数字引用、注释、不成对的标签当文字、换行空白），`Plain` 把元素转成给 agent 的文字。解析是线性的（不成对标签、没闭合的引号都不会变成平方）；有模糊测试。
  - HTTP `POST /v1/{资源}.{方法}`：`message.create/get/list`、`channel.get/list`、`guild.get/list`、`login.get`、`user.get`、`upload.create`，`POST /v1/meta`，`GET /v1/proxy/internal:…`，`POST /v1/internal/button`。
  - WebSocket `/v1/events`：IDENTIFY（TOKEN、`sn` 续传）、READY、PING/PONG、EVENT。事件是 `message-created`（progress、reply、ask、error）、`message-updated`（提问结束）、`interaction/button`。
  - 通过 `Backend` 接口用 server 的逻辑，不引用 server 包。
- server：`internal/server/satori.go` 实现 `Backend`（发消息走 `checkSend` + `files.Message` + `SendWith`，答卡片走 `using` + `AnswerCard`，上传走 `CreateUpload/Put/Complete`），错误按决定 28 映射成 422 加原因码。
- 登录由 `Satori-User-ID` 选，沿用 `withLogin`（新登录 403 `login_pending` 加需要）。
- 测试：`internal/satori` 用假 Backend 测握手、续传、错误码、分页、按钮、上传和代理；`internal/server/satori_test.go` 对真 daemon 和假 Claude 走一遍（登录加入、发消息收回复、审批按钮、两个问题累计提交、上传后发附件、删应用断开）。

和计划不一样的地方：
- outbox 在内存里（24 小时、最多 1 万条），没进库：不用加迁移，也不和别的步骤抢 schema 编号。重启后续传补不回之前的事件；`sn` 从启动时刻的毫秒数开始，不会倒退。决定 26 说存库，要的话之后把 `outbox` 换成 store 实现。
- 一个事件按登录各发一份，`sn` 相同。
- agent 说的话先在记录里找对应的块，用块的位置作 `message.id`（和 `message.list` 一致）；找不到就是 `live:…`。
- 按钮按下走 `POST /v1/internal/button`（Satori 没有“应用按按钮”的接口）；`type="input"` 的回答走 `message.create`，以按钮的 `text` 开头。
- guild 用网页的文件夹组，加一个 `loose`（未分组）。
- 没做：`login-*`、`channel-*` 事件，META 信令，webhook，自定义 op（草稿、状态、“正在看”），`proxy_urls`。

共用文件的改动（合并时注意）：
- `internal/server/server.go`：`Server.sat` 字段；`New` 里 `satori.New` 和 `m.Watch(s.sat.Notice)`；`Close` 里 `s.sat.Close()`。
- `internal/server/access.go`：`webHandler` 加 `s.withSatori`（在 `withLogin` 里面、`WrapApp` 外面）。
- `internal/server/pairing.go`：`openPath` 放过不带 TOKEN 的 `GET /v1/events`（TOKEN 在 IDENTIFY 里，由 satori 查）。
- `web/vite.config.ts`：代理 `/v1`（含 WebSocket）。
- `go.mod` 没改：`github.com/coder/websocket` 本来就在（`go mod tidy` 会把它挪到直接依赖，主线原来就这样）。

合并时要接的：
- S2 权限：
  - `satoriBackend.Sees`（现在全部可见）换成登录范围 ∩ 应用范围；`channel.list`、`guild.list`、事件投递、代理都经过它。
  - `callerAs` 现在把登录当成所属应用（`Identity.ID()` 是应用 ID）；S2 的登录权限要让 `checkSend`、`using`（Send、Answer、Upload）按登录查。
  - `satoriBackend.File` 只查 ref，没查权限（同 `/api/sessions/{id}/files`），S2 一起加。
  - `_data.origin` 现在是 `Notice.From`（应用 ID）；决定 18 的 `inputs.login` 做好后换成登录。
  - 不允许的 agent 的会话（决定 16）：现在发消息、答提问按 `access.Agent` 拒绝（403 `no_permission`），读照常。
- S4a 删聊天层：
  - satori 只用 `session.Manager.Watch` 的 Notice 和 `Manager.AnswerCard`，不用 `internal/chat`；删聊天层不影响它。`/api/answer` 并进 `/sessions/{id}/answer` 后，按钮仍直接调 `AnswerCard`。
  - “auto 拦了 N 步”的文案从聊天层抄过来，在 `events.go`。
  - `channel-*`（会话新建、改名、归档）和 `login-*` 事件要从 hub 的 `list`/`terminals` 变化里做，加在 `Service.emit`。
  - 聊天层的 store outbox（`Post`、`PruneOutbox`）删掉后，若决定 26 仍要存库，satori 的 `outbox` 换成新的 store 表。
  - QQ 桥之后改成 Satori 客户端时用 `/v1/events` 和 `message.create`。
- S5：server 里其余的 404 → 422 和这里的原因码统一；`docs/api.md` 加一节指向 `docs/satori.md`。
