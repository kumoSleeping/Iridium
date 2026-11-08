package testagent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kumoSleeping/iridium/internal/transcript"
)

// dshInput is what the fake reads of an input line: its offset in the recording's out.log, its
// request id, session id and cwd (initialize's), where it has them.
type dshInput struct {
	off           int
	id, sess, cwd string
}

func parseDshInput(off int, line string) dshInput {
	var m struct {
		ID     string `json:"id"`
		Params struct {
			SessionID string `json:"sessionId"`
			Cwd       string `json:"cwd"`
		} `json:"params"`
	}
	_ = json.Unmarshal([]byte(line), &m)
	return dshInput{off, m.ID, m.Params.SessionID, m.Params.Cwd}
}

// Dsh is a fake dsh --profile sdk that replays a recording (internal/agent/dsh/testdata/<name>:
// in.log, out.log) in $IRIDIUM_FAKE_DSH: after the k-th line it reads it writes what dsh wrote
// after the k-th input, with the recording's request ids, session id and cwd made the ones it is
// sent. Once it knows the session and its cwd it puts $IRIDIUM_FAKE_DSH_TRANSCRIPT, if set, where
// dsh keeps the session's transcript under $DSH_HOME. It ends at the end of its input.
func Dsh() int {
	if probe() {
		return 0
	}
	dir := os.Getenv("IRIDIUM_FAKE_DSH")
	inLog, err := os.ReadFile(filepath.Join(dir, "in.log"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	out, err := os.ReadFile(filepath.Join(dir, "out.log"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	var rec []dshInput
	var recSess, recCwd string
	for _, l := range strings.Split(strings.TrimSpace(string(inLog)), "\n") {
		off, line, _ := strings.Cut(l, " ")
		n, _ := strconv.Atoi(off)
		in := parseDshInput(n, line)
		recSess, recCwd = cmpOr(recSess, in.sess), cmpOr(recCwd, in.cwd)
		rec = append(rec, in)
	}
	var sess, cwd string
	ids := map[string]string{} // the recording's request id → the one sent
	placed := false
	br := bufio.NewReader(os.Stdin)
	for k := 0; ; k++ {
		line, err := br.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				fmt.Fprintln(os.Stderr, err)
			}
			return 0
		}
		if k >= len(rec) {
			continue
		}
		got := parseDshInput(0, line)
		sess, cwd = cmpOr(sess, got.sess), cmpOr(cwd, got.cwd)
		if got.id != "" && rec[k].id != "" {
			ids[rec[k].id] = got.id
		}
		if !placed && sess != "" && cwd != "" {
			placed = true
			if src := os.Getenv("IRIDIUM_FAKE_DSH_TRANSCRIPT"); src != "" {
				placeDshTranscript(src, sess, cwd)
			}
		}
		end := len(out)
		if k+1 < len(rec) {
			end = rec[k+1].off
		}
		seg := bytes.Clone(out[rec[k].off:end])
		for from, to := range ids {
			seg = bytes.ReplaceAll(seg, []byte(`"id":"`+from+`"`), []byte(`"id":"`+to+`"`))
		}
		for _, p := range [][2]string{{recSess, sess}, {recCwd, cwd}} {
			if p[0] != "" && p[1] != "" {
				seg = bytes.ReplaceAll(seg, []byte(p[0]), []byte(p[1]))
			}
		}
		_, _ = os.Stdout.Write(seg)
	}
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// placeDshTranscript copies the transcript src to where dsh keeps session sess of cwd.
func placeDshTranscript(src, sess, cwd string) {
	home := os.Getenv("DSH_HOME")
	if home == "" {
		return
	}
	b, err := os.ReadFile(src)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	dir := filepath.Join(home, "sessions", transcript.DshProjectDir(cwd), sess)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	if err := os.WriteFile(filepath.Join(dir, filepath.Base(src)), b, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}

// fakeCatalog is what the fake prints for Iridium's probe of dsh's models: dsh 0.2.2-rc.1's with
// a DeepSeek key.
const fakeCatalog = `IRIDIUM-CATALOG {"providers":[{"id":"deepseek-official","name":"DeepSeek","models":[` +
	`{"id":"deepseek-flash","name":"DeepSeek-V4.1-Flash","efforts":["off","low","high","max"],"defaultEffort":"high"},` +
	`{"id":"deepseek-v4-pro","name":"DeepSeek-V4-Pro","efforts":["off","low","high","max"],"defaultEffort":"high"}]},` +
	`{"id":"deepseek-account","name":"DeepSeek Account","models":[]}],"default":{"provider":"deepseek-official","model":"deepseek-flash"}}`

// probe: started with the --patch of Iridium's probe of dsh's models, the fake prints them, as the
// probe would, and ends.
func probe() bool {
	for i, a := range os.Args {
		if a != "--patch" || i+1 >= len(os.Args) {
			continue
		}
		if b, err := os.ReadFile(os.Args[i+1]); err == nil && bytes.Contains(b, []byte("iridium-catalog-probe")) {
			fmt.Println(fakeCatalog)
			return true
		}
	}
	return false
}
