package validate

import (
	"fmt"
	"net"
	"strings"

	"golang.org/x/net/idna"
)

// Domain lowercases a domain, converts it to its ASCII (punycode) form, and
// checks it: at least two labels, each 1-63 characters of a-z, 0-9 and inner
// hyphens, 253 characters at most, and not an IP address.
func Domain(d string) (string, error) {
	d = strings.TrimSuffix(strings.TrimSpace(strings.ToLower(d)), ".")
	ascii, err := idna.Lookup.ToASCII(d)
	if err != nil {
		return "", fmt.Errorf("invalid domain %q: %v", d, err)
	}
	if net.ParseIP(ascii) != nil {
		return "", fmt.Errorf("%q is an IP address; a domain name is needed for HTTPS", d)
	}
	if len(ascii) > 253 {
		return "", fmt.Errorf("domain %q is too long", d)
	}
	labels := strings.Split(ascii, ".")
	if len(labels) < 2 {
		return "", fmt.Errorf("domain %q needs at least two parts, like chat.example.com", d)
	}
	for _, l := range labels {
		if len(l) < 1 || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return "", fmt.Errorf("invalid domain %q", d)
		}
		for _, c := range l {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return "", fmt.Errorf("invalid domain %q", d)
			}
		}
	}
	if tld := labels[len(labels)-1]; strings.Trim(tld, "0123456789") == "" {
		return "", fmt.Errorf("invalid domain %q", d)
	}
	return ascii, nil
}
