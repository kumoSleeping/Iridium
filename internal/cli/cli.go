// Package cli is `ir`, the command line agents and you use to reach the local daemon over its
// unix socket (plan "ir"), with the same API as the web page.
package cli

import (
	"fmt"
	"io"
	"os"
)

// Help is ir --help: at most 20 lines (plan "ir"). The agent part is the first 11.
const Help = `ir：本机 Iridium 的命令行（经 unix socket）。<id> 写开头几位就行。

  ir new <agent> [目录] "任务"       开会话，打印 ID
  ir send <id> "消息" [-a 文件]...   发消息，可带附件
  ir wait <id>                       等回合结束，打印最后一段回复
  ir read <id>                       读最近一页对话
  ir ls / ir stop <id>               列会话 / 停掉 agent
  ir cp <源> <目标>                  拷文件，打印路径、大小、sha256
  ir files <目录>                    列一层：名字、大小、时间、类型
  new、send 加 --wait 等到回复；send 加 --steer 插进正在跑的回合（不加就排队）
  new 可加 --model <模型>、--approve <审批模式>
  退出码：0 结束，2 需要你（提问、审批），1 出错或已退出。路径可写 机器:路径
`

// Main runs ir with args, giving the exit code.
func Main(args []string) int { return run(args, os.Stdout, os.Stderr) }

// commands are ir's commands by name. Each gets its arguments after the name.
var commands = map[string]func(e *env, args []string) int{
	"new":   cmdNew,
	"send":  cmdSend,
	"wait":  cmdWait,
	"read":  cmdRead,
	"ls":    cmdLs,
	"stop":  cmdStop,
	"cp":    cmdCp,
	"files": cmdFiles,
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(stdout, Help)
		return 0
	}
	cmd := commands[args[0]]
	if cmd == nil {
		fmt.Fprintf(stderr, "ir: no command %q\n\n%s", args[0], Help)
		return 1
	}
	return cmd(newEnv(stdout, stderr), args[1:])
}
