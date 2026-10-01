package game

import (
	"database/sql"
	"testing"

	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
	"kungfu.local/server/internal/tunnel"
)

func TestTaskDatabaseFailureKeepsSession(t *testing.T) {
	h, s, _, _ := waitingRoomFixture()
	db, err := sql.Open("mysql", "unused:unused@tcp(127.0.0.1:1)/unused")
	if err != nil {
		t.Fatal(err)
	}
	db.Close() // Deterministic storage failure; never connects to any database.
	h.Store = &persistence.Store{DB: db}
	packet, err := protocol.Encode(protocol.Message{ID: 6000, Payload: make([]byte, 4)})
	if err != nil {
		t.Fatal(err)
	}
	if err = h.Handle(s, tunnel.Frame{Op: "data", Channel: s.GameChannel, Data: packet}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Done:
		t.Fatal("task failure closed connection")
	default:
	}
	roomOutputs(t, s, 20150)
	if err = h.Handle(s, tunnel.Frame{Op: "ping"}); err != nil {
		t.Fatal(err)
	}
}
