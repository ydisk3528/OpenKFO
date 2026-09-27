package game

import (
	"log"
	"time"

	"kungfu.local/server/internal/protocol"
)

// Called only after room/authentication/context/finite-value checks and event
// deduplication. Native 9E46B0 clamps damage at zero; 9E4740 caps healing at max.
// Source-side effects (9C5E40) send their own 8121; never apply them twice here.
// Native local callbacks and unreceived events can diverge from this projection,
// so neither corpse removal nor this value alone authorizes progress or rewards.
func (r *Room) trackFosterHealth(payload []byte) {
	if r.Type() != protocol.FosterMode {
		return
	}
	event, err := protocol.ParseBattleHealth(payload)
	if err != nil {
		return
	}
	actor := r.PVEActors[event.Target]
	if !actor.active || actor.maximumHP <= 0 {
		return
	}
	// Shared across TCP and observed UDP: one native health event applies once.
	key := [2]uint64{event.Sender, event.Target}
	sequence := protocol.ReadUint32(payload, 19)
	if old, ok := r.HealthReceipts[key]; ok && int32(sequence-old) <= 0 {
		return
	}
	if r.HealthReceipts == nil {
		r.HealthReceipts = map[[2]uint64]uint32{}
	}
	r.HealthReceipts[key] = sequence
	before := actor.reportedHP
	actor.reportedHP = min(actor.maximumHP, max(float32(0), before-event.Damage))
	r.PVEActors[event.Target] = actor
	r.refreshFosterBatchEnds()
	if time.Since(r.lastFosterHealthLog) < 5*time.Second {
		return
	}
	r.lastFosterHealthLog = time.Now()
	log.Printf("怪物血量（按收包累计，非通关依据） foster_health room=%d serial=%d sender=%d target=%d source=%d amount=%g hp_before=%g hp_after=%g max_hp=%g basis=received_events",
		r.ID, r.Serial, event.Sender, event.Target, event.Source, event.Damage, before, actor.reportedHP, actor.maximumHP)
}
