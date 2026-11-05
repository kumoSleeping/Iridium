// Package cli is `ir`, the command line agents and you use to reach the local daemon over its
// unix socket (plan "ir"). For now it only prints its help.
package cli

import (
	"fmt"
	"io"
	"os"
)

// Help is ir --help: at most 20 lines (plan "ir").
const Help = `ir: Iridium 的命令行，经本机 unix socket 用网页那套接口。

  ir new <agent> [目录] "任务"   开会话，返回 ID
  ir send <id> "消息"            发消息
  ir wait <id>                   等回合结束，打印最后一段回复
  ir read <id>                   读对话
  ir ls                          列出会话
  ir stop <id>                   停掉会话的 agent

  new、send 可加 --wait；可选 --model、--approve。
  wait 的退出码：0 结束，2 需要你，1 出错或已退出。

（还没有实现：现在只打印这段说明。）
`

// Main runs ir with args, giving the exit code.
func Main(args []string) int { return run(args, os.Stdout, os.Stderr) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(stdout, Help)
		return 0
	}
	fmt.Fprintf(stderr, "ir %s: not implemented yet\n\n%s", args[0], Help)
	return 2
}
