package game

import (
	"testing"
	"time"

	"kungfu.local/server/internal/protocol"
)

func TestPendingStageFinishRejectsChangedRoundOrController(t *testing.T) {
	for _, change := range []struct {
		name  string
		apply func(*Hub, *Room, *Session)
	}{
		{"next round", func(h *Hub, r *Room, s *Session) { r.Serial++ }},
		{"removed room", func(h *Hub, r *Room, s *Session) { delete(h.Rooms, r.ID) }},
		{"new owner", func(h *Hub, r *Room, s *Session) { r.Owner++ }},
		{"disconnected", func(h *Hub, r *Room, s *Session) { delete(h.Sessions, s.UID) }},
		{"left room", func(h *Hub, r *Room, s *Session) { delete(r.Members, s.UID) }},
		{"spectator", func(h *Hub, r *Room, s *Session) { r.Members[s.UID].Spectator = true }},
		{"already settled", func(h *Hub, r *Room, s *Session) { r.Stage = "settlement" }},
	} {
		t.Run(change.name, func(t *testing.T) {
			h, s, _, _ := combatFixture()
			r := s.Room
			p := &pendingStageFinish{owner: s, serial: r.Serial}
			r.pendingStageFinish = p
			if !h.pendingStageFinishCurrent(r, p) {
				t.Fatal("invalid fixture")
			}
			change.apply(h, r, s)
			if h.pendingStageFinishCurrent(r, p) {
				t.Fatal("stale report accepted")
			}
		})
	}
}

func TestPendingStageFinishExpiresWithoutGrantingIncompleteStage(t *testing.T) {
	h, s, peer, _ := combatFixture()
	r := s.Room
	r.Request[46] = byte(protocol.StageAssault)
	r.StageWaves, _ = newStageWaves([]StageWavePlan{{Monsters: map[uint32]uint32{7: 1}}})
	report := settlementReport(r)
	for _, m := range r.Members {
		protocol.WriteUint16(report, int(m.Slot)*87+65, 1)
	}
	p := &pendingStageFinish{owner: s, serial: r.Serial, report: report, deadline: time.Now()}
	r.pendingStageFinish = p
	h.scheduleStageFinishCheck(r, p)
	deadline := time.Now().Add(2 * time.Second)
	for {
		h.Mutex.RLock()
		finished := r.pendingStageFinish == nil
		stage := r.Stage
		h.Mutex.RUnlock()
		if finished {
			if stage != "battle" {
				t.Fatal("incomplete stage was settled")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("report did not expire")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// There is no Store in this fixture: a payout would panic. Only the owner
	// gets a diagnostic notice; neither player receives a result or reward.
	for _, m := range roomOutputs(t, s, 20150) {
		if m.ID == 4120 || m.ID == 1240 || m.ID == 4300 {
			t.Fatal("unverified reward", m.ID)
		}
	}
	roomOutputs(t, peer)
}
