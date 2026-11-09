package netinfo

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func serve(t *testing.T, body string) string {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintln(w, body) }))
	t.Cleanup(s.Close)
	return s.URL
}

func TestPublicIPv4NeedsTwoToAgree(t *testing.T) {
	a, b, c := serve(t, "203.0.113.10"), serve(t, "198.51.100.1"), serve(t, "203.0.113.10")
	ip, err := PublicIPv4(context.Background(), http.DefaultClient, []string{a, b, c})
	if err != nil || ip != "203.0.113.10" {
		t.Fatalf("%q %v", ip, err)
	}
	if _, err := PublicIPv4(context.Background(), http.DefaultClient, []string{a, b}); err == nil {
		t.Fatal("disagreement accepted")
	}
	junk := serve(t, "<html>")
	if _, err := PublicIPv4(context.Background(), http.DefaultClient, []string{a, junk, junk}); err == nil {
		t.Fatal("single answer accepted")
	}
}

func TestIsPublicIPv4(t *testing.T) {
	for ip, want := range map[string]bool{
		"203.0.113.10": true, "8.8.8.8": true, "10.0.0.1": false, "192.168.1.5": false,
		"100.64.0.1": false, "100.127.255.255": false, "100.128.0.1": true, "127.0.0.1": false, "::1": false,
	} {
		if IsPublicIPv4(ip) != want {
			t.Errorf("%s: want %v", ip, want)
		}
	}
	if !IsCGNAT(net.ParseIP("100.100.1.1")) {
		t.Fatal("CGNAT not spotted")
	}
}
