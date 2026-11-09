package netinfo

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/huin/goupnp/dcps/internetgateway2"
)

// Router is a home router's port-mapping service, whichever of the three
// common UPnP services it offers.
type Router interface {
	AddPortMappingCtx(ctx context.Context, remoteHost string, externalPort uint16, protocol string, internalPort uint16,
		internalClient string, enabled bool, description string, leaseDuration uint32) error
	DeletePortMappingCtx(ctx context.Context, remoteHost string, externalPort uint16, protocol string) error
	GetExternalIPAddressCtx(ctx context.Context) (string, error)
}

// UPnP finds the router and maps ports on it.
type UPnP struct {
	router Router
	host   string // the router's address, to find our LAN address
}

var ErrNoRouter = errors.New("no router answered UPnP; it may be switched off in the router's settings")

// FindRouter looks for WANIPConnection2, then WANIPConnection1, then
// WANPPPConnection1.
func FindRouter(ctx context.Context) (*UPnP, error) {
	if c, _, err := internetgateway2.NewWANIPConnection2ClientsCtx(ctx); err == nil && len(c) > 0 {
		return &UPnP{router: c[0], host: c[0].Location.Host}, nil
	}
	if c, _, err := internetgateway2.NewWANIPConnection1ClientsCtx(ctx); err == nil && len(c) > 0 {
		return &UPnP{router: c[0], host: c[0].Location.Host}, nil
	}
	if c, _, err := internetgateway2.NewWANPPPConnection1ClientsCtx(ctx); err == nil && len(c) > 0 {
		return &UPnP{router: c[0], host: c[0].Location.Host}, nil
	}
	return nil, ErrNoRouter
}

// NewUPnP wraps a router (for tests).
func NewUPnP(r Router, host string) *UPnP { return &UPnP{router: r, host: host} }

// ExternalIP is the address the router has on the internet side.
func (u *UPnP) ExternalIP(ctx context.Context) (string, error) {
	return u.router.GetExternalIPAddressCtx(ctx)
}

// NATProblem says why outside traffic can't reach this server, or "".
// A router WAN address in 100.64.0.0/10 is carrier-grade NAT; one that
// differs from the public address means a second router (double NAT).
func NATProblem(wanIP, publicIP string) string {
	ip := net.ParseIP(wanIP)
	switch {
	case ip == nil:
		return ""
	case IsCGNAT(ip):
		return fmt.Sprintf("your router's internet address (%s) is carrier-grade NAT: nothing outside can reach this server. Ask your internet provider for a public IPv4 address, or run the manager on a VPS", wanIP)
	case publicIP != "" && wanIP != publicIP:
		return fmt.Sprintf("your router's internet address (%s) isn't your public address (%s): there's another router or carrier NAT in between, so port forwarding on this router isn't enough", wanIP, publicIP)
	}
	return ""
}

// LANAddress finds this machine's address towards the router.
func (u *UPnP) LANAddress() (string, error) {
	host := u.host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	conn, err := net.Dial("udp4", net.JoinHostPort(host, "1900"))
	if err != nil {
		return "", err
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String(), nil
}

// Mapping is one forwarded port.
type Mapping struct {
	Port  int
	Proto string // "TCP" or "UDP"
	Label string // "shiver-server-manager <id>"
}

// LeaseFallback is used when a router refuses permanent mappings; the health
// check renews them.
const LeaseFallback = 86400

// Map forwards a port to this machine. It asks for a permanent mapping,
// then a day-long one if the router refuses. It returns the lease used.
func (u *UPnP) Map(ctx context.Context, m Mapping) (uint32, error) {
	lan, err := u.LANAddress()
	if err != nil {
		return 0, err
	}
	proto := strings.ToUpper(m.Proto)
	err = u.router.AddPortMappingCtx(ctx, "", uint16(m.Port), proto, uint16(m.Port), lan, true, m.Label, 0)
	if err == nil {
		return 0, nil
	}
	if err2 := u.router.AddPortMappingCtx(ctx, "", uint16(m.Port), proto, uint16(m.Port), lan, true, m.Label, LeaseFallback); err2 != nil {
		return 0, fmt.Errorf("the router refused to forward %d/%s: %v", m.Port, proto, err)
	}
	return LeaseFallback, nil
}

func (u *UPnP) Unmap(ctx context.Context, port int, proto string) error {
	return u.router.DeletePortMappingCtx(ctx, "", uint16(port), strings.ToUpper(proto))
}
