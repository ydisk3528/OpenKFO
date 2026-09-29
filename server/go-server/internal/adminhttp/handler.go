package adminhttp

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"io"
	"kungfu.local/server/internal/desktop"
	"kungfu.local/server/internal/gmversion"
	"net/http"
	"strings"
	"sync"
)

// New exposes only online management operations; local client editing is never remote.
func New(token string, call func(desktop.Request) (any, error)) http.Handler {
	expected := sha256.Sum256([]byte(token))
	var mu sync.Mutex
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		fail := func(code int, msg string) {
			w.WriteHeader(code)
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": msg})
		}
		if r.URL.Path != "/gm/api" {
			fail(404, "接口不存在")
			return
		}
		if r.Method != "POST" {
			fail(405, "仅支持 POST")
			return
		}
		auth := r.Header.Get("Authorization")
		actual := sha256.Sum256([]byte(strings.TrimPrefix(auth, "Bearer ")))
		if len(token) < 32 || !strings.HasPrefix(auth, "Bearer ") || subtle.ConstantTimeCompare(actual[:], expected[:]) != 1 {
			fail(401, "管理凭据无效")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
		var req desktop.Request
		dec := json.NewDecoder(r.Body)
		if dec.Decode(&req) != nil {
			fail(400, "请求格式错误或过大")
			return
		}
		var extra any
		if dec.Decode(&extra) != io.EOF {
			fail(400, "请求格式错误")
			return
		}
		if req.Environment != "online" {
			fail(403, "此接口仅允许线上管理")
			return
		}
		if req.Operation == "gm_version" {
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": gmversion.Info()})
			return
		}
		switch req.Operation {
		case "grant_batch_create", "grant_batch_get", "grant_batch_list", "grant_batch_send", "grant_batch_send_many":
		case "login_errors_get", "login_errors_save", "treasure_get", "treasure_save", "treasure_preview", "users_list", "user_ban_save", "user_ban_history", "banned_words_get", "banned_words_save", "tasks_get", "tasks_save", "titles_get", "titles_save":
		case "stage_unlocks_get", "stage_unlocks_save", "vip_shop_settings_get", "vip_shop_settings_save", "talisman_settings_get", "talisman_settings_save", "weapon_settings_get", "weapon_settings_save", "training_get", "training_save", "stages_get", "stages_save", "honour_get", "honour_save", "vip_get", "vip_grant":
		case "definition_from_item", "definitions_get", "definition_save", "catalog", "accounts", "inventory", "inventory_expiry", "grant", "shop_catalog", "shop_images", "shop_image_status", "talisman_client_rules", "shop_save", "shop_batch", "shop_prices", "shop_rank", "wallet_accounts", "wallet_update", "rewards_get", "rewards_save":
		default:
			fail(403, "不允许此管理操作")
			return
		}
		if err := gmversion.Check(req.GMVersion); err != nil {
			fail(http.StatusConflict, err.Error())
			return
		}
		mu.Lock()
		result, err := call(req)
		mu.Unlock()
		if err != nil {
			fail(400, "操作失败："+err.Error())
			return
		}
		// Never expose server filesystem paths as client resources.
		if req.Operation == "catalog" {
			if data, ok := result.(map[string]any); ok {
				data["root"] = ""
				if items, ok := data["items"].([]desktop.Item); ok {
					for i := range items {
						items[i].Icon = ""
					}
					data["items"] = items
				}
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
	})
}
