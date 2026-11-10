package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/GenericD00d/shiver-server-manager/internal/actionlog"
	"github.com/GenericD00d/shiver-server-manager/internal/auth"
	"github.com/GenericD00d/shiver-server-manager/internal/help"
	"github.com/GenericD00d/shiver-server-manager/internal/paths"
	"github.com/GenericD00d/shiver-server-manager/internal/sharkord"
)

const cookieName = "ssm_session"

// Reader runs a read-only command and returns its stdout and stderr apart.
type Reader func(ctx context.Context, args, stdin []string) (stdout, stderr string, code int, err error)

// ExecReader runs bin for reads.
func ExecReader(bin string) Reader {
	return func(ctx context.Context, args, stdin []string) (string, string, int, error) {
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8", "NO_COLOR=1"}
		cmd.Stdin = strings.NewReader(strings.Join(stdin, "\n") + "\n")
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		err := cmd.Run()
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return out.String(), errb.String(), exitErr.ExitCode(), nil
		}
		return out.String(), errb.String(), 0, err
	}
}

// Server is the web page's back end.
type Server struct {
	Version  string
	Sessions *Sessions
	Jobs     *Jobs
	Read     Reader
	// Login checks an admin's password (and code) and group.
	Login func(user string, p auth.Prompter) error
	// LogFile is the manager's action log.
	LogFile string
	// Hosts are the Host headers accepted (localhost names plus the admin
	// domain), against DNS rebinding.
	Hosts []string
	// Static is the built front end.
	Static fs.FS
	// HealthFile and RootCert are read directly.
	HealthFile string
	RootCert   string
	// UploadsDir holds files uploaded for an action (empty: no uploads).
	UploadsDir string
}

// Handler routes every request.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.authed(s.logout))
	mux.HandleFunc("GET /api/me", s.authed(s.me))
	mux.HandleFunc("GET /api/data/{name}", s.authed(s.data))
	mux.HandleFunc("GET /api/meta", s.authed(s.meta))
	mux.HandleFunc("GET /api/root.crt", s.authed(s.rootCert))
	mux.HandleFunc("POST /api/actions/{name}", s.authed(s.action))
	mux.HandleFunc("POST /api/token", s.authed(s.token))
	mux.HandleFunc("POST /api/upload", s.authed(s.upload))
	mux.HandleFunc("GET /api/jobs", s.authed(s.jobs))
	mux.HandleFunc("GET /api/jobs/{id}/events", s.authed(s.events))
	if s.Static != nil {
		files := http.FileServerFS(s.Static)
		mux.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// The app's routes all serve index.html.
			if _, err := fs.Stat(s.Static, strings.TrimPrefix(r.URL.Path, "/")); err != nil || r.URL.Path == "/" {
				r = r.Clone(r.Context())
				r.URL.Path = "/"
			}
			files.ServeHTTP(w, r)
		}))
	}
	return s.guard(mux)
}

// guard sets security headers, refuses unknown Host headers, and for every
// request that changes something, demands same-origin and the app's header.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		if !s.hostOK(host) {
			http.Error(w, "unknown host", http.StatusMisdirectedRequest)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if r.Header.Get("X-Requested-By") != paths.Cmd {
				http.Error(w, "missing request header", http.StatusForbidden)
				return
			}
			if o := r.Header.Get("Origin"); o != "" && o != scheme(r)+"://"+r.Host {
				http.Error(w, "cross-origin request refused", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) hostOK(host string) bool {
	host = strings.ToLower(host)
	switch host {
	case "localhost", "127.0.0.1", "::1", "[::1]":
		return true
	}
	for _, h := range s.Hosts {
		if strings.EqualFold(h, host) {
			return true
		}
	}
	return false
}

// scheme is https when Caddy forwarded the request.
func scheme(r *http.Request) string {
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		return "https"
	}
	return "http"
}

type ctxKey struct{}

