package game

import (
	"bytes"
	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
)

func (h *Hub) extendedTaskLists(s *Session) error {
	return h.extendedTaskList(s, 0)
}

// selected=0 refreshes both lists; 6001/6002 refresh only their own native container.
func (h *Hub) extendedTaskList(s *Session, selected uint32) error {
	// Extended catalogues require a known client version; legacy unbound
	// servers cannot safely publish these native template keys.
	if h.Config.ConfigHash == "" {
		return nil
	}
	var states []persistence.ExtendedTaskState
	var err error
	if selected != 0 {
		manager, uid, hash := h.Store.TaskManager(), s.UID, h.Config.ConfigHash
		if !h.readSessionSnapshot(s, func() { states, err = manager.ExtendedTasks(uid, hash) }) || h.Config.ConfigHash != hash {
			return nil
		}
	} else {
		// Automatic refreshes can be part of a larger state transition.
		states, err = storage2_2(h, h.Store.TaskManager().ExtendedTasks, s.UID, h.Config.ConfigHash)
	}
	if err != nil {
		return err
	}
	if states == nil {
		return nil
	}
	return sendExtendedTaskLists(s, states, selected)
}

func sendExtendedTaskLists(s *Session, states []persistence.ExtendedTaskState, selected uint32) error {
	lists := map[uint32][]protocol.ExtendedTaskProgress{6041: {}, 6042: {}}
	for _, state := range states {
		id := uint32(6041)
		if state.Snapshot.Rule.Kind == "newbie" {
			id = 6042
		}
		r := protocol.ExtendedTaskProgress{Key: state.Key, State: state.State}
		slot := 0
		for i, c := range state.Snapshot.Template.Conditions {
			if c.Required == 0 {
				continue
			}
			r.Conditions[slot] = protocol.ExtendedTaskCondition{Key: c.Key, Current: state.Counts[i]}
			slot++
		}
		lists[id] = append(lists[id], r)
	}
	// Validate both complete lists before sending either. The client clears
	// each container on receipt, so never split one list across messages.
	var messages []protocol.Message
	for _, id := range []uint32{6041, 6042} {
		p, err := protocol.EncodeExtendedTaskProgress(id, lists[id])
		if err != nil {
			return err
		}
		messages = append(messages, protocol.Message{ID: id, Payload: p})
	}
	for _, m := range messages {
		if selected == 0 || m.ID == selected {
			s.sendGame(m)
		}
	}
	if s.ExtendedTaskNotified == nil {
		s.ExtendedTaskNotified = map[uint16]string{}
	}
	if selected != 0 {
		filtered := states[:0]
		for _, state := range states {
			id := uint32(6041)
			if state.Snapshot.Rule.Kind == "newbie" {
				id = 6042
			}
			if id == selected {
				filtered = append(filtered, state)
			}
		}
		states = filtered
	}
	for _, m := range extendedTaskCompletionMessages(states, s.ExtendedTaskNotified) {
		s.sendGame(m)
	}
	return nil
}

// Send only after both complete lists exist in the client. A new daily cycle
// may notify again; repeated queries and repeated return acknowledgements do not.
func extendedTaskCompletionMessages(states []persistence.ExtendedTaskState, notified map[uint16]string) []protocol.Message {
	var messages []protocol.Message
	for _, state := range states {
		if state.State != 4 {
			continue
		}
		token := state.Snapshot.ClientHash + ":" + state.Cycle
		if notified[state.Key] == token {
			continue
		}
		id := uint32(6031)
		if state.Snapshot.Rule.Kind == "newbie" {
			id = 6032
		}
		p := make([]byte, 3)
		protocol.WriteUint16(p, 0, state.Key)
		p[2] = 4
		messages = append(messages, protocol.Message{ID: id, Payload: p})
		notified[state.Key] = token
	}
	return messages
}

func (h *Hub) extendedTaskAction(s *Session, m protocol.Message) error {
	r, err := protocol.ParseExtendedTaskAction(m.ID, m.Payload)
	if err != nil {
		s.sendGame(notice("每日/新手任务操作格式不正确。"))
		return nil
	}
	if m.ID == 6311 || m.ID == 6312 {
		growth, err := storage2_1(h, h.Store.RewardManager().BattleRewards, h.Config.Settlement)
		if err != nil {
			return err
		}
		award, err := storage2_5(h, h.Store.TaskManager().ClaimExtendedTask, s.UID, h.Config.ConfigHash, m.ID, r.Key, growth.Rules)
		if err != nil {
			s.sendGame(notice("任务奖励未发放：请确认已完成、尚未领取且配置仍开放。"))
			return nil
		}
		if !award.AlreadyClaimed {
			s.sendGame(protocol.Message{ID: 4300, Payload: bytes.Clone(award.Profile[persistence.ExperienceOffset : persistence.ExperienceOffset+8])})
			s.sendGame(protocol.Message{ID: 1240, Payload: protocol.Uint32Bytes(award.GoldBalance)})
			for _, item := range award.Items {
				s.sendGame(protocol.Message{ID: protocol.MsgItemAdded, Payload: bytes.Clone(item)})
				if s.Inventory == nil {
					s.Inventory = map[uint32][]byte{}
				}
				s.Inventory[protocol.ReadUint32(item, 0)] = bytes.Clone(item)
			}
			if err := h.rewardBalances(s, growth.Rules.LevelGifts); err != nil {
				return err
			}
		}
		// Native 6301/6302 consumers update the daily/newbie state using
		// WORD key + BYTE state; newbie state 3 removes the received entry.
		p := make([]byte, 3)
		protocol.WriteUint16(p, 0, r.Key)
		p[2] = 3
		s.sendGame(protocol.Message{ID: m.ID - 10, Payload: p})
		return nil
	}
	state, _, err := storage3_4(h, h.Store.TaskManager().ExtendedTaskTransition, s.UID, h.Config.ConfigHash, m.ID, r.Key)
	if err != nil {
		s.sendGame(notice("每日/新手任务操作未完成，请检查任务配置、客户端版本和当前状态。"))
		return nil
	}
	// Replay the acknowledgement after response loss. Native A47A00/A480E0
	// preserve counters for state 2; state 1 clears them, as cancellation does.
	// The store validates the same account, kind, cycle and client hash and
	// does not rewrite the accepted snapshot or counts on an unchanged state.
	// Native handlers consume only WORD key and BYTE state. This server uses
	// that minimal 3B acknowledgement, not an unproven echo of the 19B request.
	p := make([]byte, 3)
	protocol.WriteUint16(p, 0, state.Key)
	p[2] = state.State
	s.sendGame(protocol.Message{ID: m.ID + 10, Payload: p})
	return nil
}
