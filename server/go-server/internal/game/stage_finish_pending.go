package game

import (
	"bytes"
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
	p := &pendingStageFinish{s, r.Serial, bytes.Clone(payload), time.Now().Add(5 * time.Second)}
	r.pendingStageFinish = p
	log.Printf("stage_finish_wait room=%d serial=%d map=%d uid=%d reason=%d marker=%t receipts=%t", r.ID, r.Serial, protocol.ReadUint32(r.Request, protocol.RoomMapOffset), s.UID, reason, r.FosterFinishReported, r.fosterReceiptsComplete())
	h.scheduleStageFinishCheck(r, p)
}

func (h *Hub) scheduleStageFinishCheck(r *Room, p *pendingStageFinish) {
	time.AfterFunc(100*time.Millisecond, func() {
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
		if err != nil || !time.Now().Before(p.deadline) {
			r.pendingStageFinish = nil
			log.Printf("stage_finish_unconfirmed room=%d serial=%d map=%d marker=%t receipts=%t error=%v", r.ID, r.Serial, protocol.ReadUint32(r.Request, protocol.RoomMapOffset), r.FosterFinishReported, r.fosterReceiptsComplete(), err)
			p.owner.sendGame(notice("关卡结束报告已收到，但关卡进度尚未对齐，暂未发放奖励。"))
			return
		}
		h.scheduleStageFinishCheck(r, p)
	})
}

func (h *Hub) pendingStageFinishCurrent(r *Room, p *pendingStageFinish) bool {
	m := r.Members[p.owner.UID]
	return h.Rooms[r.ID] == r && r.pendingStageFinish == p && r.Serial == p.serial && r.Stage == "battle" && r.Owner == p.owner.UID && p.owner.Room == r && h.Sessions[p.owner.UID] == p.owner && m != nil && m.Session == p.owner && !m.Spectator
}
