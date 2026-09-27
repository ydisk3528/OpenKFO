package game

import (
	"bytes"
	"fmt"
	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
	"time"
)

type TalismanUseRule = persistence.TalismanUseRule

type pendingTalisman struct {
	delivered map[uint64]bool
	event     protocol.TalismanEvent
	message   protocol.Message
	use       persistence.TalismanUse
	expires   time.Time
}

func (c Config) ValidateTalismanUses() error {
	seen := map[uint32]bool{}
	for _, r := range c.TalismanUses {
		if r.Item == 0 || seen[r.Item] {
			return fmt.Errorf("invalid or duplicate talisman use rule")
		}
		seen[r.Item] = true
	}
	return nil
}

func (h *Hub) useTalisman(s *Session, ch *Channel, m protocol.Message) error {
	return h.useTalismanObserved(s, ch, m, nil)
}
func (h *Hub) useTalismanObserved(s *Session, ch *Channel, m protocol.Message, delivered map[uint64]bool) error {
	room := s.Room
	if room == nil || room.Stage != "battle" || ch.Phase != "battle" {
		return nil
	}
	member := room.Members[s.UID]
	if member == nil || member.Session != s {
		return protocol.ErrFrame
	}
	if m.ID == protocol.MsgBattleEvent {
		e, err := protocol.ParseTalismanEvent(m.Payload)
		if err != nil || !e.Match(s.UID, uint32(room.ID), room.Serial) {
			return protocol.ErrFrame
		}
		settings, err := h.talismanRules()
		if err != nil {
			return err
		}
		if !settings.Rules.Enabled || settings.Validate() != nil {
			return nil
		}
		a, err := storage2_1(h, h.Store.RoleManager().Snapshot, s.UID)
		if err != nil {
			return err
		}
		var item []byte
		for _, r := range a.Inventory {
			if len(r) == 68 && r[4] == protocol.ItemTalisman && protocol.ReadUint16(r, 17) == e.Slot {
				if item != nil {
					return protocol.ErrFrame
				}
				item = r
			}
		}
		if item == nil {
			return nil
		}
		var rule *TalismanUseRule
		for i := range settings.Rules.Uses {
			if settings.Rules.Uses[i].Item == protocol.ReadUint32(item, 5) {
				rule = &settings.Rules.Uses[i]
				break
			}
		}
		if rule == nil {
			return nil
		}
		instance := protocol.ReadUint32(item, 0)
		if s.TalismanPending == nil {
			s.TalismanPending = map[uint32]pendingTalisman{}
		}
		now := time.Now()
		for key, p := range s.TalismanPending {
			if !now.Before(p.expires) || p.event.Room != e.Room || p.event.Battle != e.Battle {
				delete(s.TalismanPending, key)
			}
		}
		// Preserve the accepted cost and deadline even for an identical retry.
		if pending, ok := s.TalismanPending[instance]; ok {
			if pending.event.Sequence == e.Sequence && bytes.Equal(pending.message.Payload, m.Payload) {
				if pending.delivered == nil {
					pending.delivered = map[uint64]bool{}
				}
				for uid := range delivered {
					pending.delivered[uid] = true
				}
				s.TalismanPending[instance] = pending
			}
			return nil
		}
		cost := rule.ActiveCost
		if e.Kind == 8291 {
			cost = rule.PassiveCost
		}
		m.Payload = bytes.Clone(m.Payload)
		s.TalismanPending[instance] = pendingTalisman{event: e, message: m, use: persistence.TalismanUse{Instance: instance, Item: rule.Item, Kind: e.Kind, Slot: e.Slot, Cost: cost}, expires: now.Add(5 * time.Second), delivered: delivered}
		return nil
	}
	if len(m.Payload) != 8 {
		return protocol.ErrFrame
	}
	instance := protocol.ReadUint32(m.Payload, 0)
	p, ok := s.TalismanPending[instance]
	delete(s.TalismanPending, instance)
	if !ok || !time.Now().Before(p.expires) || !p.event.Match(s.UID, uint32(room.ID), room.Serial) {
		return nil
	}
	key := uint64(p.event.Kind)<<32 | uint64(instance)
	previous, seen := member.TalismanEvents[key]
	if seen && int32(p.event.Sequence-previous) < 0 {
		return nil
	}
	fresh := !seen || p.event.Sequence != previous
	sequence := p.event.Sequence
	// EquipCostPerBattle is charged once per item in a battle, regardless of
	// duplicate passive announcements. Active use is keyed by event sequence.
	if p.event.Kind == 8291 {
		sequence = 0
	}
	operation := fmt.Sprintf("%s:%d:%d:%d:%d:%d", s.Namespace, room.ID, room.Serial, p.event.Kind, instance, sequence)
	item, applied, err := storage3_3(h, h.Store.ItemManager().UseTalisman, s.UID, operation, p.use)
	if err == persistence.ErrTalismanQuota {
		out := make([]byte, 8)
		protocol.WriteUint32(out, 0, instance)
		protocol.WriteUint32(out, 4, p.use.Item)
		s.sendGame(protocol.Message{ID: 4207, Payload: out})
		return nil
	}
	if err == persistence.ErrDenied {
		s.sendGame(notice("法宝使用未完成，请检查装备和有效期限。"))
		return nil
	}
	if err != nil {
		return err
	}
	// Billing and effect delivery are different: native code can reapply a
	// passive within the same battle. Only the fee is once-per-battle.
	if fresh && (applied || p.event.Kind == 8291) {
		for uid, target := range room.Members {
			if uid != s.UID && !p.delivered[uid] {
				target.Session.sendGame(p.message)
			}
		}
	}
	if fresh {
		if member.TalismanEvents == nil {
			member.TalismanEvents = map[uint64]uint32{}
		}
		member.TalismanEvents[key] = p.event.Sequence
	}
	out := make([]byte, 12)
	protocol.WriteUint32(out, 0, instance)
	protocol.WriteUint32(out, 4, uint32(protocol.ReadUint16(item, 23)))
	s.sendGame(protocol.Message{ID: 4206, Payload: out})
	return nil
}
