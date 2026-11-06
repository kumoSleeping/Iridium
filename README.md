# Iridium

一个网页（加一个 `ir` 命令），在一台 Linux 机器上托管和发现本机的 Claude Code、Codex、pi、DeepSeek Harness（dsh）会话：手机电脑都能看、接着聊、审批、开新会话；agent 之间用 `ir` 互相派活。一个 Go 二进制，网页嵌在里面。

![](web/sample/shots/desktop-light-zh.png)

## 装

```sh
curl -fLO https://github.com/kumoSleeping/Iridium/releases/latest/download/iridium-linux-amd64.tar.gz
tar xzf iridium-linux-amd64.tar.gz
./iridium install -now
```

打开 `http://127.0.0.1:7810`。要从外面访问：`ir public`（Cloudflare Tunnel + Passkey）。更新在设置里点一下，或 `ir update`。

MIT 许可。细节：[部署](docs/deploy.md) · [接口](docs/api.md) · [登录与权限](docs/gate.md) · [设计](docs/plan.md)

## 开发

Go 1.27、Node 24。

```sh
(cd web && npm ci && npm run build)   # 网页，嵌进二进制
go test ./... && go vet ./...
(cd web && npm run check)
scripts/deploy.sh                     # 装到本机用户服务
```
