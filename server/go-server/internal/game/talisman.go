package game

import (
	"fmt"
	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
)

func (c Config) ValidateTalismanRepairs() error {
	seen := map[uint32]bool{}
	for _, r := range c.TalismanRepairs {
		if !r.Valid() || seen[r.Item] {
			return fmt.Errorf("invalid or duplicate talisman repair rule for %d", r.Item)
		}
		seen[r.Item] = true
	}
	return nil
}

type talismanQuote struct {
	Instance uint32
	Revision uint64
	Rule     persistence.TalismanRepairRule
}

func (h *Hub) talismanRules() (persistence.TalismanSettings, error) {
	if h.Store != nil {
		settings, err := storage2_0(h, h.Store.ItemManager().TalismanSettings)
		if err != nil || settings.Revision != 0 {
			return settings, err
		}
	}
	return persistence.TalismanSettings{Rules: persistence.TalismanRules{Enabled: len(h.Config.TalismanUses)+len(h.Config.TalismanRepairs) > 0, Uses: h.Config.TalismanUses, Repairs: h.Config.TalismanRepairs}}, nil
}

func (h *Hub) repairTalisman(s *Session, ch *Channel, m protocol.Message) error {
	expected := 4
	if m.ID == 4204 {
		expected = 12
	}
	if len(m.Payload) != expected {
		return protocol.ErrFrame
	}
	reject := func() { s.sendGame(notice("修理未完成，请重新打开修理窗口核对材料与报价。")) }
	if m.ID == 4202 {
		s.TalismanQuote = nil
	}
	settings, err := h.talismanRules()
	if err != nil {
		return err
	}
	if !settings.Rules.Enabled || settings.Validate() != nil {
		reject()
		return nil
	}
	// Do not update loadout while waiting for an already accepted start.
	if s.Room != nil && (s.Room.Stage != "room" || s.Room.Members[s.UID] == nil || s.Room.Members[s.UID].Ready) {
		reject()
		return nil
	}
	account, err := storage2_1(h, h.Store.RoleManager().Snapshot, s.UID)
	if err != nil {
		return err
	}
	instance := protocol.ReadUint32(m.Payload, 0)
	var item []byte
	for _, p := range account.Inventory {
		if len(p) == 68 && protocol.ReadUint32(p, 0) == instance {
			item = p
			break
		}
	}
	if item == nil || item[4] != protocol.ItemTalisman || protocol.ReadUint32(item, 19) == 2 || protocol.ReadUint32(item, 19) == 0xffffffff {
		reject()
		return nil
	}
	var rule persistence.TalismanRepairRule
	for _, r := range settings.Rules.Repairs {
		if r.Item == protocol.ReadUint32(item, 5) {
			rule = r
			break
		}
	}
	if !rule.Valid() {
		reject()
		return nil
	}
	if m.ID == 4202 {
		p := make([]byte, 32)
		protocol.WriteUint32(p, 0, instance)
		protocol.WriteUint32(p, 4, rule.Item)
		protocol.WriteUint32(p, 8, rule.Material)
		protocol.WriteUint32(p, 16, uint32(rule.Quantity))
		protocol.WriteUint32(p, 20, uint32(protocol.ReadUint16(item, 23)))
		protocol.WriteUint32(p, 24, uint32(rule.Capacity))
		s.TalismanQuote = &talismanQuote{instance, settings.Revision, rule}
		s.sendGame(protocol.Message{ID: 4203, Payload: p})
		return nil
	}
	if protocol.ReadUint32(m.Payload, 4) != rule.Material || protocol.ReadUint32(m.Payload, 8) != 0 {
		reject()
		return nil
	}
	operation := fmt.Sprintf("%s:%d:%d", s.Namespace, ch.ID, ch.Sequence)
	if settings.Revision != 0 {
		quote := s.TalismanQuote
		if quote == nil || quote.Instance != instance || quote.Revision != settings.Revision || quote.Rule != rule {
			reject()
			return nil
		}
		_, err = storage2_5(h, h.Store.ItemManager().RepairTalismanConfigured, s.UID, operation, instance, quote.Rule, quote.Revision)
	} else {
		_, err = storage2_4(h, h.Store.ItemManager().RepairTalisman, s.UID, operation, instance, rule)
	}
	if err != nil {
		reject()
		return nil
	}

	account, err = storage2_1(h, h.Store.RoleManager().Snapshot, s.UID)
	if err != nil {
		return err
	}
	s.syncInventory(account.Inventory)
	ack := make([]byte, 8)
	protocol.WriteUint32(ack, 0, instance)
	s.sendGame(protocol.Message{ID: 4205, Payload: ack})
	return nil
}
