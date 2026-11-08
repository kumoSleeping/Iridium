# dsh-remote-iridium

装进 dsh 的 web profile，让这台机器替一个 Iridium 跑 dsh 会话（Iridium `docs/exec.md`）。

- 安装：`dsh web` 的“添加插件”里填包名 `dsh-remote-iridium`（npm，跟着 Iridium 每次发版一起发，版本号相同），或 `dsh plugin --profile web add dsh-remote-iridium`。
- 申请连接：dsh 设置里的“Iridium”一页，填“Iridium 地址”，点“保存”；或在对话里输入 `/iridium https://你的-iridium`。不用重启 dsh。
- 配对：Iridium 网页上弹出确认卡片，对上配对码后点允许，之后自己连。卡片上写着本机提供什么：系统、dsh 版本、能否执行命令（照 `shell`、`agentShell`）。配对码过期就自动换新码；对方停止接收申请时不再重试，用 `/iridium` 重新申请。换地址就向新地址重新申请。
- 状态：`/iridium` 显示配对码或连接（已连接多久、断开多久和原因）、Iridium 给的机器名、提供的 agent、命令和文件范围、正在运行的进程数；终端里也打一行。dsh 低于 0.2.2 时提示 Iridium 不会在本机开 dsh。
- 配置（profile 里 `remote-iridium` 这一行）：`url` 服务器地址（可在设置里“Iridium”一页改）；`unsynced` 不同步到 Iridium 的工作区的文件夹（设置里“Iridium”一页逐个工作区开关，立刻生效）；`ungrouped` 未分组的会话是否同步（同一页最后一行，默认同步）；`name` 机器名（默认主机名）；`roots` 允许读写的目录（默认除禁区外都行；`shell: false` 只让它启动 dsh，Iridium 网页上的命令不在本机执行（默认执行）；`agentShell: true` 让 Iridium 上的 agent 也能用 `ir sh` 在本机执行命令（默认不能）。
- 和 dsh 本身：Iridium 能看到这台机器上 dsh 的工作区和会话；本机 dsh 里打开着（拿着写锁）的会话，Iridium 的消息交给它在本机的 agent 接着跑（`dsh.prompt`）；Iridium 在本机开的会话登记进它目录的工作区（没有就新建），dsh 的侧边栏里也有。
- 本机上 Iridium 开的会话里有 `ir`（`ir.js`，放在状态目录的 `bin/`，排在 PATH 最前）：经 HTTPS、用这个会话自己的 TOKEN 连 Iridium，能开会话、发消息、等结果、列会话（Iridium `docs/exec.md`“远端的 ir”）。
- 状态在 `$DSH_HOME/remote-iridium/`：每个 Iridium 一份，`token/<主机>` 是它的 TOKEN，`<主机>/` 是它的进程。换到别的 Iridium 再换回来不用重新配对；删掉 `token/<主机>` 就重新配对。
