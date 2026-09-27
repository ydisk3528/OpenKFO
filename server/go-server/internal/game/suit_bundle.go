package game

import "kungfu.local/server/internal/protocol"

func (h *Hub) openSuit(s *Session, p []byte) error {
	r, err := protocol.ParseRenewalRequest(p)
	if err != nil || r.SenderUID() != s.UID || r.RecipientUID() != s.UID || r.Operation() != 105 {
		return nil
	}
	return h.consumeSuit(s, r.InventoryInstance(), true)
}

func (h *Hub) consumeSuit(s *Session, instance uint32, confirm bool) error {
	if s.Room != nil {
		m := s.Room.Members[s.UID]
		if s.Room.Stage != "room" || m == nil || m.Ready {
			return nil
		}
	}
	parts, err := storage2_3(h, h.Store.EquipmentManager().OpenSuit, s.UID, instance, h.Config.SuitBundles)
	if err != nil {
		s.sendGame(notice("套装使用失败：请检查套装是否可用、部件配置是否完整。"))
		return nil
	}
	a, err := storage2_1(h, h.Store.RoleManager().Snapshot, s.UID)
	if err != nil {
		return err
	}
	// Complete the native confirmation while its source instance still exists.
	if confirm {
		s.sendGame(protocol.Message{ID: protocol.MsgRenewItemResult, Payload: append([]byte{1}, protocol.Uint32Bytes(instance)...)})
	}
	for _, part := range parts {
		s.sendGame(protocol.Message{ID: protocol.MsgItemAdded, Payload: part})
		s.Inventory[protocol.ReadUint32(part, 0)] = part
	}
	for _, part := range parts {
		request := make([]byte, 16)
		protocol.WriteUint32(request, 0, protocol.ReadUint32(part, 0))
		protocol.WriteUint32(request, 4, uint32(protocol.ReadUint16(part, 17)))
		s.syncEquipmentChange(protocol.Message{ID: protocol.MsgEquipItem, Payload: request}, part, a.Inventory)
	}
	h.broadcastEquipment(s, a)
	return nil
}
