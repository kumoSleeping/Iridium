# 0.2 改版交接（2026-11-10 晚）

## 在哪

- 工作树 `~/kumo/Iridium-next`，分支 `next`，从 main 的 `02a9adc`（v0.1.41）分出；只在本地，没推远端。
- 主目录 `~/kumo/Iridium` 没动过；那里 `docs/exec.md`、`scripts/dev/exec-mini.py` 有别的会话没提交的改动（exec-mini 多 Iridium），合并回 main 时要处理。
- 必读：`decisions.md`（定下的事，编号对 `prep.md` 第 3 节）、`prep.md`（对过代码的准备报告：出入、改动面、顺序）、`progress.md`（每步做了什么、垫片、留给后面的）。`draft.md`、`k_glossary.md` 是 kumo 的计划和叫法；`k_bot.md` 是旧文档，只当待删清单。
- Satori 规范副本：`~/kumo/satori-spec-50fee9ef/`。

## 做到哪

| 步 | 状态 | 提交 |
| --- | --- | --- |
| 文档 | 完成 | `86de593` |
| P1 安全网 | 完成；审查只跑了一半（推送订阅的旧行没清，已并进 S1a 做掉） | `2e00a88`..`5932a95` |
| P2 特征测试 | 没做，拆进 S1a（数据库样例）和 S2（路由登记表、权限矩阵） | — |
| S1a 应用、ir_web 登录、主机 | 完成，审过修过，全绿 | `1ab0bc0`、`18da56a` |
| S1b 配对窗口、需要、收录 | **半成品，WIP 提交，测试不绿** | 见下 |
| S1c 及以后 | 没开始 | — |

### S1b 现状（WIP 提交）

已有：
- 合并的 `POST /api/pair`、`GET /api/pair/{request}`，`/api/exec/pair` 和 `routes_pair.go` 删了。
- 配对窗口 `GET`/`PUT /api/pairing`，关着时 403 `pair_closed`。
- `app_update` 更新配对。
- `needs.go` 由 `confirms.go` 改来，`ir needs`/`allow`/`deny` 在 `internal/cli/needs.go`。
- 登录：`store/logins.go`、`server/logins.go`。
- 收录：`catalogue.go`。
- 删了 `/api/extensions`、`extensions.go`、`update.Extension`。

不绿：
- `internal/cli`：`TestAdmin`、`TestOwnerOnlyFromAPerson`、`TestForwardAt` 失败。
- `internal/gate`：`TestChannelTokensAndProfiles` 失败。
- `internal/join`：测试挂住到超时，多半是 `ir join` 遇到 `pair_closed` 一直重试，测试没先开窗口。

没核对：
- 每来源 4 个、全部 20 个的限额。
- IPv6 /64。
- 登录加入的 403 `login_pending`。
- 401/403 原因码是否全覆盖。
- `web/src/api/types.gen.ts` 是否已重新生成（网页没动，`extensions.tsx` 还在）。
- `progress.md` 里还没有 S1b 一段。

## 后面的顺序（每步的详细范围在 `steps.json`；`next-all.workflow.js` 是跑它们的 workflow 脚本：S1 依次做，再两条线并行，最后合并和 S5）

1. **S1b 收尾**：修上面的失败；补齐限额、登录加入和测试；生成类型；写 progress。
2. **S1c 网页**：应用管理、应用详情、新建应用（开窗口 + 收录的安装命令）、需要卡片；删扩展页。
3. **S2 权限**：Agents + Scope；登录 ∩ 应用；每个接口按会话或位置查；路由登记表加权限矩阵测试；发起登录持久化；凭据和批准规则；去掉 S1 的垫片（`progress.md` 有清单）。
4. **S3 带 Agent 和位置**：
   - hello 报机器、agent 开关、主机上的应用去 agent。
   - machines 并进应用。
   - `internal/place`，`ir new <agent> <位置>`。
   - `ir sh` 走 `run`，没有 `run` cap 时退回 hold。
   - exec-mini 只认 401。
5. **S4a 消息**：
   - 按登录推事件。
   - 需要队列包括提问、审批、计划。
   - `ir send` 取代 `ir bot`；分享。
   - 删聊天层、跟随、指针。
   - Web Push 改到 `/api/web/push`。
6. **S4b Satori**：可以和 S2 到 S4a 并行，在另一个工作树开发，最后合并。
7. **S5 收尾**：irhelp、系统提示、文档；`0.2.0-dev`；扫一遍旧概念的残留。然后 `scripts/push-test.sh` 推测试实例，先备份 ir 账户的 state 目录。

QQ 桥和 dsh 插件先不管（kumo，2026-11-10）：只保证能编译、测试能过。现在 `iridium-qq setup` 已经不能用（`ir token` 删了）。

## 经验

- 每步一个新 agent 从头读代码，加上审查、修复，很费：S1a 一步约 75 万 token、35 分钟。S1b 第一次碰到 5 小时额度上限，白跑了一次。
- 建议：
  - 一步再拆小：后端、网页、测试分开。
  - 审查只给权限、迁移这类风险大的步骤。
  - 实现者直接拿 `progress.md` 和 `prep.md` 的对应小节，不要通读全部文档。
