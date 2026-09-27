package game

import (
	"kungfu.local/server/internal/protocol"
	"testing"
	"time"
)

func TestPVELeaveAbortsWithoutControllerMigration(t *testing.T) {
	for _, mode := range []protocol.RoomType{protocol.StageAssault, protocol.FosterMode} {
		t.Run(mode.String(), func(t *testing.T) { testPVELeaveAbortsWithoutControllerMigration(t, mode) })
	}
}

func testPVELeaveAbortsWithoutControllerMigration(t *testing.T, mode protocol.RoomType) {
	for _, phase := range []string{"room", "loading", "wait_ready", "battle", "finishing", "settlement"} {
		for _, ownerLeaves := range []bool{true} {
			h, owner, peer, outsider := combatFixture()
			r := owner.Room
			r.Stage = phase
			r.Request[46] = byte(mode)
			r.StageWaves, _ = newStageWaves([]StageWavePlan{{Monsters: map[uint32]uint32{7: 1}}})
			r.PVEActors = map[uint64]pveActor{42: {active: true}}
			r.PVEBlocks = map[uint32]pveBlock{100: {sequence: 1, payload: []byte{1}}}
			r.FosterPlan = &protocol.FosterPlan{}
			r.FosterSpawned = []int{1}
			r.FosterTriggered = []bool{true}
			r.FosterRetired = []int{1}
			r.FosterFinishReported = true
			r.Reports = map[uint64][]byte{owner.UID: settlementReport(r)}
			r.LoadTimer = time.NewTimer(time.Hour)
			leaver, remaining := owner, peer
			if !ownerLeaves {
				leaver, remaining = peer, owner
			}
			h.leave(leaver, false) // No Store: abort must not depend on a DB snapshot.
			roomOutputs(t, leaver)
			roomOutputs(t, remaining, protocol.MsgRoomLeft, 20150)
			roomOutputs(t, outsider)
			if len(h.Rooms) != 0 || len(r.Members) != 0 || r.PVEActors != nil || r.PVEBlocks != nil || r.StageWaves != nil || r.FosterPlan != nil || r.FosterSpawned != nil || r.FosterTriggered != nil || r.FosterRetired != nil || r.FosterFinishReported || r.Reports != nil || r.LoadTimer != nil {
				t.Fatal("stage state survived departure", phase, ownerLeaves)
			}
			if remaining.Room != nil || remaining.game().Phase != "lobby" {
				t.Fatal("remaining client stranded")
			}
		}
	}
}

func TestPVEReportCannotUseCompetitiveRewards(t *testing.T) {
	for _, mode := range []protocol.RoomType{protocol.StageAssault, protocol.FosterMode} {
		h, owner, peer, _ := combatFixture()
		r := owner.Room
		r.Request[46] = byte(mode)
		p := settlementReport(r)
		for _, s := range []*Session{owner, peer} {
			if err := h.settleReport(s, p); err != nil {
				t.Fatal(err)
			}
		}
		roomOutputs(t, owner)
		roomOutputs(t, peer)
		if len(r.Reports) != 0 || r.Stage != "battle" {
			t.Fatal("PVE entered PvP settlement")
		}
	}
}

func TestPVELeaveAfterSettlementKeepsRemainingResult(t *testing.T) {
	for _, mode := range []protocol.RoomType{protocol.StageAssault, protocol.FosterMode} {
		t.Run(mode.String(), func(t *testing.T) { testPVELeaveAfterSettlementKeepsRemainingResult(t, mode) })
	}
}

func testPVELeaveAfterSettlementKeepsRemainingResult(t *testing.T, mode protocol.RoomType) {
	for _, ownerLeaves := range []bool{false} {
		h, owner, peer, outsider := combatFixture()
		r := owner.Room
		r.Request[46] = byte(mode)
		r.Stage = "settlement"
		for _, m := range r.Members {
			m.Ready = false
			m.Session.game().Phase = "settlement"
		}
		leaver, remaining := owner, peer
		if !ownerLeaves {
			leaver, remaining = peer, owner
		}
		h.leave(leaver, false)
		want := []uint32{protocol.MsgPlayerLeftRoom}
		if ownerLeaves {
			want = append(want, protocol.MsgRoomOwner)
		}
		roomOutputs(t, remaining, want...)
		roomOutputs(t, leaver)
		roomOutputs(t, outsider)
		if h.Rooms[r.ID] != r || len(r.Members) != 1 || r.Stage != "settlement" || remaining.Room != r || remaining.game().Phase != "settlement" || r.Owner != remaining.UID {
			t.Fatal("settled stage aborted on departure", ownerLeaves)
		}
		// The last departure still removes the room without database access.
		h.leave(remaining, false)
		if len(h.Rooms) != 0 {
			t.Fatal("empty result room retained")
		}
	}
}

func TestPVEPeerLeaveContinues(t *testing.T) {
	for _, mode := range []protocol.RoomType{protocol.FosterMode, protocol.StageAssault} {
		for _, phase := range []string{"loading", "wait_ready", "battle", "finishing"} {
			for _, ack := range []bool{false, true} {
				h, owner, peer, outsider := combatFixture()
				r := owner.Room
				r.Request[46] = byte(mode)
				r.Stage = phase
				owner.game().Phase = phase
				peer.game().Phase = phase
				r.FosterPlan = &protocol.FosterPlan{}
				r.PVEActors = map[uint64]pveActor{42: {active: true}}
				r.StageWaves, _ = newStageWaves([]StageWavePlan{{Monsters: map[uint32]uint32{7: 1}}})
				r.FosterSpawned = []int{1}
				r.FosterRetired = []int{0}
				report := settlementReport(r)
				h.leave(peer, ack)
				if ack {
					roomOutputs(t, peer, protocol.MsgRoomLeft)
				} else {
					roomOutputs(t, peer)
				}
				roomOutputs(t, owner, protocol.MsgPlayerLeftRoom)
				roomOutputs(t, outsider)
				if h.Rooms[r.ID] != r || r.Stage != phase || owner.Room != r || owner.game().Phase != phase || r.Owner != owner.UID || !r.hasPVEActor(42) || r.FosterPlan == nil || r.StageWaves == nil || r.FosterSpawned[0] != 1 {
					t.Fatal("peer departure destroyed stage", mode, phase)
				}
				hp, err := validateBattleReport(r, report)
				if err != nil || len(hp) != 1 {
					t.Fatal("departed player blocked report", err)
				}
				if _, ok := hp[peer.UID]; ok {
					t.Fatal("departed player included in result")
				}
			}
		}
	}
}

func TestPVEPeerLeaveReleasesInputBarrier(t *testing.T) {
	h, owner, peer, _ := combatFixture()
	r := owner.Room
	r.Request[46] = byte(protocol.FosterMode)
	r.Stage = "wait_ready"
	owner.game().Phase = "wait_ready"
	peer.game().Phase = "wait_ready"
	r.Members[owner.UID].Input = true
	h.leave(peer, false)
	roomOutputs(t, owner, protocol.MsgPlayerLeftRoom, protocol.MsgBattleStarted, 8090)
	if r.Stage != "battle" || owner.game().Phase != "battle" {
		t.Fatal("still waiting for departed player")
	}
}
