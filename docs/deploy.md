# 部署

在一台 Linux 机器上，以普通用户装 Iridium。照 Rhodium 的做法：用户级 systemd 服务 + linger。

## 位置

| 东西 | 位置 |
|---|---|
| 程序 | `~/.local/opt/iridium/iridium`；`~/.local/bin/ir` 链到它 |
| 配置 | `~/.config/iridium/config.json` |
| 状态（库、托管进程的输出、上传兜底） | `~/.local/state/iridium/` |
| 服务 | `~/.config/systemd/user/iridium.service`，听 `127.0.0.1:7810` |
| 隧道服务（`ir public` 建） | 用户级单元，不碰系统的 cloudflared |

## 装和升级

从 Release 装（一个二进制，网页在里面）：

```sh
curl -fLO https://github.com/kumoSleeping/Iridium/releases/latest/download/iridium-linux-amd64.tar.gz
tar xzf iridium-linux-amd64.tar.gz
./iridium install -now     # 复制到 ~/.local/opt/iridium、链 ~/.local/bin/ir、写服务并启动
```

- ARM 机器换成 `iridium-linux-arm64.tar.gz`。`-prefix`、`-bin` 改位置。
- 之后升级：设置里“更新”（默认自动在后台下好），或 `ir update`。

从源码装：

```sh
git clone https://github.com/kumoSleeping/Iridium && cd Iridium
scripts/deploy.sh                          # 构建网页和程序、装好、写服务单元
systemctl --user enable --now iridium.service
```

- 要 Go 1.27 和 Node 24（npm）。
- `deploy.sh` 不启动没在跑的服务；在跑的会重启到新版本。托管的 agent 不受影响。
- 之后升级：拉代码，再跑一次 `scripts/deploy.sh`；或者 `ir update`（装 Release 里的版本）。

## 发版

推一个 `v*` 标签，`.github/workflows/release.yml` 构建网页、跑测试，`scripts/release.sh` 出 `iridium-linux-{amd64,arm64}.tar.gz`（静态、去符号）和 `SHA256SUMS`，Release 说明里带大小表。本地试：`scripts/release.sh v0.0.0 /tmp/rel`（先 `npm run build`）。

## linger：必须开

没开 linger，账号退出登录后服务、托管的 agent、隧道都会停，重启后也不会自己起来。

- `deploy.sh` 会先试 `loginctl enable-linger`（有的系统不用 root）。
- 不行就要 root 跑一次：`sudo loginctl enable-linger <用户>`。
- 检查：`loginctl show-user <用户> -p Linger` 应为 `yes`。

## 上公网（可选）

```sh
ir public        # 在真正的终端里跑
```

1. 打开它给的 Cloudflare 登录链接，选域名。
2. 填子域名（默认 `ir`）。已有同名 DNS 记录时会问，先确认不是别的服务在用。
3. 它建隧道、加 DNS、起隧道服务、从外网检查，最后打印第一个面板邀请（链接 + 二维码）。
4. 手机打开邀请，起名、注册 Passkey。

撤掉：`ir public off`（DNS 记录要去 Cloudflare 控制台删）。

## 检查

```sh
systemctl --user is-active iridium.service      # active
ss -ltn | grep ':7810 '                         # 只听 127.0.0.1
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:7810/   # 200
ir ls
```

## 停和卸载

```sh
systemctl --user disable --now iridium.service
ir public off                                    # 如果上过公网
rm -r ~/.local/opt/iridium ~/.local/bin/ir ~/.config/systemd/user/iridium.service
```

配置和状态目录留着，要彻底删再手动删 `~/.config/iridium` 和 `~/.local/state/iridium`。agent 自己的对话记录不动。

## 测试节点

`scripts/push-test.sh`：构建工作区（版本由 `scripts/version.sh` 算，比上一个 Release 新、比下一个旧），经 `ssh ir@localhost` 放到测试节点的 `~/.local/opt/iridium`，跑一次 `version`，重启服务；第一次推时写配置（`127.0.0.1:7811`）、装单元。`TARGET`、`KEY`、`PORT`、`NO_WEB=1` 可改。主节点不推。

## 测试部署

`scripts/test-deploy.sh up|down`：装到临时目录、临时状态，用假 agent，单元名 `iridium-test-deploy-*`，随机端口。不碰正式服务。
