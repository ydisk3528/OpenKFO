//go:build clientarchive

package game

import (
	"kungfu.local/server/internal/desktop"
	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
	"os"
	"testing"
)

// Exercise actual imported counts through the game routes, not just a second
// calculation of expected totals. This is a server regression, not native UI QA.
func TestNativeStagePlanAllWaves(t *testing.T) {
	path := os.Getenv("OPENKFO_CLIENT_ARCHIVE")
	if path == "" {
		t.Skip("client archive required")
	}
	maps, err := desktop.ReadStageMaps(path)
	if err != nil {
		t.Fatal(err)
	}
	var preview *desktop.StageWavePreview
	for _, m := range maps {
		if m.MapID == 9170 {
			preview = m.WavePreview
		}
	}
	if preview == nil {
		t.Fatal("verified wave preview missing")
	}
	for _, variant := range preview.Variants {
		h, owner, peer, outsider := combatFixture()
		r := owner.Room
		r.Request[protocol.RoomTypeOffset] = byte(protocol.StageAssault)
		protocol.WriteUint32(r.Request, protocol.RoomMapOffset, 9170)
		r.StageWaves, err = newStageWaves(variant.Waves)
		if err != nil {
			t.Fatal(err)
		}
		sequence := uint32(0)
		for wave, plan := range variant.Waves {
			for template, count := range plan.Monsters {
				for n := uint32(0); n < count; n++ {
					for _, create := range []bool{true, false} {
						id, size := protocol.BattleEventPVEActorRemove, 47
						if create {
							id, size = protocol.BattleEventPVEActorCreate, 67
						}
						packet := combatPacket(id, size, owner.UID, 42, 0)
						sequence++
						protocol.WriteUint32(packet.Payload, 19, sequence)
						if create {
							protocol.WriteUint32(packet.Payload, 47, template)
						}
						if err := h.battleMessage(owner, owner.game(), packet); err != nil {
							t.Fatal(err)
						}
						roomOutputs(t, peer, 8071)
					}
				}
			}
			report := make([]byte, 40)
			protocol.WriteUint32(report, 0, uint32(r.ID))
			protocol.WriteUint32(report, 4, r.Serial)
			protocol.WriteUint32(report, 8, uint32(wave+1))
			protocol.WriteUint32(report, 12, 1)
			if err := h.route(owner, owner.game(), protocol.Message{ID: protocol.MsgStageWaveReport, Payload: report}); err != nil {
				t.Fatal(err)
			}
			want := int32(wave + 2)
			if wave == len(variant.Waves)-1 {
				want = -1
			}
			for _, recipient := range []*Session{owner, peer} {
				out := roomOutputs(t, recipient, protocol.MsgStageWaveControl)[0]
				control, err := protocol.ParseStageWaveControl(out.Payload)
				if err != nil || control.Wave != want {
					t.Fatalf("party %d wave %d: %+v %v", variant.MinPlayers, wave+1, control, err)
				}
			}
			if err := h.route(owner, owner.game(), protocol.Message{ID: protocol.MsgStageWaveReport, Payload: report}); err != nil {
				t.Fatal(err)
			}
			roomOutputs(t, owner)
			roomOutputs(t, peer)
			roomOutputs(t, outsider)
		}
		finish := settlementReport(r)
		for _, m := range r.Members {
			protocol.WriteUint16(finish, int(m.Slot)*87+65, 1)
		}
		if outcome, err := validateStageFinish(r, finish); err != nil || outcome != persistence.StageOutcomeClear {
			t.Fatal("completed native plan rejected", outcome, err)
		}
	}
}
