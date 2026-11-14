package sharkord

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/GenericD00d/shiver-server-manager/internal/testutil"
)

// firstStartOutput is the notice Sharkord 0.0.30 prints (db/seed.ts), with
// chalk's colour codes.
const firstStartOutput = "[info] Starting server...\n\n" +
	"\x1b[91m\x1b[1m🚨🚨 I M P O R T A N T 🚨🚨\x1b[22m\x1b[39m\n" +
	"\x1b[2m────────────────────────────────────────────────────\x1b[22m\n" +
	"\x1b[97mThis server has been started for the first time.\x1b[39m\n" +
	"\x1b[97mPlease save this access token somewhere safe, as it will not be shown again and there is no way to recover it.\x1b[39m\n" +
	"\x1b[97mThe access token below is used to gain admin privileges. Anyone with this token can take over the server.\x1b[39m\n" +
	"\x1b[37mPlease read the documentation on how to use this token.\x1b[39m\n" +
	"\x1b[93m────────────────────────────────────────────────────\x1b[39m\n" +
	"\x1b[1m\x1b[92m019a6b2c-3d4e-7f80-9123-456789abcdef\x1b[39m\x1b[22m\n" +
	"\x1b[93m────────────────────────────────────────────────────\x1b[39m\n"

func TestCaptureToken(t *testing.T) {
	tok, ok := CaptureToken(firstStartOutput)
	if !ok || tok != "019a6b2c-3d4e-7f80-9123-456789abcdef" {
		t.Fatalf("got %q %v", tok, ok)
	}
	// A UUIDv7 before the notice (say, a server ID in a log line) is ignored.
	if tok, _ := CaptureToken("id 019a0000-0000-7000-8000-000000000000\n" + firstStartOutput); tok != "019a6b2c-3d4e-7f80-9123-456789abcdef" {
		t.Fatalf("took the wrong UUID: %q", tok)
	}
	if _, ok := CaptureToken("[info] Server listening on 127.0.0.1:5000\n"); ok {
		t.Fatal("found a token in a normal start")
	}
}

