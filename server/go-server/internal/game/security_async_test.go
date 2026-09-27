package game

import (
	"fmt"
	"kungfu.local/server/internal/protocol"
	"kungfu.local/server/internal/tunnel"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecurityAuditSnapshotsBeforeBackgroundWrite(t *testing.T) {
	h := NewHub(nil, Config{})
	h.SecurityLogDirectory = t.TempDir()
	var pending func()
	h.SubmitSecurityAudit = func(job func()) bool { pending = job; return true }
	s := &Session{UID: 7, Account: "before", Room: &Room{ID: 3, Serial: 4}}
	c := &Channel{ID: 2}
	m := protocol.Message{ID: 8071, Payload: []byte{1, 2, 3, 4}}
	h.recordSecurityRejection(s, c, m, fmt.Errorf("test"))
	path := filepath.Join(h.SecurityLogDirectory, "before_offline.log")
	if _, e := os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("write occurred on caller")
	}
	s.Account = "after"
	s.UID = 9
	s.Room.ID = 8
	c.ID = 5
	m.Payload[0] = 255
	pending()
	b, e := os.ReadFile(path)
	if e != nil || !strings.Contains(string(b), `"account":"before"`) || !strings.Contains(string(b), `"room":3`) || !strings.Contains(string(b), `"payload_hex_prefix":"01020304"`) {
		t.Fatalf("snapshot lost: %s %v", b, e)
	}
}

func TestAuditOverflowDoesNotDropAllowedGameplay(t *testing.T) {
	h, s, peer, _ := combatFixture()
	h.SubmitSecurityAudit = func(func()) bool { return false }
	p := combatPacket(protocol.BattleEventBuff, 87, s.UID, peer.UID, 79)
	protocol.WriteUint64(p.Payload, 47, peer.UID)
	protocol.WriteUint32(p.Payload, 55, 186)
	protocol.WriteUint32(p.Payload, 59, 3)
	protocol.WriteUint32(p.Payload, 63, 2000)
	protocol.WriteUint32(p.Payload, 75, 1)
	raw, _ := protocol.Encode(p)
	if e := h.Handle(s, tunnel.Frame{Op: "data", Channel: 1, Data: raw}); e != nil {
		t.Fatal(e)
	}
	if len(peer.Output) != 1 {
		t.Fatal("audit congestion changed forwarding")
	}
}
