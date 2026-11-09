//go:build linux

package cli

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/GenericD00d/shiver-server-manager/internal/sharkord"
)

// TestE2EHealthAndNetwork: milestone 10.
func TestE2EHealthAndNetwork(t *testing.T) {
	e := newE2E(t)
	ctx := context.Background()

	// A webhook receiver.
	var mu sync.Mutex
	var got []string
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body map[string]string
		json.Unmarshal(b, &body)
		mu.Lock()
		got = append(got, body["text"])
		mu.Unlock()
	})}
	go srv.Serve(l)
	defer srv.Close()
	received := func() string {
		mu.Lock()
		defer mu.Unlock()
		return strings.Join(got, "\n---\n")
	}

	t.Run("alerts go to a webhook when a problem appears and clears", func(t *testing.T) {
		e.asAdmin(t, 0, "", "health", "alerts", "--webhook", "http://"+l.Addr().String()+"/hook")
		e.expect(t, 0, "", "", "health", "--unattended") // baseline
		sh(t, "systemctl stop sharkord@chat-a")         // a crash the admin didn't ask for
		out := e.expect(t, 0, "", "", "health", "--unattended")
		if !strings.Contains(out, "chat-a isn't running") {
			t.Fatalf("health:\n%s", out)
		}
		if !strings.Contains(received(), "chat-a isn't running") {
			t.Fatalf("no alert sent; received:\n%s", received())
		}
		sh(t, "systemctl start sharkord@chat-a")
		if err := sharkord.WaitHealthy(ctx, e.port(t, "chat-a"), 60e9); err != nil {
			t.Fatal(err)
		}
		e.expect(t, 0, "", "", "health", "--unattended")
		if !strings.Contains(received(), "fixed: chat-a isn't running") {
			t.Fatalf("no all-clear sent; received:\n%s", received())
		}
		var rep map[string]any
		b, _ := os.ReadFile("/var/lib/shiver-server-manager/health.json")
		if json.Unmarshal(b, &rep) != nil || rep["checkedAt"] == nil {
			t.Fatalf("health.json: %s", b)
		}
		e.asAdmin(t, 0, "", "health", "alerts", "--off")
	})

	t.Run("a new public address is announced after a restart", func(t *testing.T) {
		orig := os.Getenv("SSM_PUBLIC_IP")
		if orig == "" {
			t.Skip("needs SSM_PUBLIC_IP")
		}
		e.asAdmin(t, 0, "", "setup", "--yes", "--public-ip", "10.99.99.99")
		env, _ := os.ReadFile("/etc/shiver-server-manager/instances/chat-a.env")
		if !strings.Contains(string(env), "SHARKORD_WEBRTC_ANNOUNCED_ADDRESS=10.99.99.99") {
			t.Fatalf("env not rewritten:\n%s", env)
		}
		pid := sh(t, "systemctl show -p MainPID --value sharkord@chat-a")
		env2 := sh(t, "tr '\\0' '\\n' < /proc/"+pid+"/environ | grep ANNOUNCED")
		if !strings.Contains(env2, "10.99.99.99") {
			t.Fatalf("the running Sharkord wasn't restarted with it: %s", env2)
		}
		e.asAdmin(t, 0, "", "setup", "--yes", "--public-ip", orig)
		for _, id := range []string{"chat-a", "chat-b"} {
			if err := sharkord.Healthz(ctx, e.port(t, id)); err != nil {
				t.Fatalf("%s: %v", id, err)
			}
		}
	})
}
