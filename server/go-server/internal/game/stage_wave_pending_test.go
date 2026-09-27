package game

import (
	"kungfu.local/server/internal/protocol"
	"testing"
	"time"
)

func TestStageWaveReportBeforeRemovalIsRetried(t *testing.T) {
	h, owner, peer, _ := combatFixture()
	r := owner.Room
	h.lockState()
	r.Request[46] = byte(protocol.StageAssault)
	r.StageWaves, _ = newStageWaves([]StageWavePlan{{Monsters: map[uint32]uint32{7: 1}}})
	report := make([]byte, 40)
	protocol.WriteUint32(report, 0, uint32(r.ID))
	protocol.WriteUint32(report, 4, r.Serial)
	protocol.WriteUint32(report, 8, 1)
	protocol.WriteUint32(report, 12, 1)
	if err := h.stageWaveReport(owner, owner.game(), report); err != nil {
		t.Fatal(err)
	}
	h.unlockState()
	defer func() { h.lockState(); delete(h.Rooms, r.ID); h.unlockState() }()
	// The report alone must not fabricate spawn receipts or finish the wave.
	time.Sleep(150 * time.Millisecond)
	h.lockState()
	if r.StageWaves.finished {
		t.Fatal("unverified wave advanced")
	}
	r.StageWaves.spawned[7] = 1
	h.unlockState()
	deadline := time.Now().Add(2 * time.Second)
	for {
		h.lockState()
		finished := r.StageWaves.finished
		h.unlockState()
		if finished {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("early report lost")
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, s := range []*Session{owner, peer} {
		roomOutputs(t, s, protocol.MsgStageWaveControl)
	}
	h.lockState()
	if err := h.stageWaveReport(owner, owner.game(), report); err != nil {
		t.Fatal(err)
	}
	h.unlockState()
	roomOutputs(t, owner)
	roomOutputs(t, peer)
}

func TestStageWaveRetryRejectsStaleRoom(t *testing.T) {
	for _, change := range []struct {
		name  string
		apply func(*Hub, *Room, *Session)
	}{
		{"next round", func(h *Hub, r *Room, s *Session) { r.Serial++ }},
		{"new owner", func(h *Hub, r *Room, s *Session) { r.Owner++ }},
		{"disconnect", func(h *Hub, r *Room, s *Session) { delete(h.Sessions, s.UID) }},
		{"removed room", func(h *Hub, r *Room, s *Session) { delete(h.Rooms, r.ID) }},
	} {
		t.Run(change.name, func(t *testing.T) {
			h, s, _, _ := combatFixture()
			r := s.Room
			h.lockState()
			r.Request[46] = byte(protocol.StageAssault)
			r.StageWaves, _ = newStageWaves([]StageWavePlan{{Monsters: map[uint32]uint32{7: 1}}})
			p := make([]byte, 40)
			protocol.WriteUint32(p, 0, uint32(r.ID))
			protocol.WriteUint32(p, 4, r.Serial)
			protocol.WriteUint32(p, 8, 1)
			protocol.WriteUint32(p, 12, 1)
			if err := h.stageWaveReport(s, s.game(), p); err != nil {
				t.Fatal(err)
			}
			r.StageWaves.spawned[7] = 1
			change.apply(h, r, s)
			h.unlockState()
			time.Sleep(200 * time.Millisecond)
			h.lockState()
			defer h.unlockState()
			if r.StageWaves.finished || r.StageWaves.index != 0 {
				t.Fatal("stale report advanced wave")
			}
			delete(h.Rooms, r.ID)
			roomOutputs(t, s)
		})
	}
}
