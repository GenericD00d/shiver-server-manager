package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/GenericD00d/shiver-server-manager/internal/fsutil"
)

const (
	MaxFailures   = 5
	FailureWindow = 15 * time.Minute
	PauseFor      = 5 * time.Minute
)

// Throttle refuses a username for PauseFor after MaxFailures failed logins
// within FailureWindow. Counts live in a root-only JSON file so they survive
// between runs.
type Throttle struct {
	Path string
	Now  func() time.Time

	mu sync.Mutex
}

type failures map[string][]time.Time

func (t *Throttle) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

func (t *Throttle) load() failures {
	f := failures{}
	if b, err := os.ReadFile(t.Path); err == nil {
		_ = json.Unmarshal(b, &f) // a damaged file just resets the counts
	}
	return f
}

func (t *Throttle) save(f failures) {
	b, _ := json.Marshal(f)
	if err := fsutil.WriteFileAtomic(t.Path, b, 0o600, nil); err != nil {
		fmt.Fprintf(os.Stderr, "warning: can't save failed-login counts: %v\n", err)
	}
}

// recent drops failures older than the window.
func (t *Throttle) recent(f failures, user string) []time.Time {
	cutoff := t.now().Add(-FailureWindow)
	var keep []time.Time
	for _, ts := range f[user] {
		if ts.After(cutoff) {
			keep = append(keep, ts)
		}
	}
	return keep
}

// Check returns a *ThrottledError while user is paused.
func (t *Throttle) Check(user string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.recent(t.load(), user)
	if len(r) < MaxFailures {
		return nil
	}
	until := r[len(r)-1].Add(PauseFor)
	if t.now().Before(until) {
		return &ThrottledError{Until: until}
	}
	return nil
}

func (t *Throttle) Fail(user string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	f := t.load()
	f[user] = append(t.recent(f, user), t.now())
	t.save(f)
}

func (t *Throttle) Succeed(user string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	f := t.load()
	if _, ok := f[user]; !ok {
		return
	}
	delete(f, user)
	t.save(f)
}
