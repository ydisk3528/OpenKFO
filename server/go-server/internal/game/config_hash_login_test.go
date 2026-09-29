package game

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"kungfu.local/server/internal/tunnel"
)

// A different resource revision must reach normal login admission. Saturating
// password workers keeps this transport regression independent of a database.
func TestConfigHashMismatchReachesLoginAdmission(t *testing.T) {
	cert, err := tunnel.Certificate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(NewHub(nil, Config{ConfigHash: strings.Repeat("a", 64), RequiredClientRelease: "new"}), cert)
	s.hashing <- struct{}{}
	s.hashing <- struct{}{}
	// Hold more than the former 80-connection ceiling; admission must still
	// complete TLS and return a protocol response, rather than dropping EOF.
	for i := 0; i < 80; i++ {
		s.connections <- struct{}{}
	}
	defer func() {
		for i := 0; i < 80; i++ {
			<-s.connections
		}
	}()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go s.ServeTLS(l)
	parsed, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(parsed)
	for _, version := range []string{"", "old", "new"} {
		hash := strings.Repeat("b", 64)
		// The same live server sends updated text while retaining the error code.
		message := "自定义繁忙提示 " + version
		messages := map[string]string{"busy": message}
		s.loginText.Store(&messages)
		c, err := tls.Dial("tcp", l.Addr().String(), &tls.Config{RootCAs: roots, ServerName: "kk-origin", MinVersion: tls.VersionTLS12})
		if err != nil {
			t.Fatal(err)
		}
		c.SetDeadline(time.Now().Add(5 * time.Second))
		if err := json.NewEncoder(c).Encode(tunnel.Frame{Op: "auth", ClientRelease: version, Account: "hashcheck", Password: strings.Repeat("0", 64), ConfigHash: hash}); err != nil {
			t.Fatal(err)
		}
		data, err := tunnel.ReadFrame(bufio.NewReader(c), 8192)
		c.Close()
		if err != nil {
			t.Fatal(err)
		}
		var reply tunnel.Frame
		if err := json.Unmarshal(data, &reply); err != nil {
			t.Fatal(err)
		}
		if reply.ErrorMessage != message {
			t.Fatalf("custom login text lost: %q", reply.ErrorMessage)
		}
		want := "busy"
		if reply.Error != want {
			t.Fatalf("hash %q: wanted normal admission busy, got %q", hash, reply.Error)
		}
	}
}
