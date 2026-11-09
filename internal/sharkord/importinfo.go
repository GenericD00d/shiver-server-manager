package sharkord

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/ini.v1"
)

// DataVersion is the Sharkord version that last ran a data folder: the
// highest version among the folders in interface/.
func DataVersion(dataPath string) (string, error) {
	entries, err := os.ReadDir(filepath.Join(dataPath, "interface"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil // never started fully; any version will do
	}
	if err != nil {
		return "", err
	}
	best := ""
	for _, e := range entries {
		v := strings.TrimPrefix(e.Name(), "v")
		if e.IsDir() && ValidVersion(v) == nil && CompareVersions(v, best) > 0 {
			best = v
		}
	}
	return best, nil
}

// iniKeys maps config.ini's section.key to env setting names. The host,
// ports, announced address and auto-update are left out: the manager sets
// those.
var iniKeys = map[string]string{
	"server.debug":               "SHARKORD_DEBUG",
	"server.backupDatabase":      "SHARKORD_BACKUP_DATABASE",
	"server.maxRequestBodyBytes": "SHARKORD_MAX_REQUEST_BODY_BYTES",
	"server.allowedOrigins":      "SHARKORD_ALLOWED_ORIGINS",
	"server.trustedProxies":      "SHARKORD_TRUSTED_PROXIES",
	"server.rateLimitersEnabled": "SHARKORD_RATE_LIMITERS_ENABLED",
	"oidc.enabled":               "SHARKORD_OIDC_ENABLED",
	"oidc.issuer":                "SHARKORD_OIDC_ISSUER",
	"oidc.clientId":              "SHARKORD_OIDC_CLIENT_ID",
	"oidc.clientSecret":          "SHARKORD_OIDC_CLIENT_SECRET",
	"oidc.redirectUri":           "SHARKORD_OIDC_REDIRECT_URI",
	"oidc.disableLocalLogin":     "SHARKORD_OIDC_DISABLE_LOCAL_LOGIN",
	"webRtc.maxBitrate":          "SHARKORD_WEBRTC_MAX_BITRATE",
}

// defaults are Sharkord 0.0.30's defaults; matching values aren't carried
// over, so the env file only holds real changes.
var defaults = map[string]string{
	"SHARKORD_DEBUG":                    "false",
	"SHARKORD_BACKUP_DATABASE":          "true",
	"SHARKORD_MAX_REQUEST_BODY_BYTES":   "262144",
	"SHARKORD_ALLOWED_ORIGINS":          "*",
	"SHARKORD_TRUSTED_PROXIES":          "127.0.0.1,::1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,fc00::/7",
	"SHARKORD_RATE_LIMITERS_ENABLED":    "true",
	"SHARKORD_OIDC_ENABLED":             "false",
	"SHARKORD_OIDC_DISABLE_LOCAL_LOGIN": "false",
	"SHARKORD_WEBRTC_MAX_BITRATE":       "30000000",
}

// SettingsFromConfigINI reads an imported config.ini and returns the
// settings that differ from Sharkord's defaults, normalized. Sharkord's ini
// library writes lists as repeated "name[]=value" lines.
func SettingsFromConfigINI(path string) (map[string]string, error) {
	out := map[string]string{}
	f, err := ini.LoadSources(ini.LoadOptions{AllowShadows: true, SpaceBeforeInlineComment: true}, path)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	for key, env := range iniKeys {
		section, name, _ := strings.Cut(key, ".")
		sec := f.Section(section)
		var raw string
		switch {
		case sec.HasKey(name + "[]"):
			raw = strings.Join(sec.Key(name+"[]").ValueWithShadows(), ",")
		case sec.HasKey(name):
			raw = sec.Key(name).String()
		default:
			continue
		}
		norm, err := NormalizeSetting(env, raw)
		if err != nil || norm == "" || norm == defaults[env] {
			continue
		}
		out[env] = norm
	}
	return out, nil
}
