package game

import (
	"bytes"
	"kungfu.local/server/internal/protocol"
	"log"
)

// Native 8A0F00/921430 creates this private introduction room before P2P
// registration. It is not mode 5 practice or the titlemission skill exercises.
func tutorialRequest(p []byte) bool {
	return len(p) == protocol.RoomRequestSize && protocol.RoomTypeFromRequest(p) == protocol.NewPlayerGuide && p[protocol.RoomCapacityOffset] == 1 &&
		protocol.ReadUint32(p, protocol.RoomMapOffset) == protocol.TutorialMapID && protocol.ReadUint32(p, protocol.RoomSuggestedMapOffset) == protocol.TutorialMapID
}

func tutorialRoom(r *Room) bool { return r != nil && tutorialRequest(r.Request) }

func (h *Hub) acknowledgeCreatedRoomJoin(s *Session, r *Room) error {
	a, err := storage2_1(h, h.Store.RoleManager().Snapshot, s.UID)
	if err != nil {
		return err
	}
	m := r.Members[s.UID]
	s.sendGame(protocol.Message{ID: protocol.MsgRoomEntered, Payload: roomEntryForMember(r, m, fighter(a, m))})
	s.sendGame(protocol.Message{ID: protocol.MsgRoomOwner, Payload: protocol.Uint64Bytes(r.Owner)})
	r.CreationPending = false
	return nil
}

// 829B50 writes profile+123 synchronously; 3115 then invokes the native
// lobby guide gate. Persisting this field alone does not update that gate.
// Keep the complete 64-byte payload required by the native selector consumer.
func syncTutorialTitle(s *Session, level byte) {
	p := make([]byte, 64)
	p[0] = level
	s.sendGame(protocol.Message{ID: protocol.MsgTitleAward, Payload: p})
}

// 4124/0 is emitted by the native guide at completion (96A2B0), not 6220.
func (h *Hub) completeTutorial(s *Session, ch *Channel, payload []byte) error {
	if len(payload) != 0 {
		return protocol.ErrFrame
	}
	r := s.Room
	if !tutorialRoom(r) || r.Stage != "battle" || ch.Phase != "battle" ||
		r.Owner != s.UID || len(r.Members) != 1 || r.Members[s.UID] == nil ||
		r.Members[s.UID].Session != s || !r.Members[s.UID].Input {
		return nil
	}
	result, err := storage2_2(h, h.Store.RewardManager().CompleteTutorial, s.UID, h.Config.ConfigHash)
	if err != nil {
		// The completion transaction rolls back on invalid reward definitions or
		// storage failure. Keep the guide session alive so completion can retry;
		// propagating this error disconnects a player who sent a valid 4124.
		log.Printf("tutorial_reward_failed uid=%d error=%v", s.UID, err)
		s.sendGame(notice("新手奖励结算失败，进度尚未提交。请检查GM新手奖励配置后重试。"))
		return nil
	}
	log.Printf("tutorial_reward uid=%d replay=%t choices=%v automatic_items=%d gold=%d tickets=%d", s.UID, result.Replay, result.Choices, len(result.Items), result.Gold, result.Tickets)
	// Synchronize BEFORE leaving: otherwise 924010 sees the old title and
	// immediately sends another 3010 despite the committed completion receipt.
	// Use the catalogue validated by the completion transaction. A second DB
	// read after committing could fail or see GM edits before the offer is sent.
	announced := false
	if len(result.Choices) != 0 {
		announced, err = sendTutorialReward(s, result.Choices, result.Catalog)
	}
	if err != nil {
		return err
	}
	if !announced {
		syncTutorialTitle(s, result.Profile[123])
	}
	for _, item := range result.Items {
		s.sendGame(protocol.Message{ID: protocol.MsgItemAdded, Payload: bytes.Clone(item)})
		if s.Inventory == nil {
			s.Inventory = map[uint32][]byte{}
		}
		s.Inventory[protocol.ReadUint32(item, 0)] = bytes.Clone(item)
	}
	s.sendGame(protocol.Message{ID: 1240, Payload: protocol.Uint32Bytes(result.Gold)})
	s.sendGame(protocol.Message{ID: 1230, Payload: protocol.Uint32Bytes(result.Tickets)})
	h.leave(s, true)
	// Reuse native full-list/status notifications after returning to the lobby.
	// Rewards remain claimed through the existing task protocol, not this event.
	if err := h.extendedTaskLists(s); err != nil {
		s.sendGame(notice("新手引导已完成，任务列表暂未刷新，请重新打开任务面板。"))
	}
	if announced {
		s.sendGame(notice("新手引导已完成，请选择一件武器并确认领取；其他奖励已发放。"))
	} else if len(result.Items) == 0 && !result.Replay {
		s.sendGame(notice("新手引导已完成，本次没有可选武器奖励；金币、点券已结算。"))
	} else {
		s.sendGame(notice("新手引导已完成，已按配置发放奖励，请查看背包和余额。"))
	}
	return nil
}

func (h *Hub) announceTutorialReward(s *Session) (bool, error) {
	if s.TitleOffer != 0 && s.TitleOffer != 2 {
		return false, nil
	}
	choices, catalog, err := storage3_1(h, h.Store.RewardManager().TutorialChoices, s.UID)
	if err != nil || len(choices) == 0 {
		return false, err
	}
	return sendTutorialReward(s, choices, catalog)
}

func sendTutorialReward(s *Session, choices []uint32, catalog []byte) (bool, error) {
	if err := sendWeaponReward(s, 2, choices, catalog); err != nil {
		return false, err
	}
	return true, nil
}
