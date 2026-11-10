# 0.2 改版：定下的事

依据：`draft.md`（计划）、`k_glossary.md`（叫法）、`prep.md`（对过代码的准备报告，第 3 节的编号下面直接引用）。`k_bot.md` 是旧文档，只当“现有、要删或要迁的”清单。

## 范围（kumo，2026-11-10）

- **Bot 这个概念彻底删掉**，只剩显示用的类型名。所有应用一视同仁。
- **QQ 桥和 dsh 插件先不管**：`cmd/iridium-qq`、`internal/qqbridge`、`plugins/dsh-remote-iridium` 不改功能，只做让它们编得过、测试过所需的最小改动；聊天层删掉后 QQ 桥暂时不能用，之后单独重写成 Satori 客户端。
- **在分支 `next` 上做**（工作树 `~/kumo/Iridium-next`），按 `prep.md` 第 5 节的顺序；每步 `go vet ./...`、`go test ./...`、`npm run check` 全绿再提交；全部做完推到测试实例。不发版、不碰主实例。
- 一次切换，不留旧接口。例外（因为装在别处的程序不会自己更新）：执行端协议版本不升、字段只加；执行端没报 `run` 时 `ir sh` 退回 hold 方式；`ir join` 继续接受旧参数。

## 第 3 节各项，采用报告的建议

1. `platform`＝应用 ID，`user.id`＝登录 ID，`login.sn` 按连接分配；`应用/登录` 只是 `ir` 和网页上的写法。
2. ir_web 的一个登录＝今天的一个面板（一套 passkey），ID 沿用。
3. 没设公网地址时，浏览器照旧算主机；ir_web 只在有公网地址时存在；关网页只能在主机的 CLI 上。
4. 存储 ID：应用 ID＝旧终端 ID；主机仍是 `local`，只改显示；位置键内部继续 `ID:路径`，名字只在输入输出时换。
5. 保留名 `host`、`主机`、`本机`、`ir_web`；单个 ASCII 字母不能当名字。
6. 申请里的名字合法就用，不合法 422 `bad_name`；重名只能“更新配对”或批准时改名；`ir join` 默认送主机名。
7. 加 `ir apps open [10m]`、`ir needs`、`ir allow`/`ir deny`（只有主机）。
8. 只有 401 表示 TOKEN 作废；其余一律 403 加原因码（`pair_closed`、`login_pending`、`no_permission`…）。
9. 删 `ir token`、`POST /api/terminals/tokens`、`--owner`、`--into`；配对是唯一入口。
10. `/api/exec/pair` 不留别名；`token_hash` 保留，已配对的不用重配。
11. 更新配对的卡片并排显示新旧两边；旧应用在线时警告；用量和权限沿用旧的。
12. 登录加入：新登录第一次说话时产生申请，这条回 403 `login_pending`，不暂存；`approved_at` 为空即待批；每个应用最多 20 个待批。
13. （QQ，暂不做）
14. 凭据：列、设、删给主机和有管理的登录；“用”只走主机 socket。
15. 第一阶段批申请：主机，以及范围为“全部”且能用全部 agent 的 ir_web 登录；agent 的 `ir` 一律不能批（`CanManage` 放行 agent 要一起修）。授予 Claude 只有主机或能用 Claude 的登录，记下谁、何时。
16. 不允许的 agent 的会话：能看（只读），不能开、发、答、归档、改名；看不看得到只由范围决定。
17. 默认范围：迁移来的应用＝全部；新的带 Agent 应用＝自己的家目录；新的不带 Agent 应用＝全部。登录 ⊆ 应用在检查时取交集。
18. 发起登录在回合开始时定下，存进 `inputs.login`；顺着派活链找到第一个人的登录；没有人的回合算主机。
19. 组排序改成局部合并；`folder_colors` 键统一成位置形式；`root` 为空的便签只有范围为全部的能看。
20. `run`：执行端收 `{line, cwd, env?}`，推 `run.out`/`run.exit`，`run.stop` 停，断线就杀；server 出 NDJSON 流；按 cap 协商，没有 `run` 退回 hold。主机网页的 `$` 照旧。
21. `shell` 一个开关同时管远端网页 `$` 和 agent 的 `ir sh`；旧 hello 按 `agentShell && !noShell` 映射；`ir join` 照收旧参数。
22. 主机上的应用：hello 带电脑标识的哈希，对上就去掉它的 agents，连接不拒；可用环境变量覆盖（测试和 ir-test 用）。
23. 机器情况放在执行端 hello 里，执行端链路单独保留；`wire.Protocol` 不升。
24. 去掉 `机器/<id>` 的会话写法，`ir send 应用/登录` 用 `/`。
25. 网页 0.2 继续用 hub 的 SSE；WebSocket 只给应用。
26. 草稿、状态、“正在看”的内容不发成 EVENT，用自定义 op（≥6）；存库的只有 `message-created`、按钮、`channel-*`；sn＝outbox 的 seq，存 24 小时。
27. 卡片：Iridium 发 `<button type="action" id="ask:…">`，应用调 `/api/sessions/{id}/answer`（body 是 CardAnswer）答；也支持 `type="input"`。
28. 404 只表示接口不存在，资源不存在 422 加原因码；规范规定的几处（未知 `Satori-User-ID` 403、代理的 400/403/404）照规范。
29. （QQ，暂不做）
30. 删 `status`；`progress` 发成 `message-created`；答过的提问发 `message-updated`。
31. 网页继续用分块的 `/api/uploads`；`upload.create` 收单个 multipart，额外带 `channel_id`。
32. Web Push 路由改到 `/api/web/push/*`，只给 ir_web 登录（和主机的浏览器）。

Satori 规范的副本：`~/kumo/satori-spec-50fee9ef/`（satorijs/docs@50fee9ef）。

## 进度

见 `progress.md`：每一步做完写一段（做了什么、和计划不一样的地方、留给后面的）。
