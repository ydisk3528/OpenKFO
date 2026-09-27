package game

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
	"kungfu.local/server/internal/tunnel"
	"sync"
	"testing"
	"time"
)

type delayedShopConnector struct{ entered, release chan struct{} }
type delayedShopConn struct {
	snapshotConn
	gate delayedShopConnector
}

func (c delayedShopConnector) Connect(context.Context) (driver.Conn, error) {
	return delayedShopConn{gate: c}, nil
}
func (c delayedShopConnector) Driver() driver.Driver { return delayedExpiryDriver{} }
func (c delayedShopConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	close(c.gate.entered)
	<-c.gate.release
	return nil, errors.New("test shop unavailable")
}

func TestShopHandlerStorageWaitAndFailureKeepOtherRoomResponsive(t *testing.T) {
	h, s, peer, shopper := combatFixture()
	shopper.game().Phase = "lobby"
	gate := delayedShopConnector{make(chan struct{}), make(chan struct{})}
	db := sql.OpenDB(gate)
	defer db.Close()
	h.Store = &persistence.Store{DB: db}
	var once sync.Once
	unblock := func() { once.Do(func() { close(gate.release) }) }
	defer unblock()
	raw, _ := protocol.Encode(protocol.Message{ID: 1540})
	done := make(chan error, 1)
	go func() { done <- h.Handle(shopper, tunnel.Frame{Op: "data", Channel: 1, Data: raw}) }()
	select {
	case <-gate.entered:
	case err := <-done:
		t.Fatalf("shop did not reach database: %v", err)
	case <-time.After(time.Second):
		t.Fatal("shop did not reach database")
	}
	m := combatPacket(protocol.BattleEventHealth, 94, s.UID, peer.UID, 86)
	protocol.WriteUint64(m.Payload, 47, s.UID)
	health, _ := protocol.Encode(m)
	combat := make(chan error, 1)
	go func() { combat <- h.Handle(s, tunnel.Frame{Op: "data", Channel: 1, Data: health}) }()
	select {
	case err := <-combat:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shop database blocked another room")
	}
	roomOutputs(t, peer, protocol.MsgBattleEvent)
	unblock()
	if err := <-done; err == nil {
		t.Fatal("database error lost")
	}
	if err := h.Handle(shopper, tunnel.Frame{Op: "ping"}); err != nil {
		t.Fatal("state gate remained locked after database error", err)
	}
}

func TestStorageWaitLetsOtherRoomCombatContinue(t *testing.T) {
	for _, kind := range []string{"shop", "settlement", "room-timer"} {
		t.Run(kind, func(t *testing.T) {
			h, s, peer, owner := combatFixture()
			if kind != "shop" {
				owner.Room = &Room{ID: 2, Stage: "finishing", Members: map[uint64]*Member{owner.UID: {Session: owner}}}
				h.Rooms[2] = owner.Room
			}
			entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			calls := 0
			go func() {
				h.lockState()
				if kind == "room-timer" {
					h.scopeRoom(owner.Room)
				} else {
					h.scopeSession(owner)
				}
				storage2_0(h, func() (int, error) { calls++; close(entered); <-release; return 1, nil })
				h.unlockState()
				close(done)
			}()
			<-entered
			// Ordinary state mutations and the owner's next request must wait.
			next := make(chan error, 1)
			go func() { next <- h.Handle(owner, tunnel.Frame{Op: "ping"}) }()
			select {
			case <-next:
				t.Fatal("owner interleaved with its transaction")
			case <-time.After(10 * time.Millisecond):
			}
			m := combatPacket(protocol.BattleEventHealth, 94, s.UID, peer.UID, 86)
			protocol.WriteUint64(m.Payload, 47, s.UID)
			raw, _ := protocol.Encode(m)
			combat := make(chan error, 1)
			go func() { combat <- h.Handle(s, tunnel.Frame{Op: "data", Channel: 1, Data: raw}) }()
			select {
			case e := <-combat:
				if e != nil {
					t.Fatal(e)
				}
			case <-time.After(time.Second):
				unblock()
				<-done
				t.Fatal("other room blocked by storage")
			}
			got := roomOutputs(t, peer, 8071)[0]
			if !bytes.Equal(got.Payload, m.Payload) {
				t.Fatal("combat modified")
			}
			unblock()
			<-done
			if e := <-next; e != nil {
				t.Fatal(e)
			}
			if calls != 1 {
				t.Fatal("transaction executed more than once")
			}
		})
	}
}
func TestStorageLaneRejectsMixedAndOwningRoomPackets(t *testing.T) {
	h, s, _, owner := combatFixture()
	h.ioScope = &storageScope{session: owner}
	h.ioPaused = true
	m := combatPacket(protocol.BattleEventHealth, 94, s.UID, s.UID, 86)
	raw, _ := protocol.Encode(m)
	f := tunnel.Frame{Op: "data", Channel: 1, Data: raw}
	if !h.storageFreeFrame(s, f) {
		t.Fatal("health blocked")
	}
	h.ioScope.room = s.Room
	if h.storageFreeFrame(s, f) {
		t.Fatal("owner room allowed")
	}
	h.ioScope.room = nil
	for _, id := range []uint32{8289, 8291, 8292, seriesFinish, protocol.BattleEventStageWaveEnd} {
		x := combatPacket(id, 94, s.UID, s.UID, 86)
		other, _ := protocol.Encode(x)
		f.Data = append(bytes.Clone(raw), other...)
		if h.storageFreeFrame(s, f) {
			t.Fatalf("storage event %d escaped gate", id)
		}
	}
	f.Data = raw[:len(raw)-1]
	if h.storageFreeFrame(s, f) {
		t.Fatal("partial TCP packet bypassed gate")
	}
}
