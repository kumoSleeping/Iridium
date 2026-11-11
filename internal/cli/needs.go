package cli

import (
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
)

// The needs (k_glossary "需要") from the host's terminal, and the pairing window (decision 7):
// ir needs, ir allow <id>, ir deny <id>, ir apps open [时长] | close. The daemon lets a person at
// the host through, never an agent.

const needsUsage = `用法：ir needs                      等待处理的申请和确认
      ir allow <ID> [--name 新名] [--claude] [--agents a,b] [--scope 位置,…] [--session]
      ir deny <ID>
  --name     应用接入：用这个名字建新应用（重名时不更新原有的）
  --claude   应用接入：带 Claude
  --agents --scope  应用接入、应用变更：只给这些（不超过申请的）
  --session  用凭据：这个会话之后都允许`

var needKinds = map[api.NeedKind]string{
	api.NeedAppJoin:   "应用接入",
	api.NeedAppUpdate: "更新配对",
	api.NeedAppChange: "应用变更",
	api.NeedLoginJoin: "登录加入",
	api.NeedInvite:    "网页邀请",
	api.NeedSecretSet: "存凭据",
	api.NeedSecretUse: "用凭据",
	api.NeedAsk:       "会话提问",
	api.NeedShare:     "分享",
}

func needsCmd(e adminEnv, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintln(stderr, needsUsage)
		return 2
	}
	var ns api.Needs
	if _, err := e.call("GET", "/v1/internal/needs", nil, &ns); err != nil {
		return failed(stderr, "needs", err)
	}
	if len(ns.Pending) == 0 {
		fmt.Fprintln(stdout, "没有等待处理的。")
		return 0
	}
	rows := [][]string{{"ID", "类型", "名字", "详情", "来自", "到期"}}
	for _, n := range ns.Pending {
		rows = append(rows, []string{n.ID, needKinds[n.Kind], n.Name, needDetail(n), n.Asker, hhmm(n.ExpiresAt)})
	}
	table(stdout, rows)
	return 0
}

// needDetail is what a need's row says beside its name.
func needDetail(n api.Need) string {
	var parts []string
	switch n.Kind {
	case api.NeedAppJoin, api.NeedAppUpdate:
		parts = append(parts, "配对码 "+n.Code)
		if p := n.Pair; p != nil {
			parts = append(parts, strings.TrimSpace(strings.Join([]string{p.Type, p.OS, p.Program}, " ")))
		}
		if n.Kind == api.NeedAppJoin && n.Wants != nil {
			parts = append(parts, permsText(*n.Wants))
		}
		if n.Kind == api.NeedAppUpdate && n.App != nil && n.App.Online > 0 {
			parts = append(parts, "原应用在线")
		}
	case api.NeedAppChange:
		if n.Wants != nil {
			parts = append(parts, "要 "+permsText(*n.Wants))
		}
	case api.NeedSecretUse:
		parts = append(parts, strings.Join(n.Secrets, ","))
	case api.NeedSecretSet:
		parts = append(parts, n.Note)
	case api.NeedAsk:
		if n.Card != nil {
			parts = append(parts, n.Card.Title)
		}
	}
	if n.Owner {
		parts = append(parts, "带 Claude")
	}
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "，")
}

func permsText(p api.Perms) string {
	list := func(l []string) string {
		if len(l) == 1 && l[0] == api.PermsAll {
			return "全部"
		}
		if len(l) == 0 {
			return "无"
		}
		return strings.Join(l, ",")
	}
	return "agent " + list(p.Agents) + "，范围 " + list(p.Scope)
}

