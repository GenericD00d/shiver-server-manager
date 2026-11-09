package web

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Runner runs one manager command and returns its output, line by line.
// The real one runs this same binary as a child process.
type Runner func(ctx context.Context, args, stdin []string, line func(string)) (exitCode int, err error)

// ExecRunner runs bin with args, answering its prompts from stdin.
func ExecRunner(bin string) Runner {
	return func(ctx context.Context, args, stdin []string, line func(string)) (int, error) {
		cmd := exec.CommandContext(ctx, bin, args...)
		// No SUDO_USER and no terminal: the child skips its own login
		// (the web login already happened) and reads answers from stdin.
		cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8", "NO_COLOR=1"}
		cmd.Stdin = strings.NewReader(strings.Join(stdin, "\n") + "\n")
		pr, pw := io.Pipe()
		cmd.Stdout, cmd.Stderr = pw, pw
		if err := cmd.Start(); err != nil {
			return -1, err
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			sc := bufio.NewScanner(pr)
			sc.Buffer(make([]byte, 64*1024), 1<<20)
			for sc.Scan() {
				line(sc.Text())
			}
			io.Copy(io.Discard, pr)
		}()
		err := cmd.Wait()
		pw.Close()
		<-done
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode(), nil
		}
		if err != nil {
			return -1, err
		}
		return 0, nil
	}
}

// Job is one action running (or finished).
type Job struct {
	ID       string    `json:"id"`
	Action   string    `json:"action"`
	Title    string    `json:"title"`
	User     string    `json:"user"`
	Started  time.Time `json:"started"`
	Done     bool      `json:"done"`
	ExitCode int       `json:"exitCode"`

	mu      sync.Mutex
	lines   []string
	changed chan struct{} // closed and replaced on every change
}

func (j *Job) add(line string) {
	j.mu.Lock()
	j.lines = append(j.lines, line)
	close(j.changed)
	j.changed = make(chan struct{})
	j.mu.Unlock()
}

func (j *Job) finish(code int) {
	j.mu.Lock()
	j.Done, j.ExitCode = true, code
	close(j.changed)
	j.changed = make(chan struct{})
	j.mu.Unlock()
}

// Since returns the lines from n on, whether the job is done, and a channel
// that's closed at the next change.
func (j *Job) Since(n int) ([]string, bool, int, <-chan struct{}) {
	j.mu.Lock()
	defer j.mu.Unlock()
	var out []string
	if n < len(j.lines) {
		out = append(out, j.lines[n:]...)
	}
	return out, j.Done, j.ExitCode, j.changed
}

// Jobs keeps the recent jobs in memory.
type Jobs struct {
	mu   sync.Mutex
	list []*Job
	run  Runner
}

func NewJobs(run Runner) *Jobs { return &Jobs{run: run} }

// Start runs a command in the background. Secrets given on stdin are
// blanked out of its output.
func (js *Jobs) Start(action, title, user string, args, stdin []string) *Job {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	j := &Job{ID: hex.EncodeToString(b), Action: action, Title: title, User: user, Started: time.Now(), changed: make(chan struct{})}
	js.mu.Lock()
	js.list = append(js.list, j)
	if len(js.list) > 50 {
		js.list = js.list[len(js.list)-50:]
	}
	js.mu.Unlock()
	go func() {
		code, err := js.run(context.Background(), args, stdin, func(l string) { j.add(redact(l, stdin)) })
		if err != nil {
			j.add("Error: " + redact(err.Error(), stdin))
		}
		j.finish(code)
	}()
	return j
}

func (js *Jobs) Get(id string) *Job {
	js.mu.Lock()
	defer js.mu.Unlock()
	for _, j := range js.list {
		if j.ID == id {
			return j
		}
	}
	return nil
}

// Recent lists jobs, newest first.
func (js *Jobs) Recent() []*Job {
	js.mu.Lock()
	defer js.mu.Unlock()
	out := make([]*Job, 0, len(js.list))
	for i := len(js.list) - 1; i >= 0; i-- {
		out = append(out, js.list[i])
	}
	return out
}

func redact(s string, secrets []string) string {
	for _, sec := range secrets {
		if len(sec) >= 4 {
			s = strings.ReplaceAll(s, sec, "[hidden]")
		}
	}
	return s
}

// RunSync runs a command and returns its stdout (for reads with --json and
// the secret token, which mustn't stay in a job's history).
func RunSync(ctx context.Context, run Runner, args, stdin []string) (string, int, error) {
	var buf bytes.Buffer
	code, err := run(ctx, args, stdin, func(l string) { buf.WriteString(l + "\n") })
	return buf.String(), code, err
}
