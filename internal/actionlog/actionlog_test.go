package actionlog

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestActionLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	l := New(path)
	l.User, l.SSHUser = "alice", "bob"
	l.now = func() time.Time { return time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC) }

	l.Action("instance create", nil, "instance", "chat-a")
	l.Action("instance delete", errors.New(`step "user": exit 1`), "instance", "chat-b")
	l.SSHUser = "alice"
	l.Line("run systemctl reload caddy exit=0")

	got, _ := os.ReadFile(path)
	want := `2026-11-09T12:00:00Z user=alice ssh_user=bob action="instance create" result=ok instance=chat-a
2026-11-09T12:00:00Z user=alice ssh_user=bob action="instance delete" result=error instance=chat-b error="step \"user\": exit 1"
2026-11-09T12:00:00Z user=alice msg="run systemctl reload caddy exit=0"
`
	if string(got) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestNoNewlineInjection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	l := New(path)
	l.Action("x", nil, "domain", "a\n2026 user=root action=fake")
	got, _ := os.ReadFile(path)
	if strings.Count(string(got), "\n") != 1 {
		t.Fatalf("a value broke the line: %q", got)
	}
}
