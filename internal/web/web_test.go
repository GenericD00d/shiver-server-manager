package web

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/GenericD00d/shiver-server-manager/internal/auth"
	"github.com/GenericD00d/shiver-server-manager/internal/paths"
)

type fakePAM struct{ needCode bool }

func (f fakePAM) Authenticate(user string, p auth.Prompter) error {
	pw, err := p.Hidden("Password: ")
	if err != nil {
		return err
	}
	if pw != "right" {
		return auth.ErrBadLogin
	}
	if f.needCode {
		code, err := p.Hidden("Verification code: ")
		if err != nil {
			return err
		}
		if code != "123456" {
			return auth.ErrBadLogin
		}
	}
	return nil
}

type harness struct {
	uploads string
	srv     *httptest.Server
	client  *http.Client
	mu      sync.Mutex
	ran     [][]string
	stdins  [][]string
}

// commands returns what ran, under the lock (jobs run in goroutines).
func (h *harness) commands() ([][]string, [][]string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([][]string{}, h.ran...), append([][]string{}, h.stdins...)
}

func newHarness(t *testing.T, needCode bool) *harness {
	h := &harness{}
	run := func(_ context.Context, args, stdin []string, line func(string)) (int, error) {
		h.mu.Lock()
		h.ran = append(h.ran, args)
		h.stdins = append(h.stdins, stdin)
		h.mu.Unlock()
		line("  … step")
		line("  ✓ step, with secret " + strings.Join(stdin, ","))
		return 0, nil
	}
	read := func(_ context.Context, args, stdin []string) (string, string, int, error) {
		h.mu.Lock()
		h.ran = append(h.ran, args)
		h.mu.Unlock()
		if args[0] == "token" {
			if stdin[0] != "vault pass" {
				return "", "Error: wrong vault passphrase", 3, nil
			}
			return "019a6b2c-3d4e-7f80-9123-456789abcdef\n", "", 0, nil
		}
		return `[{"id":"chat-a"}]`, "", 0, nil
	}
	s := &Server{
		Version: "test", Sessions: NewSessions(), Jobs: NewJobs(run), Read: read,
		Login: func(user string, p auth.Prompter) error {
			return (&auth.Login{Auth: fakePAM{needCode}, IsAdmin: func(u string) (bool, error) { return u == "alice", nil }}).Run(user, p)
		},
		LogFile: t.TempDir() + "/log", Static: fstest.MapFS{"index.html": {Data: []byte("<html>app</html>")}},
		Hosts: []string{"admin.example.com"}, UploadsDir: filepath.Join(t.TempDir(), "uploads"),
	}
	h.uploads = s.UploadsDir
	h.srv = httptest.NewServer(s.Handler())
	t.Cleanup(h.srv.Close)
	jar, _ := cookiejar.New(nil)
	h.client = &http.Client{Jar: jar}
	return h
}

