package game

import (
	"bytes"
	"math"
	"os"
	"testing"

	"kungfu.local/server/internal/desktop"
	"kungfu.local/server/internal/protocol"
)

func fosterSpawnPacket(owner, actor uint64, sequence uint32, spawn protocol.FosterSpawn) protocol.Message {
	m := combatPacket(protocol.BattleEventPVEActorCreate, 67, owner, actor, 0)
	protocol.WriteUint32(m.Payload, 19, sequence)
	protocol.WriteUint32(m.Payload, 47, spawn.Template)
	for i, v := range spawn.Position {
		protocol.WriteUint32(m.Payload, 51+4*i, math.Float32bits(v))
	}
	protocol.WriteUint32(m.Payload, 63, spawn.Direction)
	return m
}

func TestFosterSpawnPlanGate(t *testing.T) {
	for _, scenario := range []string{"valid", "missing-movement", "missing-movement-timed", "no-plan", "template", "position", "direction", "peer", "capacity", "ambiguous"} {
		t.Run(scenario, func(t *testing.T) {
			h, owner, peer, outsider := combatFixture()
			r := owner.Room
			r.Request[46] = byte(protocol.FosterMode)
			spawn := protocol.FosterSpawn{Template: 251, Position: [3]float32{-1610, -4, -15}, Direction: 2}
			r.FosterPlan = &protocol.FosterPlan{GlobalLimit: 1, Groups: []protocol.FosterGroup{{SubLimit: 2, GroupLimit: 2, Spawns: []protocol.FosterSpawn{spawn}}}}
			r.FosterSpawned = []int{0}
			r.FosterTriggered = []bool{true}
			sender := owner
			switch scenario {
			case "missing-movement", "missing-movement-timed":
				r.FosterTriggered[0] = false
				if scenario == "missing-movement-timed" {
					delay := float32(2)
					r.FosterPlan.Groups[0].EndAfter = &delay
				}
			case "no-plan":
				r.FosterPlan = nil
			case "template":
				spawn.Template = 51
			case "position":
				spawn.Position[0]++
			case "direction":
				spawn.Direction = 0
			case "peer":
				sender = peer
			case "capacity":
				r.PVEActors = map[uint64]pveActor{43: {active: true}}
			case "ambiguous":
				r.FosterPlan.Groups = append(r.FosterPlan.Groups, r.FosterPlan.Groups[0])
				r.FosterSpawned = append(r.FosterSpawned, 0)
				r.FosterTriggered = append(r.FosterTriggered, true)
			}
			m := fosterSpawnPacket(sender.UID, 42, 1, spawn)
			if err := h.battleMessage(sender, sender.game(), m); err != nil {
				t.Fatal(err)
			}
			if scenario == "valid" || scenario == "missing-movement" {
				if !r.FosterTriggered[0] || !r.hasPVEActor(42) || !r.controlsBattleActor(owner, 42) || r.controlsBattleActor(peer, 42) || r.FosterSpawned[0] != 1 {
					t.Fatal("spawn permission not registered")
				}
				if out := roomOutputs(t, peer, 8071); !bytes.Equal(out[0].Payload, m.Payload) {
					t.Fatal("native spawn changed")
				}
				if err := h.battleMessage(owner, owner.game(), m); err != nil {
					t.Fatal(err)
				}
				if r.FosterSpawned[0] != 1 {
					t.Fatal("retry consumed another spawn")
				}
			} else if r.hasPVEActor(42) || r.FosterSpawned[0] != 0 {
				t.Fatal("invalid spawn consumed plan")
			}
			roomOutputs(t, peer)
			roomOutputs(t, owner)
			roomOutputs(t, outsider)
		})
	}
}

func TestNativeFosterSpawnPlan(t *testing.T) {
	path := os.Getenv("OPENKFO_CLIENT_ARCHIVE")
	if path == "" {
		t.Skip("native client archive required")
	}
	maps, err := desktop.ReadStageMaps(path)
	if err != nil {
		t.Fatal(err)
	}
	var plan *protocol.FosterPlan
	for _, m := range maps {
		if m.MapID == 8110 {
			plan = m.FosterPreview
		}
	}
	if plan == nil {
		t.Fatal("verified map plan missing")
	}
	h, owner, peer, outsider := combatFixture()
	r := owner.Room
	r.Request[46] = byte(protocol.FosterMode)
	r.FosterPlan, r.FosterSpawned = plan, make([]int, len(plan.Groups))
	r.FosterTriggered = make([]bool, len(plan.Groups))
	r.triggerFosterGroups([3]float32{-1500, -4, -17})
	sequence, total := uint32(1), 0
	// Trying to skip directly to the boss cannot advance either event group.
	boss := plan.Groups[1].Spawns[len(plan.Groups[1].Spawns)-1]
	if err = h.battleMessage(owner, owner.game(), fosterSpawnPacket(owner.UID, 42, sequence, boss)); err != nil {
		t.Fatal(err)
	}
	roomOutputs(t, peer)
	if len(r.PVEActors) != 0 {
		t.Fatal("early boss admitted")
	}
	// Groups are concurrent: interleave their lists instead of flattening waves.
	for index := 0; index < 21; index++ {
		for group := len(plan.Groups) - 1; group >= 0; group-- {
			if index >= len(plan.Groups[group].Spawns) {
				continue
			}
			create := fosterSpawnPacket(owner.UID, 42, sequence, plan.Groups[group].Spawns[index])
			if err = h.battleMessage(owner, owner.game(), create); err != nil {
				t.Fatal(err)
			}
			roomOutputs(t, peer, 8071)
			sequence++
			remove := combatPacket(protocol.BattleEventPVEActorRemove, 47, owner.UID, 42, 0)
			protocol.WriteUint32(remove.Payload, 19, sequence)
			if err = h.battleMessage(owner, owner.game(), remove); err != nil {
				t.Fatal(err)
			}
			roomOutputs(t, peer, 8071)
			if !r.stalePVEEvent(owner, 42, sequence+1) {
				t.Fatal("removed identity accepts events")
			}
			sequence++
			total++
		}
	}
	if total != 23 || r.FosterSpawned[0] != 2 || r.FosterSpawned[1] != 21 || r.Stage != "battle" || len(r.Reports) != 0 {
		t.Fatal("spawn plan progress or settlement changed")
	}
	if err = h.battleMessage(owner, owner.game(), fosterSpawnPacket(owner.UID, 42, sequence, boss)); err != nil {
		t.Fatal(err)
	}
	roomOutputs(t, peer)
	roomOutputs(t, owner)
	roomOutputs(t, outsider)
}

