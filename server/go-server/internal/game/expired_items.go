package game

import (
	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
)

func (h *Hub) expiredItems(s *Session) error {
	// Snapshot processes explicit server deadlines first. Displayed duration
	// (including 365+) is never interpreted as a server expiration timestamp.
	account, err := storage2_1(h, h.Store.RoleManager().Snapshot, s.UID)
	if err != nil {
		return err
	}
	reply, err := expiredItemsPacket(account.Inventory)
	if err != nil {
		return err
	}
	s.sendGame(reply)
	return nil
}
func expiredItemsPacket(inventory [][]byte) (protocol.Message, error) {
	payload := make([]byte, 4)
	var count uint32
	for _, item := range inventory {
		if len(item) != protocol.InventoryRecordSize {
			return protocol.Message{}, protocol.ErrFrame
		}
		if persistence.IsExpiredInventoryRecord(item) {
			payload = append(payload, item[:4]...)
			count++
		}
	}
	protocol.WriteUint32(payload, 0, count)
	return protocol.Message{ID: protocol.MsgExpiredItems, Payload: payload}, nil
}
