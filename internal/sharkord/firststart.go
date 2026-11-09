package sharkord

import (
	"bufio"
	"bytes"
	"errors"
	"strings"
	"time"
)

// FirstStartOpts describes Sharkord's first run, outside systemd, so the
// owner token it prints once never reaches the journal.
type FirstStartOpts struct {
	Binary   string
	DataPath string
	Env      []string // from the env file
	UID, GID uint32
	HTTPPort int
	Timeout  time.Duration // default 90s
}

// ErrNoToken means Sharkord started but printed no token, because the
// database already existed (a retry after a crash). The token was shown once
// and is lost; the admin can save it later if they have it.
var ErrNoToken = errors.New("Sharkord started without printing a secret token; it had already set up its database, so the token was shown once before and is lost. If you have it, use \"Save owner token\"")

// ParseEnvFile reads KEY=VALUE lines the way systemd does for the manager's
// own files: comments skipped, one pair of surrounding single quotes removed.
func ParseEnvFile(b []byte) []string {
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
			v = v[1 : len(v)-1]
		}
		out = append(out, k+"="+v)
	}
	return out
}

// lastLines keeps the end of the output for error messages.
func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
