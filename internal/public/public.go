// Package public is `ir public`: put Iridium on the internet through a Cloudflare Tunnel of its
// own (plan "Cloudflare 一键上公网"), and `ir public off` to take it down again.
//
// Everything of Iridium's lives in its state directory (Dir): the login (cert.pem, under
// Dir/.cloudflared, since cloudflared writes it to $HOME/.cloudflared), the tunnel's credentials,
// its config and what was set up (public.json). The tunnel runs as a user systemd unit of its own.
// The machine's own cloudflared service, /etc/cloudflared and ~/.cloudflared are never touched.
package public

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Daemon is the running Iridium, over its socket.
type Daemon interface {
	// SetOrigin sets public.origin ("" turns it off); the passkey gate takes over at once.
	SetOrigin(ctx context.Context, origin string) error
	// Invite makes a panel invite: the join link and until when it works.
	Invite(ctx context.Context) (url string, expires time.Time, err error)
}

type Options struct {
	Dir     string // Iridium's own cloudflared files; created 0700
	BinDir  string // where cloudflared goes if there is none: ~/.local/bin
	Path    string // where else to look for cloudflared; "" is $PATH
	UnitDir string // user systemd units: ~/.config/systemd/user
	Unit    string // the tunnel's unit; "" is iridium-cloudflared
	Port    int    // the daemon's port on 127.0.0.1
	Machine string // names the tunnel iridium-<machine>; "" is the host name
	Home    string // the user's home, where a cloudflared too old for $HOME might write cert.pem

	HTTP   *http.Client // downloads, the Cloudflare API, the health check
	GitHub string       // "" is https://api.github.com
	CFAPI  string       // "" is https://api.cloudflare.com/client/v4

	// Systemctl runs systemctl --user with args.
	Systemctl func(ctx context.Context, args ...string) error
	// Ask asks the person at the terminal; def is the answer to an empty line.
	Ask    func(question, def string) (string, error)
	Out    io.Writer
	Daemon Daemon

	HealthWait  time.Duration // "" is 3 minutes
	HealthEvery time.Duration // "" is 3 seconds
}

func (o *Options) defaults() {
	if o.Unit == "" {
		o.Unit = "iridium-cloudflared"
	}
	if o.Machine == "" {
		o.Machine, _ = os.Hostname()
	}
	if o.HTTP == nil {
		o.HTTP = &http.Client{Timeout: 5 * time.Minute}
	}
	if o.GitHub == "" {
		o.GitHub = "https://api.github.com"
	}
	if o.CFAPI == "" {
		o.CFAPI = "https://api.cloudflare.com/client/v4"
	}
	if o.HealthWait == 0 {
		o.HealthWait = 3 * time.Minute
	}
	if o.HealthEvery == 0 {
		o.HealthEvery = 3 * time.Second
	}
	if o.Out == nil {
		o.Out = io.Discard
	}
}

// State is what On set up, in Dir/public.json, for Off.
type State struct {
	Host        string `json:"host"`
	Tunnel      string `json:"tunnel"` // its UUID
	Name        string `json:"name"`
	Unit        string `json:"unit"`
	Cloudflared string `json:"cloudflared"`
}

func (o *Options) cert() string      { return filepath.Join(o.Dir, ".cloudflared", "cert.pem") }
func (o *Options) creds() string     { return filepath.Join(o.Dir, "tunnel.json") }
func (o *Options) config() string    { return filepath.Join(o.Dir, "config.yml") }
func (o *Options) stateFile() string { return filepath.Join(o.Dir, "public.json") }
func (o *Options) unitFile() string  { return filepath.Join(o.UnitDir, o.Unit+".service") }

func (o *Options) say(format string, a ...any) { fmt.Fprintf(o.Out, format+"\n", a...) }

func (o *Options) yes(question string, def bool) (bool, error) {
	d := "n"
	hint := "[y/N]"
	if def {
		d, hint = "y", "[Y/n]"
	}
	a, err := o.Ask(question+" "+hint, d)
	if err != nil {
		return false, err
	}
	a = strings.ToLower(strings.TrimSpace(a))
	if a == "" {
		a = d
	}
	return a == "y" || a == "yes" || a == "是", nil
}

