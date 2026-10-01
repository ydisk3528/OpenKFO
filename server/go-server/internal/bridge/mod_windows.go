//go:build windows

package bridge

import (
	"fmt"
	"kungfu.local/server/internal/protocol"
	"os"
	"path/filepath"
	"time"
)

func modToolAvailable() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	info, err := os.Stat(filepath.Join(filepath.Dir(exe), "GameMod.exe"))
	return err == nil && !info.IsDir()
}
func modStatusPath(identity Identity) string {
	root, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(root, "OpenKFO", "mod", fmt.Sprintf("%d-%d.bin", identity.PID, identity.Created))
}
func writeModStatus(identity Identity, data []byte) {
	if len(data) != 216 {
		return
	}
	path := modStatusPath(identity)
	if path == "" {
		return
	}
	if os.MkdirAll(filepath.Dir(path), 0700) != nil {
		return
	}
	// Freshness is measured using the player's clock, not server clock skew.
	protocol.WriteUint64(data, 0, uint64(time.Now().UnixMilli()))
	temp := path + ".tmp"
	if os.WriteFile(temp, data, 0600) == nil {
		_ = os.Rename(temp, path)
	}
}
