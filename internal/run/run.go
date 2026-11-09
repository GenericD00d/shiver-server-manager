// Package run is the single way the manager runs external programs: an
// argument list (never a shell string), a timeout, a fixed environment, a log
// line per command, and secrets redacted from logs and errors.
package run

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DefaultTimeout applies when Cmd.Timeout is zero.
const DefaultTimeout = 2 * time.Minute

// BaseEnv is the whole environment every command gets, plus Cmd.Env.
var BaseEnv = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C", "LANG=C.UTF-8"}

// SearchDirs are the only folders programs are looked up in.
var SearchDirs = []string{"/usr/sbin", "/usr/bin", "/sbin", "/bin"}

type Cmd struct {
	Name    string   // program name, or an absolute path
	Args    []string // never put a secret here; use Stdin
	Stdin   io.Reader
	Env     []string // added to BaseEnv
	Dir     string
	Timeout time.Duration
	// Secrets are redacted from this command's log line and error, on top of
	// the runner's own list.
	Secrets [][]byte
}

type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// ExitError is a command that ran and exited non-zero.
type ExitError struct {
	Argv     string
	ExitCode int
	Stderr   string
}

func (e *ExitError) Error() string {
	msg := fmt.Sprintf("%s: exit code %d", e.Argv, e.ExitCode)
	if e.Stderr != "" {
		msg += ": " + e.Stderr
	}
	return msg
}

type Runner interface {
	Run(ctx context.Context, c Cmd) (Result, error)
}

// Exec runs real programs.
type Exec struct {
	// Log receives one line per command. Nil means no logging.
	Log func(line string)

	mu       sync.Mutex
	resolved map[string]string
	secrets  [][]byte
}

// AddSecret registers a value to redact from every later log line and error.
func (e *Exec) AddSecret(s []byte) {
	if len(s) == 0 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.secrets = append(e.secrets, bytes.Clone(s))
}

func (e *Exec) resolve(name string) (string, error) {
	if filepath.IsAbs(name) {
		return name, nil
	}
	if strings.ContainsRune(name, '/') {
		return "", fmt.Errorf("refusing relative program path %q", name)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if p, ok := e.resolved[name]; ok {
		return p, nil
	}
	for _, d := range SearchDirs {
		p := filepath.Join(d, name)
		if _, err := exec.LookPath(p); err == nil {
			if e.resolved == nil {
				e.resolved = map[string]string{}
			}
			e.resolved[name] = p
			return p, nil
		}
	}
	return "", fmt.Errorf("%s is not installed (looked in %s)", name, strings.Join(SearchDirs, ", "))
}

func (e *Exec) Run(ctx context.Context, c Cmd) (Result, error) {
	path, err := e.resolve(c.Name)
	if err != nil {
		return Result{ExitCode: -1}, err
	}
	timeout := c.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, path, c.Args...)
	cmd.Env = append(append([]string{}, BaseEnv...), c.Env...)
	cmd.Dir = c.Dir
	cmd.Stdin = c.Stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	runErr := cmd.Run()

	res := Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	argv := e.redact(Argv(c.Name, c.Args), c.Secrets)
	var exitErr *exec.ExitError
	switch {
	case runErr == nil:
	case errors.As(runErr, &exitErr) && ctx.Err() == nil:
		res.ExitCode = exitErr.ExitCode()
	default:
		res.ExitCode = -1
	}
	if e.Log != nil {
		e.Log(fmt.Sprintf("run %s exit=%d time=%s", argv, res.ExitCode, time.Since(start).Round(time.Millisecond)))
	}
	switch {
	case runErr == nil:
		return res, nil
	case ctx.Err() == context.DeadlineExceeded:
		return res, fmt.Errorf("%s: timed out after %s", argv, timeout)
	case res.ExitCode > 0:
		return res, &ExitError{Argv: argv, ExitCode: res.ExitCode, Stderr: e.redact(strings.TrimSpace(string(res.Stderr)), c.Secrets)}
	default:
		return res, fmt.Errorf("%s: %s", argv, e.redact(runErr.Error(), c.Secrets))
	}
}

func (e *Exec) redact(s string, extra [][]byte) string {
	e.mu.Lock()
	all := append(append([][]byte{}, e.secrets...), extra...)
	e.mu.Unlock()
	return Redact(s, all)
}

// Redact replaces every occurrence of each secret in s.
func Redact(s string, secrets [][]byte) string {
	for _, sec := range secrets {
		if len(sec) > 0 {
			s = strings.ReplaceAll(s, string(sec), "[redacted]")
		}
	}
	return s
}

// Argv renders a command for logs, quoting arguments with spaces.
func Argv(name string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	for _, a := range append([]string{name}, args...) {
		if a == "" || strings.ContainsAny(a, " \t\n'\"") {
			a = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
		parts = append(parts, a)
	}
	return strings.Join(parts, " ")
}
