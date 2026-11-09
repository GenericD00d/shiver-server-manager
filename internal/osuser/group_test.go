package osuser

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/GenericD00d/shiver-server-manager/internal/run"
)

var ctx = context.Background()

func group(f *run.Fake) *Group {
	g := Admins(f)
	g.UserExists = func(n string) bool { return n != "ghost" }
	return g
}

func TestEnsureCreatesMissingGroup(t *testing.T) {
	f := (&run.Fake{}).On("getent group shiver-admins", run.FakeResponse{ExitCode: 2})
	if err := group(f).Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	want := []string{"getent group shiver-admins", "groupadd --system shiver-admins"}
	if !reflect.DeepEqual(f.Lines(), want) {
		t.Fatalf("got %q", f.Lines())
	}
}

func TestEnsureLeavesExistingGroup(t *testing.T) {
	f := (&run.Fake{}).On("getent group shiver-admins", run.FakeResponse{Stdout: "shiver-admins:x:998:\n"})
	if err := group(f).Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.Lines()) != 1 {
		t.Fatalf("got %q", f.Lines())
	}
}

func TestMembers(t *testing.T) {
	f := (&run.Fake{}).On("getent group shiver-admins", run.FakeResponse{Stdout: "shiver-admins:x:998:alice,bob\n"})
	m, err := group(f).Members(ctx)
	if err != nil || !reflect.DeepEqual(m, []string{"alice", "bob"}) {
		t.Fatalf("%q %v", m, err)
	}
	f = (&run.Fake{}).On("getent group shiver-admins", run.FakeResponse{Stdout: "garbage\n"})
	if _, err := group(f).Members(ctx); err == nil {
		t.Fatal("garbage accepted")
	}
}

func TestAdd(t *testing.T) {
	f := (&run.Fake{}).On("getent group shiver-admins", run.FakeResponse{Stdout: "shiver-admins:x:998:alice\n"})
	g := group(f)
	if err := g.Add(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := g.Add(ctx, "carol"); err != nil {
		t.Fatal(err)
	}
	if err := g.Add(ctx, "ghost"); err == nil {
		t.Fatal("missing account added")
	}
	if err := g.Add(ctx, "--help"); err == nil {
		t.Fatal("bad name accepted")
	}
	want := []string{"getent group shiver-admins", "getent group shiver-admins", "gpasswd -a carol shiver-admins"}
	if !reflect.DeepEqual(f.Lines(), want) {
		t.Fatalf("got %q", f.Lines())
	}
}

func TestRemove(t *testing.T) {
	f := (&run.Fake{}).On("getent group shiver-admins", run.FakeResponse{Stdout: "shiver-admins:x:998:alice,bob\n"})
	if err := group(f).Remove(ctx, "bob"); err != nil {
		t.Fatal(err)
	}
	if got := f.Lines(); got[len(got)-1] != "gpasswd -d bob shiver-admins" {
		t.Fatalf("got %q", got)
	}
	f = (&run.Fake{}).On("getent group shiver-admins", run.FakeResponse{Stdout: "shiver-admins:x:998:alice\n"})
	if err := group(f).Remove(ctx, "alice"); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("last admin: %v", err)
	}
	if err := group(f).Remove(ctx, "nobody"); err != nil {
		t.Fatalf("non-member: %v", err)
	}
}
