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
