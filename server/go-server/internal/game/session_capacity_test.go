package game

import (
	"errors"
	"kungfu.local/server/internal/persistence"
	"testing"
)

func TestSessionCapacityAdmission(t *testing.T) {
	h := NewHub(nil, Config{})
	for id := uint64(1); id <= 65; id++ {
		if _, err := h.Attach(persistence.Account{UID: id}, 18001); err != nil {
			t.Fatalf("login %d: %v", id, err)
		}
	}
	if _, err := h.Attach(persistence.Account{UID: 1}, 18001); !errors.Is(err, errAccountOnline) {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err := h.Attach(persistence.Account{UID: 66}, 0); !errors.Is(err, errClientPort) {
		t.Fatalf("port: %v", err)
	}
	h.Config.MaxSessions = 65
	if _, err := h.Attach(persistence.Account{UID: 66}, 18001); !errors.Is(err, errServerFull) {
		t.Fatalf("capacity: %v", err)
	}
	if len(h.Sessions) != 65 {
		t.Fatal("rejection changed sessions")
	}
}
