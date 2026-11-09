package ops

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GenericD00d/shiver-server-manager/internal/run"
	"github.com/GenericD00d/shiver-server-manager/internal/sharkord"
	"github.com/GenericD00d/shiver-server-manager/internal/state"
	"github.com/GenericD00d/shiver-server-manager/internal/systemd"
	"github.com/GenericD00d/shiver-server-manager/internal/vault"
	_ "modernc.org/sqlite"

	"github.com/GenericD00d/shiver-server-manager/internal/backup"
	"github.com/GenericD00d/shiver-server-manager/internal/caddy"
	"github.com/GenericD00d/shiver-server-manager/internal/firewall"
)

// copyTree is the fake rsync: copies plain files and folders, skipping what
// Sharkord unpacks at every start.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		switch strings.Split(filepath.ToSlash(rel), "/")[0] {
		case "interface", "mediasoup", "drizzle", "tmp":
			return filepath.SkipDir
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o600)
	})
}

// memFirewall keeps rules in memory.
type memFirewall struct {
	mu    sync.Mutex
	rules []firewall.Rule
}

func (m *memFirewall) Name() string                         { return "mem" }
func (m *memFirewall) Active(context.Context) (bool, error) { return true, nil }
func (m *memFirewall) Allow(_ context.Context, r firewall.Rule) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !firewall.Contains(m.rules, r) {
		m.rules = append(m.rules, r)
	}
	return nil
}
func (m *memFirewall) Remove(_ context.Context, r firewall.Rule) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, x := range m.rules {
		if x == r {
			m.rules = append(m.rules[:i], m.rules[i+1:]...)
			break
		}
	}
	return nil
}
func (m *memFirewall) List(context.Context) ([]firewall.Rule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]firewall.Rule{}, m.rules...), nil
}

const testToken = "019a6b2c-3d4e-7f80-9123-456789abcdef"

// fakeSystem keeps the state that fake commands change.
type fakeSystem struct {
	mu      sync.Mutex
	users   map[string]bool
	enabled map[string]bool
	active  map[string]bool
}

func (fs *fakeSystem) handle(c run.Call) (run.FakeResponse, bool) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	a := c.Args
	switch {
	case c.Name == "rsync":
		src, dst := strings.TrimSuffix(a[len(a)-2], "/"), strings.TrimSuffix(a[len(a)-1], "/")
		if err := copyTree(src, dst); err != nil {
			return run.FakeResponse{ExitCode: 1, Stderr: err.Error()}, true
		}
	case c.Name == "getent" && a[0] == "passwd" && len(a) == 1:
		var out string
		for u := range fs.users {
			out += u + ":x:999:999::/x:/usr/sbin/nologin\n"
		}
		return run.FakeResponse{Stdout: out + "root:x:0:0::/root:/bin/bash\n"}, true
	case c.Name == "getent" && a[0] == "passwd":
		if fs.users[a[1]] {
			return run.FakeResponse{Stdout: a[1] + ":x:999:999::/x:/usr/sbin/nologin\n"}, true
		}
		return run.FakeResponse{ExitCode: 2}, true
	case c.Name == "useradd":
		fs.users[a[len(a)-1]] = true
	case c.Name == "userdel":
		delete(fs.users, a[0])
	case c.Name == "systemctl" && a[0] == "enable":
		fs.enabled[a[2]], fs.active[a[2]] = true, true
	case c.Name == "systemctl" && a[0] == "disable":
		fs.enabled[a[2]], fs.active[a[2]] = false, false
	case c.Name == "systemctl" && (a[0] == "start" || a[0] == "restart"):
		fs.active[a[1]] = true
	case c.Name == "systemctl" && a[0] == "stop":
		fs.active[a[1]] = false
	case c.Name == "systemctl" && a[0] == "show":
		act, en := "inactive", "disabled"
		if fs.active[a[1]] {
			act = "active"
		}
		if fs.enabled[a[1]] {
			en = "enabled"
		}
		return run.FakeResponse{Stdout: fmt.Sprintf("ActiveState=%s\nUnitFileState=%s\n", act, en)}, true
	}
	return run.FakeResponse{}, true
}

