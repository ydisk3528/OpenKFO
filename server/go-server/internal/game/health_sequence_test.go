package game

import (
	"math"
	"testing"

	"kungfu.local/server/internal/protocol"
)

func TestHealthSequenceFirstReceiptWins(t *testing.T) {
	h, sender, peer, _ := combatFixture()
	sender.Room.Request[protocol.RoomTypeOffset] = byte(protocol.TeamSurvival)
	for _, step := range []struct {
		sequence uint32
		amount   float32
		forward  bool
	}{
		{math.MaxUint32, 2, true},
		{math.MaxUint32, 3, false},   // Same identity, different damage.
		{math.MaxUint32, -10, false}, // Cannot turn an accepted hit into healing.
		{0, 2, true},                 // Native counter wraps.
		{math.MaxUint32, 8, false},
		{1, 2, true}, // Equal amount with a new identity remains a new hit.
		{3, 2, true},
		{2, -1, true}, // Previously unseen healing arrived after a newer hit.
		{2, -1, false},
		{2, 10, false}, // Changed payload is still the same receipt.
		{4, 2, true},
	} {
		m := combatPacket(protocol.BattleEventHealth, 94, sender.UID, peer.UID, 86)
		protocol.WriteUint64(m.Payload, 47, sender.UID)
		protocol.WriteUint32(m.Payload, 19, step.sequence)
		protocol.WriteUint32(m.Payload, 67, math.Float32bits(step.amount))
		if err := h.battleMessage(sender, sender.game(), m); err != nil {
			t.Fatal(err)
		}
		if step.forward {
			roomOutputs(t, peer, 8071)
		} else {
			roomOutputs(t, peer)
		}
	}
	// The accepted identity belongs to this sender and target. Independent
	// actors/senders must not suppress one another's legitimate health events.
	for _, s := range []*Session{sender, peer} {
		m := combatPacket(protocol.BattleEventHealth, 94, s.UID, sender.UID, 86)
		protocol.WriteUint64(m.Payload, 47, s.UID)
		protocol.WriteUint32(m.Payload, 67, math.Float32bits(2))
		if err := h.battleMessage(s, s.game(), m); err != nil {
			t.Fatal(err)
		}
		if s == sender {
			roomOutputs(t, peer, 8071)
		} else {
			roomOutputs(t, sender, 8071)
		}
	}
}

func TestHealthReorderingLimitedToCompetitiveModes(t *testing.T) {
	for _, mode := range []protocol.RoomType{protocol.TeamSurvival, protocol.TeamDeathmatch, protocol.SoloSurvival, protocol.SoloDeathmatch, protocol.FreePractice, protocol.StageAssault, protocol.FosterMode} {
		t.Run(mode.String(), func(t *testing.T) {
			h, sender, peer, _ := combatFixture()
			sender.Room.Request[protocol.RoomTypeOffset] = byte(mode)
			for _, sequence := range []uint32{10, 12, 11, 11} {
				m := combatPacket(protocol.BattleEventHealth, 94, sender.UID, peer.UID, 86)
				protocol.WriteUint64(m.Payload, 47, sender.UID)
				protocol.WriteUint32(m.Payload, 19, sequence)
				protocol.WriteUint32(m.Payload, 67, math.Float32bits(2))
				if err := h.battleMessage(sender, sender.game(), m); err != nil {
					t.Fatal(err)
				}
			}
			want := 2
			if mode.IsCompetitive() {
				want = 3
			}
			if len(peer.Output) != want {
				t.Fatalf("forwarded %d want %d", len(peer.Output), want)
			}
			for len(peer.Output) > 0 {
				<-peer.Output
			}
			for _, sequence := range []uint32{12, 11} {
				m := combatPacket(protocol.BattleEventMovement, 108, sender.UID, sender.UID, 0)
				protocol.WriteUint32(m.Payload, 15, sequence)
				if err := h.battleMessage(sender, sender.game(), m); err != nil {
					t.Fatal(err)
				}
			}
			roomOutputs(t, peer, 8071)
		})
	}
}
