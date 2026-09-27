package game

import (
	"bytes"
	"fmt"

	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
)

// The client multiplies whole hours and rate as signed int32 before capping.
// Only the wire display timer saturates; persisted start and awards stay exact.
func trainingStatus(uid uint64, rank, minutes uint32, active bool, settings persistence.TrainingSettings) ([]byte, error) {
	if rank > 8 {
		return nil, persistence.ErrDenied
	}
	if err := settings.Validate(); err != nil {
		return nil, err
	}
	if minutes > 35791394 {
		minutes = 35791394
	}
	status := make([]byte, 56)
	protocol.WriteUint64(status, 0, uid)
	protocol.WriteUint32(status, 8, rank)
	if active {
		protocol.WriteUint32(status, 28, 1)
	}
	if settings.Rules.Enabled {
		rule := settings.Rules.Levels[rank]
		if rule.XPPerHour != 0 && uint64(minutes/60)*uint64(rule.XPPerHour) > 0x7fffffff {
			minutes = (0x7fffffff/rule.XPPerHour)*60 + 59
		}
		protocol.WriteUint32(status, 36, rule.XPPerHour)
		protocol.WriteUint32(status, 40, rule.XPCap)
	}
	protocol.WriteUint32(status, 20, minutes)
	return status, nil
}

func (h *Hub) claimTraining(s *Session, ch *Channel, payload []byte) error {
	if len(payload) != 0 {
		return protocol.ErrFrame
	}
	settings, err := storage2_1(h, h.Store.RewardManager().BattleRewards, h.Config.Settlement)
	if err != nil {
		return err
	}
	operation := fmt.Sprintf("%s:%d:%d", s.Namespace, ch.ID, ch.Sequence)
	r, err := storage2_3(h, h.Store.TrainingManager().ClaimTraining, s.UID, operation, settings.Rules)
	if err != nil {
		s.sendGame(notice("名侠奖励未领取，请确认奖励已开放且训练已满一小时。"))
		return nil
	}
	for _, item := range r.Items {
		s.sendGame(protocol.Message{ID: protocol.MsgItemAdded, Payload: bytes.Clone(item)})
		if s.Inventory == nil {
			s.Inventory = map[uint32][]byte{}
		}
		s.Inventory[protocol.ReadUint32(item, 0)] = bytes.Clone(item)
	}
	rank, err := storage2_1(h, h.Store.TrainingManager().TrainingRank, s.UID)
	if err != nil {
		return err
	}
	s.sendGame(protocol.Message{ID: 4300, Payload: bytes.Clone(r.Profile[persistence.ExperienceOffset : persistence.ExperienceOffset+8])})
	if err := h.rewardBalances(s, settings.Rules.LevelGifts); err != nil {
		return err
	}
	// 21007 refreshes training and causes native 21002 for the next cycle.
	// It does not replace role-level synchronization after AdvanceLevel.
	status := make([]byte, 56)
	protocol.WriteUint64(status, 0, s.UID)
	protocol.WriteUint32(status, 8, rank)
	s.sendGame(protocol.Message{ID: 21007, Payload: status})
	return nil
}
