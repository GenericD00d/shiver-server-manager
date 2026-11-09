// Package netinfo finds the server's public address, checks DNS, spots
// carrier-grade NAT, and maps ports on home routers.
package netinfo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// IPv4Sources are asked in parallel; an answer counts once two agree.
var IPv4Sources = []string{
	"https://ipv4.icanhazip.com",
	"https://api.ipify.org",
	"https://ifconfig.me/ip",
}

// IPv4Client dials over IPv4 only, with a 3-second limit per source.
func IPv4Client() *http.Client {
	d := &net.Dialer{Timeout: 3 * time.Second}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = func(ctx context.Context, _, addr string) (net.Conn, error) {
		return d.DialContext(ctx, "tcp4", addr)
	}
	return &http.Client{Transport: tr, Timeout: 3 * time.Second}
}

// PublicIPv4 asks every source and returns an address two of them agree on.
func PublicIPv4(ctx context.Context, client *http.Client, sources []string) (string, error) {
	if client == nil {
		client = IPv4Client()
	}
	type answer struct {
		ip  string
		err error
	}
	ch := make(chan answer, len(sources))
	for _, src := range sources {
		go func(src string) {
			ip, err := askIPv4(ctx, client, src)
			ch <- answer{ip, err}
		}(src)
	}
	counts := map[string]int{}
	var errs []error
	for range sources {
		a := <-ch
		if a.err != nil {
			errs = append(errs, a.err)
			continue
		}
		counts[a.ip]++
		if counts[a.ip] >= 2 {
			return a.ip, nil
		}
	}
	if len(counts) > 0 {
		return "", fmt.Errorf("the address lookups disagree (%v); set the public address by hand", counts)
	}
	return "", fmt.Errorf("couldn't find the public IPv4 address: %w", errors.Join(errs...))
}

func askIPv4(ctx context.Context, client *http.Client, src string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "shiver-server-manager")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
	ip := net.ParseIP(strings.TrimSpace(string(b))).To4()
	if resp.StatusCode != http.StatusOK || ip == nil {
		return "", fmt.Errorf("%s gave no IPv4 address", src)
	}
	return ip.String(), nil
}

// IsPublicIPv4 reports whether ip is a usable public unicast IPv4 address.
func IsPublicIPv4(s string) bool {
	ip := net.ParseIP(s).To4()
	if ip == nil || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	return !IsCGNAT(ip)
}

var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// IsCGNAT reports whether ip is in 100.64.0.0/10, carrier-grade NAT space.
func IsCGNAT(ip net.IP) bool { return cgnat.Contains(ip) }