func (h *harness) do(t *testing.T, method, path string, body any, hdr map[string]string) (*http.Response, map[string]any) {
	t.Helper()
	var rd *strings.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
	} else {
		rd = strings.NewReader("")
	}
	req, _ := http.NewRequest(method, h.srv.URL+path, rd)
	if method != http.MethodGet {
		req.Header.Set("X-Requested-By", paths.Cmd)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

func (h *harness) login(t *testing.T) {
	t.Helper()
	if resp, out := h.do(t, "POST", "/api/login", map[string]string{"username": "alice", "password": "right"}, nil); resp.StatusCode != 200 {
		t.Fatalf("login: %d %v", resp.StatusCode, out)
	}
}

func TestLogin(t *testing.T) {
	h := newHarness(t, false)
	if resp, _ := h.do(t, "GET", "/api/me", nil, nil); resp.StatusCode != 401 {
		t.Fatalf("me without login: %d", resp.StatusCode)
	}
	resp, wrong := h.do(t, "POST", "/api/login", map[string]string{"username": "alice", "password": "wrong"}, nil)
	if resp.StatusCode != 401 {
		t.Fatal("wrong password accepted")
	}
	// An account outside the admins group, with its right password, gets the same answer as a
	// wrong password: the page must not confirm passwords for the machine's other accounts.
	if resp, out := h.do(t, "POST", "/api/login", map[string]string{"username": "bob", "password": "right"}, nil); resp.StatusCode != 401 || out["error"] != wrong["error"] || strings.Contains(out["error"].(string), "group") {
		t.Fatalf("outsider: %d %v, wrong password: %v", resp.StatusCode, out, wrong)
	}
	h.login(t)
	resp, out := h.do(t, "GET", "/api/me", nil, nil)
	if resp.StatusCode != 200 || out["user"] != "alice" {
		t.Fatalf("me: %v", out)
	}
	cookie := resp.Request.Header.Get("Cookie")
	if !strings.Contains(cookie, cookieName) {
		t.Fatal("no session cookie sent")
	}
	h.do(t, "POST", "/api/logout", nil, nil)
	if resp, _ := h.do(t, "GET", "/api/me", nil, nil); resp.StatusCode != 401 {
		t.Fatal("still logged in after logout")
	}
}

func TestTwoFactor(t *testing.T) {
	h := newHarness(t, true)
	resp, out := h.do(t, "POST", "/api/login", map[string]string{"username": "alice", "password": "right"}, nil)
	if resp.StatusCode != 401 || out["needCode"] != true || out["prompt"] != "Verification code:" {
		t.Fatalf("first step: %d %v", resp.StatusCode, out)
	}
	if resp, _ := h.do(t, "POST", "/api/login", map[string]string{"username": "alice", "password": "right", "code": "000000"}, nil); resp.StatusCode != 401 {
		t.Fatal("wrong code accepted")
	}
	if resp, _ := h.do(t, "POST", "/api/login", map[string]string{"username": "alice", "password": "right", "code": "123456"}, nil); resp.StatusCode != 200 {
		t.Fatal("right code refused")
	}
}

func TestRequestGuards(t *testing.T) {
	h := newHarness(t, false)
	h.login(t)
	// No app header: refused (a form post from another site can't add it).
	req, _ := http.NewRequest("POST", h.srv.URL+"/api/actions/system.health", strings.NewReader(`{"params":{}}`))
	resp, _ := h.client.Do(req)
	if resp.StatusCode != 403 {
		t.Fatalf("no header: %d", resp.StatusCode)
	}
	// Another origin: refused.
	if resp, _ := h.do(t, "POST", "/api/actions/system.health", map[string]any{"params": map[string]string{}}, map[string]string{"Origin": "https://evil.example"}); resp.StatusCode != 403 {
		t.Fatalf("cross-origin: %d", resp.StatusCode)
	}
	// DNS rebinding: an unknown Host is refused.
	req, _ = http.NewRequest("GET", h.srv.URL+"/api/me", nil)
	req.Host = "evil.example"
	if resp, _ := h.client.Do(req); resp.StatusCode != http.StatusMisdirectedRequest {
		t.Fatalf("unknown host: %d", resp.StatusCode)
	}
	req.Host = "admin.example.com"
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != 401 { // known host, no session
		t.Fatalf("admin host: %d", resp.StatusCode)
	}
	resp, _ = h.do(t, "GET", "/", nil, nil)
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatal("no CSP")
	}
}

