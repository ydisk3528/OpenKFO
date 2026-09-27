package game

import (
	"bytes"
	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
	"sort"
)

func (s *Session) rememberInventory(records [][]byte) {
	s.Inventory = make(map[uint32][]byte, len(records))
	for _, r := range records {
		s.Inventory[protocol.ReadUint32(r, 0)] = bytes.Clone(r)
	}
}

// 1120 initializes login inventory; native live equipment uses 2310/2090.
// In particular, 2090 only changes its own instance, so displaced equipment
// must receive 2310 before the new item is equipped (native 9C0E10).
func (s *Session) syncEquipmentChange(request protocol.Message, changed []byte, records [][]byte) {
	instance := protocol.ReadUint32(changed, 0)
	for _, record := range records {
		id := protocol.ReadUint32(record, 0)
		old := s.Inventory[id]
		if id != instance && len(old) == protocol.InventoryRecordSize && protocol.ReadUint16(old, 17) != protocol.SlotUnequipped && protocol.ReadUint16(record, 17) == protocol.SlotUnequipped {
			s.sendGame(protocol.Message{ID: protocol.MsgItemUnequipped, Payload: append(protocol.Uint32Bytes(id), record...)})
		}
	}
	prefix := bytes.Clone(request.Payload)
	responseID := uint32(protocol.MsgItemUnequipped)
	if request.ID == protocol.MsgEquipItem {
		responseID = protocol.MsgEquipmentChanged
		protocol.WriteUint32(prefix, 4, uint32(protocol.ReadUint16(changed, 17)))
	}
	s.sendGame(protocol.Message{ID: responseID, Payload: append(prefix, changed...)})
	// Preserve incremental updates for expiry/other inventory changes committed
	// by the equipment transaction; do not rely on a login-only inventory packet.
	s.syncInventory(records)
}

// Apply equipment removals only after returning to a safe lobby/room phase.
func (s *Session) syncUnequippedInventory(records [][]byte) bool {
	changedEquipment := false
	for _, record := range records {
		old := s.Inventory[protocol.ReadUint32(record, 0)]
		if len(old) == 68 && protocol.ReadUint16(old, 17) != 0 && protocol.ReadUint16(record, 17) == 0 {
			// Same 2310 form as successful native unequip: instance + item.
			s.sendGame(protocol.Message{ID: 2310, Payload: append(protocol.Uint32Bytes(protocol.ReadUint32(record, 0)), record...)})
			changedEquipment = true
		}
	}
	s.syncInventory(records)
	return changedEquipment
}

func (s *Session) syncInventory(records [][]byte) {
	seen := make(map[uint32]bool, len(records))
	var expired []uint32
	for _, r := range records {
		id := protocol.ReadUint32(r, 0)
		seen[id] = true
		old, exists := s.Inventory[id]
		if bytes.Equal(old, r) {
			continue
		}
		messageID := uint32(2160)
		if exists {
			messageID = 2161
			if protocol.ReadUint32(r, 19) == 2 && protocol.ReadUint32(old, 19) != 2 {
				expired = append(expired, id)
			}
		}
		s.sendGame(protocol.Message{ID: messageID, Payload: bytes.Clone(r)})
	}
	if len(expired) > 0 {
		sort.Slice(expired, func(i, j int) bool { return expired[i] < expired[j] })
		payload := protocol.Uint32Bytes(uint32(len(expired)))
		for _, id := range expired {
			payload = append(payload, protocol.Uint32Bytes(id)...)
		}
		// 8266E0 expects count + instance IDs. Update records first so its
		// item lookup finds the authoritative inactive, unequipped state.
		s.sendGame(protocol.Message{ID: 2121, Payload: payload})
	}
	// Current A2C2C0 accepts exactly one DWORD instance, calls 9CE160 to
	// erase that inventory entry and refreshes warehouse UI. It is not an
	// expiry list (2120/2121) and takes no count prefix.
	var deleted []uint32
	for id := range s.Inventory {
		if !seen[id] {
			deleted = append(deleted, id)
		}
	}
	sort.Slice(deleted, func(i, j int) bool { return deleted[i] < deleted[j] })
	for _, id := range deleted {
		s.sendGame(protocol.Message{ID: 2162, Payload: protocol.Uint32Bytes(id)})
	}
	s.rememberInventory(records)
	s.syncVIPIdentity(records)
}

// Unknown perks remain zero. Shop percentage is independently verified against
// persisted membership and rules, then rechecked inside the purchase transaction.
func vipIdentityPacket(kind uint32, percent ...uint32) protocol.Message {
	p := make([]byte, 28)
	protocol.WriteUint32(p, 0, kind)
	if len(percent) > 0 {
		protocol.WriteUint32(p, 20, percent[0])
	}
	return protocol.Message{ID: 1038, Payload: p}
}

func (h *Hub) refreshVIPShop(s *Session) error {
	rate := uint32(0)
	if s.VIPKind >= 2 {
		var err error
		rate, err = storage2_1(h, h.Store.ShopManager().VIPShopPercent, s.UID)
		if err != nil {
			return err
		}
	}
	if rate != s.VIPShopPercent {
		s.VIPShopPercent = rate
		s.sendGame(vipIdentityPacket(s.VIPKind, rate))
	}
	return nil
}

func (s *Session) syncVIPIdentity(records [][]byte) {
	kind := (persistence.Account{Inventory: records}).VIPKind()
	old := s.VIPKind
	if old == 0 {
		old = 1
	}
	s.VIPKind = kind
	if old != kind {
		s.VIPShopPercent = 0
		s.sendGame(vipIdentityPacket(kind))
	}
}

func weaponCollection(account persistence.Account) protocol.Message {
	ids := map[uint32]bool{}
	for _, r := range account.Inventory {
		if len(r) == 68 && r[4] == protocol.ItemWeapon && protocol.ReadUint32(r, 19) != 0xffffffff {
			ids[protocol.ReadUint32(r, 5)] = true
		}
	}
	ordered := make([]uint32, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	p := make([]byte, 20+9*len(ordered))
	protocol.WriteUint64(p, 8, account.UID)
	protocol.WriteUint32(p, 16, uint32(len(ordered)))
	for i, id := range ordered {
		protocol.WriteUint32(p, 20+i*9, id)
		p[20+i*9+8] = 1
	}
	return protocol.Message{ID: 2431, Payload: p}
}
