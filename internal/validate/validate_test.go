package validate

import (
	"strings"
	"testing"
)

func TestInstanceID(t *testing.T) {
	for _, ok := range []string{"a", "chat-a", "c0", "abcdefghijklmnopqrst"} {
		if err := InstanceID(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "A", "0chat", "-chat", "chat_a", "chat.a", "abcdefghijklmnopqrstu", "a/b", "../x", "chat a", "chat\n"} {
		if InstanceID(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestUsername(t *testing.T) {
	for _, ok := range []string{"alice", "_svc", "bob-2", "machine$"} {
		if err := Username(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "-rf", "Alice", "a b", "a/b", "a:b", strings.Repeat("a", 33)} {
		if Username(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// Anything accepted must be safe as a path element and a systemd instance name.
func FuzzInstanceID(f *testing.F) {
	for _, s := range []string{"chat-a", "", "../..", "a\x00b"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if InstanceID(s) != nil {
			return
		}
		if len(s) > 20 || strings.ContainsAny(s, "/.\\@:_ \x00\n") || s[0] == '-' {
			t.Fatalf("unsafe ID accepted: %q", s)
		}
	})
}