func (s *Server) authed(next func(http.ResponseWriter, *http.Request, *Session)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		var sess *Session
		if err == nil {
			// Watching a job's output isn't activity.
			if strings.HasSuffix(r.URL.Path, "/events") {
				sess = s.Sessions.Peek(c.Value)
			} else {
				sess = s.Sessions.Get(c.Value)
			}
		}
		if sess == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "log in again"})
			return
		}
		next(w, r, sess)
	}
}

// logAs writes one action line for a user.
func (s *Server) logAs(user, action string, err error, fields ...string) {
	l := actionlog.New(s.LogFile)
	l.User = user
	l.Action(action, err, fields...)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func readBody(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var body struct{ Username, Password, Code string }
	if err := readBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	p := &webPrompter{password: body.Password, code: body.Code}
	err := s.Login(body.Username, p)
	switch {
	case errors.Is(err, ErrCodeNeeded) || (err != nil && p.asked != "" && body.Code == ""):
		writeJSON(w, http.StatusUnauthorized, map[string]any{"needCode": true, "prompt": p.asked})
		return
	case err != nil:
		s.logAs(body.Username, "web login", err)
		var thr *auth.ThrottledError
		msg := "wrong username or password"
		if errors.As(err, &thr) || errors.Is(err, auth.ErrNotAdmin) {
			msg = err.Error()
		}
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": msg})
		return
	}
	s.logAs(body.Username, "web login", nil)
	tok, err := s.Sessions.New(body.Username)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: tok, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, Secure: scheme(r) == "https"})
	writeJSON(w, http.StatusOK, map[string]string{"user": body.Username})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request, sess *Session) {
	if c, err := r.Cookie(cookieName); err == nil {
		s.Sessions.Delete(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	writeJSON(w, http.StatusOK, map[string]string{})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request, sess *Session) {
	writeJSON(w, http.StatusOK, map[string]any{"user": sess.User, "version": s.Version, "app": paths.AppName})
}

// reads are the read-only commands the page can ask for.
var reads = map[string][]string{
	"servers":         {"instance", "list"},
	"certs":           {"cert", "status"},
	"root":            {"cert", "root"},
	"backups":         {"backup", "list"},
	"storage":         {"storage", "list"},
	"drives":          {"storage", "mount"},
	"shiver":          {"shiver", "status"},
	"firewall":        {"firewall", "status"},
	"admins":          {"admins", "list"},
	"repair":          {"repair", "--dry-run"},
	"alerts":          {"health", "alerts"},
	"update":          {"self-update", "--check"},
	"backupSchedules": {"backup", "schedule"},
	"plugins":         {"plugin", "list"},
}

func (s *Server) globals(user string) []string {
	return []string{"--json", "--yes", "--passphrase-stdin", "--acting-user", user}
}

func (s *Server) data(w http.ResponseWriter, r *http.Request, sess *Session) {
	name := r.PathValue("name")
	if name == "health" {
		b, err := os.ReadFile(s.HealthFile)
		if err != nil {
			b = []byte(`{"notices":[]}`)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(b)
		return
	}
	args, ok := reads[name]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	out, errOut, code, err := s.Read(ctx, append(append([]string{}, args...), s.globals(sess.User)...), nil)
	if err != nil || code != 0 {
		msg := strings.TrimSpace(errOut)
		var e struct{ Error string }
		if json.Unmarshal([]byte(msg), &e) == nil && e.Error != "" {
			msg = e.Error
		}
		if err != nil {
			msg = err.Error()
		}
		status := http.StatusBadGateway
		if code == 4 {
			status = http.StatusConflict // another operation holds the lock
		}
		writeJSON(w, status, map[string]any{"error": msg, "exitCode": code})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if strings.TrimSpace(out) == "" {
		out = "null"
	}
	w.Write([]byte(out))
}

// meta is what the page needs to build its forms.
func (s *Server) meta(w http.ResponseWriter, r *http.Request, sess *Session) {
	type setting struct{ Name, Help, Kind string }
	var settings []setting
	for _, st := range sharkord.Settings {
		kind := map[sharkord.Kind]string{sharkord.KindInt: "number", sharkord.KindBool: "bool", sharkord.KindString: "text", sharkord.KindList: "list"}[st.Kind]
		settings = append(settings, setting{strings.TrimPrefix(st.Name, "SHARKORD_"), st.Help, kind})
	}
	actions := map[string]map[string]any{}
	for name, a := range Actions {
		actions[name] = map[string]any{"title": a.Title, "reauth": a.Reauth}
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": settings, "actions": actions, "help": help.All(), "version": s.Version})
}

func (s *Server) rootCert(w http.ResponseWriter, r *http.Request, sess *Session) {
	b, err := os.ReadFile(s.RootCert)
	if err != nil {
		http.Error(w, "no self-signed root yet", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/x-x509-ca-cert")
	w.Header().Set("Content-Disposition", `attachment; filename="sharkord-root.crt"`)
	w.Write(b)
}

// reauth checks the admin's password again, for sensitive actions.
func (s *Server) reauth(sess *Session, password, code string) error {
	if password == "" {
		return errors.New("this needs your password again")
	}
	p := &webPrompter{password: password, code: code}
	if err := s.Login(sess.User, p); err != nil {
		if errors.Is(err, ErrCodeNeeded) {
			return errors.New("this needs your password and code again")
		}
		return errors.New("wrong password")
	}
	return nil
}

type actionBody struct {
	Params   Params `json:"params"`
	Password string `json:"password"`
	Code     string `json:"code"`
}

func (s *Server) action(w http.ResponseWriter, r *http.Request, sess *Session) {
	name := r.PathValue("name")
	var body actionBody
	if err := readBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	a, args, stdin, err := Build(name, body.Params)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if a.Reauth {
		if err := s.reauth(sess, body.Password, body.Code); err != nil {
			s.logAs(sess.User, "web reauth", err, "for", name)
			writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
			return
		}
	}
	args = append(args, s.globals(sess.User)[1:]...) // not --json: show progress
	j := s.Jobs.Start(name, a.Title, sess.User, args, stdin)
	writeJSON(w, http.StatusAccepted, j.Info())
}

// token shows a secret token: password again, then the vault passphrase.
// It's returned once and kept nowhere.
func (s *Server) token(w http.ResponseWriter, r *http.Request, sess *Session) {
	var body struct {
		ID, Password, Code, Passphrase string
	}
	if err := readBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	c := &checker{p: Params{"id": body.ID}}
	id := c.id("id")
	if c.err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": c.err.Error()})
		return
	}
	if err := s.reauth(sess, body.Password, body.Code); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	out, errOut, code, err := s.Read(ctx, []string{"token", "show", id, "--passphrase-stdin", "--acting-user", sess.User}, []string{body.Passphrase})
	if err != nil || code != 0 {
		msg := strings.TrimPrefix(strings.TrimSpace(errOut), "Error: ")
		if err != nil {
			msg = err.Error()
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": redact(msg, []string{body.Passphrase, body.Password})})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": strings.TrimSpace(out)})
}

func (s *Server) jobs(w http.ResponseWriter, r *http.Request, sess *Session) {
	writeJSON(w, http.StatusOK, s.Jobs.Recent())
}

// events streams a job's output as server-sent events.
func (s *Server) events(w http.ResponseWriter, r *http.Request, sess *Session) {
	j := s.Jobs.Get(r.PathValue("id"))
	if j == nil {
		http.Error(w, "no such job", http.StatusNotFound)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	n := 0
	for {
		lines, done, code, changed := j.Since(n)
		for _, l := range lines {
			b, _ := json.Marshal(l)
			fmt.Fprintf(w, "event: line\ndata: %s\n\n", b)
		}
		n += len(lines)
		if done {
			fmt.Fprintf(w, "event: done\ndata: %d\n\n", code)
			flusher.Flush()
			return
		}
		flusher.Flush()
		select {
		case <-r.Context().Done():
			return
		case <-changed:
		case <-time.After(20 * time.Second):
			fmt.Fprint(w, ": keep-alive\n\n")
		}
	}
}
