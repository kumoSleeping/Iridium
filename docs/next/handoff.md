# 0.2 改版交接（2026-11-11）

## 在哪

- 工作树 `~/kumo/Iridium-next`，分支 `next`，从 main 的 `02a9adc`（v0.1.41）分出；只在本地，没推远端。HEAD `45d27e3`，全部提交，`go vet`、`go test ./...`、`scripts/check-types.sh`、`npm run check` 全绿。
- 主目录 `~/kumo/Iridium` 没动过；那里 `docs/exec.md`、`scripts/dev/exec-mini.py` 有别的会话没提交的改动（exec-mini 多 Iridium），合并回 main 时要手工合。
- 以 kumo 的 `draft.md` 为准（kumo，2026-11-10：“按照我的计划做”），`prep.md`、`decisions.md` 只作参考。每步做了什么在 `progress.md`。
- Satori 规范副本：`~/kumo/satori-spec-50fee9ef/`。
- 测试实例跑着 `v0.2.0-dev.55+g45d27e3`；ir 账户备份 `~/iridium-backup-*.tgz`、`iridium.db.v17.bak`。

## 做到哪

S1a 到 S5 都做完并合进 `next`，之后又做了：需要队列带提问和分享、机器并进应用和 agent 开关、网页全走 Satori `/v1`、`message.list` 带全部记录（`iridium:` 元素）、网页独立成一层（`internal/webapp` 管 Passkey、Cookie、公网；核心只认 TOKEN）。细节见 `progress.md` 对应各节。

待确认：用 passkey 登录的浏览器打开 https://ir-test.kumo.ltd，旧登录是否还在、页面是否正常（没法自己验）。

## 还没做（等 kumo 点头）

- 记录里人说的消息 `user.id` 是应用，不是登录（记录里没存登录 ID）。
- “分享到”只在会话菜单，回复、文件、位置的菜单没有；网页上写留言；分享和提问只在内存。
- 配对卡片上划掉 agent；`web-token` 轮换。
- Satori：事件存库、`channel.create`、`login-*`/`channel-*` 事件等（`docs/satori.md`“还没做”）。
- 约 25 处 404 改 422。
- QQ 桥重写成 Satori 客户端；dsh 插件改用新的 `shell` 字段。QQ、dsh 先不管（kumo，2026-11-10）。
- 合回 main、发 0.2.0：要 kumo 决定。
- 注意：
  - worktree 里不要把 `node_modules` 软链到主目录。`test-deploy`、`push-test` 会跑 `npm ci`，把主目录的也清空。
  - 在 agent 的 shell 里对测试实例跑 `ir`：要 unset `IRIDIUM_SESSION`、`CLAUDECODE`、`CLAUDE_*`，再用 `script -qec` 给个 pty。

## 经验

- 每步一个新 agent 从头读代码，加上审查、修复，很费：S1a 一步约 75 万 token、35 分钟。S1b 第一次碰到 5 小时额度上限，白跑了一次。
- 建议：
  - 一步再拆小：后端、网页、测试分开。
  - 审查只给权限、迁移这类风险大的步骤。
  - 实现者直接拿 `progress.md` 和 `prep.md` 的对应小节，不要通读全部文档。
