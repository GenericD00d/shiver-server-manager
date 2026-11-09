package sharkord

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// config.ini as Sharkord 0.0.30 writes it (npm "ini" stringify).
const configINI = `[server]
host=
port=4991
debug=false
autoupdate=false
backupDatabase=true
maxRequestBodyBytes=262144
rateLimitersEnabled=true
allowedOrigins[]=https://chat.example.com
allowedOrigins[]=https://other.example.com
trustedProxies[]=127.0.0.1
trustedProxies[]=::1
trustedProxies[]=10.0.0.0/8
trustedProxies[]=172.16.0.0/12
trustedProxies[]=192.168.0.0/16
trustedProxies[]=fc00::/7

[oidc]
enabled=true
issuer=https://id.example.com
clientId=12345
clientSecret=s3cret
redirectUri=
disableLocalLogin=false

[webRtc]
port=40000
announcedAddress=198.51.100.7
maxBitrate=20000000
`

func TestSettingsFromConfigINI(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.ini")
	os.WriteFile(p, []byte(configINI), 0o600)
	got, err := SettingsFromConfigINI(p)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"SHARKORD_ALLOWED_ORIGINS":    "https://chat.example.com,https://other.example.com",
		"SHARKORD_OIDC_ENABLED":       "true",
		"SHARKORD_OIDC_ISSUER":        "https://id.example.com",
		"SHARKORD_OIDC_CLIENT_ID":     "12345",
		"SHARKORD_OIDC_CLIENT_SECRET": "s3cret",
		"SHARKORD_WEBRTC_MAX_BITRATE": "20000000",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
	if got, _ := SettingsFromConfigINI(filepath.Join(t.TempDir(), "missing.ini")); len(got) != 0 {
		t.Fatal("missing file gave settings")
	}
}

func TestDataVersion(t *testing.T) {
	d := t.TempDir()
	for _, v := range []string{"0.0.9", "0.0.29", "0.0.30", "junk"} {
		os.MkdirAll(filepath.Join(d, "interface", v), 0o700)
	}
	if v, _ := DataVersion(d); v != "0.0.30" {
		t.Fatalf("got %q", v)
	}
	if v, err := DataVersion(t.TempDir()); v != "" || err != nil {
		t.Fatal("no interface folder")
	}
}