// run runs cloudflared, its output both shown and given back.
func (o *Options) run(ctx context.Context, env []string, cf string, args ...string) (string, error) {
	var out strings.Builder
	cmd := exec.CommandContext(ctx, cf, args...)
	cmd.Stdout = io.MultiWriter(o.Out, &out)
	cmd.Stderr = io.MultiWriter(o.Out, &out)
	cmd.Env = append(os.Environ(), env...)
	err := cmd.Run()
	return out.String(), err
}

// quiet runs cloudflared and gives its output without showing it.
func (o *Options) quiet(ctx context.Context, cf string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, cf, args...).CombinedOutput()
	return string(out), err
}

var label = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// TunnelName is iridium-<machine>, in the letters a tunnel name takes.
func TunnelName(machine string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(machine) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	m := strings.Trim(b.String(), "-")
	if m == "" {
		m = "host"
	}
	return "iridium-" + m
}

// On sets the public way in up, step by step, asking where it must; run again, it reuses what is
// there.
func On(ctx context.Context, o Options) error {
	o.defaults()
	if err := os.MkdirAll(filepath.Join(o.Dir, ".cloudflared"), 0o700); err != nil {
		return err
	}
	// 1. cloudflared.
	cf, err := o.cloudflared(ctx)
	if err != nil {
		return err
	}
	// 2. The login: Cloudflare has you pick the domain (zone) there.
	if err := o.login(ctx, cf); err != nil {
		return err
	}
	zone, err := readCert(o.cert())
	if err != nil {
		return err
	}
	domain := o.zoneName(ctx, zone)
	if domain == "" {
		if domain, err = o.Ask("你在 Cloudflare 上选的域名是？", ""); err != nil {
			return err
		}
		domain = strings.Trim(strings.ToLower(strings.TrimSpace(domain)), ".")
		if domain == "" {
			return errors.New("没有域名：要一个托管在 Cloudflare 上的域名")
		}
	}
	// 3. The host name.
	host, err := o.hostName(domain)
	if err != nil {
		return err
	}
	// 4. The tunnel, its DNS record, its config.
	name := TunnelName(o.Machine)
	id, err := o.tunnel(ctx, cf, name)
	if err != nil {
		return err
	}
	if err := o.route(ctx, cf, id, host); err != nil {
		return err
	}
	if err := o.writeConfig(ctx, cf, id, host); err != nil {
		return err
	}
	st := State{Host: host, Tunnel: id, Name: name, Unit: o.Unit, Cloudflared: cf}
	if err := writeJSON(o.stateFile(), st); err != nil {
		return err
	}
	// 5. Its own user service.
	if err := o.installUnit(ctx, cf, host); err != nil {
		return err
	}
	// 6. public.origin, then from outside until it answers.
	origin := "https://" + host
	if err := o.Daemon.SetOrigin(ctx, origin); err != nil {
		return fmt.Errorf("设置 public.origin：%w（Iridium 在运行吗？）", err)
	}
	o.say("public.origin = %s：Passkey 已开，现在所有网页请求都要登录。", origin)
	if err := o.health(ctx, host); err != nil {
		return err
	}
	// 7. The first panel.
	link, expires, err := o.Daemon.Invite(ctx)
	if err != nil {
		return fmt.Errorf("邀请：%w", err)
	}
	o.say("\n第一个网页面板：在浏览器里打开这个链接，起名、建 Passkey（%s 前有效，只能用一次）：\n\n  %s\n", expires.Format("15:04"), link)
	o.say("过期了就在这台机器上跑 ir invite 再拿一个。关掉：ir public off")
	return nil
}

