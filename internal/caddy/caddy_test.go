package caddy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GenericD00d/shiver-server-manager/internal/run"
	"github.com/GenericD00d/shiver-server-manager/internal/testutil"
)

const marker = "Managed by shiver-server-manager"

func TestGolden(t *testing.T) {
	testutil.Golden(t, "global.caddy", RenderGlobal(marker, Global{AdminSocket: true}))
	if g := string(RenderGlobal(marker, Global{AdminSocket: true, NoRenewalRatio: true})); strings.Contains(g, "renewal_window_ratio") || !strings.Contains(g, "intermediate_lifetime 730d\n\t}\n}") {
		t.Fatalf("for Caddy 2.10:\n%s", g)
	}
	testutil.Golden(t, "global-test-ca.caddy", RenderGlobal(marker, Global{Email: "admin@example.com", ACMECA: "https://localhost:14000/dir", ACMECARoot: "/etc/caddy/pebble.pem"}))
	testutil.Golden(t, "site-letsencrypt.caddy", RenderSite(marker, Site{ID: "chat-a", Domain: "chat-a.example.com", Port: 5000}))
	testutil.Golden(t, "site-staging.caddy", RenderSite(marker, Site{ID: "chat-a", Domain: "chat-a.example.com", Port: 5000, TestMode: true}))
	testutil.Golden(t, "site-selfsigned.caddy", RenderSite(marker, Site{ID: "chat-b", Domain: "chat-b.example.com", Port: 5001, SelfSigned: true}))
	testutil.Golden(t, "Caddyfile", FreshCaddyfile(marker, "/etc/caddy/shiver-server-manager/global.caddy", "/etc/caddy/sites/*.caddy"))
}

func TestIssuerDir(t *testing.T) {
	for in, want := range map[string]string{
		"https://acme-v02.api.letsencrypt.org/directory":         "acme-v02.api.letsencrypt.org-directory",
		"https://acme-staging-v02.api.letsencrypt.org/directory": "acme-staging-v02.api.letsencrypt.org-directory",
		"https://localhost:14000/dir":                            "localhost-14000-dir",
	} {
		if got := IssuerDir(in); got != want {
			t.Errorf("%s: got %q, want %q", in, got, want)
		}
	}
}

const packageDefault = `# The Caddyfile is an easy way to configure your Caddy web server.
#
# Unless the file starts with a global options block, the first
# uncommented line is always the address of your site.

:80 {
	# Set this path to your site's directory.
	root * /usr/share/caddy

	# Enable the static file server.
	file_server
}
`

func TestCaddyfileLayout(t *testing.T) {
	g, s := "/etc/caddy/g.caddy", "/etc/caddy/sites/*.caddy"
	if !IsPackageDefault(packageDefault) {
		t.Fatal("package default not recognised")
	}
	if IsPackageDefault(packageDefault + "\nblog.example.com {\n\troot * /srv\n}\n") {
		t.Fatal("edited file taken for the default")
	}

	withOptions := "# mine\n{\n\temail me@example.com\n}\n\nblog.example.com {\n\tfile_server\n}\n"
	got, err := AddImports(withOptions, g, s)
	if err != nil {
		t.Fatal(err)
	}
	want := "# mine\n{\n\timport /etc/caddy/g.caddy\n\temail me@example.com\n}\n\nblog.example.com {\n\tfile_server\n}\n\nimport /etc/caddy/sites/*.caddy\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if again, _ := AddImports(got, g, s); again != got || !HasImports(got, g, s) {
		t.Fatal("not idempotent")
	}

	noOptions := "blog.example.com {\n\tfile_server\n}"
	got, _ = AddImports(noOptions, g, s)
	if !strings.HasPrefix(got, "{\n\timport /etc/caddy/g.caddy\n}\n\nblog.example.com") || !strings.HasSuffix(got, "import /etc/caddy/sites/*.caddy\n") {
		t.Fatalf("got:\n%s", got)
	}

	if _, err := AddImports("{ email me@example.com\n}\n", g, s); err == nil {
		t.Fatal("one-line options block not refused")
	}
}

