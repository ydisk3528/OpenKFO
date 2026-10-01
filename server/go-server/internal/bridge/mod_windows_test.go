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
