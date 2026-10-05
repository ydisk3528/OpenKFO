//go:build windows

package bridge

import (
	"fmt"
	"io"
	"kungfu.local/server/internal/logqueue"
	"kungfu.local/server/internal/protocol"
	"os"
	"path/filepath"
	"time"
)

// One bounded filesystem worker for this bridge process. Receives and socket
// shutdown never wait for antivirus/disk I/O. Owners are accessed only by jobs.
type modFileWriter struct {
	queue      *logqueue.Queue
	owners     map[Identity]*remoteSession
	write      func(Identity, []byte)
	removeFile func(Identity)
}

func newModFileWriter(write func(Identity, []byte), remove func(Identity)) *modFileWriter {
	return &modFileWriter{queue: logqueue.New(io.Discard, 64), owners: map[Identity]*remoteSession{}, write: write, removeFile: remove}
}

var modFiles = newModFileWriter(writeModStatus, func(id Identity) {
	if path := modStatusPath(id); path != "" {
		_ = os.Remove(path)
	}
})

func (w *modFileWriter) submit(s *remoteSession, data []byte) {
	if len(data) != 216 || !s.modPending.CompareAndSwap(false, true) {
		return
	}
	copyData := append([]byte(nil), data...)
	created := time.Now()
	if !w.queue.Submit(func() {
		defer s.modPending.Store(false)
		select {
		case <-s.done:
			return
		default:
		}
		// Never give an old queued room snapshot a fresh timestamp.
		if time.Since(created) > 2*time.Second {
			return
		}
		w.owners[s.identity] = s
		w.write(s.identity, copyData)
	}) {
		s.modPending.Store(false)
	}
}

func (w *modFileWriter) remove(s *remoteSession) {
	w.queue.Submit(func() {
		// A previous login may finish closing after a new login of the same
		// native process. It must not delete the new session's status.
		if w.owners[s.identity] == s {
			w.removeFile(s.identity)
			delete(w.owners, s.identity)
		}
	})
}

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