type harness struct {
	s          *Sys
	fake       *run.Fake
	sys        *fakeSystem
	root       string
	firstStart int
	healthy    bool
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	if runtime.GOOS == "windows" {
		link := filepath.Join(t.TempDir(), "l")
		if err := os.Symlink("x", link); err != nil {
			t.Skip("symlinks need Developer Mode on Windows")
		}
	}
	root := t.TempDir()
	// Like a real storage location, instance users must pass through.
	os.Chmod(root, 0o755)
	os.Chmod(filepath.Dir(root), 0o755)
	bin := []byte("fake sharkord")
	sum := sha256.Sum256(bin)
	asset, _ := sharkord.AssetName(runtime.GOARCH)
	mux := http.NewServeMux()
	mux.HandleFunc("/dl/v0.0.30/release.json", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"artifacts":[{"name":%q,"size":%d,"checksum":%q}]}`, asset, len(bin), hex.EncodeToString(sum[:]))
	})
	mux.HandleFunc("/dl/v0.0.30/"+asset, func(w http.ResponseWriter, _ *http.Request) { w.Write(bin) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	h := &harness{root: root, sys: &fakeSystem{users: map[string]bool{}, enabled: map[string]bool{}, active: map[string]bool{}}, healthy: true}
	h.fake = &run.Fake{Handler: h.sys.handle}
	st := state.Default()
	loc := filepath.Join(root, "data")
	os.MkdirAll(loc, 0o711)
	st.Settings.Locations = []state.Location{{Path: loc, Default: true}}

	p := Paths{
		StateFile:      filepath.Join(root, "state.json"),
		EnvDir:         filepath.Join(root, "etc", "instances"),
		VersionLinkDir: filepath.Join(root, "bin"),
		OptDir:         filepath.Join(root, "opt"),
		SystemdDir:     filepath.Join(root, "systemd"),
		VaultDir:       filepath.Join(root, "vault"),
	}
	v := &vault.Vault{Dir: p.VaultDir, WorkFactor: 10}
	if _, err := v.Create("test vault passphrase"); err != nil {
		t.Fatal(err)
	}
	fw := &memFirewall{rules: append([]firewall.Rule{}, firewall.CaddyRules...)}
	cad := &caddy.Caddy{R: h.fake, Caddyfile: filepath.Join(root, "caddy", "Caddyfile"),
		GlobalFile: filepath.Join(root, "caddy", "global.caddy"), SitesDir: filepath.Join(root, "caddy", "sites"),
		DataDir: filepath.Join(root, "caddydata")}
	client := sharkord.NewClient("test", "")
	client.DownloadBase = srv.URL + "/dl"
	h.s = &Sys{
		Paths: p, R: h.fake, St: st, Vault: v, Systemd: systemd.Systemd{R: h.fake}, Sharkord: client,
		Caddy: cad, FirewallFor: func(string) firewall.Firewall { return fw },
		FirstStart: func(_ context.Context, o sharkord.FirstStartOpts) (string, error) {
			h.firstStart++
			db, err := sql.Open("sqlite", "file:"+filepath.Join(o.DataPath, "db.sqlite"))
			if err != nil {
				return "", err
			}
			defer db.Close()
			if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS settings (secret_token TEXT)`); err != nil {
				return "", err
			}
			_, err = db.Exec(`INSERT INTO settings VALUES (?)`, sharkord.HashToken(testToken))
			return testToken, err
		},
		WaitHealthy: func(context.Context, int, time.Duration) error {
			if !h.healthy {
				return errors.New("not healthy")
			}
			return nil
		},
		PublicIPv4: func(context.Context) (string, error) { return "203.0.113.10", nil },
		PortFree:   func(int, bool) bool { return true },
		UserIDs:    func(string) (int, int, error) { return 0, 0, nil },
		Chown:      func(string, int, int) error { return nil },
		OwnerOf: func(p string) (int, int, error) {
			_, err := os.Lstat(p)
			return 0, 0, err
		},
		Now:      func() time.Time { return time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC) },
		VacuumAs: func(_ context.Context, _, _ int, src, dst string) error { return backup.Vacuum(src, dst) },
	}
	h.s.St.Settings.BackupLocation = filepath.Join(root, "backups")
	return h
}

func req(id string) CreateRequest {
	return CreateRequest{ID: id, Domain: id + ".example.com"}
}

