# dsh-remote-iridium

装进 dsh 的 web profile，让这台机器替一个 Iridium 跑 dsh 会话（Iridium `docs/exec.md`）。

- 安装：`dsh plugin --profile web add <包>`，然后重启 `dsh web`。Iridium 网页上下载的包已经写好服务器地址。
- 配对：第一次启动时 `dsh web` 的终端里打出 4 位配对码，网页上弹出确认卡片，对上了点允许。之后自己连。
- 配置（`cordis.patch.yml` 里 `remote-iridium` 这一行）：`url` 服务器地址；`name` 机器名（默认主机名）；`roots` 允许读写的目录（默认除禁区外都行）。
- 状态在 `$DSH_HOME/remote-iridium/`；删掉 `token/` 就重新配对。
