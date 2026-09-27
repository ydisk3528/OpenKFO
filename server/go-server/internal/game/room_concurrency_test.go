package game

import (
	"sync"
	"testing"
	"time"

	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
	"kungfu.local/server/internal/tunnel"
)

func TestRoomLaneRevalidatesConcurrentDisconnect(t *testing.T) {
	for n := 0; n < 100; n++ {
		h, s, _, _ := combatFixture()
		r := s.Room
		m := combatPacket(protocol.BattleEventHealth, 94, s.UID, s.UID, 86)
		protocol.WriteUint64(m.Payload, 47, s.UID)
		p := mustEncodedMessage(t, m)
		done := make(chan error, 1)
		go func() { done <- h.Handle(s, tunnel.Frame{Op: "data", Channel: 1, Data: p}) }()
		// Membership changes use the registry writer. A frame can observe the
		// old pointer before this writer and must revalidate after its room wait.
		h.lockState()
		delete(h.Sessions, s.UID)
		delete(r.Members, s.UID)
		delete(h.Rooms, r.ID)
		s.Room = nil
		h.unlockState()
		select {
		case err := <-done:
			if err != nil && err != persistence.ErrDenied {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("disconnect deadlocked with battle")
		}
		if !r.combatMutex.TryLock() {
			t.Fatal("old room lock leaked")
		}
		r.combatMutex.Unlock()
	}
}

// Mix room readers, registry writers and a yielding database owner. In
// particular, a queued writer must not deadlock with a busy room waiter.
func TestRoomLanesWithStateWritersAndStorageWait(t *testing.T) {
	h, _, _, _ := combatFixture()
	var players []*Session
	for i := 0; i < 8; i++ {
		s := &Session{UID: uint64(2000 + i), GameChannel: 1, Channels: map[uint32]*Channel{1: {ID: 1, Kind: "game", Phase: "battle"}}, Output: make(chan tunnel.Frame, 1024), Done: make(chan struct{})}
		r := &Room{ID: uint16(10 + i), Serial: 7, Owner: s.UID, Stage: "battle", Request: make([]byte, 81), Members: map[uint64]*Member{s.UID: {Session: s}}}
		s.Room = r
		h.Rooms[r.ID], h.Sessions[s.UID] = r, s
		players = append(players, s)
	}
	var wg sync.WaitGroup
	errors := make(chan error, 16)
	for _, s := range players {
		for worker := 0; worker < 2; worker++ {
			wg.Add(1)
			go func(s *Session) {
				defer wg.Done()
				m := combatPacket(protocol.BattleEventHealth, 94, s.UID, s.UID, 86)
				protocol.WriteUint64(m.Payload, 47, s.UID)
				protocol.WriteUint32(m.Payload, 86, uint32(s.Room.ID))
				p, _ := protocol.Encode(m)
				for i := 0; i < 300; i++ {
					if err := h.Handle(s, tunnel.Frame{Op: "data", Channel: 1, Data: p}); err != nil {
						errors <- err
						return
					}
				}
			}(s)
		}
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			h.lockState()
			h.scopeRoom(players[0].Room)
			if i%4 == 0 {
				resume := h.yieldStorage()
				time.Sleep(100 * time.Microsecond)
				resume()
			}
			h.unlockState()
		}
	}()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("combat/state/storage lock cycle did not finish")
	}
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
}

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
