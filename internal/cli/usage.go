package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/kumoSleeping/iridium/internal/api"
)

// usageDims are what ir usage --by may group by, in the order a row shows them.
var usageDims = []string{"day", "terminal", "agent", "model"}

const usageHelp = `用法：ir usage [名字|me] [-d 天数] [--by day,terminal,agent,model] [--json]
  默认最近 7 天、所有终端（只管自己的终端看自己的），按天、终端、agent、模型分；--by 只按其中几样合起来。`

// usageCmd is ir usage: the tokens used by day, terminal, agent and model (GET /api/usage).
func usageCmd(e adminEnv, args []string, stdout, stderr io.Writer) int {
	q := url.Values{}
	by := usageDims
	asJSON := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		val := func() (string, bool) {
			if k, v, ok := strings.Cut(a, "="); ok && strings.HasPrefix(k, "-") {
				return v, true
			}
			if i+1 < len(args) {
				i++
				return args[i], true
			}
			return "", false
		}
		switch {
		case a == "-h" || a == "--help":
			fmt.Fprintln(stdout, usageHelp)
			return 0
		case a == "--json":
			asJSON = true
		case a == "-d" || a == "--days" || strings.HasPrefix(a, "--days="):
			v, ok := val()
			if n, err := strconv.Atoi(v); !ok || err != nil || n < 1 {
				fmt.Fprintln(stderr, usageHelp)
				return 2
			}
			q.Set("days", v)
		case a == "--by" || strings.HasPrefix(a, "--by="):
			v, ok := val()
			if !ok {
				fmt.Fprintln(stderr, usageHelp)
				return 2
			}
			by = nil
			for _, d := range strings.Split(v, ",") {
				d = strings.TrimSpace(d)
				if d == "" {
					continue
				}
				if !slices.Contains(usageDims, d) {
					fmt.Fprintln(stderr, usageHelp)
					return 2
				}
				by = append(by, d)
			}
		case strings.HasPrefix(a, "-") || q.Has("terminal"):
			fmt.Fprintln(stderr, usageHelp)
			return 2
		default:
			q.Set("terminal", a)
		}
	}
	path := "/api/usage"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var u api.Usage
	if _, err := e.call("GET", path, nil, &u); err != nil {
		return failed(stderr, "usage", err)
	}
	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(u)
		return 0
	}
	if len(u.Rows) == 0 {
		fmt.Fprintf(stdout, "%s 到 %s 没有用量。\n", u.Days[0], u.Days[len(u.Days)-1])
		return 0
	}
	printUsage(stdout, u, sortedDims(by))
	return 0
}

// sortedDims puts by in a row's order (day, terminal, agent, model), without repeats.
func sortedDims(by []string) []string {
	var out []string
	for _, d := range usageDims {
		if slices.Contains(by, d) {
			out = append(out, d)
		}
	}
	return out
}

// usageTerminalName is a row's terminal as a person reads it.
func usageTerminalName(u api.Usage, id string) string {
	if id == "" {
		return "（不明）"
	}
	if t, ok := u.Terminals[id]; ok {
		return t.Name
	}
	if len(id) > 8 {
		id = id[:8]
	}
	return "已移除 " + id
}

// printUsage prints u's rows summed by the dimensions by, the latest day first and the most used
// first within it, then the total.
func printUsage(w io.Writer, u api.Usage, by []string) {
	type sum struct {
		keys  []string
		turns int
		in    int64
		out   int64
		cr    int64
		cw    int64
		cost  float64
	}
	index := map[string]int{}
	var sums []sum
	var all sum
	for _, r := range u.Rows {
		var keys []string
		for _, d := range by {
			switch d {
			case "day":
				keys = append(keys, r.Day)
			case "terminal":
				keys = append(keys, usageTerminalName(u, r.Terminal))
			case "agent":
				keys = append(keys, string(r.Agent))
			case "model":
				keys = append(keys, r.Model)
			}
		}
		k := strings.Join(keys, "\x00")
		i, ok := index[k]
		if !ok {
			i = len(sums)
			index[k] = i
			sums = append(sums, sum{keys: keys})
		}
		for _, s := range []*sum{&sums[i], &all} {
			s.turns += r.Turns
			s.in += r.Input
			s.out += r.Output
			s.cr += r.CacheRead
			s.cw += r.CacheWrite
			s.cost += r.CostUSD
		}
	}
	total := func(s sum) int64 { return s.in + s.out + s.cr + s.cw }
	day := slices.Contains(by, "day")
	sort.SliceStable(sums, func(i, j int) bool {
		if day && sums[i].keys[0] != sums[j].keys[0] {
			return sums[i].keys[0] > sums[j].keys[0]
		}
		return total(sums[i]) > total(sums[j])
	})
	head := map[string]string{"day": "日期", "terminal": "终端", "agent": "agent", "model": "模型"}
	rows := [][]string{{}}
	for _, d := range by {
		rows[0] = append(rows[0], head[d])
	}
	rows[0] = append(rows[0], "回合", "输入", "输出", "缓存读", "缓存写", "合计", "费用")
	line := func(keys []string, s sum) []string {
		cost := "—"
		if s.cost > 0 {
			cost = fmt.Sprintf("$%.2f", s.cost)
		}
		return append(append([]string{}, keys...), strconv.Itoa(s.turns), compact(s.in), compact(s.out), compact(s.cr), compact(s.cw), compact(total(s)), cost)
	}
	for _, s := range sums {
		rows = append(rows, line(s.keys, s))
	}
	if len(sums) > 1 {
		keys := make([]string, len(by))
		if len(keys) > 0 {
			keys[0] = "合计"
		}
		rows = append(rows, line(keys, all))
	}
	rightAlign(rows, len(by))
	fmt.Fprintf(w, "%s 到 %s\n", u.Days[0], u.Days[len(u.Days)-1])
	table(w, rows)
}

// rightAlign pads the cells from column from on to their column's width, numbers to the right.
func rightAlign(rows [][]string, from int) {
	for c := from; c < len(rows[0]); c++ {
		n := 0
		for _, r := range rows {
			n = max(n, width(r[c]))
		}
		for _, r := range rows {
			r[c] = strings.Repeat(" ", n-width(r[c])) + r[c]
		}
	}
}

// compact writes a token count short: 980, 12.3k, 4.5M.
func compact(n int64) string {
	switch {
	case n >= 1_000_000:
		return strconv.FormatFloat(float64(n)/1_000_000, 'f', 1, 64) + "M"
	case n >= 1000:
		return strconv.FormatFloat(float64(n)/1000, 'f', 1, 64) + "k"
	}
	return strconv.FormatInt(n, 10)
}
