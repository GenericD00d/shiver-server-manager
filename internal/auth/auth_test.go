package auth

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

type fakeAuth struct {
	password string
	calls    int
}

func (f *fakeAuth) Authenticate(_ string, p Prompter) error {
	f.calls++
	got, err := p.Hidden("Password: ")
	if err != nil {
		return err
	}
	if got != f.password {
		return ErrBadLogin
	}
	return nil
}

// answers replies to hidden prompts in order and records messages.
type answers struct {
	hidden []string
	infos  []string
}

func (a *answers) Hidden(string) (string, error) {
	if len(a.hidden) == 0 {
		return "", errors.New("no more answers")
	}
	s := a.hidden[0]
	a.hidden = a.hidden[1:]
	return s, nil
}
func (a *answers) Visible(m string) (string, error) { return a.Hidden(m) }
func (a *answers) Info(m string)                    { a.infos = append(a.infos, m) }
func (a *answers) Error(m string)                   { a.infos = append(a.infos, m) }

func admins(names ...string) func(string) (bool, error) {
	return func(u string) (bool, error) {
		for _, n := range names {
			if n == u {
				return true, nil
			}
		}
		return false, nil
	}
}

func TestLoginOutcomes(t *testing.T) {
	l := &Login{Auth: &fakeAuth{password: "pw"}, IsAdmin: admins("alice")}
	if err := l.Run("alice", &answers{hidden: []string{"pw"}}); err != nil {
		t.Fatalf("good login: %v", err)
	}
	if err := l.Run("alice", &answers{hidden: []string{"nope"}}); !errors.Is(err, ErrBadLogin) {
		t.Fatalf("wrong password: %v", err)
	}
	if err := l.Run("bob", &answers{hidden: []string{"pw"}}); !errors.Is(err, ErrNotAdmin) {
		t.Fatalf("outside the group: %v", err)
	}
	if err := l.Run("-rf", &answers{hidden: []string{"pw"}}); !errors.Is(err, ErrBadLogin) {
		t.Fatalf("bad username: %v", err)
	}
}

func TestThrottle(t *testing.T) {
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	th := &Throttle{Path: filepath.Join(t.TempDir(), "logins.json"), Now: func() time.Time { return now }}
	fa := &fakeAuth{password: "pw"}
	l := &Login{Auth: fa, Throttle: th, IsAdmin: admins("alice")}

	for i := 0; i < MaxFailures; i++ {
		if err := l.Run("alice", &answers{hidden: []string{"bad"}}); !errors.Is(err, ErrBadLogin) {
			t.Fatalf("failure %d: %v", i, err)
		}
		now = now.Add(time.Minute)
	}
	// Paused now, even with the right password, and PAM isn't asked.
	calls := fa.calls
	var thr *ThrottledError
	if err := l.Run("alice", &answers{hidden: []string{"pw"}}); !errors.As(err, &thr) {
		t.Fatalf("got %v, want ThrottledError", err)
	}
	if fa.calls != calls {
		t.Fatal("PAM asked while paused")
	}
	// Another name isn't affected.
	if err := th.Check("carol"); err != nil {
		t.Fatalf("other user paused: %v", err)
	}
	// Survives a new process (a fresh Throttle on the same file).
	th2 := &Throttle{Path: th.Path, Now: th.Now}
	if th2.Check("alice") == nil {
		t.Fatal("pause lost between runs")
	}
	now = now.Add(PauseFor)
	if err := l.Run("alice", &answers{hidden: []string{"pw"}}); err != nil {
		t.Fatalf("after the pause: %v", err)
	}
	// Success clears the count.
	if err := l.Run("alice", &answers{hidden: []string{"bad"}}); !errors.Is(err, ErrBadLogin) {
		t.Fatal(err)
	}
	if th.Check("alice") != nil {
		t.Fatal("one failure after a success paused the user")
	}
}

func TestThrottleWindow(t *testing.T) {
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	th := &Throttle{Path: filepath.Join(t.TempDir(), "logins.json"), Now: func() time.Time { return now }}
	// Failures spread out over more than the window never pause.
	for i := 0; i < 10; i++ {
		th.Fail("alice")
		if th.Check("alice") != nil {
			t.Fatalf("paused after spread-out failure %d", i)
		}
		now = now.Add(4 * time.Minute)
	}
}

func TestSessionIdle(t *testing.T) {
	now := time.Now()
	s := NewSession("alice", "alice", now)
	if s.Idle(now.Add(IdleTimeout - time.Second)) {
		t.Fatal("idle too early")
	}
	s.Touch(now.Add(5 * time.Minute))
	if s.Idle(now.Add(IdleTimeout)) {
		t.Fatal("Touch ignored")
	}
	if !s.Idle(now.Add(5*time.Minute + IdleTimeout)) {
		t.Fatal("never idle")
	}
}
