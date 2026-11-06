// Package cli is `ir`, the command line agents and you use to reach the local daemon over its
// unix socket (plan "ir"), with the same API as the web page. The commands that manage
// interactive terminals, ir public and ir update are in admin.go.
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
  ir read <id> [-n 条数] [--full]    每条消息和它的最终回复，截短（默认最近 10 条；--full 整页原文）
  ir ls | find <词> [--all]          列会话 | 按名字、标题、最后一句找（--all 连归档、做完的子代理）
  ir stop | archive | unarchive <id> 停掉 agent | 归档 | 取消归档
  ir cp <源> <目标> | files <目录>   拷文件，打印路径、大小、sha256 | 列一层：名字、大小、时间、类型
  new、send 加 --wait 等到回复；send 在回合进行中默认插进这一回合，加 --queue 排到回合结束后
  new 可加 --model <模型>、--approve <审批模式>
  退出码：0 结束，2 需要你（提问、审批），1 出错或已退出。路径可写 机器:路径
管理（agent 调用时 TOKEN 不进对话，由网页面板确认）：
  ir invite [名字] | token <名字>    新网页面板的链接 | 插件 TOKEN
    --owner：确认这是我本人用的终端，带 Claude（只能你本人跑，agent 不行）
  ir access | revoke <名字>          列出终端和邀请 | 撤销
  ir access <名字> --claude on|off   开关 Claude（开：同 --owner）
  ir profile <名字> [--name 新名] [--avatar 图片] [--device computer|phone]
  ir public [off]                    经 Cloudflare 上公网（在终端里跑）
  ir update [--check]                更新 Iridium
`

// Main runs ir with args, giving the exit code.
func Main(args []string) int { return run(args, os.Stdout, os.Stderr) }

// commands are ir's commands by name. Each gets its arguments after the name.
var commands = map[string]func(e *env, args []string) int{
	"new":       cmdNew,
	"send":      cmdSend,
	"wait":      cmdWait,
	"read":      cmdRead,
	"ls":        cmdLs,
	"find":      cmdFind,
	"stop":      cmdStop,
	"archive":   func(e *env, args []string) int { return cmdArchive(e, args, true) },
	"unarchive": func(e *env, args []string) int { return cmdArchive(e, args, false) },
	"cp":        cmdCp,
	"files":     cmdFiles,
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(stdout, Help)
		return 0
	}
	if admin, ok := adminCommands[args[0]]; ok { // admin.go
		e, err := detect()
		if err != nil {
			return failed(stderr, args[0], err)
		}
		return admin(e, args[1:], stdout, stderr)
	}
	cmd := commands[args[0]]
	if cmd == nil {
		fmt.Fprintf(stderr, "ir: no command %q\n\n%s", args[0], Help)
		return 1
	}
	return cmd(newEnv(stdout, stderr), args[1:])
}
