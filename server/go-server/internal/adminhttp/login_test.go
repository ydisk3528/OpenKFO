package adminhttp

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"kungfu.local/server/internal/desktop"
)

func TestAccountLoginSessionAndAuthorization(t *testing.T) {
	calls := 0
	handler := WithLogin(New(strings.Repeat("x", 32), func(r desktop.Request) (any, error) { calls++; return map[string]bool{"saved": true}, nil }), strings.Repeat("x", 32), func(a, p string) bool { return a == "admin" && p == "correct" })
	request := func(path, body, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	body := `{"gm_version":"1.1.0","operation":"rewards_get","environment":"online"}`
	if request("/gm/api", body, "").Code != 401 {
		t.Fatal("anonymous API allowed")
	}
	if request("/gm/login", `{"account":"player","password":"correct"}`, "").Code != 401 {
		t.Fatal("game account allowed")
	}
	w := request("/gm/login", `{"account":"admin","password":"correct"}`, "")
	var result struct {
		Result struct {
			Token string `json:"token"`
		} `json:"result"`
	}
	json.Unmarshal(w.Body.Bytes(), &result)
	if w.Code != 200 || len(result.Result.Token) < 32 {
		t.Fatalf("login failed: %d", w.Code)
	}
	if request("/gm/api", body, result.Result.Token).Code != 200 || calls != 1 {
		t.Fatal("session authentication failed")
	}
	if request("/gm/api", `{"operation":"weapon_apply","environment":"online","gm_version":"1.1.0"}`, result.Result.Token).Code != 403 {
		t.Fatal("session bypassed operation whitelist")
	}
	if request("/gm/api", body, "invalid-session").Code != 401 {
		t.Fatal("invalid session accepted")
	}
	for i := 0; i < 10; i++ {
		request("/gm/login", `{"account":"admin","password":"wrong"}`, "")
	}
	if request("/gm/login", `{"account":"admin","password":"correct"}`, "").Code != 429 {
		t.Fatal("rate limit missing")
	}
}
