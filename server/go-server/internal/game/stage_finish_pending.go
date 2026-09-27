package game

import (
	"bytes"
	"fmt"
	"log"
	"time"

	"kungfu.local/server/internal/protocol"
)

// TCP finish reports can precede UDP progress. Retain an authenticated report,
// not a victory: every retry still uses the existing full progress validation.
type pendingStageFinish struct {
	owner    *Session
	serial   uint32
	report   []byte
	deadline time.Time
	warned   bool
}

func (h *Hub) deferStageFinish(s *Session, payload []byte) {
	r := s.Room
	if r == nil || !r.cooperativePVE() || r.Stage != "battle" || r.Owner != s.UID || (r.FosterPlan == nil && r.StageWaves == nil) {
		return
	}
	if m := r.Members[s.UID]; m == nil || m.Session != s || m.Spectator {
		return
	}
	_, reason, err := protocol.ParsePVEFinishReport(r.Type(), payload)
	if err != nil {
		return
	}
	if _, err := validateBattleReport(r, payload); err != nil {
		return
	}
	// Only completion/clock progression can become true without a new report.
	if reason != protocol.StageFinishWaves && reason != protocol.StageFinishCounterZero {
		return
	}
	if old := r.pendingStageFinish; old != nil && old.serial == r.Serial && old.owner == s {
		return
	}
	p := &pendingStageFinish{owner: s, serial: r.Serial, report: bytes.Clone(payload), deadline: time.Now().Add(5 * time.Second)}
	r.pendingStageFinish = p
	log.Printf("stage_finish_wait room=%d serial=%d map=%d uid=%d reason=%d marker=%t receipts=%t", r.ID, r.Serial, protocol.ReadUint32(r.Request, protocol.RoomMapOffset), s.UID, reason, r.FosterFinishReported, r.fosterReceiptsComplete())
	h.scheduleStageFinishCheck(r, p)
}

func (h *Hub) scheduleStageFinishCheck(r *Room, p *pendingStageFinish) {
	interval := 100 * time.Millisecond
	if p.warned {
		interval = time.Second
	}
	time.AfterFunc(interval, func() {
		h.lockState()
		h.scopeRoom(r)
		defer h.unlockState()
		if !h.pendingStageFinishCurrent(r, p) {
			return
		}
		outcome, err := validateStageFinish(r, p.report)
		if err == nil && outcome != "" {
			r.pendingStageFinish = nil
			if err := h.stageFinishReport(p.owner, p.report); err != nil {
				log.Printf("stage_finish_retry_failed room=%d serial=%d error=%v", r.ID, r.Serial, err)
			}
			return
		}
		if err != nil {
			r.pendingStageFinish = nil
			log.Printf("stage_finish_invalid room=%d serial=%d error=%v", r.ID, r.Serial, err)
			return
		}
		if !p.warned && !time.Now().Before(p.deadline) {
			p.warned = true
			log.Printf("stage_finish_waiting_progress room=%d serial=%d map=%d marker=%t receipts=%t %s", r.ID, r.Serial, protocol.ReadUint32(r.Request, protocol.RoomMapOffset), r.FosterFinishReported, r.fosterReceiptsComplete(), stageProgressSummary(r))
			h.broadcast(r, notice("关卡结束报告已收到，正在等待关卡进度同步，尚未发放奖励。请暂勿退出。"), 0)
		}
		// Retain the report for this round. Late progress must still resume
		// settlement; missing evidence must never be treated as a victory.
		h.scheduleStageFinishCheck(r, p)
	})
}

// Compact diagnostics only on the wait transition, never full actor dumps or
// reward inference. This remains available with packet tracing disabled.
func stageProgressSummary(r *Room) string {
	active, unaccounted := 0, 0
	for _, a := range r.PVEActors {
		if a.active {
			active++
			if a.maximumHP <= 0 || a.reportedHP != 0 {
				unaccounted++
			}
		}
	}
	if r.StageWaves != nil && r.Type() == protocol.StageAssault {
		w := r.StageWaves
		return fmt.Sprintf("wave=%d wave_total=%d wave_finished=%t active_actors=%d", w.index, len(w.plans), w.finished, active)
	}
	expected, spawned, retired := 0, 0, 0
	if r.FosterPlan != nil {
		for _, g := range r.FosterPlan.Groups {
			expected += len(g.Spawns)
		}
	}
	for _, n := range r.FosterSpawned {
		spawned += n
	}
	for _, n := range r.FosterRetired {
		retired += n
	}
	return fmt.Sprintf("expected_spawns=%d received_spawns=%d retired=%d active_actors=%d active_unaccounted=%d", expected, spawned, retired, active, unaccounted)
}

func (h *Hub) pendingStageFinishCurrent(r *Room, p *pendingStageFinish) bool {
	m := r.Members[p.owner.UID]
	return h.Rooms[r.ID] == r && r.pendingStageFinish == p && r.Serial == p.serial && r.Stage == "battle" && r.Owner == p.owner.UID && p.owner.Room == r && h.Sessions[p.owner.UID] == p.owner && m != nil && m.Session == p.owner && !m.Spectator
}
