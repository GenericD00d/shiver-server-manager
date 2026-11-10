package netinfo

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// A firewall that drops new incoming traffic (ufw's default) also drops the
// router's answers to a multicast search: they come from the router's own
// address, which conntrack doesn't match to a packet sent to 239.255.255.250.
// Asking the default gateway directly makes the answer a reply the firewall
// already lets in.

// DefaultGateway reads the IPv4 default route from /proc/net/route.
func DefaultGateway() (string, error) {
	b, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return "", err
	}
	return parseRoute(b)
}

func parseRoute(b []byte) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 3 || f[1] != "00000000" || f[2] == "00000000" {
			continue
		}
		raw, err := hex.DecodeString(f[2])
		if err != nil || len(raw) != 4 {
			continue
		}
		ip := make(net.IP, 4)
		binary.LittleEndian.PutUint32(ip, binary.BigEndian.Uint32(raw))
		return ip.String(), nil
	}
	return "", errors.New("no IPv4 default route")
}

// searchUnicast sends an SSDP M-SEARCH for target to host:1900 and returns
// the device description URL from the first answer.
func searchUnicast(ctx context.Context, host, target string) (*url.URL, error) {
	return searchUnicastPort(ctx, host, 1900, target)
}

func searchUnicastPort(ctx context.Context, host string, port int, target string) (*url.URL, error) {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	conn, err := net.Dial("udp4", addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	deadline := time.Now().Add(3 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	conn.SetDeadline(deadline)
	req := "M-SEARCH * HTTP/1.1\r\nHOST: " + net.JoinHostPort(host, "1900") +
		"\r\nMAN: \"ssdp:discover\"\r\nMX: 2\r\nST: " + target + "\r\n\r\n"
	for i := 0; i < 2; i++ { // UDP: send twice
		if _, err := conn.Write([]byte(req)); err != nil {
			return nil, err
		}
	}
	buf := make([]byte, 4096)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return nil, err
		}
		if loc, err := parseSearchReply(buf[:n]); err == nil {
			return loc, nil
		}
	}
}

func parseSearchReply(b []byte) (*url.URL, error) {
	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(b)), nil)
	if err != nil {
		return nil, err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	return url.Parse(resp.Header.Get("Location"))
}
