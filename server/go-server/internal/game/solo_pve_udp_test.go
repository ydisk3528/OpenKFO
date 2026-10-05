package game

import (
	"math"
	"testing"
	"time"

	"kungfu.local/server/internal/protocol"
	"kungfu.local/server/internal/tunnel"
)

func TestSoloPVELifecycleThroughUDP(t *testing.T) {
	for _, scenario := range []string{"valid", "multiple-fighters", "non-owner", "spectator", "team", "wrong-port", "wrong-source", "expired", "fragment"} {
		t.Run(scenario, func(t *testing.T) {
			h, owner, peer, outsider := combatFixture()
			r := owner.Room
			delete(r.Members, peer.UID)
			peer.Room = nil
			r.Request[46] = byte(protocol.FosterMode)
			spawn := protocol.FosterSpawn{Template: 0}
			r.FosterPlan = &protocol.FosterPlan{InitialHP: []float32{8}, GlobalLimit: 1, Groups: []protocol.FosterGroup{{SubLimit: 1, GroupLimit: 1, Spawns: []protocol.FosterSpawn{spawn}}}}
			r.FosterSpawned = []int{0}
			r.FosterTriggered = []bool{true}
			r.FosterRetired = []int{0}
			owner.P2P, owner.UDPPort, owner.Bound = 2001, 40001, true
			owner.P2PUntil = time.Now().Add(time.Minute)
			wrap := func(m protocol.Message) tunnel.Frame {
				m.Payload[12], m.Payload[13] = 1, 1
				raw, err := protocol.Encode(m)
				if err != nil {
					t.Fatal(err)
				}
				packet := make([]byte, 24)
				protocol.WriteUint16(packet, 0, 1)
				protocol.WriteUint16(packet, 2, 1008)
				protocol.WriteUint32(packet, 4, owner.P2P)
				protocol.WriteUint32(packet, 12, owner.P2P)
				return tunnel.Frame{Op: "udp", Port: owner.UDPPort, Data: append(packet, raw...)}
			}
			create := wrap(fosterSpawnPacket(owner.UID, 0, 1, spawn))
			switch scenario {
			case "multiple-fighters":
				r.Members[peer.UID] = &Member{Session: peer, Slot: 1}
				peer.Room = r
			case "non-owner":
				r.Owner = peer.UID
			case "spectator":
				r.Members[owner.UID].Spectator = true
			case "team":
				r.Request[46] = 0
			case "wrong-port":
				create.Port++
			case "wrong-source":
				protocol.WriteUint32(create.Data, 12, 999)
			case "expired":
				owner.P2PUntil = time.Now().Add(-time.Minute)
			case "fragment":
				create.Data = create.Data[:len(create.Data)-1]
			}
			err := h.Handle(owner, create)
			if scenario != "valid" {
				if r.hasPVEActor(0) {
					t.Fatal("unauthorized or incomplete report registered actor")
				}
				roomOutputs(t, owner)
				roomOutputs(t, peer)
				roomOutputs(t, outsider)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !r.hasPVEActor(0) || r.PVEActors[0].reportedHP != 8 {
				t.Fatal("solo actor zero was not registered")
			}
			if err := h.Handle(owner, create); err != nil {
				t.Fatal(err)
			}
			if r.FosterSpawned[0] != 1 {
				t.Fatal("duplicate create consumed spawn twice")
			}
			roomOutputs(t, owner)
			roomOutputs(t, peer)
			roomOutputs(t, outsider)
			health := combatPacket(protocol.BattleEventHealth, 94, owner.UID, 0, 86)
			protocol.WriteUint64(health.Payload, 47, owner.UID)
			protocol.WriteUint32(health.Payload, 19, 2)
			protocol.WriteUint32(health.Payload, 67, math.Float32bits(2))
			raw, err := protocol.Encode(health)
			if err != nil {
				t.Fatal(err)
			}
			if err := h.Handle(owner, tunnel.Frame{Op: "data", Channel: owner.GameChannel, Data: raw}); err != nil {
				t.Fatal(err)
			}
			if r.PVEActors[0].reportedHP != 6 {
				t.Fatal("TCP health failed after UDP registration")
			}
			remove := combatPacket(protocol.BattleEventPVEActorRemove, 47, owner.UID, 0, 0)
			protocol.WriteUint32(remove.Payload, 19, 3)
			if err := h.Handle(owner, wrap(remove)); err != nil {
				t.Fatal(err)
			}
			if r.hasPVEActor(0) {
				t.Fatal("solo removal not registered")
			}
		})
	}
}
