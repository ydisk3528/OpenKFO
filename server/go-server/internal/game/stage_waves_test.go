package game

import (
	"kungfu.local/server/internal/protocol"
	"testing"
)

func TestStageWaveProgression(t *testing.T) {
	h, owner, peer, outsider := combatFixture()
	// Direct route calls need the same state guard as Hub.Handle now that
	// early reports schedule a background recheck.
	h.lockState()
	defer h.unlockState()
	r := owner.Room
	r.Request[46] = byte(protocol.StageAssault)
	plans := []StageWavePlan{{Monsters: map[uint32]uint32{7: 1}}, {Monsters: map[uint32]uint32{8: 1}}}
	var err error
	r.StageWaves, err = newStageWaves(plans)
	if err != nil {
		t.Fatal(err)
	}
	plans[0].Monsters[7] = 99 // Live config changes cannot alter an active battle.
	roomOutputs(t, owner)
	roomOutputs(t, peer) // Native starts wave one; no 20572/1.
	report := make([]byte, 40)
	protocol.WriteUint32(report, 0, uint32(r.ID))
	protocol.WriteUint32(report, 4, r.Serial)
	protocol.WriteUint32(report, 8, 1)
	protocol.WriteUint32(report, 12, 1)
	sendReport := func(s *Session) {
		t.Helper()
		if err := h.route(s, s.game(), protocol.Message{ID: protocol.MsgStageWaveReport, Payload: report}); err != nil {
			t.Fatal(err)
		}
	}
	sendReport(owner) // Empty room is not evidence that configured monsters spawned.
	roomOutputs(t, peer)
	for wave, template := range []uint32{7, 8} {
		create := combatPacket(protocol.BattleEventPVEActorCreate, 67, owner.UID, 42, 0)
		protocol.WriteUint32(create.Payload, 19, uint32(wave*2+1))
		protocol.WriteUint32(create.Payload, 47, template)
		if err := h.battleMessage(owner, owner.game(), create); err != nil {
			t.Fatal(err)
		}
		roomOutputs(t, peer, 8071)
		if err := h.battleMessage(owner, owner.game(), create); err != nil {
			t.Fatal(err)
		}
		roomOutputs(t, peer) // Repeated create cannot inflate the wave's count.
		for _, badTemplate := range []uint32{template, 999} {
			extra := combatPacket(protocol.BattleEventPVEActorCreate, 67, owner.UID, 43, 0)
			protocol.WriteUint32(extra.Payload, 47, badTemplate)
			if err := h.battleMessage(owner, owner.game(), extra); err != nil {
				t.Fatal(err)
			}
			roomOutputs(t, peer)
			if _, exists := r.PVEActors[43]; exists {
				t.Fatal("unplanned spawn registered")
			}
		}
		protocol.WriteUint32(report, 8, uint32(wave+1))
		sendReport(owner) // A living/uncleaned monster blocks advancement.
		roomOutputs(t, peer)
		remove := combatPacket(protocol.BattleEventPVEActorRemove, 47, owner.UID, 42, 0)
		protocol.WriteUint32(remove.Payload, 19, uint32(wave*2+2))
		if err := h.battleMessage(owner, owner.game(), remove); err != nil {
			t.Fatal(err)
		}
		roomOutputs(t, peer, 8071)
		sendReport(peer)
		roomOutputs(t, owner)
		protocol.WriteUint32(report, 4, r.Serial+1)
		sendReport(owner)
		roomOutputs(t, peer)
		protocol.WriteUint32(report, 4, r.Serial)
		sendReport(owner)
		want := int32(wave + 2)
		if wave == 1 {
			want = -1
		}
		for _, recipient := range []*Session{owner, peer} {
			out := roomOutputs(t, recipient, protocol.MsgStageWaveControl)[0]
			parsed, err := protocol.ParseStageWaveControl(out.Payload)
			if err != nil || parsed.Wave != want {
				t.Fatal("wrong next wave", parsed.Wave, err)
			}
		}
		sendReport(owner) // Old report must not respawn the next wave.
		roomOutputs(t, owner)
		roomOutputs(t, peer)
	}
	roomOutputs(t, outsider)
	if !r.StageWaves.finished || len(r.Reports) != 0 || r.Stage != "battle" {
		t.Fatal("scene end became a settlement")
	}
}

func TestStageWavePlanValidation(t *testing.T) {
	for _, plans := range [][]StageWavePlan{nil, {{}}, {{Monsters: map[uint32]uint32{7: 0}}}} {
		if _, err := newStageWaves(plans); err == nil {
			t.Fatal("empty plan accepted")
		}
	}
	w, err := newStageWaves([]StageWavePlan{{Monsters: map[uint32]uint32{7: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	if w.canSpawn(8) || !w.canSpawn(7) {
		t.Fatal("template not enforced")
	}
	w.spawned[7] = 1
	if w.canSpawn(7) {
		t.Fatal("wave quota exceeded")
	}
	w.finished = true
	if w.canSpawn(7) {
		t.Fatal("spawn after end")
	}
}
