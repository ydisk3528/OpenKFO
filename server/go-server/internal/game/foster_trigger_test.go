package game

import (
	"math"
	"testing"

	"kungfu.local/server/internal/protocol"
)

func TestFosterTriggerMovement(t *testing.T) {
	for _, scenario := range []string{"inside", "min", "max", "outside-x", "outside-y", "outside-z", "nan", "stale", "monster", "spoof", "not-battle"} {
		t.Run(scenario, func(t *testing.T) {
			h, owner, peer, _ := combatFixture()
			r := owner.Room
			r.Request[46] = byte(protocol.FosterMode)
			spawn := protocol.FosterSpawn{Template: 0}
			r.FosterPlan = &protocol.FosterPlan{GlobalLimit: 10, Groups: []protocol.FosterGroup{{SubLimit: 2, GroupLimit: 2, Spawns: []protocol.FosterSpawn{spawn}, TriggerBox: [6]float32{-1, -2, -3, 1, 2, 3}}}}
			r.FosterSpawned, r.FosterTriggered = []int{0}, []bool{false}
			if r.fosterSpawnGroup(protocol.PVEActorCreate{}) != 0 {
				t.Fatal("valid untimed spawn requires a movement receipt")
			}
			position := [3]float32{}
			sender, actor := peer, peer.UID
			switch scenario {
			case "min":
				position = [3]float32{-1, -2, -3}
			case "max":
				position = [3]float32{1, 2, 3}
			case "outside-x":
				position[0] = 2
			case "outside-y":
				position[1] = 3
			case "outside-z":
				position[2] = 4
			case "nan":
				position[0] = float32(math.NaN())
			case "stale":
				peer.Room.Members[peer.UID].BattleEvents = map[battleEventKey]battleSequence{{Kind: protocol.BattleEventMovement, Actor: actor}: {Sequence: 2}}
			case "monster":
				sender, actor = owner, 42
				r.PVEActors = map[uint64]pveActor{42: {active: true, fosterGroup: 0}}
			case "spoof":
				actor = owner.UID
			case "not-battle":
				r.Stage = "loading"
			}
			m := combatPacket(protocol.BattleEventMovement, 108, actor, 0, 0)
			protocol.WriteUint32(m.Payload, 15, 1)
			for axis, value := range position {
				protocol.WriteUint32(m.Payload, 51+axis*4, math.Float32bits(value))
			}
			err := h.battleMessage(sender, sender.game(), m)
			if (err != nil) != (scenario == "nan" || scenario == "spoof") {
				t.Fatal("unexpected route error", err)
			}
			want := scenario == "inside" || scenario == "min" || scenario == "max"
			if r.FosterTriggered[0] != want {
				t.Fatalf("trigger=%t want=%t", r.FosterTriggered[0], want)
			}
			if want {
				if r.fosterSpawnGroup(protocol.PVEActorCreate{}) != 0 {
					t.Fatal("triggered group rejected spawn")
				}
				r.triggerFosterGroups([3]float32{100, 100, 100})
				if !r.FosterTriggered[0] {
					t.Fatal("leaving region cleared latched activation")
				}
			}
		})
	}
}