func makeDB(t *testing.T, dir, token string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`PRAGMA journal_mode=WAL`,
		`CREATE TABLE settings (name TEXT, secret_token TEXT, enable_plugins INTEGER NOT NULL DEFAULT 0)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if token != "" {
		if _, err := db.Exec(`INSERT INTO settings (name, secret_token) VALUES ('x', ?)`, HashToken(token)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCheckToken(t *testing.T) {
	dir := t.TempDir()
	tok := "019a6b2c-3d4e-7f80-9123-456789abcdef"
	makeDB(t, dir, tok)
	if err := CheckToken(dir, tok); err != nil {
		t.Fatal(err)
	}
	if err := CheckToken(dir, " "+strings.ToUpper(tok)+"\n"); err != nil {
		t.Fatalf("pasted form refused: %v", err)
	}
	if err := CheckToken(dir, "019a6b2c-3d4e-7f80-9123-456789abcdee"); !errors.Is(err, ErrTokenMismatch) {
		t.Fatalf("wrong token: %v", err)
	}
	if !HasSettings(dir) {
		t.Fatal("HasSettings false")
	}
	// No -shm or -wal files are left beside the database.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("files beside the database: %v", entries)
	}
	empty := t.TempDir()
	makeDB(t, empty, "")
	if err := CheckToken(empty, tok); !errors.Is(err, ErrNoTokenInDB) {
		t.Fatalf("empty settings: %v", err)
	}
	if HasSettings(t.TempDir()) {
		t.Fatal("HasSettings true without a database")
	}
}

func TestHashToken(t *testing.T) {
	sum := sha256.Sum256([]byte("dev"))
	if HashToken("dev") != hex.EncodeToString(sum[:]) {
		t.Fatal("hash differs")
	}
}

func TestNormalizeSetting(t *testing.T) {
	ok := map[[2]string]string{
		{"SHARKORD_WEBRTC_MAX_BITRATE", " 30000000 "}:       "30000000",
		{"SHARKORD_DEBUG", "False"}:                         "false",
		{"SHARKORD_DEBUG", "yes"}:                           "true",
		{"SHARKORD_OIDC_CLIENT_ID", "12345"}:                "12345",
		{"SHARKORD_ALLOWED_ORIGINS", "https://a.com, ,b"}:   "https://a.com,b",
		{"SHARKORD_OIDC_ISSUER", "https://id.example.com/"}: "https://id.example.com/",
		{"SHARKORD_DEBUG", ""}:                              "",
	}
	for in, want := range ok {
		got, err := NormalizeSetting(in[0], in[1])
		if err != nil || got != want {
			t.Errorf("%v: got %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range [][2]string{
		{"SHARKORD_PORT", "1"}, {"SHARKORD_NOPE", "1"}, {"SHARKORD_WEBRTC_MAX_BITRATE", "fast"},
		{"SHARKORD_WEBRTC_MAX_BITRATE", "-1"}, {"SHARKORD_DEBUG", "maybe"}, {"SHARKORD_OIDC_CLIENT_ID", "a'b"},
		{"SHARKORD_OIDC_CLIENT_ID", "a\nSHARKORD_HOST=0.0.0.0"}, {"SHARKORD_OIDC_ISSUER", "javascript:x"},
	} {
		if got, err := NormalizeSetting(bad[0], bad[1]); err == nil {
			t.Errorf("%v accepted as %q", bad, got)
		}
	}
}

// Each encoded value must JSON.parse to the right type once systemd strips
// the single quotes.
func TestEncodeSettingParsesToRightType(t *testing.T) {
	cases := map[string]any{
		"SHARKORD_WEBRTC_MAX_BITRATE=30000000":                 float64(30000000),
		"SHARKORD_DEBUG=false":                                 false,
		"SHARKORD_OIDC_CLIENT_ID=12345":                        "12345",
		"SHARKORD_OIDC_CLIENT_SECRET=a\"b\\c":                  "a\"b\\c",
		"SHARKORD_ALLOWED_ORIGINS=https://a.com,https://b.com": []any{"https://a.com", "https://b.com"},
	}
	for in, want := range cases {
		k, v, _ := strings.Cut(in, "=")
		enc, err := EncodeSetting(k, v)
		if err != nil {
			t.Fatal(err)
		}
		parsed := ParseEnvFile([]byte(k + "=" + enc))
		_, raw, _ := strings.Cut(parsed[0], "=")
		var got any
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			t.Fatalf("%s: %q doesn't parse: %v", in, raw, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: parses to %#v, want %#v", in, got, want)
		}
	}
}

func TestRenderEnvGolden(t *testing.T) {
	env, err := RenderEnv(EnvParams{
		ID: "chat-a", DataPath: "/srv/sharkord-disk2/sharkord/chat-a", HTTPPort: 5000, WebRTCPort: 40000,
		AnnouncedIPv4: "203.0.113.10", ManagedByMarker: "Managed by shiver-server-manager",
		Settings: map[string]string{
			"SHARKORD_WEBRTC_MAX_BITRATE": "30000000",
			"SHARKORD_OIDC_CLIENT_ID":     "12345",
			"SHARKORD_ALLOWED_ORIGINS":    "https://chat-a.example.com",
		},
		SecretSettings: map[string]string{"SHARKORD_OIDC_CLIENT_SECRET": "s3cret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	testutil.Golden(t, "chat-a.env", env)
	if got := ReadSecretSettings(env); got["SHARKORD_OIDC_CLIENT_SECRET"] != "s3cret" || len(got) != 1 {
		t.Fatalf("secrets read back as %v", got)
	}
}

func TestVersions(t *testing.T) {
	if CompareVersions("0.0.30", "v0.0.9") != 1 || CompareVersions("0.1", "0.1.0") != 0 || CompareVersions("0.0.29", "0.0.30") != -1 {
		t.Fatal("CompareVersions")
	}
	for _, bad := range []string{"", "1", "0.0.30/../x", "0.0.30-beta", "a.b.c"} {
		if ValidVersion(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func fakeGitHub(t *testing.T, binary []byte, sum string) *Client {
	asset, _ := AssetName(runtime.GOARCH)
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/releases", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			http.Error(w, "no UA", 403)
			return
		}
		fmt.Fprint(w, `[{"tag_name":"v0.0.31","prerelease":true},{"tag_name":"v0.0.30","published_at":"2026-11-06T19:04:31Z"},{"tag_name":"v0.0.29","draft":true}]`)
	})
	mux.HandleFunc("/dl/v0.0.30/release.json", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"version":"0.0.30","artifacts":[{"name":%q,"size":%d,"checksum":%q}]}`, asset, len(binary), sum)
	})
	mux.HandleFunc("/dl/v0.0.30/"+asset, func(w http.ResponseWriter, _ *http.Request) { w.Write(binary) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := NewClient("test", filepath.Join(t.TempDir(), "cache.json"))
	c.APIBase, c.DownloadBase = srv.URL+"/repos", srv.URL+"/dl"
	return c
}

func TestReleasesSkipsDraftsAndCaches(t *testing.T) {
	c := fakeGitHub(t, nil, "")
	rs, err := c.Releases(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].Version != "0.0.30" || !rs[0].Tested {
		t.Fatalf("got %+v", rs)
	}
	c.APIBase = "http://127.0.0.1:1" // unreachable: must come from the cache
	if rs, err := c.Releases(context.Background()); err != nil || len(rs) != 1 {
		t.Fatalf("cache not used: %v", err)
	}
	c.Now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if _, err := c.Releases(context.Background()); err == nil {
		t.Fatal("stale cache used")
	}
}

func TestInstallChecksChecksum(t *testing.T) {
	bin := []byte("#!/bin/sh\necho sharkord\n")
	sum := sha256.Sum256(bin)
	opt := t.TempDir()

	bad := fakeGitHub(t, bin, strings.Repeat("0", 64))
	if err := bad.Install(context.Background(), opt, "0.0.30"); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("bad checksum: %v", err)
	}
	if _, err := os.Stat(BinaryPath(opt, "0.0.30")); err == nil {
		t.Fatal("bad download installed")
	}

	good := fakeGitHub(t, bin, hex.EncodeToString(sum[:]))
	if err := good.Install(context.Background(), opt, "0.0.30"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(BinaryPath(opt, "0.0.30"))
	if string(got) != string(bin) {
		t.Fatal("installed binary differs")
	}
	if err := good.Install(context.Background(), opt, "../evil"); err == nil {
		t.Fatal("path in version accepted")
	}
}

func TestParseEnvFile(t *testing.T) {
	got := ParseEnvFile([]byte("# c\nA=1\n\nB='\"x\"'\nbad line\n"))
	if !reflect.DeepEqual(got, []string{"A=1", `B="x"`}) {
		t.Fatalf("%q", got)
	}
}

func TestSetTokenHash(t *testing.T) {
	data := t.TempDir()
	db, err := sql.Open("sqlite", "file:"+DBPath(data))
	if err != nil {
		t.Fatal(err)
	}
	db.Exec(`CREATE TABLE settings (secret_token TEXT)`)
	db.Exec(`INSERT INTO settings VALUES ('x')`)
	db.Close()
	tok, err := NewToken()
	if err != nil || !tokenUUID.MatchString(tok) {
		t.Fatalf("%q %v", tok, err)
	}
	if err := SetTokenHash(DBPath(data), "not a hash"); err == nil {
		t.Fatal("accepted a bad hash")
	}
	if err := SetTokenHash(DBPath(data), HashToken(tok)); err != nil {
		t.Fatal(err)
	}
	if err := CheckToken(data, tok); err != nil {
		t.Fatal(err)
	}
	if err := SetTokenHash(filepath.Join(data, "missing.sqlite"), HashToken(tok)); err == nil {
		t.Fatal("made a database")
	}
}

func TestOfferedIsAnyPublishedRelease(t *testing.T) {
	c := fakeGitHub(t, nil, "")
	ctx := context.Background()
	if v, err := c.Offered(ctx, ""); err != nil || v != "0.0.30" {
		t.Fatalf("default: %q %v", v, err)
	}
	if v, err := c.Offered(ctx, "v0.0.30"); err != nil || v != "0.0.30" {
		t.Fatalf("named: %q %v", v, err)
	}
	// a pre-release, a draft and a version that doesn't exist
	for _, v := range []string{"0.0.31", "0.0.29", "0.0.28", "../x"} {
		if _, err := c.Offered(ctx, v); err == nil {
			t.Errorf("%s offered", v)
		}
	}
}

func TestFactsCarryForward(t *testing.T) {
	newest := LatestTested()
	if f := FactsFor("9.9.9"); f.Version != "9.9.9" || f.PluginSDK != newest.PluginSDK || f.DBSwitches != newest.DBSwitches {
		t.Fatalf("newer version: %+v", f)
	}
	if f := FactsFor("v" + Tested[0].Version); f != Tested[0] {
		t.Fatalf("checked version: %+v", f)
	}
	if f := FactsFor("0.0.1"); f.PluginSDK != Tested[0].PluginSDK {
		t.Fatalf("older version: %+v", f)
	}
}

func TestOfferedWithoutGitHubsList(t *testing.T) {
	c := fakeGitHub(t, nil, "")
	c.CacheFile = ""
	c.APIBase = "http://127.0.0.1:1" // GitHub refusing, or unreachable
	ctx := context.Background()
	if v, err := c.Offered(ctx, ""); err != nil || v != LatestTested().Version {
		t.Fatalf("default: %q %v", v, err)
	}
	if v, err := c.Offered(ctx, "v0.0.30"); err != nil || v != "0.0.30" {
		t.Fatalf("named: %q %v", v, err)
	}
	if _, err := c.Offered(ctx, "../x"); err == nil {
		t.Fatal("a bad version was taken")
	}
}
