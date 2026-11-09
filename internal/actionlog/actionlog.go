// Package actionlog appends one line per action and per external command to
// the manager's log, with the time and the logged-in OS user.
package actionlog

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Logger struct {
	Path string
	// User is the logged-in OS user; SSHUser is who ran sudo, when different.
	User    string
	SSHUser string

	mu  sync.Mutex
	now func() time.Time
}

func New(path string) *Logger { return &Logger{Path: path} }

// Action logs a finished action. A nil err logs result=ok.
func (l *Logger) Action(action string, err error, fields ...string) {
	result := "ok"
	if err != nil {
		result = "error"
		fields = append(fields, "error", err.Error())
	}
	l.write(append([]string{"action", action, "result", result}, fields...))
}

// Line logs free text, such as the runner's command lines.
func (l *Logger) Line(msg string) { l.write([]string{"msg", msg}) }

// write appends key/value pairs. Failures to log never stop an action; they
// go to stderr instead.
func (l *Logger) write(kv []string) {
	if l == nil || l.Path == "" {
		return
	}
	now := time.Now
	if l.now != nil {
		now = l.now
	}
	var b strings.Builder
	b.WriteString(now().UTC().Format(time.RFC3339))
	pairs := []string{"user", l.User}
	if l.SSHUser != "" && l.SSHUser != l.User {
		pairs = append(pairs, "ssh_user", l.SSHUser)
	}
	pairs = append(pairs, kv...)
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i+1] == "" {
			continue
		}
		fmt.Fprintf(&b, " %s=%s", pairs[i], quote(pairs[i+1]))
	}
	b.WriteByte('\n')

	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.OpenFile(l.Path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: can't write to the log: %v\n", err)
		return
	}
	defer f.Close()
	if _, err := f.WriteString(b.String()); err != nil {
		fmt.Fprintf(os.Stderr, "warning: can't write to the log: %v\n", err)
	}
}

func quote(v string) string {
	if v != "" && !strings.ContainsAny(v, " \t\n\"=\\") {
		return v
	}
	return strconv.Quote(v)
}
