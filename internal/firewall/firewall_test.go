package firewall

import (
	"context"
	"reflect"
	"testing"

	"github.com/GenericD00d/shiver-server-manager/internal/run"
)

// Output of "ufw status numbered" on Ubuntu 24.04 (ufw 0.36.2).
const ufwNumbered = `Status: active

     To                         Action      From
     --                         ------      ----
[ 1] 22/tcp                     ALLOW IN    Anywhere                   # sharkord:ssh
[ 2] 80/tcp                     ALLOW IN    Anywhere                   # sharkord:caddy
[ 3] 40000/udp                  ALLOW IN    Anywhere                   # sharkord:chat-a
[ 4] 40000/tcp                  ALLOW IN    Anywhere                   # sharkord:chat-a
[ 5] 8080/tcp                   ALLOW IN    Anywhere                   # someone else's
[ 6] 22/tcp (v6)                ALLOW IN    Anywhere (v6)              # sharkord:ssh
[ 7] 80/tcp (v6)                ALLOW IN    Anywhere (v6)              # sharkord:caddy
[ 8] 40000/udp (v6)             ALLOW IN    Anywhere (v6)              # sharkord:chat-a
[ 9] 40000/tcp (v6)             ALLOW IN    Anywhere (v6)              # sharkord:chat-a
[10] 443                        ALLOW IN    Anywhere
`

var ctx = context.Background()

func TestUFWList(t *testing.T) {
	f := (&run.Fake{}).On("ufw status numbered", run.FakeResponse{Stdout: ufwNumbered})
	rules, err := UFW{R: f}.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []Rule{{22, "tcp", "ssh"}, {80, "tcp", "caddy"}, {40000, "udp", "chat-a"}, {40000, "tcp", "chat-a"}}
	if !reflect.DeepEqual(rules, want) {
		t.Fatalf("got %v", rules)
	}
}

func TestUFWRemoveDeletesHighestFirstWithV6Twin(t *testing.T) {
	f := (&run.Fake{}).On("ufw status numbered", run.FakeResponse{Stdout: ufwNumbered})
	if err := (UFW{R: f}).Remove(ctx, Rule{40000, "udp", "chat-a"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"ufw status numbered", "ufw --force delete 8", "ufw --force delete 3"}
	if !reflect.DeepEqual(f.Lines(), want) {
		t.Fatalf("got %q", f.Lines())
	}
}

func TestUFWAllowSkipsExisting(t *testing.T) {
	f := (&run.Fake{}).On("ufw status numbered", run.FakeResponse{Stdout: ufwNumbered})
	u := UFW{R: f}
	u.Allow(ctx, Rule{40000, "udp", "chat-a"})
	u.Allow(ctx, Rule{40001, "udp", "chat-b"})
	want := []string{"ufw status numbered", "ufw status numbered", "ufw allow 40001/udp comment sharkord:chat-b"}
	if !reflect.DeepEqual(f.Lines(), want) {
		t.Fatalf("got %q", f.Lines())
	}
}

func TestUFWEnableAllowsSSHFirst(t *testing.T) {
	f := (&run.Fake{}).On("ufw status numbered", run.FakeResponse{Stdout: "Status: inactive\n"}).
		On("sshd -T", run.FakeResponse{Stdout: "port 2222\nport 22\naddressfamily any\n"})
	ports := SSHPorts(ctx, f)
	if !reflect.DeepEqual(ports, []int{2222, 22}) {
		t.Fatalf("ports %v", ports)
	}
	if err := (UFW{R: f}).Enable(ctx, ports); err != nil {
		t.Fatal(err)
	}
	lines := f.Lines()
	if lines[len(lines)-1] != "ufw --force enable" || lines[2] != "ufw allow 2222/tcp comment sharkord:ssh" {
		t.Fatalf("got %q", lines)
	}
}

func TestFirewalld(t *testing.T) {
	services := ""
	ports := map[string]string{}
	f := &run.Fake{Handler: func(c run.Call) (run.FakeResponse, bool) {
		a := c.Args
		switch {
		case len(a) == 2 && a[1] == "--list-services":
			return run.FakeResponse{Stdout: services}, true
		case len(a) == 2 && a[1] == "--list-ports":
			return run.FakeResponse{Stdout: ""}, true
		case len(a) == 3 && a[2] == "--get-ports":
			return run.FakeResponse{Stdout: ports[a[1][len("--service="):]]}, true
		case len(a) == 2 && a[1] == "--add-service=sharkord-chat-a":
			services += " sharkord-chat-a"
		case len(a) == 3 && a[2] == "--add-port=40000/udp":
			ports["sharkord-chat-a"] += " 40000/udp"
		}
		return run.FakeResponse{}, true
	}}
	fw := Firewalld{R: f}
	if err := fw.Allow(ctx, Rule{40000, "udp", "chat-a"}); err != nil {
		t.Fatal(err)
	}
	rules, _ := fw.List(ctx)
	if !reflect.DeepEqual(rules, []Rule{{40000, "udp", "chat-a"}}) {
		t.Fatalf("rules %v (calls %q)", rules, f.Lines())
	}
	want := []string{
		"firewall-cmd --permanent --new-service=sharkord-chat-a",
		"firewall-cmd --permanent --service=sharkord-chat-a --add-port=40000/udp",
		"firewall-cmd --permanent --add-service=sharkord-chat-a",
		"firewall-cmd --reload",
	}
	got := f.Lines()
	for _, w := range want {
		found := false
		for _, g := range got {
			found = found || g == w
		}
		if !found {
			t.Errorf("missing %q in %q", w, got)
		}
	}
}
