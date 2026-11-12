//go:build linux

package cli

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/GenericD00d/shiver-server-manager/internal/caddy"
)

// httpsClient trusts roots (PEM) and sends every request to 127.0.0.1,
// whatever the domain, like curl --resolve.
func httpsClient(t *testing.T, roots []byte) *http.Client {
	t.Helper()
	pool := x509.NewCertPool()
	if roots != nil && !pool.AppendCertsFromPEM(roots) {
		t.Fatal("no certificates in the root PEM")
	}
	d := &net.Dialer{Timeout: 5 * time.Second}
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool},
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				_, port, _ := net.SplitHostPort(addr)
				return d.DialContext(ctx, network, "127.0.0.1:"+port)
			},
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// pebbleRoots fetches the root Pebble signs certificates with (from its
// management port, which uses Pebble's own test certificate).
func pebbleRoots(t *testing.T) []byte {
	t.Helper()
	u := os.Getenv("SSM_PEBBLE_ROOTS")
	if u == "" {
		t.Skip("set SSM_PEBBLE_ROOTS to Pebble's roots URL")
	}
	c := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} // test CA's own port
	resp, err := c.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return b
}

// TestE2EHTTPS: milestone 4. The server from milestone 3 has a certificate
// from the test CA, answers over HTTPS through Caddy, shows the maintenance
// page while stopped, and its voice port is open in ufw.
func TestE2EHTTPS(t *testing.T) {
	e := newE2E(t)
	const id = "chat-a"
	client := httpsClient(t, pebbleRoots(t))

	t.Run("cert status", func(t *testing.T) {
		out := e.stdout(t, "", "cert", "status", id)
		if !strings.Contains(out, "valid") {
			t.Fatalf("cert status: %s", out)
		}
	})

	t.Run("https", func(t *testing.T) {
		resp, err := client.Get("https://chat-a.example.com/healthz")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || !strings.Contains(string(body), `"status":"ok"`) {
			t.Fatalf("%s %s", resp.Status, body)
		}
	})

	t.Run("maintenance page while stopped", func(t *testing.T) {
		e.asAdmin(t, 0, "", "instance", "stop", id)
		defer e.asAdmin(t, 0, "", "instance", "start", id)
		resp, err := client.Get("https://chat-a.example.com/")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 503 || !strings.Contains(string(body), "offline for maintenance") {
			t.Fatalf("%s %s", resp.Status, body)
		}
	})

	t.Run("firewall", func(t *testing.T) {
		name, out := firewallText(t)
		wants := []string{"40000/udp", "40000/tcp", "# sharkord:chat-a", "443/udp", "# sharkord:caddy", "# sharkord:ssh"}
		if name == "firewalld" {
			wants = []string{"40000/udp", "40000/tcp", "sharkord-chat-a", "443/udp", "https", "http"}
		}
		for _, want := range wants {
			if !strings.Contains(out, want) {
				t.Fatalf("missing %q in:\n%s", want, out)
			}
		}
		if strings.Contains(out, "5000/") {
			t.Fatal("the instance's HTTP port is open")
		}
		fw := e.stdout(t, "", "firewall", "status")
		if !strings.Contains(fw, "chat-a") {
			t.Fatalf("firewall status: %s", fw)
		}
	})

	t.Run("caddy admin interface is a socket", func(t *testing.T) {
		if _, err := net.DialTimeout("tcp", "127.0.0.1:2019", time.Second); err == nil {
			t.Fatal("Caddy's admin interface still listens on 127.0.0.1:2019")
		}
		fi, err := os.Stat(caddy.SocketPath(caddy.Confined()))
		if err != nil || fi.Mode().Perm()&0o007 != 0 {
			t.Fatalf("admin socket: %v %v", fi, err)
		}
	})

	t.Run("caddy refuses a broken change and keeps running", func(t *testing.T) {
		// Retry reloads Caddy; it must still succeed with the valid config.
		e.asAdmin(t, 0, "", "cert", "retry", id)
	})
}
