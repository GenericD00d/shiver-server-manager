package netinfo

import (
	"context"
	"net"
	"strings"
	"testing"
)

func TestParseRoute(t *testing.T) {
	route := "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\n" +
		"enp3s0\t0001A8C0\t00000000\t0001\t0\t0\t100\t00FFFFFF\n" +
		"enp3s0\t00000000\t0101A8C0\t0003\t0\t0\t100\t00000000\n"
	gw, err := parseRoute([]byte(route))
	if err != nil || gw != "192.168.1.1" {
		t.Fatalf("%q %v", gw, err)
	}
	if _, err := parseRoute([]byte("Iface\tDestination\tGateway\n")); err == nil {
		t.Fatal("no default route accepted")
	}
}

// A router that only answers searches sent straight to it.
func TestSearchUnicast(t *testing.T) {
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	go func() {
		buf := make([]byte, 2048)
		n, from, err := pc.ReadFrom(buf)
		if err != nil || !strings.Contains(string(buf[:n]), "ST: urn:schemas-upnp-org:device:InternetGatewayDevice:1") {
			return
		}
		pc.WriteTo([]byte("HTTP/1.1 200 OK\r\nST: urn:schemas-upnp-org:device:InternetGatewayDevice:1\r\nLOCATION: http://192.168.1.1:5000/rootDesc.xml\r\n\r\n"), from)
	}()
	port := pc.LocalAddr().(*net.UDPAddr).Port
	loc, err := searchUnicastPort(context.Background(), "127.0.0.1", port, "urn:schemas-upnp-org:device:InternetGatewayDevice:1")
	if err != nil || loc.String() != "http://192.168.1.1:5000/rootDesc.xml" {
		t.Fatalf("%v %v", loc, err)
	}
}
