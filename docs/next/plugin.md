# 插件方案

kumo 定（2026-11-11）：撤掉只为 OMD 写的适配，换成 ir 的通用扩展，OMD 照样能用；核心里不留 OMD、也不留只有它用得到的东西。旧的 dsh 专用接口一并删，插件和 ir 一起升级。本机装了 DSH Desktop 和 OMD 的，ir 在本机起的 dsh 也带上 Desktop 的模型、OMD 的预设和 Ultra。

## 示意

```
网页 ── 位置 effort（画挂上来的元素）、按下按钮
ir 核心
├─ 挂元素：会话上的一段 Satori 元素，按位置放（核心不看内容）
├─ 按钮转发：按下后交回挂它的一方
├─ 执行端代拼命令行：agent.presets、agent.prepare（插件用 Desktop 的配置拼 patch）
└─ 起进程照旧由 ir：本机 holder，远端 hold.start
执行端（DSH Desktop 里的 dsh-remote-iridium；本机、远端一样，都经配对连上）
├─ 答 agent.presets / agent.prepare；起会话时登记到 Desktop 的工作区
└─ 在 Desktop 和会话进程里提供服务 iridium：attach、onButton
会话进程 ── 插件里的 omd.js：用 iridium 服务挂 Ultra、收按下
```

## 核心

1. **挂元素。** 一段 Satori 元素挂在会话的某个位置，可按语言给几份（`{"zh": …, "en": …}`）。来源三种：
   - 执行端：执行端连接上的请求 `session.attach {session, place, content}`（`session` 是那台机器上 agent 自己的会话 id；`content` 空即去掉）。执行端断开，它挂的都去掉，重连后它再挂。
   - 应用：`PUT /v1/internal/sessions/{id}/attach/{place}` `{content}`，`DELETE` 去掉；只限能用这个会话的第三方应用。
   - 核心自己：Claude 的 UltraCode。
   核心只限大小（一份 4KB）、个数（一方一个会话 4 段），存内存，放进会话状态 `attached: [{place, from, content}]`。按钮 id 改写成 `att:<会话>:<from>:<原 id>`，冒充不了提问卡片。
2. **按钮转发。** 网页照旧 `POST /v1/internal/button {id}`。`att:` 开头的：核心自己的（UltraCode）就地切换；执行端的发事件 `session.button {session, id, user}`；应用的发 `interaction/button`，只给这个应用。
3. **执行端代拼命令行。** 能力 `agent.prepare`：
   - `agent.presets {agent}` → `{presets: [{id, name?, description?, plugins, broken?}], default}`（同旧的 `dsh.presets`）。
   - `agent.prepare {agent, session?, cwd?, preset?, system?}` → `{patches: [路径], preset: bool}`：`--patch` 依次加在 `--profile sdk` 之后。`preset` true：预设（含 bundle、绑定、`system` 接在人设后）由它拼好，ir 不再加自己的预设 patch；false：ir 照旧拼 dsh 自带预设的 patch。没有 `session`：只要模型（探测模型列表用）。
   - 执行端和 ir 在同一台电脑上（握手的 MachineID 同 ir 的）：ir 在本机起 dsh 也问它；它给的 patch 必须是本机用户自己的、别人不可写的普通文件，否则不用。
4. **Desktop 拿着的会话**（`held.list/send/get/set/changed`）留着，去掉功能和“强度归谁管”：只剩列出、发话、模型和强度。

删：`api.Feature`、`PUT …/features/{id}`、网页的 `NATIVE`；`wire.Feature`、`CapHeldFeatures`、`HeldLive.Owner`；`dsh.open`、`dsh.prompt`、`dsh.llm`、`dsh.presets`、`dsh.bundles`、`dsh.session`、`dsh.attach`；`agent/dsh/bound.go`、`agent.Bundle`。老插件连上新 ir 只是个普通执行端，不报错。

## 网页

- 位置 `effort`：第一个 `<button>` 是强度滑条右边的键，其余的按钮在键按下时排成一行（子模式）。
- `<button>` 多认 `pressed`（开着）、`disabled`、`title`（说明或原因）；别的应用不认识就忽略。

## dsh-remote-iridium

- 删 `features.js`、`adapters/omd.js`；`dsh.*` 方法换成 `agent.presets`、`agent.prepare`。bound 的拼法（原 `bound.go`、`modules.go` 的改名）搬进来，结果和原来一致：omd-ptc 63 个工具。
- 服务 `iridium`（Desktop 进程和会话进程里都有）：`attach(session, place, content)`、`onButton((session, id, user) => …)`。会话进程里的经 `bridge.js` 交给 Desktop 里的，再经执行端连接给 ir。
- 会话进程里有了会话，告诉 Desktop 里的，由它登记到工作区（代替 `dsh.attach`）。

## OMD 的 Ultra

在 dsh-remote-iridium 里，一个子插件 `omd.js`（kumo，2026-11-11：并进来，只装一个插件）：注入 `omd`、`iridium`，没装 OMD 时不启用。每个会话按 OMD 的状态挂 `<button id="omd.ultra" pressed? disabled? title>Ultra</button>` 和子模式按钮；按下就切换再重挂。插件里只有这个文件认识 OMD，核心里没有。

## 验证（要有 Desktop 和 OMD 的机器）

- omd-ptc 在 ir 起的会话里 63 个工具，本机、远端都是。
- Ultra 在 ir 起的会话和 Desktop 拿着的会话里都能切换，子模式也能。
- Ultra 开着时在 ir 里改强度。

## 不做

- 会话 TOKEN 连 `/v1/events`：会话进程经执行端挂，用不到。
- `discover/remote.go` 的爬取换成插件报：和 OMD 无关，以后再说。
