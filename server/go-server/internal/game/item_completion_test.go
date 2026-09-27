package game

import (
	"bytes"
	"encoding/hex"
	"testing"

	"kungfu.local/server/internal/protocol"
)

// Captured from test001 (controller) while test002 was stuck in CDrinkAct,
// 2026-09-24 10:42:43.688. Only sender and room serial are adapted to the fixture.
func capturedItemCompletion(t *testing.T, owner *Session) protocol.Message {
	t.Helper()
	p, err := hex.DecodeString("542000009dc617baa00100000101c8420000000b00000019a800004200000000000000000000000700000001000000af000000")
	if err != nil {
		t.Fatal(err)
	}
	protocol.WriteUint64(p, 4, owner.UID)
	protocol.WriteUint32(p, 47, owner.Room.Serial)
	return protocol.Message{ID: 8071, Payload: p}
}

func TestItemCompletionCapturedPickup(t *testing.T) {
	h, owner, actor, outsider := combatFixture()
	for phase, sender := range []*Session{actor, owner, actor} {
		m := combatPacket(9000+uint32(phase), 63, sender.UID, actor.UID, 55)
		protocol.WriteUint32(m.Payload, 47, 7)
		protocol.WriteUint32(m.Payload, 51, 1)
		if err := h.battleMessage(sender, sender.game(), m); err != nil {
			t.Fatal(err)
		}
		recipient := owner
		if sender == owner {
			recipient = actor
		}
		roomOutputs(t, recipient, 8071)
		roomOutputs(t, sender)
	}
	m := capturedItemCompletion(t, owner)
	if err := h.battleMessage(owner, owner.game(), m); err != nil {
		t.Fatal(err)
	}
	got := roomOutputs(t, actor, 8071)[0]
	if !bytes.Equal(got.Payload, m.Payload) {
		t.Fatal("item completion payload changed")
	}
	roomOutputs(t, owner)
	roomOutputs(t, outsider)
	// Both an exact retry and a new sequence for the consumed completion drop.
	for _, seq := range []uint32{11, 12} {
		protocol.WriteUint32(m.Payload, 19, seq)
		if err := h.battleMessage(owner, owner.game(), m); err != nil {
			t.Fatal(err)
		}
		roomOutputs(t, actor)
	}
}

func TestItemCompletionValidation(t *testing.T) {
	for _, scenario := range []string{"peer", "sender", "short", "context", "object", "pending", "chest", "serial", "spectator", "new pickup"} {
		t.Run(scenario, func(t *testing.T) {
			h, owner, actor, outsider := combatFixture()
			r := owner.Room
			x := &reliableExchange{Object: 7, Completed: true}
			r.ReliableSerial = r.Serial
			r.Reliable = map[reliableActor]*reliableExchange{{actor.UID, 9000}: x}
			m := capturedItemCompletion(t, owner)
			sender := owner
			switch scenario {
			case "peer":
				sender = actor
				protocol.WriteUint64(m.Payload, 4, actor.UID)
			case "sender":
				protocol.WriteUint64(m.Payload, 4, outsider.UID)
			case "short":
				m.Payload = m.Payload[:50]
			case "context":
				protocol.WriteUint32(m.Payload, 43, 2)
			case "object":
				protocol.WriteUint32(m.Payload, 39, 8)
			case "pending":
				x.Completed, x.Pending, x.Approved = false, true, true
			case "chest":
				delete(r.Reliable, reliableActor{actor.UID, 9000})
				r.Reliable[reliableActor{actor.UID, 9500}] = x
			case "serial":
				r.ReliableSerial--
			case "spectator":
				r.Members[actor.UID].Spectator = true
			case "new pickup":
				req := combatPacket(9000, 63, actor.UID, actor.UID, 55)
				protocol.WriteUint32(req.Payload, 47, 8)
				protocol.WriteUint32(req.Payload, 51, 1)
				if err := h.battleMessage(actor, actor.game(), req); err != nil {
					t.Fatal(err)
				}
				roomOutputs(t, owner, 8071)
				if x.Completed {
					t.Fatal("new pickup retained old completion")
				}
			}
			_ = h.battleMessage(sender, sender.game(), m)
			roomOutputs(t, owner)
			roomOutputs(t, actor)
			roomOutputs(t, outsider)
			// A rejected packet must not consume the sequence or valid completion.
			r.Members[actor.UID].Spectator = false
			r.ReliableSerial = r.Serial
			r.Reliable = map[reliableActor]*reliableExchange{{actor.UID, 9000}: {Object: 7, Completed: true}}
			if err := h.battleMessage(owner, owner.game(), capturedItemCompletion(t, owner)); err != nil {
				t.Fatal(err)
			}
			roomOutputs(t, actor, 8071)
		})
	}
}

func TestItemCompletionControllerFastPath(t *testing.T) {
	h, owner, actor, _ := combatFixture()
	m := combatPacket(9002, 63, owner.UID, owner.UID, 55)
	protocol.WriteUint32(m.Payload, 47, 7)
	protocol.WriteUint32(m.Payload, 51, 1)
	if err := h.battleMessage(owner, owner.game(), m); err != nil {
		t.Fatal(err)
	}
	roomOutputs(t, actor, 8071)
	if err := h.battleMessage(owner, owner.game(), capturedItemCompletion(t, owner)); err != nil {
		t.Fatal(err)
	}
	roomOutputs(t, actor, 8071)
}