// snapshot lists every file under root with its content, for comparing runs.
func snapshot(t *testing.T, root string) string {
	var lines []string
	filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		switch {
		case err != nil || strings.HasPrefix(rel, "vault"):
		case fi.Mode()&os.ModeSymlink != 0:
			target, _ := os.Readlink(p)
			lines = append(lines, rel+" -> "+filepath.ToSlash(strings.TrimPrefix(target, root)))
		case fi.Mode().IsRegular():
			b, _ := os.ReadFile(p)
			txt := strings.ReplaceAll(string(b), strings.ReplaceAll(root, `\`, `\\`), "<root>") // JSON-escaped
			lines = append(lines, rel+": "+strings.ReplaceAll(txt, root, "<root>"))
		}
		return nil
	})
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func TestCreate(t *testing.T) {
	h := newHarness(t)
	var events []string
	h.s.Progress = func(e Event) { events = append(events, e.Step+":"+e.Status) }
	if err := h.s.Create(context.Background(), req("chat-a")); err != nil {
		t.Fatal(err)
	}
	inst := h.s.St.Instances["chat-a"]
	if inst.Status != state.StatusActive || inst.Operation != nil || inst.HTTPPort != 5000 || inst.WebRTCPort != 40000 {
		t.Fatalf("record %+v", inst)
	}
	k, _ := h.s.Vault.Unlock("test vault passphrase")
	if tok, err := h.s.Token(k, "chat-a"); err != nil || tok != testToken {
		t.Fatalf("token %q %v", tok, err)
	}
	env, _ := os.ReadFile(h.s.envPath("chat-a"))
	if !strings.Contains(string(env), "SHARKORD_PORT=5000\n") || !strings.Contains(string(env), "SHARKORD_WEBRTC_ANNOUNCED_ADDRESS=203.0.113.10\n") {
		t.Fatalf("env file:\n%s", env)
	}
	if !h.sys.users["sharkord-chat-a"] || !h.sys.enabled["sharkord@chat-a.service"] {
		t.Fatal("user or service missing")
	}
	// A second server gets the next ports; a duplicate ID or domain is refused.
	if err := h.s.Create(context.Background(), req("chat-b")); err != nil {
		t.Fatal(err)
	}
	if b := h.s.St.Instances["chat-b"]; b.HTTPPort != 5001 || b.WebRTCPort != 40001 {
		t.Fatalf("ports %d %d", b.HTTPPort, b.WebRTCPort)
	}
	if err := h.s.Create(context.Background(), req("chat-a")); err == nil {
		t.Fatal("duplicate ID accepted")
	}
	if err := h.s.Create(context.Background(), CreateRequest{ID: "chat-c", Domain: "CHAT-A.example.com"}); err == nil {
		t.Fatal("duplicate domain accepted")
	}
	if err := h.s.Create(context.Background(), CreateRequest{ID: "chat-c", Domain: "c.example.com", Version: "0.0.1"}); err == nil {
		t.Fatal("untested version accepted")
	}
	// Running every step's Done again finds nothing to do.
	saved, _ := state.Load(h.s.Paths.StateFile)
	if saved.Instances["chat-a"].Status != state.StatusActive {
		t.Fatal("records not saved")
	}
}

// Stop a create after each step in turn, resume it, and check the result
// matches an uninterrupted run.
func TestCreateResumesAfterEveryStep(t *testing.T) {
	ref := newHarness(t)
	if err := ref.s.Create(context.Background(), req("chat-a")); err != nil {
		t.Fatal(err)
	}
	want := snapshot(t, ref.root)
	steps := len(ref.s.createSteps("chat-a"))

	for stopAfter := 1; stopAfter <= steps; stopAfter++ {
		t.Run(fmt.Sprint(stopAfter), func(t *testing.T) {
			h := newHarness(t)
			ctx, cancel := context.WithCancel(context.Background())
			done := 0
			h.s.Progress = func(e Event) {
				if e.Status == "done" && e.Step != "record" {
					if done++; done == stopAfter {
						cancel()
					}
				}
			}
			err := h.s.Create(ctx, req("chat-a"))
			if stopAfter < steps {
				if !IsPartial(err) {
					t.Fatalf("got %v, want a PartialError", err)
				}
				// A fresh run, as after a crash: reload the records.
				st, lerr := state.Load(h.s.Paths.StateFile)
				if lerr != nil {
					t.Fatal(lerr)
				}
				if st.Instances["chat-a"].Operation == nil {
					t.Fatal("operation not saved")
				}
				h.s.St, h.s.Progress = st, nil
				if err := h.s.Resume(context.Background(), "chat-a"); err != nil {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if got := snapshot(t, h.root); got != want {
				t.Fatalf("differs from an uninterrupted run:\n%s\n---\n%s", got, want)
			}
			if h.firstStart != 1 {
				t.Fatalf("first start ran %d times", h.firstStart)
			}
		})
	}
}

func TestFailedCreateKeepsOperation(t *testing.T) {
	h := newHarness(t)
	h.healthy = false
	err := h.s.Create(context.Background(), req("chat-a"))
	var p *PartialError
	if !errors.As(err, &p) || p.Step != "first-start" && p.Step != "service" {
		t.Fatalf("got %v", err)
	}
	op := h.s.St.Instances["chat-a"].Operation
	if op == nil || op.FailedStep == "" || op.Error == "" {
		t.Fatalf("operation %+v", op)
	}
	h.healthy = true
	if err := h.s.Resume(context.Background(), "chat-a"); err != nil {
		t.Fatal(err)
	}
	if h.s.St.Instances["chat-a"].Status != state.StatusActive {
		t.Fatal("not active after resume")
	}
}

func TestSetSettings(t *testing.T) {
	h := newHarness(t)
	if err := h.s.Create(context.Background(), req("chat-a")); err != nil {
		t.Fatal(err)
	}
	if err := h.s.Set(context.Background(), "chat-a", []string{"OIDC_CLIENT_ID=12345", "SHARKORD_OIDC_CLIENT_SECRET=hush", "debug=yes"}); err != nil {
		t.Fatal(err)
	}
	inst := h.s.St.Instances["chat-a"]
	if inst.Settings["SHARKORD_OIDC_CLIENT_ID"] != "12345" || inst.Settings["SHARKORD_DEBUG"] != "true" {
		t.Fatalf("settings %v", inst.Settings)
	}
	if _, ok := inst.Settings["SHARKORD_OIDC_CLIENT_SECRET"]; ok {
		t.Fatal("secret setting stored in the records")
	}
	env, _ := os.ReadFile(h.s.envPath("chat-a"))
	for _, want := range []string{`SHARKORD_OIDC_CLIENT_ID='"12345"'`, `SHARKORD_OIDC_CLIENT_SECRET='"hush"'`, "SHARKORD_DEBUG=true"} {
		if !strings.Contains(string(env), want+"\n") {
			t.Fatalf("missing %s in:\n%s", want, env)
		}
	}
	// The secret survives a rewrite of the files.
	os.Remove(filepath.Join(h.s.dropinDir("chat-a"), "data.conf"))
	if err := h.s.writeFiles(context.Background(), "chat-a"); err != nil {
		t.Fatal(err)
	}
	env, _ = os.ReadFile(h.s.envPath("chat-a"))
	if !strings.Contains(string(env), "hush") {
		t.Fatal("secret lost on rewrite")
	}
	// Back to default, and a failed restart restores the old settings.
	h.healthy = false
	if err := h.s.Set(context.Background(), "chat-a", []string{"SHARKORD_DEBUG="}); err == nil {
		t.Fatal("failed restart not reported")
	}
	if h.s.St.Instances["chat-a"].Settings["SHARKORD_DEBUG"] != "true" {
		t.Fatal("old settings not restored")
	}
	if err := h.s.Set(context.Background(), "chat-a", []string{"SHARKORD_PORT=1"}); err == nil {
		t.Fatal("managed setting accepted")
	}
}

func TestSaveToken(t *testing.T) {
	h := newHarness(t)
	if err := h.s.Create(context.Background(), req("chat-a")); err != nil {
		t.Fatal(err)
	}
	if err := h.s.SaveToken("chat-a", "019a6b2c-3d4e-7f80-9123-000000000000"); !errors.Is(err, sharkord.ErrTokenMismatch) {
		t.Fatalf("wrong token: %v", err)
	}
	if err := h.s.SaveToken("chat-a", strings.ToUpper(testToken)); err != nil {
		t.Fatal(err)
	}
}
