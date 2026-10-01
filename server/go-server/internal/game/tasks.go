package game

import (
	"bytes"
	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
	"log"
)

func (h *Hub) tasks(s *Session, m protocol.Message) (result error) {
	// Task configuration/read errors are not transport/authentication failures.
	// Never retry a grant here: its transaction may already have committed.
	defer func() {
		if result != nil {
			log.Printf("task_request_failed uid=%d message=%d error=%v", s.UID, m.ID, result)
			s.sendGame(notice("[TASK-001] 任务服务暂时不可用，请稍后刷新；奖励结果请以背包和余额为准。"))
			result = nil
		}
	}()
	var action uint32
	var key uint16
	var awards persistence.TaskAwards
	var gifts []persistence.LevelGift
	if m.ID == 6000 {
		if len(m.Payload) != 4 {
			s.sendGame(notice("任务查询格式不正确。"))
			return nil
		}
		growth, err := storage2_1(h, h.Store.RewardManager().BattleRewards, h.Config.Settlement)
		if err != nil {
			return err
		}
		gifts = growth.Rules.LevelGifts
		awards, err = storage2_2(h, h.Store.TaskManager().CompleteTasks, s.UID, growth.Rules)
		if err != nil {
			s.sendGame(notice("任务完成检查未成功，未确认发奖，请稍后刷新。"))
			return nil
		}
	} else {
		r, err := protocol.ParseTaskAction(m.ID, m.Payload)
		if err != nil {
			s.sendGame(notice("任务操作格式不正确。"))
			return nil
		}
		action, key = m.ID, r.Key
	}
	rows, changed, err := storage3_3(h, h.Store.TaskManager().TaskTransition, s.UID, action, key)
	if err != nil {
		s.sendGame(notice("任务操作未完成，请检查任务是否开放及前置条件。"))
		return nil
	}
	if action == 0 {
		successors := taskSuccessorMessages(h.Config.ConfigHash, awards, rows)
		listed := rows
		if len(successors) > 0 {
			skip := map[uint16]bool{}
			for _, m := range successors {
				skip[protocol.ReadUint16(m.Payload, 4)] = true
			}
			listed = nil
			for _, r := range rows {
				if !skip[r.Key] {
					listed = append(listed, r)
				}
			}
		}
		p, err := protocol.EncodeTaskProgress(listed)
		if err != nil {
			return err
		}
		// Send current absolute balances even after a repeated query: a prior
		// committed award may have lost its response when transport closed.
		s.sendGame(protocol.Message{ID: 4300, Payload: bytes.Clone(awards.Profile[persistence.ExperienceOffset : persistence.ExperienceOffset+8])})
		s.sendGame(protocol.Message{ID: 1240, Payload: protocol.Uint32Bytes(awards.GoldBalance)})
		if err := h.rewardBalances(s, gifts); err != nil {
			return err
		}
		// Native clears the list before sending 6000. Do not append another
		// full list after an action: its 6020 handler does not deduplicate.
		s.sendGame(protocol.Message{ID: 6020, Payload: p})
		if len(awards.Keys) > 0 {
			for _, item := range awards.Items {
				s.sendGame(protocol.Message{ID: protocol.MsgItemAdded, Payload: bytes.Clone(item)})
				if s.Inventory == nil {
					s.Inventory = map[uint32][]byte{}
				}
				s.Inventory[protocol.ReadUint32(item, 0)] = bytes.Clone(item)
			}
			for _, message := range taskCompletionMessages(s.UID, h.Config.ConfigHash, awards, rows) {
				s.sendGame(message)
			}
			for _, message := range successors {
				s.sendGame(message)
			}
			s.sendGame(notice("任务已完成，配置的奖励已发放。"))
		}
		if err := h.announceTitleReward(s); err != nil {
			s.sendGame(notice("称号奖励查询未成功，请稍后刷新。"))
		}
		if err := h.extendedTaskLists(s); err != nil {
			s.sendGame(notice("每日/新手任务列表查询未成功，请检查配置及客户端版本。"))
		}
		return nil
	}
	if !changed {
		// Repeating 6060 resets the native baseline to its current profile.
		// Preserve the original baseline on an already accepted task.
		s.sendGame(notice("任务状态未改变，请刷新任务列表。"))
		return nil
	}
	p := make([]byte, 14)
	protocol.WriteUint64(p, 0, s.UID)
	for _, r := range rows {
		if r.Key == key {
			protocol.WriteUint32(p, 8, r.Unknown0)
			break
		}
	}
	protocol.WriteUint16(p, 12, key)
	s.sendGame(protocol.Message{ID: action + 10, Payload: p})
	return nil
}

func taskCompletionMessages(uid uint64, hash string, awards persistence.TaskAwards, rows []protocol.TaskProgress) []protocol.Message {
	if uid == 0 || len(awards.Profile) != 360 {
		return nil
	}
	completed := map[uint16]bool{}
	for _, r := range rows {
		if r.State == 3 {
			completed[r.Key] = true
		}
	}
	var messages []protocol.Message
	for _, key := range awards.Keys {
		if !completed[key] || !awards.NotificationRules.CanNotifyCompletion(hash, key, awards.Profile[123]) {
			continue
		}
		// Current native handler consumes the WORD at +12. The prefix follows
		// the existing task acknowledgement identity/context policy.
		p := make([]byte, 14)
		protocol.WriteUint64(p, 0, uid)
		protocol.WriteUint32(p, 8, protocol.ReadUint32(awards.Profile, 0))
		protocol.WriteUint16(p, 12, key)
		messages = append(messages, protocol.Message{ID: 6030, Payload: p})
		delete(completed, key)
	}
	return messages
}

func taskSuccessorMessages(hash string, awards persistence.TaskAwards, rows []protocol.TaskProgress) []protocol.Message {
	rules := awards.NotificationRules
	if hash == "" || rules.ClientHash != hash || (persistence.TaskSettings{Rules: rules}).Validate() != nil {
		return nil
	}
	fresh := map[uint16]bool{}
	for _, key := range awards.Keys {
		fresh[key] = true
	}
	catalogue := map[uint16]persistence.TaskCatalogueEntry{}
	for _, e := range rules.Catalogue {
		catalogue[e.ID] = e
	}
	targets := map[uint16]bool{}
	for _, rule := range rules.Tasks {
		parent, ok := catalogue[rule.ID]
		child, exists := catalogue[rule.Next]
		if fresh[rule.ID] && rule.Enabled && ok && parent.Enabled && parent.Next == rule.Next && rule.Next != 0 && exists && child.Enabled {
			targets[rule.Next] = true
		}
	}
	var messages []protocol.Message
	for _, r := range rows {
		if targets[r.Key] && r.State == 1 {
			// 6040 inserts a new native record. Exclude this key from the preceding
			// 6020 list so one request never inserts the same task twice.
			p := make([]byte, 6)
			protocol.WriteUint32(p, 0, r.Unknown0)
			protocol.WriteUint16(p, 4, r.Key)
			messages = append(messages, protocol.Message{ID: 6040, Payload: p})
			delete(targets, r.Key)
		}
	}
	return messages
}
