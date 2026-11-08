# dsh-remote-iridium

装进 dsh 的 web profile，让这台机器替一个 Iridium 跑 dsh 会话（Iridium `docs/exec.md`）。

- 安装：`dsh web` 的“添加插件”里填包名 `dsh-remote-iridium`（npm，跟着 Iridium 每次发版一起发，版本号相同），或 `dsh plugin --profile web add dsh-remote-iridium`。
- 申请连接：插件页里打开它，填“Iridium 地址”，点“申请连接”；或在对话里输入 `/iridium https://你的-iridium`。不用重启 dsh。
- 配对：Iridium 网页上弹出确认卡片，对上配对码后点允许，之后自己连。`/iridium` 显示配对码和连接状态（终端里也打一行）。换地址就向新地址重新申请。
- 配置（profile 里 `remote-iridium` 这一行）：`url` 服务器地址（可在插件页改）；`name` 机器名（默认主机名）；`roots` 允许读写的目录（默认除禁区外都行）；`shell: false` 只让它启动 dsh，Iridium 网页上的命令不在本机执行（默认执行）。
- 状态在 `$DSH_HOME/remote-iridium/`；删掉 `token/` 就重新配对。
