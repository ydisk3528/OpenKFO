package game

import (
	"kungfu.local/server/internal/protocol"
	"math"
	"time"
)

// Each event group advances independently. Match the complete native spawn
// literal; template counts alone would permit skipping ahead to the boss.
// Trigger state is latched from validated player position receipts.
func (r *Room) fosterSpawnGroup(event protocol.PVEActorCreate) int {
	plan := r.FosterPlan
	if plan == nil || len(r.FosterSpawned) != len(plan.Groups) || len(r.FosterTriggered) != len(plan.Groups) {
		return -1
	}
	active := uint32(0)
	living := make([]uint32, len(plan.Groups))
	for _, actor := range r.PVEActors {
		if actor.active {
			active++ // Includes corpses until native 20401 destruction.
			if actor.fosterGroup < 0 || actor.fosterGroup >= len(living) {
				return -1
			}
			// The Lua sub-list counts living monsters only. Unknown template
			// health must not create spare capacity by looking like a corpse.
			if actor.maximumHP <= 0 || actor.reportedHP != 0 {
				living[actor.fosterGroup]++
			}
		}
	}
	if active >= plan.GlobalLimit {
		return -1
	}
	matched := -1
	for i, group := range plan.Groups {
		if !r.FosterTriggered[i] {
			continue
		}
		familyLiving := living[i]
		if group.Family != 0 {
			familyLiving = 0
			for j, g := range plan.Groups {
				if g.Family == group.Family {
					familyLiving += living[j]
				}
			}
		}
		if group.EndAfter != nil {
			since := r.FosterActivated[i]
			if group.PreviousBatch > 0 {
				since = r.FosterBatchEnded[int(group.PreviousBatch)-1]
			}
			if since.IsZero() || time.Since(since).Seconds()+0.5 < float64(*group.EndAfter) {
				continue
			}
		}
		if living[i] >= group.SubLimit || familyLiving >= group.GroupLimit {
			continue
		}
		next := r.FosterSpawned[i]
		if next < 0 || next >= len(group.Spawns) {
			continue
		}
		spawn := group.Spawns[next]
		if spawn.Template == event.TemplateValue && fosterPositionMatches(spawn, event.Position) && spawn.Direction == event.DirectionValue {
			if matched != -1 {
				if group.Family != 0 && plan.Groups[matched].Family == group.Family {
					continue
				}
				return -1 // Ambiguous plans need native group evidence, not a guess.
			}
			matched = i
		}
	}
	return matched
}

func fosterPositionMatches(spawn protocol.FosterSpawn, position [3]float32) bool {
	if spawn.BornBox == nil {
		return spawn.Position == position
	}
	for i, v := range position {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) || v < spawn.BornBox[i] || v > spawn.BornBox[i+3] || float32(math.Trunc(float64(v))) != v {
			return false
		}
	}
	return true
}

// Native end_last_time starts once the preceding batch has no living actors.
func (r *Room) refreshFosterBatchEnds() {
	if r.FosterPlan == nil {
		return
	}
	if r.FosterBatchEnded == nil {
		r.FosterBatchEnded = map[int]time.Time{}
	}
	for i, g := range r.FosterPlan.Groups {
		if i >= len(r.FosterSpawned) || r.FosterSpawned[i] != len(g.Spawns) || !r.FosterBatchEnded[i].IsZero() {
			continue
		}
		living := false
		accounted := 0
		if i < len(r.FosterRetired) {
			accounted = r.FosterRetired[i]
		}
		for _, a := range r.PVEActors {
			if a.active && a.fosterGroup == i && a.maximumHP > 0 && a.reportedHP == 0 {
				accounted++
			}
			if a.active && a.fosterGroup == i && (a.maximumHP <= 0 || a.reportedHP != 0) {
				living = true
				break
			}
		}
		if !living && accounted == len(g.Spawns) {
			r.FosterBatchEnded[i] = time.Now()
		}
	}
}
