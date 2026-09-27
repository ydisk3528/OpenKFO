package game

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestReleaseRefreshUsesOSSAndRetainsLastGoodVersion(t *testing.T) {
	var requests atomic.Int32
	body := `{"version":"release-one","manifest":"https://example.com/launcher.json","client_manifest":"https://example.com/client.json"}`
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.Write([]byte(body)) }))
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
	if requests.Load() != 0 {
		t.Fatal("startup fetched release")
	}
	if e := h.checkLoginRelease(context.Background()); e != nil {
		t.Fatal(e)
	}
	if h.requiredRelease() != "release-one" {
		t.Fatal("did not use OSS release")
	}
	body = `{"version":"release-two","manifest":"https://example.com/launcher.json","client_manifest":"https://example.com/client.json"}`
	if e := h.checkLoginRelease(context.Background()); e != nil || h.requiredRelease() != "release-two" || requests.Load() != 2 {
		t.Fatal("next login did not refresh", e)
	}
	body = `{"version":"bad"}`
	if e := h.checkLoginRelease(context.Background()); e != nil {
		t.Fatal("last known version unavailable", e)
	}
	if h.requiredRelease() != "release-two" {
		t.Fatal("failed refresh replaced good version")
	}
	unknown := NewHub(nil, Config{ReleaseVersionURL: srv.URL})
	if e := unknown.checkLoginRelease(context.Background()); e == nil {
		t.Fatal("unverified version accepted without any fallback")
	}
	disabled := NewHub(nil, Config{})
	before := requests.Load()
	if e := disabled.checkLoginRelease(context.Background()); e != nil || requests.Load() != before {
		t.Fatal("unconfigured login contacted OSS")
	}
}
