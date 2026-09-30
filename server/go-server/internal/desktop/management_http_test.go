package desktop

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHTTPLoginDoesNotPersistPasswordOrSession(t *testing.T) {
	secret := strings.Repeat("session-secret", 4)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/gm/login":
			var input map[string]string
			json.NewDecoder(r.Body).Decode(&input)
			if input["account"] != "admin" || input["password"] != "test-password" {
				t.Error("login credentials not transmitted")
			}
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"token": secret}})
		case "/gm/api":
			var input Request
			json.NewDecoder(r.Body).Decode(&input)
			if r.Header.Get("Authorization") != "Bearer "+secret || input.Environment != "online" || input.ManagementSessionToken != "" {
				t.Error("API authentication or scope incorrect")
			}
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": []any{}})
		default:
			t.Error("unexpected endpoint")
		}
	}))
	defer server.Close()
	oldTransport := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = oldTransport }()
	dir := t.TempDir()
	settings := filepath.Join(dir, "gm-settings.json")
	os.WriteFile(settings, []byte(`{"root":"keep"}`), 0600)
	admin := New(dir)
	admin.GMSettings = settings
	endpoint := server.URL + "/gm/api"
	_, err := admin.Call(Request{Operation: "management_connection_login", Connection: &connection{Endpoint: endpoint}, LoginAccount: "admin", LoginPassword: "test-password"})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(settings)
	if strings.Contains(string(data), secret) || strings.Contains(string(data), "test-password") || !strings.Contains(string(data), "keep") {
		t.Fatal("secret persisted or settings lost")
	}
	if _, err = admin.Call(Request{Operation: "accounts", Environment: "online"}); err == nil {
		t.Fatal("API allowed without memory session")
	}
	if _, err = admin.Call(Request{Operation: "accounts", Environment: "online", ManagementSessionToken: secret}); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"weapon_merge_apply", "client_config_catalog", "catalog", "shop_images"} {
		if !localManagementOperation(op) {
			t.Fatal("local editing routed remotely", op)
		}
	}
}
