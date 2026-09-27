package game

import "kungfu.local/server/internal/protocol"

// The controller's current-round script marker and validated finish report
// determine script completion. Imported plans can contain optional branches;
// require death receipts for actual accepted spawns, not every planned spawn.
// This remains client-reported completion, not authoritative simulation.
func (r *Room) fosterReceiptsComplete() bool {
	plan := r.FosterPlan
	if r.Type() != protocol.FosterMode || !r.FosterFinishReported || plan == nil || len(plan.Groups) == 0 ||
		len(r.FosterSpawned) != len(plan.Groups) || len(r.FosterRetired) != len(plan.Groups) {
		return false
	}
	accounted := append([]int(nil), r.FosterRetired...)
	for _, actor := range r.PVEActors {
		if !actor.active {
			continue
		}
		if actor.maximumHP <= 0 || actor.reportedHP != 0 || actor.fosterGroup < 0 || actor.fosterGroup >= len(accounted) {
			return false
		}
		accounted[actor.fosterGroup]++
	}
	spawned := 0
	for i := range plan.Groups {
		if r.FosterSpawned[i] < 0 || r.FosterRetired[i] < 0 || accounted[i] != r.FosterSpawned[i] {
			return false
		}
		spawned += r.FosterSpawned[i]
	}
	return spawned > 0
}