func TestFosterConcurrentSpawnLimits(t *testing.T) {
	for _, scenario := range []string{"room", "sub-full", "group-full", "other-group", "corpse", "corpse-global-full", "removed", "unknown-health", "invalid-group", "zero-limit"} {
		t.Run(scenario, func(t *testing.T) {
			_, owner, _, _ := combatFixture()
			r := owner.Room
			spawn := protocol.FosterSpawn{Template: 1}
			r.FosterPlan = &protocol.FosterPlan{GlobalLimit: 3, Groups: []protocol.FosterGroup{
				{SubLimit: 2, GroupLimit: 3, Spawns: []protocol.FosterSpawn{spawn}},
				{SubLimit: 2, GroupLimit: 3, Spawns: []protocol.FosterSpawn{{Template: 2}}},
			}}
			r.FosterSpawned = []int{0, 0}
			r.FosterTriggered = []bool{true, true}
			r.PVEActors = map[uint64]pveActor{42: {active: true, maximumHP: 8, reportedHP: 8, fosterGroup: 0}}
			actor := r.PVEActors[42]
			allowed := true
			switch scenario {
			case "sub-full":
				r.PVEActors[43] = actor
				allowed = false
			case "group-full":
				r.FosterPlan.Groups[0].GroupLimit = 1
				allowed = false
			case "other-group":
				actor.fosterGroup = 1
				r.FosterPlan.Groups[0].SubLimit = 1
			case "corpse", "corpse-global-full":
				actor.reportedHP = 0
				r.FosterPlan.Groups[0].SubLimit = 1
				if scenario == "corpse-global-full" {
					r.FosterPlan.GlobalLimit = 1
					allowed = false
				}
			case "removed":
				actor.active = false
				r.FosterPlan.GlobalLimit, r.FosterPlan.Groups[0].SubLimit = 1, 1
			case "unknown-health":
				actor.maximumHP, actor.reportedHP = 0, 0
				r.FosterPlan.Groups[0].SubLimit = 1
				allowed = false
			case "invalid-group":
				actor.fosterGroup = -1
				allowed = false
			case "zero-limit":
				r.FosterPlan.Groups[0].SubLimit = 0
				allowed = false
			}
			r.PVEActors[42] = actor
			got := r.fosterSpawnGroup(protocol.PVEActorCreate{TemplateValue: spawn.Template})
			if (got == 0) != allowed {
				t.Fatalf("group=%d allowed=%t", got, allowed)
			}
			if r.FosterSpawned[0] != 0 {
				t.Fatal("validation consumed spawn plan")
			}
		})
	}
}

// Regression: map 8113's second area can spawn before a movement receipt.
// Losing the spawn also loses the peer's 20406, leaving the host waiting.
func TestFosterSecondAreaSpawnAndPeerAckWithoutMovement(t *testing.T) {
	h, owner, peer, _ := combatFixture()
	r := owner.Room
	r.Request[46] = byte(protocol.FosterMode)
	box := [6]float32{-600, -5, -60, -400, 10, 40}
	spawns := make([]protocol.FosterSpawn, 10)
	for i := range spawns {
		template := uint32(107)
		if i%5 >= 3 {
			template = 108
		}
		spawns[i] = protocol.FosterSpawn{Template: template, BornBox: &box, Position: [3]float32{-450, 0, 0}, Direction: 2}
	}
	r.FosterPlan = &protocol.FosterPlan{GlobalLimit: 100, Groups: []protocol.FosterGroup{
		{SubLimit: 10, GroupLimit: 20, Spawns: make([]protocol.FosterSpawn, 25)},
		{SubLimit: 10, GroupLimit: 20, Spawns: spawns},
	}}
	r.FosterSpawned, r.FosterRetired = []int{25, 0}, []int{25, 0}
	r.FosterTriggered = []bool{true, false}
	for i, spawn := range spawns {
		actor := uint64(18750 + i)
		create := fosterSpawnPacket(owner.UID, actor, uint32(i+1), spawn)
		if err := h.battleMessage(owner, owner.game(), create); err != nil {
			t.Fatal(err)
		}
		roomOutputs(t, peer, 8071)
		ack := combatPacket(20406, 47, peer.UID, actor, 0)
		ack.Payload[12], ack.Payload[13] = 1, 1
		if err := h.battleMessage(peer, peer.game(), ack); err != nil {
			t.Fatal(err)
		}
		roomOutputs(t, owner, 8071)
	}
	if r.FosterSpawned[1] != 10 {
		t.Fatal("second area stalled", r.FosterSpawned)
	}
}
