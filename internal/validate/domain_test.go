package validate

import (
	"strings"
	"testing"
)

func TestDomain(t *testing.T) {
	for in, want := range map[string]string{
		"chat.example.com":   "chat.example.com",
		"Chat.Example.COM.":  "chat.example.com",
		" a-b.example.co.uk": "a-b.example.co.uk",
		"bücher.example":     "xn--bcher-kva.example",
	} {
		got, err := Domain(in)
		if err != nil || got != want {
			t.Errorf("%q: got %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "localhost", "-a.example.com", "a-.example.com", "a..example.com",
		"a_b.example.com", "203.0.113.10", "a.123", strings.Repeat("a", 64) + ".com",
		strings.Repeat("abcdefghi.", 26) + "com", "chat.example.com/x", "chat example.com", "chat.example.com:443"} {
		if got, err := Domain(bad); err == nil {
			t.Errorf("%q accepted as %q", bad, got)
		}
	}
}

func FuzzDomain(f *testing.F) {
	for _, s := range []string{"chat.example.com", "bücher.example", "a..b", "x\n.com"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		d, err := Domain(s)
		if err != nil {
			return
		}
		if len(d) > 253 || strings.ContainsAny(d, " \t\n{}/\\:;'\"#*") || d != strings.ToLower(d) {
			t.Fatalf("unsafe domain accepted: %q -> %q", s, d)
		}
	})
}
