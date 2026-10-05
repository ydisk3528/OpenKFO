//go:build windows

package bridge

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
)

func TestEmbeddedCertificatesWithoutFiles(t *testing.T) {
	c := Config{EmbeddedCertificates: true}
	cert, err := c.certificateBytes("launcher-certificates/online/login.crt")
	if err != nil {
		t.Fatal(err)
	}
	key, err := c.certificateBytes("launcher-certificates/online/login.key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tls.X509KeyPair(cert, key); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"online", "realm2", "realm3"} {
		pin, err := c.certificateBytes("launcher-files/launcher-certificates/" + name + "/origin.crt")
		if err != nil || !x509.NewCertPool().AppendCertsFromPEM(pin) {
			t.Fatal("invalid embedded pin", name, err)
		}
	}
	f := filepath.Join(t.TempDir(), "other.crt")
	if err = os.WriteFile(f, cert, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = c.certificateBytes(f); err == nil {
		t.Fatal("embedded mode must not fall back to disk")
	}
	c.EmbeddedCertificates = false
	if _, err = c.certificateBytes(f); err != nil {
		t.Fatal("legacy file mode", err)
	}
}

func TestLoadEmbeddedCertificateIDs(t *testing.T) {
	f := filepath.Join(t.TempDir(), "bridge.json")
	err := os.WriteFile(f, []byte(`{"embedded_certificates":true,"server_certificate":"launcher-files/launcher-certificates/realm3/origin.crt","client_directory":"."}`), 0600)
	if err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(f)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(c.ClientDirectory) {
		t.Fatal("client directory must remain absolute")
	}
	if _, err = c.certificateBytes(c.ServerCertificate); err != nil {
		t.Fatal(err)
	}
}