func allowCmd(e adminEnv, args []string, stdout, stderr io.Writer) int {
	usage := func() int { fmt.Fprintln(stderr, needsUsage); return 2 }
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return usage()
	}
	id, rest := args[0], args[1:]
	a := api.NeedAnswer{Allow: true}
	var agents, scope *[]string
	for i := 0; i < len(rest); i++ {
		next := func() (string, bool) {
			if i+1 < len(rest) {
				i++
				return rest[i], true
			}
			return "", false
		}
		switch rest[i] {
		case "--claude":
			a.Owner = true
		case "--session":
			a.ForSession = true
		case "--name", "--agents", "--scope":
			v, ok := next()
			if !ok {
				return usage()
			}
			switch rest[i-1] {
			case "--name":
				a.Name = v
			case "--agents":
				l := splitList(v)
				agents = &l
			default:
				l := splitList(v)
				scope = &l
			}
		default:
			return usage()
		}
	}
	var n api.Need
	if err := findNeed(e, id, &n); err != nil {
		return failed(stderr, "allow", err)
	}
	if agents != nil || scope != nil {
		// What is not narrowed stays as asked.
		p := api.Perms{Agents: []string{api.PermsAll}, Scope: []string{api.PermsAll}}
		if n.Wants != nil {
			p = *n.Wants
		}
		if agents != nil {
			p.Agents = *agents
		}
		if scope != nil {
			p.Scope = *scope
		}
		a.Perms = &p
	}
	if n.Kind == api.NeedSecretSet {
		v, err := readValue(e, n.Name, stderr)
		if err != nil {
			return failed(stderr, "allow", err)
		}
		a.Value = v
	}
	var r api.NeedReply
	if _, err := e.call("POST", "/v1/internal/needs/"+url.PathEscape(id), a, &r); err != nil {
		return failed(stderr, "allow", err)
	}
	if r.State == "failed" {
		return failed(stderr, "allow", fmt.Errorf("%s", r.Error))
	}
	switch {
	case r.Invite != nil:
		fmt.Fprintf(stdout, "已允许。网页的邀请（%s 前有效，只能用一次）：\n\n  %s\n", hhmm(r.Invite.ExpiresAt), r.Invite.URL)
	case r.Name != "":
		fmt.Fprintf(stdout, "已允许：%s\n", r.Name)
	default:
		fmt.Fprintln(stdout, "已允许。")
	}
	return 0
}

func denyCmd(e adminEnv, args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		fmt.Fprintln(stderr, needsUsage)
		return 2
	}
	if _, err := e.call("POST", "/v1/internal/needs/"+url.PathEscape(args[0]), api.NeedAnswer{}, nil); err != nil {
		return failed(stderr, "deny", err)
	}
	fmt.Fprintln(stdout, "已拒绝。")
	return 0
}

// findNeed is pending need id.
func findNeed(e adminEnv, id string, out *api.Need) error {
	var ns api.Needs
	if _, err := e.call("GET", "/v1/internal/needs", nil, &ns); err != nil {
		return err
	}
	for _, n := range ns.Pending {
		if n.ID == id {
			*out = n
			return nil
		}
	}
	return fmt.Errorf("没有等待处理的 %s（ir needs 看列表）", id)
}

func splitList(v string) []string {
	out := []string{}
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// pairingCmd: ir apps open [时长] (10m by default; 15m, 1h, or minutes) | ir apps close.
func pairingCmd(e adminEnv, open bool, args []string, stdout, stderr io.Writer) int {
	usage := func() int { fmt.Fprintln(stderr, appsUsage); return 2 }
	req := api.PairWindow{Open: open}
	switch {
	case !open && len(args) > 0, len(args) > 1:
		return usage()
	case len(args) == 1:
		d, err := time.ParseDuration(args[0])
		if err != nil {
			n, nerr := strconv.Atoi(args[0])
			if nerr != nil {
				return usage()
			}
			d = time.Duration(n) * time.Minute
		}
		if d < time.Minute || d > time.Hour {
			fmt.Fprintln(stderr, "ir apps open：时长 1m 到 60m")
			return 2
		}
		req.Minutes = int((d + time.Minute - 1) / time.Minute)
	}
	var st api.PairWindow
	if _, err := e.call("PUT", "/v1/internal/pairing", req, &st); err != nil {
		return failed(stderr, "apps "+map[bool]string{true: "open", false: "close"}[open], err)
	}
	if st.Open {
		fmt.Fprintf(stdout, "配对已开放，到 %s。申请在 ir needs 里。\n", hhmm(st.Until))
	} else {
		fmt.Fprintln(stdout, "配对已关闭。")
	}
	return 0
}
