package game

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReleaseRefreshUsesOSSAndRetainsLastGoodVersion(t *testing.T) {
	body := `{"version":"release-one","manifest":"https://example.com/launcher.json","client_manifest":"https://example.com/client.json"}`
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
	defer srv.Close()
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	previous := http.DefaultTransport
	http.DefaultTransport = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}}
	defer func() {
		http.DefaultTransport.(*http.Transport).CloseIdleConnections()
		http.DefaultTransport = previous
	}()
	h := NewHub(nil, Config{RequiredClientRelease: "compiled-old", ReleaseVersionURL: srv.URL})
	if e := h.RefreshRelease(context.Background()); e != nil {
		t.Fatal(e)
	}
	if h.requiredRelease() != "release-one" {
		t.Fatal("did not use OSS release")
	}
	body = `{"version":"bad"}`
	if e := h.RefreshRelease(context.Background()); e == nil {
		t.Fatal("accepted incomplete manifest")
	}
	if h.requiredRelease() != "release-one" {
		t.Fatal("failed refresh replaced good version")
	}
}
