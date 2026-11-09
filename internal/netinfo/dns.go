package netinfo

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"
)

// DNSResult is what a domain's records say.
type DNSResult struct {
	Domain  string   `json:"domain"`
	A       []string `json:"a"`
	AAAA    []string `json:"aaaa"`
	OK      bool     `json:"ok"`
	Problem string   `json:"problem,omitempty"`
}

// PublicResolvers are asked directly, so a stale local cache doesn't hide
// a fresh record. The system resolver is the fallback when port 53 is
// blocked.
var PublicResolvers = []string{"1.1.1.1:53", "8.8.8.8:53"}

func resolver(server string) *net.Resolver {
	if server == "" {
		return net.DefaultResolver
	}
	d := &net.Dialer{Timeout: 3 * time.Second}
	return &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return d.DialContext(ctx, network, server)
	}}
}

func lookup(ctx context.Context, domain string) (a, aaaa []string, err error) {
	for _, srv := range append(append([]string{}, PublicResolvers...), "") {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		ips, lerr := resolver(srv).LookupIPAddr(ctx, domain)
		cancel()
		if lerr != nil {
			if dnsErr, ok := lerr.(*net.DNSError); ok && dnsErr.IsNotFound {
				return nil, nil, nil
			}
			err = lerr
			continue
		}
		for _, ip := range ips {
			if v4 := ip.IP.To4(); v4 != nil {
				a = append(a, v4.String())
			} else {
				aaaa = append(aaaa, ip.IP.String())
			}
		}
		return a, aaaa, nil
	}
	return nil, nil, err
}

// CheckDNS passes when every A record is publicIPv4. An AAAA record must
// point at this server too (one of localIPv6), or be removed: Let's Encrypt
// tries IPv6 first when one exists.
func CheckDNS(ctx context.Context, domain, publicIPv4 string, localIPv6 []string) DNSResult {
	r := DNSResult{Domain: domain}
	a, aaaa, err := lookup(ctx, domain)
	r.A, r.AAAA = a, aaaa
	switch {
	case err != nil:
		r.Problem = fmt.Sprintf("couldn't look up %s: %v", domain, err)
	case len(a) == 0:
		r.Problem = fmt.Sprintf("%s has no A record yet. At your DNS provider, add an A record for it holding %s.", domain, publicIPv4)
	default:
		for _, ip := range a {
			if ip != publicIPv4 {
				r.Problem = fmt.Sprintf("%s points at %s, not this server (%s). Change its A record to %s.", domain, strings.Join(a, ", "), publicIPv4, publicIPv4)
				break
			}
		}
		if r.Problem == "" {
			for _, ip := range aaaa {
				if !slices.Contains(localIPv6, ip) {
					r.Problem = fmt.Sprintf("%s also has an IPv6 (AAAA) record, %s, that isn't this server. Remove it or point it here: Let's Encrypt tries IPv6 first.", domain, ip)
					break
				}
			}
		}
	}
	r.OK = r.Problem == ""
	return r
}

// LocalIPv6 lists this machine's global IPv6 addresses.
func LocalIPv6() []string {
	var out []string
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() == nil && n.IP.IsGlobalUnicast() {
			out = append(out, n.IP.String())
		}
	}
	return out
}

// LocalIPv4 lists this machine's IPv4 addresses, to tell a VPS (public
// address on an interface) from a home server behind a router.
func LocalIPv4() []string {
	var out []string
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() {
			out = append(out, n.IP.String())
		}
	}
	return out
}
