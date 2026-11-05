package cli

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// publicHTTP is the client of ir public. It looks host names up at 1.1.1.1 first, then as the
// machine does: a local resolver may still hold "no such name" for the new record from before.
func publicHTTP() *http.Client {
	cf := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, network, "1.1.1.1:53")
	}}
	d := &net.Dialer{Timeout: 10 * time.Second}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err == nil && net.ParseIP(host) == nil {
			rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			ips, lerr := cf.LookupHost(rctx, host)
			cancel()
			if lerr == nil && len(ips) > 0 {
				if c, err := d.DialContext(ctx, network, net.JoinHostPort(ips[0], port)); err == nil {
					return c, nil
				}
			}
		}
		return d.DialContext(ctx, network, addr)
	}
	return &http.Client{Timeout: 5 * time.Minute, Transport: tr}
}

func runSystemctl(ctx context.Context, args ...string) error {
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "systemctl", append([]string{"--user"}, args...)...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}
