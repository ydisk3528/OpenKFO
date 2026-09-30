package adminhttp

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Login issues short-lived opaque sessions. Password verification stays on the server.
// The legacy API token remains internal and is never sent to account-login clients.
func WithLogin(api http.Handler, internalToken string, verify func(string, string) bool) http.Handler {
	var mu sync.Mutex
	sessions := map[string]time.Time{}
	type attempts struct {
		Count int
		Until time.Time
	}
	failures := map[string]attempts{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		fail := func(status int, text string) {
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": text})
		}
		if r.URL.Path == "/gm/login" {
			if r.Method != http.MethodPost {
				fail(405, "仅支持 POST")
				return
			}
			// The API only listens on loopback; nginx replaces this header rather than trusting client input.
			peer, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				peer = r.RemoteAddr
			}
			if ip := net.ParseIP(peer); ip != nil && ip.IsLoopback() {
				if real := net.ParseIP(r.Header.Get("X-Real-IP")); real != nil {
					peer = real.String()
				}
			}
			now := time.Now()
			mu.Lock()
			for key, v := range failures {
				if !now.Before(v.Until) {
					delete(failures, key)
				}
			}
			a := failures[peer]
			blocked := a.Count >= 10 || len(failures) >= 1024 && a.Count == 0
			if !blocked {
				if a.Count == 0 {
					a.Until = now.Add(15 * time.Minute)
				}
				a.Count++
				failures[peer] = a
			}
			mu.Unlock()
			if blocked {
				fail(429, "登录尝试过多，请稍后再试")
				return
			}
			var credentials struct {
				Account  string `json:"account"`
				Password string `json:"password"`
			}
			r.Body = http.MaxBytesReader(w, r.Body, 4096)
			decoder := json.NewDecoder(r.Body)
			if decoder.Decode(&credentials) != nil || decoder.Decode(&struct{}{}) != io.EOF || len(credentials.Account) > 128 || len(credentials.Password) > 128 {
				fail(400, "登录请求格式错误")
				return
			}
			if !verify(credentials.Account, credentials.Password) {
				fail(401, "GM 账号或密码错误，或未获得管理权限")
				return
			}
			secret := make([]byte, 32)
			if _, err := rand.Read(secret); err != nil {
				fail(503, "登录服务暂不可用")
				return
			}
			token := hex.EncodeToString(secret)
			expires := now.Add(8 * time.Hour)
			mu.Lock()
			for key, v := range sessions {
				if !now.Before(v) {
					delete(sessions, key)
				}
			}
			if len(sessions) >= 128 {
				mu.Unlock()
				fail(429, "管理会话数量已达上限，请稍后再试")
				return
			}
			sessions[token] = expires
			delete(failures, peer)
			mu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"token": token, "expires_at": expires.Unix(), "account": credentials.Account}})
			return
		}
		auth := r.Header.Get("Authorization")
		if strings.HasPrefix(auth, "Bearer ") {
			mu.Lock()
			expires, exists := sessions[strings.TrimPrefix(auth, "Bearer ")]
			mu.Unlock()
			if exists && time.Now().Before(expires) {
				r = r.Clone(r.Context())
				r.Header = r.Header.Clone()
				r.Header.Set("Authorization", "Bearer "+internalToken)
			}
		}
		api.ServeHTTP(w, r)
	})
}