func (o *Options) login(ctx context.Context, cf string) error {
	if _, err := os.Stat(o.cert()); err == nil {
		again, err := o.yes("已经登录过 Cloudflare。重新登录（换域名）？", false)
		if err != nil {
			return err
		}
		if !again {
			return nil
		}
		if err := os.Rename(o.cert(), o.cert()+".old"); err != nil {
			return err
		}
	}
	// A cloudflared that ignored $HOME would write the user's ~/.cloudflared/cert.pem: take it
	// from there only if this login made it.
	var homeCert string
	if o.Home != "" {
		homeCert = filepath.Join(o.Home, ".cloudflared", "cert.pem")
		if _, err := os.Stat(homeCert); err == nil {
			homeCert = ""
		}
	}
	o.say("cloudflared 会给出一个登录链接：在浏览器里打开，登录 Cloudflare，选一个域名（站点）。")
	_, err := o.run(ctx, []string{"HOME=" + o.Dir}, cf, "tunnel", "login")
	if _, serr := os.Stat(o.cert()); serr != nil && homeCert != "" {
		if _, herr := os.Stat(homeCert); herr == nil {
			if rerr := os.Rename(homeCert, o.cert()); rerr == nil {
				serr = nil
			}
		}
	}
	if _, serr := os.Stat(o.cert()); serr != nil {
		if err == nil {
			err = errors.New("cloudflared 没有写出 cert.pem")
		}
		return fmt.Errorf("登录没成功：%w。要一个托管在 Cloudflare 上的域名；没有就先在 Cloudflare 添加站点，再跑 ir public", err)
	}
	return nil
}

// zone is what cert.pem says.
type zone struct {
	ZoneID    string `json:"zoneID"`
	AccountID string `json:"accountID"`
	APIToken  string `json:"apiToken"`
}

// readCert reads the token cloudflared's login wrote (the PEM block ARGO TUNNEL TOKEN).
func readCert(path string) (zone, error) {
	var z zone
	data, err := os.ReadFile(path)
	if err != nil {
		return z, err
	}
	for {
		var b *pem.Block
		b, data = pem.Decode(data)
		if b == nil {
			break
		}
		if strings.Contains(b.Type, "TOKEN") {
			if err := json.Unmarshal(b.Bytes, &z); err != nil {
				return z, fmt.Errorf("%s: %w", path, err)
			}
			if z.ZoneID == "" {
				return z, fmt.Errorf("%s: no zone", path)
			}
			return z, nil
		}
	}
	return z, fmt.Errorf("%s: not a cloudflared login", path)
}