func TestActionsBuildFixedCommands(t *testing.T) {
	h := newHarness(t, false)
	h.login(t)
	// Bad input never reaches a command.
	for _, params := range []map[string]string{
		{"id": "--help", "domain": "a.example.com", "tls": "letsencrypt"},
		{"id": "chat-a", "domain": "not a domain", "tls": "letsencrypt"},
		{"id": "chat-a", "domain": "a.example.com", "tls": "plaintext"},
		{"id": "chat-a", "domain": "a.example.com", "tls": "letsencrypt", "location": "relative/path"},
	} {
		if resp, out := h.do(t, "POST", "/api/actions/server.create", map[string]any{"params": params}, nil); resp.StatusCode != 400 {
			t.Fatalf("%v accepted: %v", params, out)
		}
	}
	if resp, _ := h.do(t, "POST", "/api/actions/no.such", map[string]any{"params": map[string]string{}}, nil); resp.StatusCode != 400 {
		t.Fatal("unknown action accepted")
	}
	resp, out := h.do(t, "POST", "/api/actions/server.create", map[string]any{"params": map[string]string{
		"id": "chat-a", "domain": "Chat-A.Example.com", "tls": "selfsigned", "encrypt": "true"}}, nil)
	if resp.StatusCode != 202 {
		t.Fatalf("create: %d %v", resp.StatusCode, out)
	}
	lines := h.stream(t, out["id"].(string))
	if !strings.Contains(lines, "step") || !strings.Contains(lines, "done:0") {
		t.Fatalf("stream: %q", lines)
	}
	ran, _ := h.commands()
	got := strings.Join(ran[0], " ")
	want := "instance create --id chat-a --domain chat-a.example.com --tls selfsigned --yes --passphrase-stdin --acting-user alice"
	if got != want {
		t.Fatalf("ran %q\nwant %q", got, want)
	}
}

