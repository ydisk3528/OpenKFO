package game

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const OldReleaseNotice = "当前游戏版本过旧。请使用新的登录器 更新到最新版本"

func (h *Hub) requiredRelease() string {
	if v := h.releaseVersion.Load(); v != nil {
		return v.(string)
	}
	return h.Config.RequiredClientRelease // compatibility for deployments not yet configured for OSS
}
func (h *Hub) outdatedRelease(s *Session) bool {
	v := h.requiredRelease()
	return v != "" && (s.ClientRelease != v || (h.Config.ConfigHash != "" && s.ClientConfigHash != h.Config.ConfigHash))
}

// Called while Hub.Mutex is held, after lobby UI can receive chat text.
func (h *Hub) warnOldRelease(s *Session) {
	v := h.requiredRelease()
	key := v + ":" + h.Config.ConfigHash
	if v != "" && s.UpdateNoticeVersion != key {
		if h.outdatedRelease(s) {
			s.sendGame(notice(OldReleaseNotice))
			if s.ClientRelease == v {
				s.sendGame(notice("版本号一致，但客户端配置文件不一致。请关闭游戏，用启动器检查更新；更新前不能开战。"))
			}
		} else {
			s.sendGame(notice("当前版本：" + v + "，配置校验通过。"))
		}
		s.UpdateNoticeVersion = key
	}
}
func (h *Hub) roomReleaseReady(r *Room) bool {
	ready := true
	for _, m := range r.Members {
		if !m.Spectator && h.outdatedRelease(m.Session) {
			m.Session.sendGame(notice(OldReleaseNotice))
			ready = false
		}
	}
	if !ready {
		if owner := r.Members[r.Owner]; owner != nil && !h.outdatedRelease(owner.Session) {
			owner.Session.sendGame(notice("房间内有玩家版本过旧，请更新后再开战。"))
		}
	}
	return ready
}
func (h *Hub) RefreshRelease(ctx context.Context) error {
	u, err := url.Parse(h.Config.ReleaseVersionURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return fmt.Errorf("OSS版本地址必须为HTTPS")
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Cache-Control", "no-cache")
	client := &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || len(via) > 4 {
			return fmt.Errorf("invalid version redirect")
		}
		return nil
	}}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("OSS版本HTTP %d", resp.StatusCode)
	}
	var m struct {
		Version        string `json:"version"`
		Manifest       string `json:"manifest"`
		ClientManifest string `json:"client_manifest"`
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 262145))
	if err != nil {
		return err
	}
	if len(b) > 262144 {
		return fmt.Errorf("version file too large")
	}
	if json.Unmarshal(b, &m) != nil || strings.TrimSpace(m.Version) == "" || len(m.Version) > 100 || m.Manifest == "" || m.ClientManifest == "" {
		return fmt.Errorf("OSS版本清单无效")
	}
	h.releaseVersion.Store(m.Version)
	return nil
}

// checkLoginRelease runs outside room/state locks, after credentials are verified.
// No periodic polling: retain the last known requirement on a temporary OSS failure.
func (h *Hub) checkLoginRelease(ctx context.Context) error {
	if h.Config.ReleaseVersionURL == "" {
		return nil
	}
	if err := h.RefreshRelease(ctx); err != nil {
		log.Printf("login_release_refresh_failed retaining_last_known_version: %v", err)
		if h.requiredRelease() == "" {
			return err
		}
	}
	return nil
}
