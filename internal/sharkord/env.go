package sharkord

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// Kind is how a setting's value must be written so Sharkord's JSON.parse of
// the env value gives the right type.
type Kind int

const (
	KindInt Kind = iota
	KindBool
	KindString
	KindList
)

// Setting is one env setting the menu offers.
type Setting struct {
	Name   string
	Kind   Kind
	Secret bool // kept out of the records; lives only in the env file
	Help   string
}

// Settings are the env settings admins may change. The host, ports,
// announced address and auto-update are always set by the manager.
var Settings = []Setting{
	{Name: "SHARKORD_WEBRTC_MAX_BITRATE", Kind: KindInt, Help: "Voice and video bitrate cap, in bits per second"},
	{Name: "SHARKORD_MAX_REQUEST_BODY_BYTES", Kind: KindInt, Help: "Largest request body, in bytes"},
	{Name: "SHARKORD_RATE_LIMITERS_ENABLED", Kind: KindBool, Help: "Rate limiting"},
	{Name: "SHARKORD_ALLOWED_ORIGINS", Kind: KindList, Help: "Origins allowed to connect (comma-separated)"},
	{Name: "SHARKORD_TRUSTED_PROXIES", Kind: KindList, Help: "Proxy addresses trusted for client IPs (comma-separated)"},
	{Name: "SHARKORD_DEBUG", Kind: KindBool, Help: "Debug logging"},
	{Name: "SHARKORD_BACKUP_DATABASE", Kind: KindBool, Help: "Sharkord's own database snapshot before upgrades"},
	{Name: "SHARKORD_OIDC_ENABLED", Kind: KindBool, Help: "Single sign-on"},
	{Name: "SHARKORD_OIDC_ISSUER", Kind: KindString, Help: "Single sign-on issuer URL"},
	{Name: "SHARKORD_OIDC_CLIENT_ID", Kind: KindString, Help: "Single sign-on client ID"},
	{Name: "SHARKORD_OIDC_CLIENT_SECRET", Kind: KindString, Secret: true, Help: "Single sign-on client secret"},
	{Name: "SHARKORD_OIDC_REDIRECT_URI", Kind: KindString, Help: "Single sign-on redirect URI"},
	{Name: "SHARKORD_OIDC_DISABLE_LOCAL_LOGIN", Kind: KindBool, Help: "Only allow single sign-on"},
}

// ManagedEnv are set by the manager and can't be changed as settings.
var ManagedEnv = []string{
	"SHARKORD_DATA_PATH", "SHARKORD_HOST", "SHARKORD_PORT", "SHARKORD_WEBRTC_PORT",
	"SHARKORD_WEBRTC_ANNOUNCED_ADDRESS", "SHARKORD_AUTOUPDATE",
}

func FindSetting(name string) (Setting, bool) {
	for _, s := range Settings {
		if s.Name == name {
			return s, true
		}
	}
	return Setting{}, false
}

// NormalizeSetting checks an admin's value and returns it in plain form
// (what the records keep): "30000000", "true", "12345", "a,b". An empty
// value means "back to the default".
func NormalizeSetting(name, value string) (string, error) {
	s, ok := FindSetting(name)
	if !ok {
		if slices.Contains(ManagedEnv, name) {
			return "", fmt.Errorf("%s is set by the manager", name)
		}
		return "", fmt.Errorf("unknown setting %s", name)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if strings.ContainsAny(value, "'\n\r") || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("%s: single quotes and control characters aren't allowed", name)
	}
	switch s.Kind {
	case KindInt:
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n <= 0 {
			return "", fmt.Errorf("%s must be a whole number above 0", name)
		}
		return strconv.FormatInt(n, 10), nil
	case KindBool:
		switch strings.ToLower(value) {
		case "true", "yes", "on", "1":
			return "true", nil
		case "false", "no", "off", "0":
			return "false", nil
		}
		return "", fmt.Errorf("%s must be true or false", name)
	case KindList:
		var items []string
		for _, it := range strings.Split(value, ",") {
			if it = strings.TrimSpace(it); it != "" {
				items = append(items, it)
			}
		}
		if len(items) == 0 {
			return "", nil
		}
		return strings.Join(items, ","), nil
	}
	if strings.HasSuffix(name, "_URI") || strings.HasSuffix(name, "_ISSUER") {
		if u, err := url.Parse(value); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return "", fmt.Errorf("%s must be an http or https URL", name)
		}
	}
	return value, nil
}

// EncodeSetting writes a normalized value for an env file line, so that
// JSON.parse gives Sharkord the right type: numbers and booleans bare,
// strings and lists as JSON inside single quotes. A bare digits-only string
// would become a number and stop Sharkord starting.
func EncodeSetting(name, value string) (string, error) {
	s, ok := FindSetting(name)
	if !ok {
		return "", fmt.Errorf("unknown setting %s", name)
	}
	switch s.Kind {
	case KindInt, KindBool:
		return value, nil
	case KindList:
		b, err := json.Marshal(strings.Split(value, ","))
		return "'" + string(b) + "'", err
	default:
		b, err := json.Marshal(value)
		return "'" + string(b) + "'", err
	}
}

// EnvParams are what the manager always sets.
type EnvParams struct {
	ID              string
	DataPath        string
	HTTPPort        int
	WebRTCPort      int
	AnnouncedIPv4   string
	Settings        map[string]string // normalized values from the records
	SecretSettings  map[string]string // normalized values kept only in the env file
	ManagedByMarker string
}

// RenderEnv builds an instance's env file.
func RenderEnv(p EnvParams) ([]byte, error) {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# %s (instance %s). Do not edit.\n", p.ManagedByMarker, p.ID)
	fmt.Fprintf(&b, "SHARKORD_DATA_PATH=%s\n", p.DataPath)
	fmt.Fprintf(&b, "SHARKORD_HOST=127.0.0.1\n")
	fmt.Fprintf(&b, "SHARKORD_PORT=%d\n", p.HTTPPort)
	fmt.Fprintf(&b, "SHARKORD_WEBRTC_PORT=%d\n", p.WebRTCPort)
	if p.AnnouncedIPv4 != "" {
		fmt.Fprintf(&b, "SHARKORD_WEBRTC_ANNOUNCED_ADDRESS=%s\n", p.AnnouncedIPv4)
	}
	fmt.Fprintf(&b, "SHARKORD_AUTOUPDATE=false\n")
	all := map[string]string{}
	for k, v := range p.Settings {
		all[k] = v
	}
	for k, v := range p.SecretSettings {
		all[k] = v
	}
	for _, s := range Settings { // fixed order
		v, ok := all[s.Name]
		if !ok || v == "" {
			continue
		}
		enc, err := EncodeSetting(s.Name, v)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&b, "%s=%s\n", s.Name, enc)
	}
	return b.Bytes(), nil
}

// ReadSecretSettings pulls secret settings back out of an existing env
// file, so rewriting it (for example after an IP change) keeps them.
func ReadSecretSettings(env []byte) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(env))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		s, found := FindSetting(k)
		if !found || !s.Secret {
			continue
		}
		v = strings.TrimSuffix(strings.TrimPrefix(v, "'"), "'")
		var str string
		if json.Unmarshal([]byte(v), &str) == nil {
			out[k] = str
		}
	}
	return out
}