// stream reads a job's server-sent events until it's done.
func (h *harness) stream(t *testing.T, id string) string {
	t.Helper()
	resp, err := h.client.Get(h.srv.URL + "/api/jobs/" + id + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out []string
	sc := bufio.NewScanner(resp.Body)
	event := ""
	deadline := time.Now().Add(5 * time.Second)
	for sc.Scan() && time.Now().Before(deadline) {
		l := sc.Text()
		switch {
		case strings.HasPrefix(l, "event: "):
			event = strings.TrimPrefix(l, "event: ")
		case strings.HasPrefix(l, "data: "):
			out = append(out, event+":"+strings.TrimPrefix(l, "data: "))
			if event == "done" {
				return strings.Join(out, "\n")
			}
		}
	}
	return strings.Join(out, "\n")
}

func TestSensitiveActionsNeedThePasswordAgain(t *testing.T) {
	h := newHarness(t, false)
	h.login(t)
	params := map[string]string{"id": "chat-a", "confirmId": "chat-a"}
	if resp, _ := h.do(t, "POST", "/api/actions/server.delete", map[string]any{"params": params}, nil); resp.StatusCode != 403 {
		t.Fatal("delete without the password")
	}
	if resp, _ := h.do(t, "POST", "/api/actions/server.delete", map[string]any{"params": params, "password": "wrong"}, nil); resp.StatusCode != 403 {
		t.Fatal("delete with a wrong password")
	}
	if resp, _ := h.do(t, "POST", "/api/actions/server.delete", map[string]any{"params": map[string]string{"id": "chat-a", "confirmId": "chat-b"}, "password": "right"}, nil); resp.StatusCode != 400 {
		t.Fatal("delete without the ID typed back")
	}
	resp, out := h.do(t, "POST", "/api/actions/server.delete", map[string]any{"params": params, "password": "right"}, nil)
	if resp.StatusCode != 202 {
		t.Fatalf("delete: %v", out)
	}
	h.stream(t, out["id"].(string))
	if _, stdins := h.commands(); stdins[0][0] != "chat-a" {
		t.Fatal("the ID wasn't typed back to the command")
	}
}

func TestSecretsStayOutOfJobOutput(t *testing.T) {
	h := newHarness(t, false)
	h.login(t)
	resp, out := h.do(t, "POST", "/api/actions/vault.unlock", map[string]any{"params": map[string]string{"passphrase": "very secret passphrase"}}, nil)
	if resp.StatusCode != 202 {
		t.Fatal(out)
	}
	lines := h.stream(t, out["id"].(string))
	if strings.Contains(lines, "very secret") || !strings.Contains(lines, "[hidden]") {
		t.Fatalf("secret in output: %q", lines)
	}
	if ran, _ := h.commands(); strings.Contains(strings.Join(ran[0], " "), "secret") {
		t.Fatal("secret in the command line")
	}
}

func TestToken(t *testing.T) {
	h := newHarness(t, false)
	h.login(t)
	if resp, _ := h.do(t, "POST", "/api/token", map[string]string{"id": "chat-a", "passphrase": "vault pass"}, nil); resp.StatusCode != 403 {
		t.Fatal("token without the password")
	}
	if resp, out := h.do(t, "POST", "/api/token", map[string]string{"id": "chat-a", "password": "right", "passphrase": "nope"}, nil); resp.StatusCode != 400 || strings.Contains(out["error"].(string), "nope") {
		t.Fatalf("wrong passphrase: %v", out)
	}
	resp, out := h.do(t, "POST", "/api/token", map[string]string{"id": "chat-a", "password": "right", "passphrase": "vault pass"}, nil)
	if resp.StatusCode != 200 || out["token"] != "019a6b2c-3d4e-7f80-9123-456789abcdef" {
		t.Fatalf("token: %v", out)
	}
	if resp, _ := h.do(t, "GET", "/api/jobs", nil, nil); resp.StatusCode != 200 {
		t.Fatal("jobs")
	}
}

func TestIdleSessionsExpire(t *testing.T) {
	s := NewSessions()
	now := time.Now()
	s.Now = func() time.Time { return now }
	tok, _ := s.New("alice")
	now = now.Add(auth.IdleTimeout - time.Second)
	if s.Get(tok) == nil {
		t.Fatal("expired early")
	}
	now = now.Add(auth.IdleTimeout)
	if s.Get(tok) != nil {
		t.Fatal("never expired")
	}
}

func TestDataReads(t *testing.T) {
	h := newHarness(t, false)
	h.login(t)
	resp, _ := h.do(t, "GET", "/api/data/servers", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("servers: %d", resp.StatusCode)
	}
	if ran, _ := h.commands(); strings.Join(ran[0], " ") != "instance list --json --yes --passphrase-stdin --acting-user alice" {
		t.Fatalf("ran %q", ran[0])
	}
	if resp, _ := h.do(t, "GET", "/api/data/"+url.PathEscape("../etc"), nil, nil); resp.StatusCode != 404 {
		t.Fatal("unknown read allowed")
	}
}

func (h *harness) upload(t *testing.T, name, body string, guarded bool) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("POST", h.srv.URL+"/api/upload?name="+url.QueryEscape(name), strings.NewReader(body))
	if guarded {
		req.Header.Set("X-Requested-By", paths.Cmd)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestUpload(t *testing.T) {
	h := newHarness(t, false)
	if code, _ := h.upload(t, "p.zip", "x", true); code != http.StatusUnauthorized {
		t.Fatalf("logged out: %d", code)
	}
	h.login(t)
	if code, _ := h.upload(t, "p.zip", "x", false); code != http.StatusForbidden {
		t.Fatalf("no header: %d", code)
	}
	if code, _ := h.upload(t, "p.exe", "x", true); code != http.StatusBadRequest {
		t.Fatalf("an .exe: %d", code)
	}
	code, out := h.upload(t, "Music-Bot.TAR.GZ", "archive bytes", true)
	if code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	p, _ := out["path"].(string)
	if filepath.Dir(p) != h.uploads || !strings.HasSuffix(p, ".tar.gz") {
		t.Fatalf("path %q", p)
	}
	if b, _ := os.ReadFile(p); string(b) != "archive bytes" {
		t.Fatalf("%q", b)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v", fi.Mode())
		}
	}
	if code, _ := h.upload(t, "big.zip", strings.Repeat("x", MaxUpload+1), true); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("too big: %d", code)
	}
	if entries, _ := os.ReadDir(h.uploads); len(entries) != 1 {
		t.Fatalf("left %v", entries)
	}
}
