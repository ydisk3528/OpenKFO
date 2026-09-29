package game

import (
	"bytes"
	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
	"testing"
)

func TestExpiredItemsQueryUsesStateNotDuration(t *testing.T) {
	item := func(id, duration, state uint32) []byte {
		p := make([]byte, 68)
		protocol.WriteUint32(p, 0, id)
		protocol.WriteUint32(p, 13, duration)
		protocol.WriteUint32(p, 19, state)
		return p
	}
	inventory := [][]byte{item(1, 0, 1), item(2, 8760, 1), item(3, 0, 2), item(4, 8760, 0xffffffff)}
	reply, err := expiredItemsPacket(inventory)
	if err != nil || reply.ID != 2120 || !bytes.Equal(reply.Payload, append(protocol.Uint32Bytes(1), protocol.Uint32Bytes(3)...)) {
		t.Fatal("permanent/discarded item treated as expired", reply, err)
	}
	if _, err = expiredItemsPacket([][]byte{make([]byte, 67)}); err == nil {
		t.Fatal("truncated inventory accepted")
	}
}
func TestIndependentTaskListQueries(t *testing.T) {
	for _, selected := range []uint32{0, 6041, 6042} {
		_, s, _, _ := combatFixture()
		states := []persistence.ExtendedTaskState{
			{Key: 2001, State: 4, Snapshot: persistence.ExtendedTaskSnapshot{Rule: persistence.ExtendedTaskRule{Kind: "daily"}}},
			{Key: 3001, State: 4, Snapshot: persistence.ExtendedTaskSnapshot{Rule: persistence.ExtendedTaskRule{Kind: "newbie"}}},
		}
		if err := sendExtendedTaskLists(s, states, selected); err != nil {
			t.Fatal(err)
		}
		switch selected {
		case 0:
			roomOutputs(t, s, 6041, 6042, 6031, 6032)
		case 6041:
			roomOutputs(t, s, 6041, 6031)
			if _, ok := s.ExtendedTaskNotified[3001]; ok {
				t.Fatal("unrequested list marked notified")
			}
		case 6042:
			roomOutputs(t, s, 6042, 6032)
			if _, ok := s.ExtendedTaskNotified[2001]; ok {
				t.Fatal("unrequested list marked notified")
			}
		}
	}
}

func TestTreasurePurchasePriceQuery(t *testing.T) {
	p := make([]byte, 9)
	p[0] = 75
	r := make([]byte, 108)
	r[4] = 75
	protocol.WriteUint32(r, 5, 753001)
	if !matchesPurchasePriceQuery(p, r) {
		t.Fatal("native zero-ID treasure request lost all specs")
	}
	protocol.WriteUint32(p, 1, 753001)
	if !matchesPurchasePriceQuery(p, r) {
		t.Fatal("exact item lookup failed")
	}
	protocol.WriteUint32(p, 1, 753002)
	if matchesPurchasePriceQuery(p, r) {
		t.Fatal("wrong item accepted")
	}
	protocol.WriteUint32(p, 1, 0)
	protocol.WriteUint32(p, 5, 1)
	if matchesPurchasePriceQuery(p, r) {
		t.Fatal("renewal treated as purchase")
	}
	protocol.WriteUint32(p, 5, 0)
	r[4] = 76
	if matchesPurchasePriceQuery(p, r) {
		t.Fatal("other ticket kind leaked")
	}
	p[0] = 25
	r[4] = 25
	if matchesPurchasePriceQuery(p, r) {
		t.Fatal("weapon wildcard enabled")
	}
	if matchesPurchasePriceQuery(nil, r) || matchesPurchasePriceQuery(p, nil) {
		t.Fatal("truncated query accepted")
	}
}
