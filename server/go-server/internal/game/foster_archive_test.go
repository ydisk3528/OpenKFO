//go:build clientarchive

package game

import (
	"kungfu.local/server/internal/desktop"
	"kungfu.local/server/internal/protocol"
	"os"
	"testing"
)

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
