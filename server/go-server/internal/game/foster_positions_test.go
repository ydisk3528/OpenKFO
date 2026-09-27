package game

import (
	"bytes"
	"kungfu.local/server/internal/protocol"
	"math"
	"testing"
)

func TestFosterPositionsLoadingBarrier(t *testing.T) {
	for _, ownerFirst := range []bool{false, true} {
		h, owner, peer, outsider := combatFixture()
		r := owner.Room
		r.Request[46] = byte(protocol.FosterMode)
		r.FosterPlan = &protocol.FosterPlan{Groups: []protocol.FosterGroup{{TriggerBox: [6]float32{-1, -1, -1, 1, 1, 1}}}}
		r.FosterTriggered = []bool{false}
		r.Stage = "loading"
		for _, m := range r.Members {
			m.Loaded = false
			m.Session.game().Phase = "loading"
		}
		p := make([]byte, 183)
		protocol.WriteUint32(p, 0, protocol.BattleEventFosterPositions)
		protocol.WriteUint64(p, 4, owner.UID)
		protocol.WriteUint64(p, 39, owner.UID)
		protocol.WriteUint64(p, 63, peer.UID)
		protocol.WriteUint32(p, 47, math.Float32bits(-1500))
		protocol.WriteUint32(p, 59, 4)
		send := func(s *Session, b []byte) error {
			return h.battleMessage(s, s.game(), protocol.Message{ID: 8071, Payload: b})
		}
		if e := send(peer, p); e != nil || r.FosterPositions != nil {
			t.Fatal("peer initialized players", e)
		}
		for _, failure := range []string{"short", "duplicate", "outsider", "missing", "empty slot", "nan", "sender"} {
			bad := bytes.Clone(p)
			switch failure {
			case "short":
				bad = bad[:182]
			case "duplicate":
				protocol.WriteUint64(bad, 63, owner.UID)
			case "outsider":
				protocol.WriteUint64(bad, 63, outsider.UID)
			case "missing":
				clear(bad[63:87])
			case "empty slot":
				bad[95] = 1
			case "nan":
				protocol.WriteUint32(bad, 47, math.Float32bits(float32(math.NaN())))
			case "sender":
				protocol.WriteUint64(bad, 4, peer.UID)
			}
			if e := send(owner, bad); e == nil || r.FosterPositions != nil || r.FosterTriggered[0] {
				t.Fatal("invalid snapshot stored", failure, e)
			}
		}
		if e := send(owner, p); e != nil {
			t.Fatal(e)
		}
		if !r.FosterTriggered[0] {
			t.Fatal("non-owner initial position did not activate group")
		}
		if e := send(owner, p); e != nil {
			t.Fatal("retry", e)
		}
		roomOutputs(t, owner)
		roomOutputs(t, peer)
		changed := bytes.Clone(p)
		changed[59] = 2
		if e := send(owner, changed); e == nil {
			t.Fatal("immutable snapshot replaced")
		}
		first, last := owner, peer
		if !ownerFirst {
			first, last = peer, owner
		}
		if e := h.route(first, first.game(), protocol.Message{ID: 4160}); e != nil {
			t.Fatal(e)
		}
		roomOutputs(t, owner, 4170)
		roomOutputs(t, peer, 4170)
		if e := h.route(last, last.game(), protocol.Message{ID: 4160}); e != nil {
			t.Fatal(e)
		}
		for _, s := range []*Session{owner, peer} {
			out := roomOutputs(t, s, 4170, 8071, 4180)
			if !bytes.Equal(out[1].Payload, p) {
				t.Fatal("initial positions changed")
			}
		}
		if e := send(owner, changed); e != nil {
			t.Fatal(e)
		}
		if e := h.route(last, last.game(), protocol.Message{ID: 4160}); e != nil {
			t.Fatal(e)
		}
		roomOutputs(t, owner)
		roomOutputs(t, peer)
		roomOutputs(t, outsider)
		if !bytes.Equal(r.FosterPositions, p) {
			t.Fatal("late reposition accepted")
		}
		h.abortStageRoom(r)
		if r.FosterPositions != nil || r.FosterTriggered != nil {
			t.Fatal("snapshot survived aborted battle")
		}
	}
}

func TestFosterInitialPositionsAfterPeerDeparture(t *testing.T) {
	h, owner, peer, _ := combatFixture()
	r := owner.Room
	r.Request[46] = byte(protocol.FosterMode)
	r.Stage = "loading"
	owner.game().Phase = "loading"
	peer.game().Phase = "loading"
	p := make([]byte, 183)
	protocol.WriteUint32(p, 0, protocol.BattleEventFosterPositions)
	protocol.WriteUint64(p, 4, owner.UID)
	protocol.WriteUint64(p, 39, owner.UID)
	protocol.WriteUint64(p, 63, peer.UID)
	h.leave(peer, false)
	roomOutputs(t, owner, protocol.MsgPlayerLeftRoom)
	if err := h.fosterPositions(owner, owner.game(), p); err != nil {
		t.Fatal("in-flight snapshot rejected", err)
	}
	if !bytes.Equal(r.FosterPositions, p) {
		t.Fatal("snapshot changed")
	}
	if err := h.route(owner, owner.game(), protocol.Message{ID: 4160}); err != nil {
		t.Fatal(err)
	}
	roomOutputs(t, owner, 4170, 8071, 4180)
	if r.Stage != "wait_ready" {
		t.Fatal("departed player blocks loading")
	}
}
