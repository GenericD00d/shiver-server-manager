package luks

import (
	"context"
	"strings"
	"testing"

	"github.com/GenericD00d/shiver-server-manager/internal/run"
)

func TestKeyGoesOnStdinOnly(t *testing.T) {
	f := &run.Fake{}
	l := LUKS{R: f}
	key, err := NewKey()
	if err != nil || len(key) != KeySize {
		t.Fatal(err)
	}
	ctx := context.Background()
	l.Format(ctx, "/srv/x/chat-a.luks", key)
	l.Open(ctx, "/srv/x/chat-a.luks", "chat-a", key)
	l.Resize(ctx, "chat-a", key)
	if err := f.CheckNoSecretInArgs(key); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.Calls {
		if string(c.Stdin) != string(key) {
			t.Fatalf("%s: key not on stdin", c)
		}
		if !strings.Contains(c.String(), "--key-file=- --keyfile-size=64") {
			t.Fatalf("%s: missing --key-file=- --keyfile-size=64", c)
		}
	}
	want := "cryptsetup luksFormat --type luks2 --batch-mode --key-file=- --keyfile-size=64 /srv/x/chat-a.luks"
	if f.Lines()[0] != want {
		t.Fatalf("got %q", f.Lines()[0])
	}
}

func TestFsckExitCodes(t *testing.T) {
	for code, ok := range map[int]bool{0: true, 1: true, 2: true, 4: false, 8: false} {
		f := (&run.Fake{}).On("e2fsck", run.FakeResponse{ExitCode: code})
		err := LUKS{R: f}.Fsck(context.Background(), "chat-a")
		if (err == nil) != ok {
			t.Errorf("exit %d: %v", code, err)
		}
	}
}

func TestLabelFits(t *testing.T) {
	f := &run.Fake{}
	LUKS{R: f}.Mkfs(context.Background(), "a-very-long-id-here")
	if !strings.Contains(f.Lines()[0], "-L sharkord-a-very- ") {
		t.Fatalf("got %q", f.Lines()[0])
	}
}
