package game

import (
	"bytes"
	"testing"
	"time"

	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
)

func TestCooperativePVETeams(t *testing.T) {
	for _, mode := range []protocol.RoomType{protocol.FosterMode, protocol.StageAssault} {
		h, owner, peer, _ := waitingRoomFixture()
		r := owner.Room
		r.Request[46] = byte(mode)
		for slot := byte(0); slot < 8; slot++ {
			if r.initialTeam(slot) != 0 {
				t.Fatal("cooperative player assigned enemy team", mode, slot)
			}
		}
		m := r.Members[peer.UID]
		m.Ready = true
		spawn := m.Spawn
		h.normalizePVETeams(r)
		for _, s := range []*Session{owner, peer} {
			p := roomOutputs(t, s, 3250)[0].Payload
			if protocol.ReadUint64(p, 0) != peer.UID || p[8] != 0 || p[9] != spawn {
				t.Fatal("incorrect team correction", p)
			}
		}
		roomRequest(t, h, peer, 3230, []byte{1})
		roomOutputs(t, owner)
		p := roomOutputs(t, peer, 3250)[0].Payload
		if p[8] != 0 || m.Team != 0 || m.Spawn != spawn || !m.Ready {
			t.Fatal("PVE team request changed team, seat or readiness")
		}
		h.normalizePVETeams(r)
		roomOutputs(t, owner)
		roomOutputs(t, peer)
	}
	h, owner, peer, _ := waitingRoomFixture()
	if owner.Room.initialTeam(1) != 1 {
		t.Fatal("ordinary room assignment changed")
	}
	h.normalizePVETeams(owner.Room)
	if owner.Room.Members[peer.UID].Team != 1 {
		t.Fatal("ordinary room team changed")
	}
	roomOutputs(t, owner)
	roomOutputs(t, peer)
}

func TestPVEDeadlineBoundary(t *testing.T) {
	_, owner, _, _ := combatFixture()
	r := owner.Room
	r.Request[46] = byte(protocol.FosterMode)
	r.BattleStartedAt = time.Unix(1000, 0)
	protocol.WriteUint16(r.Request, 47, 999)
	deadline := r.BattleStartedAt.Add(999 * time.Second)
	if pveDeadlineReached(r, deadline.Add(-time.Nanosecond)) || !pveDeadlineReached(r, deadline) {
		t.Fatal("incorrect deadline boundary")
	}
	protocol.WriteUint16(r.Request, 47, 0)
	if pveDeadlineReached(r, deadline) {
		t.Fatal("unlimited room expired")
	}
	protocol.WriteUint16(r.Request, 47, 999)
	r.BattleStartedAt = time.Time{}
	if pveDeadlineReached(r, deadline) || pveDeadlineReached(nil, deadline) {
		t.Fatal("unstarted room expired")
	}
}

func TestPVETimedFinishAuthentication(t *testing.T) {
	for _, mode := range []protocol.RoomType{protocol.FosterMode, protocol.StageAssault} {
		_, owner, peer, _ := combatFixture()
		r := owner.Room
		r.Request[46] = byte(mode)
		protocol.WriteUint16(r.Request, 47, 999)
		if mode == protocol.FosterMode {
			r.FosterPlan = &protocol.FosterPlan{Groups: []protocol.FosterGroup{{Spawns: []protocol.FosterSpawn{{}}}}}
		} else {
			r.StageWaves, _ = newStageWaves([]StageWavePlan{{Monsters: map[uint32]uint32{7: 1}}})
		}
		p := settlementReport(r)
		for _, m := range r.Members {
			protocol.WriteUint16(p, int(m.Slot)*87+65, uint16(protocol.StageFinishCounterZero))
		}
		r.BattleStartedAt = time.Now()
		if accepted, err := recordStageFinish(owner, p); err != nil || accepted {
			t.Fatal("early counter-zero accepted", mode, accepted, err)
		}
		r.BattleStartedAt = time.Now().Add(-1040 * time.Second)
		if accepted, err := recordStageFinish(peer, p); err != nil || accepted {
			t.Fatal("peer authorized timeout", mode, accepted, err)
		}
		bad := bytes.Clone(p)
		protocol.WriteUint32(bad, 71, r.Serial+1)
		if accepted, err := recordStageFinish(owner, bad); err == nil || accepted {
			t.Fatal("stale timeout accepted", mode)
		}
		if r.Stage != "battle" || len(r.Reports) != 0 {
			t.Fatal("rejected report changed battle")
		}
		if outcome, err := validateStageFinish(r, p); err != nil || outcome != persistence.StageOutcomeFailed {
			t.Fatal("elapsed round did not fail", mode, outcome, err)
		}
		if accepted, err := recordStageFinish(owner, p); err != nil || !accepted || r.Stage != "finishing" {
			t.Fatal("controller timeout not recorded", mode, accepted, err)
		}
		p[0] ^= 1
		if bytes.Equal(r.Reports[owner.UID], p) {
			t.Fatal("saved timeout aliases caller buffer")
		}
	}
}
