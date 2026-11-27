# dsh-remote-iridium

装进 DSH Desktop（或 dsh 的 web profile），让 Iridium 的 dsh 会话在 Desktop 自己的进程里跑（Iridium `docs/exec.md`）。

- 安装：Desktop 的“插件”里填包名 `dsh-remote-iridium`（npm，跟着 Iridium 每次发版一起发，版本号相同），或 `dsh plugin --profile web add dsh-remote-iridium`。
- 本机：同一台电脑上运行的 Iridium 自动接入，不用配对（`local.js`）。Iridium 运行时在状态目录写 `local-exec.json`（只有本用户可读：本机回环地址和一个本机专用 TOKEN），插件看到就连，Iridium 退出就断。别的 Iridium 实例（测试用的）用环境变量 `IRIDIUM_STATE` 或配置 `localState` 指它的状态目录。设置里“本机 Iridium”关掉就不连。
- 其他 Iridium：设置里“Iridium”一页填地址，点“保存”；或在对话里输入 `/iridium https://你的-iridium`。Iridium 网页上对上配对码后允许，之后自己连。配对码过期自动换新码；对方停止接收申请时不再重试，用 `/iridium` 重新申请。
- 会话（`inproc.js`）：Iridium 在这台机器上开的 dsh 会话不起子进程，在本进程里由 dsh 自带的 SDK JSON-RPC 服务接待，经 Desktop 的会话控制器取会话：同一个会话在进程里只有一个 agent，Desktop 窗口和 Iridium 看的、驱动的是同一个，没有写锁之争。新会话登记进它目录的工作区，用 Iridium 选的预设；Iridium 的系统提示是这个 agent 自己的一段。停止只取消正在跑的回合，agent 留在 Desktop。
- 审批和提问：同时出现在 Desktop 窗口和 Iridium 网页，先答的算，另一边的卡片收起。
- `ir`：会话的 shell 调用拿到 `DSH_IRIDIUM_URL`、`DSH_IRIDIUM_TOKEN`、`DSH_IRIDIUM_SESSION`（按会话给，不改进程环境）；`ir`（`ir.js`，状态目录的 `bin/`，排在 PATH 最后）用它们连 Iridium。
- 告诉 Iridium 的（`desktop.js`，不轮询，跟着 dsh 自己的事件）：模型、预设、工作区、打开着的会话、“同步新会话”开关。“同步新会话”默认关；打开后在 Desktop 新开的会话出现在 Iridium 里。Iridium 搜索时按需列 Desktop 的会话（`desktop.sessions`）。
- 无窗口：Iridium 在 Desktop 没开时可用 Desktop 自带的运行时起一个无窗口的（环境变量 `IRIDIUM_DESKTOP_HEADLESS=1`）；真 Desktop 一连上，它收到 `desktop.freeze` 就不再写存储、不再建会话，回合跑完就退出。
- 状态：`/iridium` 显示本机 Iridium 和其他 Iridium 的连接、配对码、机器名、提供什么、运行中的会话数。
- 配置（profile 里 `remote-iridium` 这一行）：`url` 其他 Iridium 的地址；`local` 是否连本机 Iridium（默认连）；`localState` 本机 Iridium 的状态目录；`syncNewSessions` 同步新会话（默认关）；`name` 机器名（默认主机名）；`roots` 允许读写的目录；`shell: false` 网页上的命令不在本机执行（默认执行）；`agentShell: true` 让 agent 也能用 `ir sh`（默认不能）。
- 状态在 `$DSH_HOME/remote-iridium/`：`token/<主机>` 是各 Iridium 的 TOKEN，`<主机>/`、`local/` 是各自的会话。删掉 `token/<主机>` 就重新配对。
- 挂元素：插件经服务 `iridium`（`attach(session, place, content)`、`onButton(fn)`）往 Iridium 网页上的会话挂一段 Satori 元素（如思考强度旁的按钮），按下时 `fn(session, id, user)`；经执行端连接发给 Iridium（事件 `session.attach`，重连后全部重发；按下是事件 `session.button`）。
- 人格（`persona.js`）：Desktop 装了人格插件（OMD 的 `plugins/persona`，或单装的 dsh-persona-plugin）时，`agent.personas` 把它的人格各拼成一段报给 Iridium；Iridium 的会话有自己的设定时，人格插件的服务对这个会话答“关闭人格”，其他会话照旧。没装人格插件时什么都不做。
- OMD 的 Ultra（`omd.js`）：装了 OMD 时，Iridium 网页的思考强度滑条右边有 Ultra 键，按一下换一种：关、Workflow、（Agent Team、）自由。没装 OMD 时不启用。