func TestApplyRestoresOnValidationFailure(t *testing.T) {
	dir := t.TempDir()
	f := (&run.Fake{}).On("runuser", run.FakeResponse{ExitCode: 1, Stderr: "Error: adapting config: unknown directive"})
	c := &Caddy{R: f, Caddyfile: filepath.Join(dir, "Caddyfile"), SitesDir: dir}
	old := filepath.Join(dir, "a.caddy")
	os.WriteFile(old, []byte("old"), 0o644)
	newFile := filepath.Join(dir, "b.caddy")
	err := c.Apply(context.Background(), map[string][]byte{old: []byte("new"), newFile: []byte("x")}, false)
	if _, ok := err.(*ValidationError); !ok || !strings.Contains(err.Error(), "unknown directive") {
		t.Fatalf("got %v", err)
	}
	if b, _ := os.ReadFile(old); string(b) != "old" {
		t.Fatal("old file not restored")
	}
	if _, err := os.Stat(newFile); err == nil {
		t.Fatal("new file left behind")
	}
	for _, l := range f.Lines() {
		if strings.Contains(l, "reload") {
			t.Fatal("reloaded after a failed validation")
		}
	}
	if !strings.Contains(f.Lines()[0], "runuser -u caddy -- /usr/bin/caddy validate") {
		t.Fatalf("validate not run as caddy: %q", f.Lines())
	}
}

func TestVersion(t *testing.T) {
	f := (&run.Fake{}).On("caddy version", run.FakeResponse{Stdout: "v2.11.9 h1:abc=\n"})
	v, ok, err := (&Caddy{R: f}).Version(context.Background())
	if err != nil || !ok || v != [3]int{2, 11, 9} || !VersionOK(v) {
		t.Fatalf("%v %v %v", v, ok, err)
	}
	// Ubuntu's package prints just the number.
	f = (&run.Fake{}).On("caddy version", run.FakeResponse{Stdout: "2.6.2\n"})
	if v, ok, err := (&Caddy{R: f}).Version(context.Background()); err != nil || !ok || v != [3]int{2, 6, 2} {
		t.Fatalf("Ubuntu's format: %v %v %v", v, ok, err)
	}
	if VersionOK([3]int{2, 6, 2}) || !VersionOK([3]int{3, 0, 0}) {
		t.Fatal("VersionOK")
	}
}

func TestLastCertError(t *testing.T) {
	journal := `{"MESSAGE":"{\"level\":\"error\",\"msg\":\"could not get certificate from issuer\",\"identifier\":\"chat-a.example.com\",\"error\":\"first\"}"}
{"MESSAGE":"{\"level\":\"error\",\"msg\":\"could not get certificate from issuer\",\"identifier\":\"other.example.com\",\"error\":\"other\"}"}
{"MESSAGE":"not json"}
{"MESSAGE":"{\"level\":\"error\",\"msg\":\"could not get certificate from issuer\",\"identifier\":\"chat-a.example.com\",\"error\":\"DNS problem: NXDOMAIN\"}"}
`
	if got := lastCertError([]byte(journal), "chat-a.example.com"); got != "DNS problem: NXDOMAIN" {
		t.Fatalf("got %q", got)
	}
}

func TestDropInFor(t *testing.T) {
	caddys := "[Service]\nExecStart=/usr/bin/caddy run --environ --config /etc/caddy/Caddyfile\nExecReload=/usr/bin/caddy reload --config /etc/caddy/Caddyfile --force\n"
	if d := DropInFor(caddys, UnitDropIn, "m", false); d != nil {
		t.Fatalf("Caddy's own unit got a drop-in:\n%s", d)
	}
	suse := "[Service]\nExecStart=/usr/bin/caddy run --resume --environ --config /etc/caddy/Caddyfile\nExecReload=/usr/bin/caddy reload --config /etc/caddy/Caddyfile\n"
	d := string(DropInFor(suse, UnitDropIn, "m", false))
	if !strings.Contains(d, "ExecStart=\nExecStart=/usr/bin/caddy run --environ") || strings.Contains(d, "--resume") || !strings.Contains(d, "--force") {
		t.Fatalf("openSUSE's unit:\n%s", d)
	}
	// With our drop-in already shown by systemctl cat, the answer is the same.
	withOurs := suse + "\n# " + UnitDropIn + "\n" + d
	if got := string(DropInFor(withOurs, UnitDropIn, "m", true)); !strings.Contains(got, "ExecStart=\nExecStart=") || !strings.Contains(got, "RuntimeDirectory=caddy\n") {
		t.Fatalf("confined openSUSE's unit:\n%s", got)
	}
	if got := string(DropInFor(caddys, UnitDropIn, "m", true)); strings.Contains(got, "ExecStart") || !strings.Contains(got, "RuntimeDirectory=caddy") {
		t.Fatalf("confined, Caddy's own unit:\n%s", got)
	}
}
