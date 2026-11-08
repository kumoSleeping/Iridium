# dsh-remote-iridium

装进 dsh 的 web profile，让这台机器替一个 Iridium 跑 dsh 会话（Iridium `docs/exec.md`）。

- 安装：`dsh web` 的“添加插件”里填包名 `dsh-remote-iridium`（npm，跟着 Iridium 每次发版一起发，版本号相同），或 `dsh plugin --profile web add dsh-remote-iridium`。
- 申请连接：插件页里打开它，填“Iridium 地址”，点“申请连接”；或在对话里输入 `/iridium https://你的-iridium`。不用重启 dsh。
- 配对：Iridium 网页上弹出确认卡片，对上配对码后点允许，之后自己连。卡片上写着本机提供什么：系统、dsh 版本、能否执行命令（照 `shell`、`agentShell`）。配对码过期就自动换新码；对方停止接收申请时不再重试，用 `/iridium` 重新申请。换地址就向新地址重新申请。
- 状态：`/iridium` 显示配对码或连接（已连接多久、断开多久和原因）、Iridium 给的机器名、提供的 agent、命令和文件范围、正在运行的进程数；终端里也打一行。dsh 低于 0.2.2 时提示 Iridium 不会在本机开 dsh。
- 配置（profile 里 `remote-iridium` 这一行）：`url` 服务器地址（可在插件页改）；`name` 机器名（默认主机名）；`roots` 允许读写的目录（默认除禁区外都行）；`shell: false` 只让它启动 dsh，Iridium 网页上的命令不在本机执行（默认执行）；`agentShell: true` 让 Iridium 上的 agent 也能用 `ir sh` 在本机执行命令（默认不能）。
- 状态在 `$DSH_HOME/remote-iridium/`：每个 Iridium 一份，`token/<主机>` 是它的 TOKEN，`<主机>/` 是它的进程。换到别的 Iridium 再换回来不用重新配对；删掉 `token/<主机>` 就重新配对。
