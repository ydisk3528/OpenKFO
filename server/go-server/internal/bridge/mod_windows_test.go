//go:build windows

package bridge

import (
	"kungfu.local/server/internal/protocol"
	"os"
	"testing"
	"time"
)

func TestModStatusFileBindsProcessAndRefreshes(t *testing.T) {
	t.Setenv("LocalAppData", t.TempDir())
	a := Identity{PID: 12, Created: 34}
	if modStatusPath(a) == modStatusPath(Identity{PID: 12, Created: 35}) {
		t.Fatal("PID reuse collision")
	}
	p := make([]byte, 216)
	protocol.WriteUint64(p, 8, 123)
	writeModStatus(a, p)
	got, err := os.ReadFile(modStatusPath(a))
	if err != nil || len(got) != 216 || protocol.ReadUint64(got, 8) != 123 {
		t.Fatal(err)
	}
	stamp := int64(protocol.ReadUint64(got, 0))
	if time.Now().UnixMilli()-stamp > 1000 {
		t.Fatal("stale local timestamp")
	}
	protocol.WriteUint64(p, 8, 456)
	writeModStatus(a, p)
	got, err = os.ReadFile(modStatusPath(a))
	if err != nil || protocol.ReadUint64(got, 8) != 456 {
		t.Fatal("status not replaced", err)
	}
}

func TestModDiskStallDoesNotBlockReceiveOrGrowQueue(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	w := newModFileWriter(func(Identity, []byte) { close(entered); <-release }, func(Identity) {})
	s := &remoteSession{done: make(chan struct{})}
	w.submit(s, make([]byte, 216))
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("worker not started")
	}
	returned := make(chan struct{})
	go func() {
		for i := 0; i < 10000; i++ {
			w.submit(s, make([]byte, 216))
		}
		w.remove(s)
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("disk blocked caller")
	}
	close(s.done)
	close(release)
	if !w.queue.Close(time.Second) {
		t.Fatal("worker did not close")
	}
}

func TestOldModSessionCleanupDoesNotDeleteNewLogin(t *testing.T) {
	removed := 0
	writes := 0
	w := newModFileWriter(func(Identity, []byte) { writes++ }, func(Identity) { removed++ })
	old := &remoteSession{done: make(chan struct{}), identity: Identity{PID: 123, Created: 456}}
	next := &remoteSession{done: make(chan struct{}), identity: old.identity}
	w.submit(old, make([]byte, 216))
	w.submit(next, make([]byte, 216))
	w.remove(old)
	if !w.queue.Close(time.Second) {
		t.Fatal("worker did not close")
	}
	if writes != 2 || removed != 0 {
		t.Fatal("new login status lost", writes, removed)
	}
}
