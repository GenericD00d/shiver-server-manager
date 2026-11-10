package web

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GenericD00d/shiver-server-manager/internal/auth"
)

// TestDemo serves the real web page with sample data on 127.0.0.1:8443, for
// looking at it during development (log in as "admin" / "demo"):
//
//	SSM_WEB_DEMO=1 go test ./internal/web -run TestDemo -timeout 0
func TestDemo(t *testing.T) {
	if os.Getenv("SSM_WEB_DEMO") == "" {
		t.Skip("set SSM_WEB_DEMO=1 to serve the demo")
	}
	data := map[string]string{
		"instance list": `[{"id":"chat-a","status":"active","domain":"chat.example.com","tls":"letsencrypt","testMode":false,"sharkordVersion":"0.0.30","location":"/var/lib/sharkord","dataPath":"/var/lib/sharkord/chat-a","httpPort":5000,"webrtcPort":40000,"encrypted":true,"operation":null,"active":"active","healthy":true,"tokenSaved":true,"locked":false},
{"id":"gaming","status":"active","domain":"gaming.example.org","tls":"selfsigned","testMode":false,"sharkordVersion":"0.0.30","location":"/srv/sharkord-disk2/sharkord","dataPath":"/srv/sharkord-disk2/sharkord/gaming","httpPort":5001,"webrtcPort":40001,"encrypted":false,"operation":null,"active":"active","healthy":true,"tokenSaved":true,"locked":false},
{"id":"family","status":"active","domain":"family.example.net","tls":"letsencrypt","testMode":false,"sharkordVersion":"0.0.30","location":"/var/lib/sharkord","dataPath":"/var/lib/sharkord/family","httpPort":5002,"webrtcPort":40002,"encrypted":true,"operation":null,"active":"inactive","healthy":false,"tokenSaved":true,"locked":true}]`,
		"cert status": `[{"instance":"chat-a","domain":"chat.example.com","issuer":"acme-v02.api.letsencrypt.org-directory","present":true,"notAfter":"2027-02-07T12:00:00Z","daysLeft":89,"pending":false},
{"instance":"gaming","domain":"gaming.example.org","issuer":"local","present":true,"notAfter":"2027-11-09T12:00:00Z","daysLeft":364,"pending":false},
{"instance":"family","domain":"family.example.net","issuer":"acme-v02.api.letsencrypt.org-directory","present":false,"daysLeft":0,"pending":true,"lastError":"DNS problem: NXDOMAIN looking up A for family.example.net"}]`,
		"cert root":           `{"sha256Fingerprint":"3F:A1:9C:22:7B:0E:44:D1:8A:6C:5F:90:12:EE:7D:31:C4:58:0B:A9:6E:21:F7:3D:88:15:C0:4A:9B:62:E3:70","notAfter":"2036-11-07T12:00:00Z","downloadURLs":["http://gaming.example.org/sharkord-root.crt"]}`,
		"backup list":         `{"chat-a":[{"file":"2026-11-09T000000Z.tar.zst.age","createdAt":"2026-11-09T00:00:00Z","size":48234567,"sha256":"x"},{"file":"2026-11-09T120000Z.tar.zst.age","createdAt":"2026-11-09T12:00:00Z","size":48934567,"sha256":"y"}],"gaming":[{"file":"2026-11-09T120000Z.tar.zst.age","createdAt":"2026-11-09T12:00:00Z","size":1234567890,"sha256":"z"}],"family":null}`,
		"storage list":        `{"locations":[{"path":"/var/lib/sharkord","default":true,"mount":{"target":"/","source":"/dev/sda1","fstype":"ext4"},"freeBytes":82345678901,"servers":["chat-a","family"]},{"path":"/srv/sharkord-disk2/sharkord","fsUUID":"6f1c","mount":{"target":"/srv/sharkord-disk2","source":"/dev/sdb1","fstype":"ext4"},"freeBytes":912345678901,"servers":["gaming"]}],"backupLocation":"/var/backups/sharkord"}`,
		"storage mount":       `[{"path":"/dev/sdc1","size":"1.8T","type":"part","fstype":"ext4","uuid":"a1b2","label":"archive","mountPoint":"/srv/sharkord-archive"}]`,
		"shiver status":       `[{"instance":"chat-a","installed":"0.3.2","latest":"0.3.2","switches":"on"}]`,
		"firewall status":     `{"firewall":"ufw","rules":[{"port":22,"proto":"tcp","instance":"ssh"},{"port":80,"proto":"tcp","instance":"caddy"},{"port":443,"proto":"tcp","instance":"caddy"},{"port":443,"proto":"udp","instance":"caddy"},{"port":40000,"proto":"udp","instance":"chat-a"},{"port":40000,"proto":"tcp","instance":"chat-a"},{"port":40001,"proto":"udp","instance":"gaming"},{"port":40001,"proto":"tcp","instance":"gaming"}]}`,
		"admins list":         `{"admins":["admin","sam"]}`,
		"repair":              `[]`,
		"backup schedule":     `[{"instance":"chat-a","off":false,"times":["00:00","12:00"],"days":[],"keepCount":14,"keepDays":0,"default":true,"next":"2026-11-10T00:00:00Z","backups":2},{"instance":"gaming","off":false,"times":["03:30"],"days":["mon","thu"],"keepCount":8,"keepDays":30,"default":false,"next":"2026-11-12T03:30:00Z","backups":1},{"instance":"family","off":true,"times":["00:00","12:00"],"days":[],"keepCount":14,"keepDays":0,"default":false,"backups":0}]`,
		"self-update --check": `{"current":"0.1.4","latest":"0.1.5","available":true,"hasToken":true}`,
		"health alerts":       `{"configured":true,"webhook":"https://discord.com/api/webhooks/…","emailTo":"","smtp":"","smtpUser":"","emailFrom":""}`,
	}
	dir := t.TempDir()
	health := filepath.Join(dir, "health.json")
	os.WriteFile(health, []byte(`{"checkedAt":"2026-11-09T21:55:00Z","notices":[{"key":"locked:family","level":"info","instance":"family","message":"family is encrypted and waits for an admin to log in and unlock the vault"},{"key":"cert-pending:family","level":"warning","instance":"family","message":"family.example.net has no certificate yet; see 'cert status family'"}]}`), 0o600)
	read := func(_ context.Context, args, _ []string) (string, string, int, error) {
		key := args[0] + " " + args[1]
		if args[0] == "repair" {
			key = "repair"
		}
		if args[0] == "token" {
			return "019a6b2c-3d4e-7f80-9123-456789abcdef\n", "", 0, nil
		}
		return data[key], "", 0, nil
	}
	run := func(ctx context.Context, args, _ []string, line func(string)) (int, error) {
		for _, l := range []string{"Working on: " + strings.Join(args[:2], " "), "  … step one", "  ✓ step one", "  … step two", "  ✓ step two"} {
			line(l)
			time.Sleep(400 * time.Millisecond)
		}
		return 0, nil
	}
	s := &Server{Version: "0.1.0", Sessions: NewSessions(), Jobs: NewJobs(run), Read: read,
		Login: func(user string, p auth.Prompter) error {
			pw, _ := p.Hidden("Password:")
			if user == "admin" && pw == "demo" {
				return nil
			}
			return auth.ErrBadLogin
		},
		LogFile: filepath.Join(dir, "log"), Static: StaticFS(), HealthFile: health}
	t.Log("serving on http://127.0.0.1:8443 (admin / demo)")
	if err := http.ListenAndServe("127.0.0.1:8443", s.Handler()); err != nil {
		t.Fatal(err)
	}
}
