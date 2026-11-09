//go:build linux

package cli

import (
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestE2ESelfSigned: milestone 6. A self-signed server runs beside the Let's
// Encrypt one; members can fetch its root over plain HTTP and it verifies
// the site; switching types works both ways.
func TestE2ESelfSigned(t *testing.T) {
	e := newE2E(t)
	const id, domain = "chat-b", "chat-b.example.com"
	var root []byte

	t.Run("create", func(t *testing.T) {
		e.asAdmin(t, 0, "", "instance", "create", "--id", id, "--domain", domain, "--tls", "selfsigned", "--no-encrypt")
	})

	t.Run("root download and trust", func(t *testing.T) {
		client := httpsClient(t, nil)
		resp, err := client.Get("http://" + domain + "/sharkord-root.crt")
		if err != nil {
			t.Fatal(err)
		}
		root, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/x-x509-ca-cert" {
			t.Fatalf("%s %s", resp.Status, resp.Header.Get("Content-Type"))
		}
		block, _ := pem.Decode(root)
		crt, err := x509.ParseCertificate(block.Bytes)
		if err != nil || crt.Subject.CommonName != "Shiver Server Manager Root" {
			t.Fatalf("root %v %v", crt.Subject, err)
		}
		// The site verifies against it, with a one-year certificate.
		resp, err = httpsClient(t, root).Get("https://" + domain + "/healthz")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		life := resp.TLS.PeerCertificates[0].NotAfter.Sub(resp.TLS.PeerCertificates[0].NotBefore)
		if life < 360*24*time.Hour {
			t.Fatalf("certificate lasts %s, want about a year", life)
		}
		// Other plain-HTTP requests redirect to HTTPS.
		resp, err = client.Get("http://" + domain + "/")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusPermanentRedirect && resp.StatusCode != http.StatusMovedPermanently {
			t.Fatalf("plain HTTP: %s", resp.Status)
		}
	})

	t.Run("cert root shows the fingerprint and saves the file", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "root.crt")
		got := e.stdout(t, "", "cert", "root", "--out", out)
		if !strings.Contains(got, "http://"+domain+"/sharkord-root.crt") || !strings.Contains(got, ":") {
			t.Fatalf("cert root:\n%s", got)
		}
		saved, _ := os.ReadFile(out)
		if string(saved) != string(root) {
			t.Fatal("saved root differs from the downloaded one")
		}
	})

	t.Run("both kinds side by side", func(t *testing.T) {
		for _, c := range []struct {
			domain string
			roots  []byte
		}{{"chat-a.example.com", pebbleRoots(t)}, {domain, root}} {
			resp, err := httpsClient(t, c.roots).Get("https://" + c.domain + "/healthz")
			if err != nil {
				t.Fatalf("%s: %v", c.domain, err)
			}
			resp.Body.Close()
		}
	})

	t.Run("switch to Let's Encrypt and back", func(t *testing.T) {
		e.asAdmin(t, 0, "", "cert", "switch", id, "--tls", "letsencrypt", "--yes")
		resp, err := httpsClient(t, pebbleRoots(t)).Get("https://" + domain + "/healthz")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if _, err := os.Stat("/var/lib/caddy/.local/share/caddy/certificates/local/" + domain); err == nil {
			t.Fatal("the self-signed certificate folder is still there")
		}
		e.asAdmin(t, 0, "", "cert", "switch", id, "--tls", "selfsigned", "--yes")
		resp, err = httpsClient(t, root).Get("https://" + domain + "/healthz")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	})
}
