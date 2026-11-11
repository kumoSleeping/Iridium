# omd-iridium

把 OMD（oh-my-dsh）的 Ultra 放到 Iridium 网页上：思考强度滑条右边一个键，开着时下面一排子模式。装在 DSH Desktop 里（和 OMD、dsh-remote-iridium 一起），Desktop 打开着的会话和 Iridium 在这台机器上起的 dsh 会话都有。

- 用 dsh-remote-iridium 提供的服务 `iridium`（`attach`、`onButton`）；没有 OMD 或没有它时不启用。
- Iridium 起的会话里由 dsh-remote-iridium 按本包 `package.json` 的 `iridium.session` 装进会话进程。
