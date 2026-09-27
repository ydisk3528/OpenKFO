package game

import (
	"bytes"
	"testing"

	"kungfu.local/server/internal/protocol"
)

func TestPVEPeerLoadAcknowledgement(t *testing.T) {
	for _, mode := range []protocol.RoomType{protocol.FosterMode, protocol.StageAssault} {
		for _, scenario := range []string{"valid", "unknown", "removed", "spoof", "header", "observer", "length", "owner", "not-battle"} {
			h, owner, peer, outsider := combatFixture()
			r := owner.Room
			r.Request[46] = byte(mode)
			r.PVEActors = map[uint64]pveActor{24284: {active: true, sequence: 100}}
			m := combatPacket(20406, 47, peer.UID, 24284, 0)
			m.Payload[12], m.Payload[13] = 1, 1
			sender := peer
			switch scenario {
			case "unknown":
				protocol.WriteUint64(m.Payload, 39, 24285)
			case "removed":
				r.PVEActors[24284] = pveActor{}
			case "spoof":
				protocol.WriteUint64(m.Payload, 4, owner.UID)
			case "header":
				m.Payload[13] = 0
			case "observer":
				r.Members[peer.UID].Spectator = true
			case "length":
				m.Payload = m.Payload[:46]
			case "owner":
				sender = owner
				protocol.WriteUint64(m.Payload, 4, owner.UID)
			case "not-battle":
				r.Stage = "room"
			}
			err := h.battleMessage(sender, sender.game(), m)
			if scenario == "valid" {
				if err != nil {
					t.Fatal(err)
				}
				out := roomOutputs(t, owner, 8071)
				if len(out) != 1 || !bytes.Equal(out[0].Payload, m.Payload) {
					t.Fatal("peer acknowledgement not delivered unchanged")
				}
				if err := h.battleMessage(sender, sender.game(), m); err != nil {
					t.Fatal(err)
				}
			}
			roomOutputs(t, owner)
			roomOutputs(t, peer)
			roomOutputs(t, outsider)
		}
	}
}
