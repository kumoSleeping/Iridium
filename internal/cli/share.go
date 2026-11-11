package cli

import (
	"cmp"
	"fmt"
	"os"
	"strings"

	"github.com/kumoSleeping/iridium/internal/api"
)

const shareUsage = `usage: ir share 应用/登录 <会话|位置|文件> ["留言"]`

// ir share 应用/登录 <what> ["note"] (draft 二「消息」 分享): a session (its ID or a prefix), a
// place (a folder, app:path at that app's computer) or a file (in the session ir runs in), shared
// with that login, which must be able to use it.
func cmdShare(e *env, args []string) int {
	o, err := parse(args, "")
	if err != nil {
		return e.fail(err)
	}
	if len(o.pos) < 2 || len(o.pos) > 3 || !strings.Contains(o.pos[0], "/") {
		return e.failf(shareUsage)
	}
	req := api.ShareRequest{To: o.pos[0], Caller: e.caller()}
	if len(o.pos) == 3 {
		req.Note = o.pos[2]
	}
	what := o.pos[1]
	if id, err := e.resolve(what); err == nil {
		req.What, req.Session = api.ShareOfSession, id
	} else {
		machine, p, err := pathArg(what)
		if err != nil {
			return e.fail(err)
		}
		st, serr := os.Stat(p)
		switch {
		case machine != "":
			req.What, req.Place = api.ShareOfPlace, what
		case serr != nil:
			return e.failf("没有会话或路径 %s", what)
		case st.IsDir():
			req.What, req.Place = api.ShareOfPlace, p
		case req.Caller == "":
			return e.failf("分享文件要在会话里运行 ir share")
		default:
			req.What, req.Session, req.Path = api.ShareOfFile, req.Caller, p
		}
	}
	var r api.ShareReply
	if err := e.do("POST", "/v1/internal/share", req, &r); err != nil {
		return e.fail(err)
	}
	fmt.Fprintf(e.out, "已分享给 %s/%s\n", r.App, cmp.Or(r.Name, r.Login))
	return exitDone
}
