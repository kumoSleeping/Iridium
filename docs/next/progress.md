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
