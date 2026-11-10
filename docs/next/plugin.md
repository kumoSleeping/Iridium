# 插件方案（暂存，第一期之后做）

来源：pi 会话 137b56ed「ir 插件系统的可行性」，按审阅改过。一句话：**让插件自己报，别让 ir 去猜。** 判断标准：核心里任何一处“读那台电脑上某个文件的某个字段，来推断它在干什么”，都是下一个 `held.*`。

## 现在的毛病（已对过代码）

- `internal/discover/remote.go`（855 行）经 `fs.*` 读 DSH Desktop 的私有文件：项目目录 mtime、投影缓存、工作区注册表，每 10 分钟深扫；“不同步”靠 `norm()` 比工作区名字。
- `held.*`（list/get/set/send/changed）一族同时答“谁在跑、能改什么、有什么开关”，靠缓存 10 秒、等 6 秒、改 23 秒、每 500ms 一次撑着；`harness/dsh/held.go` 501 行 + `session/channel.go` 216 行。另有 `dsh.open/prompt/llm/session/bundles/presets/attach`。
- 为了 OMD 的 Ultra：插件在 Desktop 开 Unix socket（`bridge.js`），把 `session.js` 注进 ir 起的 dsh 子进程。
- 网页：`web/src/chat/meters.tsx` 只画第一个开关；`disabled` 按 id 认 `NATIVE`。

## 改成

1. **会话状态由跑它的一方推。** 状态里带 `owner`（主机，或某个带 Agent 的应用）、`status`、`model`、`effort`、`effortBy`、`features[]`；连上时一份快照，之后变化推送（Satori `channel-updated`）。ir 只校验、存内存、转发。改走 owner（`/v1/internal/sessions/{id}/features/{fid}` 等现有接口转给 owner），接管回 `granted`／`conflict`（写锁只有插件知道）。
2. **开关是封闭的形状。** 插件只给数据：`kind`（switch/select/action/badge）、`place`、`tone` 是枚举，文字按 locale 一次给全、限长、每个资源有上限；越界整条丢。插件不写前端、不写 Tailwind。新控件 = ir 发版加一个枚举值。自己家的功能（Claude UltraCode）用字段 `keepOn` 表示，不按 id 判断。
3. **工作区和会话由插件报。** “哪些不同步”是插件自己的设置，它在状态里报结果；核心对 dsh 只保留“记录文件在哪、长什么样”。

## 和原方案不同的地方

- **起进程默认仍由 ir 拼参数**（`ir join`、exec-mini、shell-bot 不用各自学 claude/codex/pi 的参数）；只有 dsh 这类自带配置的，由插件改写或接手。dsh 会话由插件起，OMD 的开关自然在 owner 那里，`bridge.js`、`session.js`、`dsh.bundles`、`dsh.session` 就不需要了。
- **不做 manifest 校验、设置表单自动生成、市场**，等真有第三方再说。
- 接口名照现有规矩：标准的走 Satori，ir 自己的走 `/v1/internal/<路径>`，不另起 `ir.*` 方法族。
- 不留旧接口的回退：插件和 ir 一起升级。

## 前提和顺序

1. Satori 的 `channel-*` 事件（第一期做）。
2. 执行端的协议（`remote/wire`：hold、run、fs）并进应用链路。
3. 状态推送和 owner；删 `held.*`、`dsh.*`、`harness` 里的对应部分、`discover/remote.go` 的爬取；dsh 插件改成推状态。
4. 网页：画全部开关，`keepOn`。
