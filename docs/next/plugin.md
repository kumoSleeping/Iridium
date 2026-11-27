# 插件方案

kumo 定（2026-11-11）：撤掉只为 OMD 写的适配，换成 ir 的通用扩展，OMD 照样能用；核心里不留 OMD、也不留只有它用得到的东西。2026-11-27 重写 DSH Desktop 架构：不再为每个会话另起 `dsh --profile sdk --patch` 子进程，也不再做 `held.*` 转交与 `agent.prepare` 拼补丁；改为插件在 Desktop 进程内以 `dsh.inproc` 直接提供 SDK JSON-RPC 服务，与 Desktop 界面共用同一份 agent、模型、预设与插件。

## 示意

```
网页 ── 位置 effort（画挂上来的元素）、按下按钮
ir 核心
├─ 挂元素：会话上的一段 Satori 元素，按位置放（核心不看内容）
├─ 按钮转发：按下后交回挂它的一方
├─ 本机凭据直连：状态目录写 local-exec.json（Unix 0600 / Windows 单用户 DACL），直连 loopback /v1/internal/exec，免配对
└─ 统一走 hold：本机 Desktop 在线时经本机执行端（harness.LocalHost）发 @dsh-desktop；离线且已装插件时起无窗口 dsh-desktop-host 兜底，真 Desktop 上线时冻结写盘并让位
执行端（DSH Desktop 里的 dsh-remote-iridium；本机读 local-exec.json 直连，远端经配对连上）
├─ 能力 dsh.inproc：argv[0] == "@dsh-desktop" 的 hold 在进程内用 dsh 自带 SDK JSON-RPC 服务直接服务（流代替子进程 stdio），审批/提问用 AbortSignal.any 与 Desktop 界面并行竞速
├─ desktop.* 与事件推送：models / workspaces / sessions / freeze + models/presets/workspaces/sessions/config 变更事件（零定时轮询），同步新会话默认关
├─ 隔离：系统提示按会话挂 section，ir 身份经 ctx.shellEnv 按会话注 DSH_IRIDIUM_*，persona 按会话（--persona own）隔离
└─ 在 Desktop 进程内提供服务 iridium：attach、onButton；omd.js 事件驱动挂 Ultra
```

## 核心

1. **挂元素。** 一段 Satori 元素挂在会话的某个位置，可按语言给几份（`{"zh": …, "en": …}`）。来源三种：
   - 执行端：执行端连接上的请求 `session.attach {session, place, content}`（`session` 是那台机器上 agent 自己的会话 id；`content` 空即去掉）。执行端断开，它挂的都去掉，重连后它再挂。
   - 应用：`PUT /v1/internal/sessions/{id}/attach/{place}` `{content}`，`DELETE` 去掉；只限能用这个会话的第三方应用。
   - 核心自己：Claude 的 UltraCode。
   核心只限大小（一份 4KB）、个数（一方一个会话 4 段），存内存，放进会话状态 `attached: [{place, from, content}]`。按钮 id 改写成 `att:<会话>:<from>:<原 id>`，冒充不了提问卡片。
2. **按钮转发。** 网页照旧 `POST /v1/internal/button {id}`。`att:` 开头的：核心自己的（UltraCode）就地切换；执行端的发事件 `session.button {session, id, user}`；应用的发 `interaction/button`，只给这个应用。
3. **进程内执行（`dsh.inproc`）。** 执行端在 `hello` 声明 `dsh.inproc` 与 `argv: ["@dsh-desktop"]`：
   - `agent.presets {agent}` → `{presets, default}`。
   - `desktop.models` / `desktop.workspaces` / `desktop.sessions` / `desktop.freeze` 与对应 `desktop.*.changed` / `desktop.config` 事件（全部留在 `internal/harness/dsh` 与 `internal/discover`，核心包零 dsh 专有引用，`neutral_test.go` 清零）。
   - `hold.start` 传 `["@dsh-desktop", ("--preset", id), ("--system", file), ("--persona", "own"), ("--keep-model")]`，不加 `--profile sdk`、不生成 `--patch`。
4. **已删**：`held.js`、`bridge.js`、`compose.js`、`prepare/`、`session.js`、`internal/harness/dsh/held.go`、`internal/harness/dsh/ownfile_*.go`、`wire.CapHeld`、`wire.CapPrepare`、`remote.DshOpen` 及 30 秒远端文件爬虫与 5 秒 `dsh.open` 轮询。

## 网页

- 位置 `effort`：第一个 `<button>` 是强度滑条右边的键；其余的按钮是它的子模式，不单独显示：键按一下换下一种（关 → 各子模式 → 关），图标是当前子模式的。键关着时按第一个子模式，开着时按下一个，最后一个之后按键本身（关）。
- 按钮里的 `<img src="data:image/…">` 是它的图标：只取形状，颜色随页面；只认 `data:` 地址。没有图标的用默认的。
- `<button>` 多认 `pressed`（开着）、`disabled`、`title`（说明或原因）；别的应用不认识就忽略。

## OMD 的 Ultra

在 dsh-remote-iridium 里，一个子插件 `omd.js`（kumo，2026-11-11：并进来，只装一个插件）：注入 `omd`、`iridium`，没装 OMD 时不启用。监听会话事件去抖刷新，为每个会话挂 `<button id="omd.ultra" pressed? disabled? title>Ultra</button>` 和带图标的子模式按钮（开关时都挂）；按下就切换再重挂。插件里只有这个文件认识 OMD，核心里没有。
