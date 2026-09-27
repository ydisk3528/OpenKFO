package game

import (
	"kungfu.local/server/internal/protocol"
	"log"
)

// Native 941910 emits the Foster script's completion transition; 827EF0
// accepts only the current controller and room context. This synchronizes a
// client mode flag, not a verified victory or permission to grant rewards.
func (h *Hub) pveFinishEvent(s *Session, message protocol.Message) error {
	return h.applyPVEFinish(s, message, false)
}
func (h *Hub) applyPVEFinish(s *Session, message protocol.Message, observed bool) error {
	r := s.Room
	if r.Type() != protocol.FosterMode || r.Owner != s.UID {
		return nil
	}
	event, err := protocol.ParseStageWaveEnd(message.Payload)
	if err != nil {
		return err
	}
	if event.Sender != s.UID {
		return protocol.ErrFrame
	}
	if event.ContextValue != uint64(r.ID)|(uint64(r.Serial)<<32) {
		return nil // Delayed notification from an earlier battle.
	}
	first := !r.FosterFinishReported
	r.FosterFinishReported = true
	if first {
		log.Printf("关卡结束标记 room=%d serial=%d 接收进度一致=%t（尚非发奖凭据）", r.ID, r.Serial, r.fosterReceiptsComplete())
	}
	// The controller set its flag before sending; peers need the same event.
	// Replays are safe: the native consumer assigns true, it does not toggle.
	if !observed {
		h.broadcast(r, message, s.UID)
	}
	return nil
}