// zoneName asks Cloudflare the name of the login's zone; "" when it will not say.
func (o *Options) zoneName(ctx context.Context, z zone) string {
	if z.APIToken == "" {
		return ""
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.CFAPI+"/zones/"+z.ZoneID, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+z.APIToken)
	resp, err := o.HTTP.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var body struct {
		Success bool `json:"success"`
		Result  struct {
			Name string `json:"name"`
		} `json:"result"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body) != nil || !body.Success {
		return ""
	}
	return strings.ToLower(body.Result.Name)
}

func (o *Options) hostName(domain string) (string, error) {
	for {
		a, err := o.Ask(fmt.Sprintf("子域名（地址是 <子域名>.%s）", domain), "ir")
		if err != nil {
			return "", err
		}
		a = strings.Trim(strings.ToLower(strings.TrimSpace(a)), ".")
		if a == "" {
			a = "ir"
		}
		if a == domain || strings.HasSuffix(a, "."+domain) {
			a = strings.TrimSuffix(strings.TrimSuffix(a, domain), ".")
		}
		ok := a != ""
		for _, l := range strings.Split(a, ".") {
			ok = ok && label.MatchString(l)
		}
		if ok {
			host := a + "." + domain
			o.say("地址：https://%s", host)
			return host, nil
		}
		o.say("%q 不是子域名：只用字母、数字和 -。", a)
	}
}

// tunnel is the tunnel's UUID: the one made before (its credentials here), else a new one, else
// the account's of that name with its credentials fetched again.
func (o *Options) tunnel(ctx context.Context, cf, name string) (string, error) {
	var st State
	if readJSON(o.stateFile(), &st) == nil && st.Tunnel != "" && st.Name == name {
		if _, err := os.Stat(o.creds()); err == nil {
			o.say("隧道 %s 已经有了（%s）。", name, st.Tunnel)
			return st.Tunnel, nil
		}
	}
	base := []string{"tunnel", "--origincert", o.cert()}
	out, err := o.quiet(ctx, cf, append(base, "create", "--credentials-file", o.creds(), "-o", "json", name)...)
	if err == nil {
		var t struct {
			ID string `json:"id"`
		}
		if json.Unmarshal([]byte(jsonPart(out)), &t) == nil && t.ID != "" {
			o.say("建好隧道 %s（%s）。", name, t.ID)
			return t.ID, nil
		}
		return "", fmt.Errorf("cloudflared tunnel create: no id in %q", out)
	}
	if !strings.Contains(strings.ToLower(out), "already exists") {
		return "", fmt.Errorf("cloudflared tunnel create %s: %v: %s", name, err, strings.TrimSpace(out))
	}
	out, err = o.quiet(ctx, cf, append(base, "list", "-o", "json", "--name", name)...)
	var list []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err != nil || json.Unmarshal([]byte(jsonPart(out)), &list) != nil || len(list) == 0 {
		return "", fmt.Errorf("隧道 %s 已经有了，但列不出来：%v %s", name, err, strings.TrimSpace(out))
	}
	id := list[0].ID
	if out, err := o.quiet(ctx, cf, append(base, "token", "--cred-file", o.creds(), id)...); err != nil {
		return "", fmt.Errorf("取隧道 %s 的凭证：%v: %s", name, err, strings.TrimSpace(out))
	}
	o.say("隧道 %s 已经有了（%s），重新取了凭证。", name, id)
	return id, nil
}

// jsonPart is out from its first [ or {: cloudflared may log a line before.
func jsonPart(out string) string {
	if i := strings.IndexAny(out, "[{"); i >= 0 {
		return out[i:]
	}
	return out
}

// route points host at the tunnel, asking before it replaces a record that is there.
func (o *Options) route(ctx context.Context, cf, id, host string) error {
	base := []string{"tunnel", "--origincert", o.cert(), "route", "dns"}
	out, err := o.quiet(ctx, cf, append(base, id, host)...)
	if err == nil {
		o.say("DNS：%s → 隧道。", host)
		return nil
	}
	low := strings.ToLower(out)
	if !strings.Contains(low, "already exists") && !strings.Contains(low, "code: 1003") {
		return fmt.Errorf("cloudflared tunnel route dns: %v: %s", err, strings.TrimSpace(out))
	}
	over, aerr := o.yes(fmt.Sprintf("%s 已经有 DNS 记录。改成指向 Iridium 的隧道？", host), false)
	if aerr != nil {
		return aerr
	}
	if !over {
		return fmt.Errorf("%s 已经有别的记录，没动它。换个子域名再跑 ir public", host)
	}
	if out, err := o.quiet(ctx, cf, append(base, "--overwrite-dns", id, host)...); err != nil {
		return fmt.Errorf("cloudflared tunnel route dns --overwrite-dns: %v: %s", err, strings.TrimSpace(out))
	}
	o.say("DNS：%s → 隧道（覆盖了原来的记录）。", host)
	return nil
}

// Config is the tunnel's config file: host to the daemon, the rest 404.
func Config(id, creds, host string, port int) string {
	return fmt.Sprintf(`# Written by ir public. The Cloudflare Tunnel of Iridium: %[3]s to the daemon, the rest 404.
tunnel: %[1]s
credentials-file: %[2]s
ingress:
  - hostname: %[3]s
    service: http://127.0.0.1:%[4]d
  - service: http_status:404
`, id, creds, host, port)
}

func (o *Options) writeConfig(ctx context.Context, cf, id, host string) error {
	if err := writeFile(o.config(), []byte(Config(id, o.creds(), host, o.Port)), 0o600); err != nil {
		return err
	}
	if out, err := o.quiet(ctx, cf, "tunnel", "--config", o.config(), "ingress", "validate"); err != nil {
		return fmt.Errorf("隧道配置不对：%v: %s", err, strings.TrimSpace(out))
	}
	return nil
}

// Unit is the tunnel's user systemd unit.
func Unit(cf, config, host string) string {
	return fmt.Sprintf(`# Written by ir public: Iridium's own tunnel, not the machine's cloudflared.
[Unit]
Description=Iridium Cloudflare Tunnel (%[3]s)
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=%[1]s tunnel --no-autoupdate --config %[2]s run
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`, systemdQuote(cf), systemdQuote(config), host)
}

// systemdQuote quotes a word of ExecStart= when it needs it.
func systemdQuote(s string) string {
	if !strings.ContainsAny(s, " \t\"'\\%$;") {
		return s
	}
	s = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "$", "$$").Replace(s)
	return `"` + s + `"`
}

func (o *Options) installUnit(ctx context.Context, cf, host string) error {
	if err := os.MkdirAll(o.UnitDir, 0o755); err != nil {
		return err
	}
	if err := writeFile(o.unitFile(), []byte(Unit(cf, o.config(), host)), 0o644); err != nil {
		return err
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", o.Unit + ".service"}, {"restart", o.Unit + ".service"}} {
		if err := o.Systemctl(ctx, args...); err != nil {
			return fmt.Errorf("systemctl --user %s: %w", strings.Join(args, " "), err)
		}
	}
	o.say("隧道服务 %s 在跑（systemctl --user status %s）。", o.Unit, o.Unit)
	return nil
}

// health waits until https://host/_ir/health answers from outside.
func (o *Options) health(ctx context.Context, host string) error {
	url := "https://" + host + "/_ir/health"
	o.say("等 %s 从外网能通……", url)
	deadline := time.Now().Add(o.HealthWait)
	var last error
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := o.HTTP.Do(req)
		if err == nil {
			var body struct {
				OK bool `json:"ok"`
			}
			derr := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK && derr == nil && body.OK {
				o.say("通了。")
				return nil
			}
			err = fmt.Errorf("%s", resp.Status)
		}
		last = err
		if time.Now().After(deadline) {
			return fmt.Errorf("%s 还不通（%v）：DNS 可能要几分钟才生效；看 systemctl --user status %s，过会儿再跑 ir public", url, last, o.Unit)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(o.HealthEvery):
		}
	}
}

// Off takes the public way in down: the unit, the tunnel, public.origin. The DNS record stays:
// cloudflared cannot delete one.
func Off(ctx context.Context, o Options) error {
	o.defaults()
	var st State
	if err := readJSON(o.stateFile(), &st); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			o.say("没有 ir public 开的公网入口。")
			if o.Daemon != nil {
				return o.Daemon.SetOrigin(ctx, "")
			}
			return nil
		}
		return err
	}
	if st.Unit == "" {
		st.Unit = o.Unit
	}
	o.Unit = st.Unit
	_ = o.Systemctl(ctx, "disable", "--now", st.Unit+".service")
	if err := os.Remove(o.unitFile()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_ = o.Systemctl(ctx, "daemon-reload")
	o.say("停了隧道服务 %s。", st.Unit)
	if err := o.Daemon.SetOrigin(ctx, ""); err != nil {
		return fmt.Errorf("清 public.origin：%w", err)
	}
	o.say("public.origin 清掉了：只听本机。")
	if st.Tunnel != "" && st.Cloudflared != "" {
		if out, err := o.quiet(ctx, st.Cloudflared, "tunnel", "--origincert", o.cert(), "delete", "-f", st.Tunnel); err != nil {
			o.say("删隧道 %s 没成功（%v）：%s\n去 Cloudflare 控制台的 Networks → Tunnels 删。", st.Name, err, strings.TrimSpace(out))
		} else {
			o.say("删了隧道 %s。", st.Name)
		}
	}
	for _, p := range []string{o.creds(), o.config(), o.stateFile()} {
		os.Remove(p)
	}
	o.say("DNS 记录 %s 还在（cloudflared 删不了）：去 Cloudflare 控制台的 DNS 页删掉它。", st.Host)
	return nil
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(path, append(b, '\n'), 0o600)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// writeFile writes path whole or not at all.
func writeFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
