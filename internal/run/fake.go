package run

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
)

// Call is one command the Fake received.
type Call struct {
	Name  string
	Args  []string
	Stdin []byte
	Env   []string
}

func (c Call) String() string { return Argv(c.Name, c.Args) }

// Fake records commands and answers from canned results, for tests.
type Fake struct {
	mu    sync.Mutex
	Calls []Call
	// Responses maps a rendered command line (see Argv) or a program name to
	// its result. Unmatched commands succeed with empty output.
	Responses map[string]FakeResponse
}

type FakeResponse struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Err      error
}

func (f *Fake) On(cmdline string, r FakeResponse) *Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Responses == nil {
		f.Responses = map[string]FakeResponse{}
	}
	f.Responses[cmdline] = r
	return f
}

func (f *Fake) Run(_ context.Context, c Cmd) (Result, error) {
	var stdin []byte
	if c.Stdin != nil {
		stdin, _ = io.ReadAll(c.Stdin)
	}
	call := Call{Name: c.Name, Args: append([]string{}, c.Args...), Stdin: stdin, Env: c.Env}
	f.mu.Lock()
	f.Calls = append(f.Calls, call)
	r, ok := f.Responses[call.String()]
	if !ok {
		r = f.Responses[c.Name]
	}
	f.mu.Unlock()

	res := Result{Stdout: []byte(r.Stdout), Stderr: []byte(r.Stderr), ExitCode: r.ExitCode}
	if r.Err != nil {
		return res, r.Err
	}
	if r.ExitCode != 0 {
		return res, &ExitError{Argv: call.String(), ExitCode: r.ExitCode, Stderr: strings.TrimSpace(r.Stderr)}
	}
	return res, nil
}

// Lines returns every recorded command as a rendered line.
func (f *Fake) Lines() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.Calls))
	for i, c := range f.Calls {
		out[i] = c.String()
	}
	return out
}

// CheckNoSecretInArgs fails if any recorded argument list contains secret.
func (f *Fake) CheckNoSecretInArgs(secret []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.Calls {
		for _, a := range append([]string{c.Name}, c.Args...) {
			if bytes.Contains([]byte(a), secret) {
				return fmt.Errorf("secret found in arguments of %q", c.String())
			}
		}
	}
	return nil
}
