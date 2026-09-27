package game

import (
	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
)

// 8218C0: target at +8, equipment count at +16, profile at +17 (360
// bytes), then 68-byte equipment records consumed by A3E1B0. The first
// eight bytes are not read by the handler and remain reserved.
func playerDetails(account persistence.Account) (protocol.Message, error) {
	if len(account.Profile) != 360 || (account.Profile[122] != 1 && account.Profile[122] != 2) {
		return protocol.Message{}, protocol.ErrFrame
	}
	payload := make([]byte, 377)
	protocol.WriteUint64(payload, 8, account.UID)
	copy(payload[17:], account.Profile)
	count := 0
	for _, item := range account.Inventory {
		if len(item) != 68 {
			return protocol.Message{}, protocol.ErrFrame
		}
		if protocol.ReadUint16(item, 17) == 0 {
			continue
		}
		count++
		if count > 255 {
			return protocol.Message{}, protocol.ErrFrame
		}
		payload = append(payload, item...)
	}
	payload[16] = byte(count)
	return protocol.Message{ID: 2421, Payload: payload}, nil
}

// Native 81E850 consumes 2591 as 68-byte records with no UID/count/profile header.
func playerEquipment(account persistence.Account) (protocol.Message, error) {
	payload := make([]byte, 0)
	for _, record := range account.Inventory {
		if len(record) != protocol.InventoryRecordSize {
			return protocol.Message{}, protocol.ErrFrame
		}
		if protocol.ReadUint16(record, protocol.InventorySlotOffset) == protocol.SlotUnequipped {
			continue
		}
		payload = append(payload, record...)
	}
	return protocol.Message{ID: protocol.MsgPlayerEquipment, Payload: payload}, nil
}
func (h *Hub) inspectEquipment(s *Session, payload []byte) error {
	if len(payload) != 8 {
		return protocol.ErrFrame
	}
	target := protocol.ReadUint64(payload, 0)
	if target == 0 {
		s.sendGame(notice("未找到该玩家的装备。"))
		return nil
	}
	account, err := storage2_1(h, h.Store.RoleManager().Snapshot, target)
	if err != nil {
		s.sendGame(notice("未找到该玩家的装备。"))
		return nil
	}
	reply, err := playerEquipment(account)
	if err != nil {
		s.sendGame(notice("该玩家的装备暂时无法显示。"))
		return nil
	}
	// Do not merge another player's instances into the viewer's inventory,
	// or send equip/room-entry notifications during this read-only inspection.
	s.sendGame(reply)
	return nil
}
