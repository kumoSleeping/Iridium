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

## 还没做

- 第一期其余都做完了（`progress.md`“第一期收尾”）。
- QQ 桥重写成 Satori 客户端、合回 main 发 0.2.0：kumo 说先不急（2026-11-11）。
- 发 0.2.0 时一起发 dsh 插件（`plugins/dsh-remote-iridium`，版本号从 0.1.0 提上去）：旧插件调 `/api/exec/pair` 配不了对，报旧的命令行开关会被算成关；已配好对的照样能用。
- 插件方案：`plugin.md`，第一期之后。
- 测试里 Codex 指向假的：另一个会话（pi c7f37f09）在这个工作树里改了 `internal/testagent` 和几处测试架子，没提交。在那之前 `go test` 会真的启动 Codex，写进 `~/.codex`。
- 注意：
  - worktree 里不要把 `node_modules` 软链到主目录。
  - 在 agent 的 shell 里对测试实例跑 `ir`：要 unset `IRIDIUM_SESSION`、`CLAUDECODE`、`CLAUDE_*`，再用 `script -qec` 给个 pty。

## 经验

- 每步一个新 agent 从头读代码，加上审查、修复，很费：S1a 一步约 75 万 token、35 分钟。S1b 第一次碰到 5 小时额度上限，白跑了一次。
- 建议：
  - 一步再拆小：后端、网页、测试分开。
  - 审查只给权限、迁移这类风险大的步骤。
  - 实现者直接拿 `progress.md` 和 `prep.md` 的对应小节，不要通读全部文档。
