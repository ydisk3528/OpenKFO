package game

import (
	"testing"
	"time"

	"kungfu.local/server/internal/protocol"
	"kungfu.local/server/internal/tunnel"
)

func TestBattleRoomsExecuteConcurrentlyAndSameRoomWaits(t *testing.T) {
	h, owner, _, other := combatFixture()
	r := &Room{ID: 2, Serial: 7, Stage: "battle", Request: make([]byte, 81), Members: map[uint64]*Member{}}
	r.Owner = other.UID
	r.Members[other.UID] = &Member{Session: other}
	other.Room = r
	h.Rooms[r.ID] = r
	makeFrame := func(s *Session) tunnel.Frame {
		m := combatPacket(protocol.BattleEventHealth, 94, s.UID, s.UID, 86)
		protocol.WriteUint64(m.Payload, 47, s.UID)
		protocol.WriteUint32(m.Payload, 86, uint32(s.Room.ID))
		p, _ := protocol.Encode(m)
		return tunnel.Frame{Op: "data", Channel: 1, Data: p}
	}
	first, second := makeFrame(owner), makeFrame(other)
	unlock := h.lockRoomFrame(owner, first)
	if unlock == nil {
		t.Fatal("battle did not acquire room lane")
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()
	same, different := make(chan error, 1), make(chan error, 1)
	go func() { same <- h.Handle(owner, first) }()
	go func() { different <- h.Handle(other, second) }()
	select {
	case err := <-different:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("one room's battle lock blocked another room")
	}
	select {
	case <-same:
		t.Fatal("same room executed concurrently")
	default:
	}
	unlock()
	unlock = nil
	select {
	case err := <-same:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("room lock not released")
	}
}

func TestRoomLaneRejectsMembershipChangesAndDatabasePackets(t *testing.T) {
	h, s, _, _ := combatFixture()
	for _, f := range []tunnel.Frame{
		{Op: "logout"}, {Op: "close", Channel: 1},
		{Op: "data", Channel: 1, Data: mustEncodedMessage(t, protocol.Message{ID: 1540})},
		{Op: "data", Channel: 1, Data: mustEncodedMessage(t, protocol.Message{ID: 4110, Payload: make([]byte, 696)})},
	} {
		if unlock := h.lockRoomFrame(s, f); unlock != nil {
			unlock()
			t.Fatal("state-changing frame entered combat lane", f.Op)
		}
	}
}

func mustEncodedMessage(t *testing.T, m protocol.Message) []byte {
	t.Helper()
	p, err := protocol.Encode(m)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
