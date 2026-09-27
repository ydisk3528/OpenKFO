package game

import (
	"bytes"

	"kungfu.local/server/internal/protocol"
)

func (hub *Hub) consume(session *Session, channel *Channel, message protocol.Message) error {
	if channel.Phase != "battle" || session.Room == nil || session.Room.isObserver(session) {
		return nil
	}
	payload := message.Payload
	account, err := storage2_1(hub, hub.Store.RoleManager().Snapshot, session.UID)
	if err != nil {
		return err
	}
	if message.ID == 4200 {
		if len(payload) != 4 {
			return protocol.ErrFrame
		}
		instance := protocol.ReadUint32(payload, 0)
		record, err := account.Consumable(instance, 0)
		if err != nil || protocol.ReadUint16(record, 23) == 0 {
			return nil
		}
		if session.ConsumeIntents == nil {
			session.ConsumeIntents = map[uint32]bool{}
		}
		session.ConsumeIntents[instance] = true
		return nil
	}
	if len(payload) != 75 || payload[12] != 1 || payload[13] != 1 || protocol.ReadUint64(payload, 4) != session.UID || protocol.ReadUint64(payload, 59) != session.UID || protocol.ReadUint32(payload, 67) != uint32(session.Room.ID) || protocol.ReadUint32(payload, 71) != session.Room.Serial {
		return protocol.ErrFrame
	}
	slot := protocol.ReadUint32(payload, 39)
	if slot != 27 && slot != 28 {
		return protocol.ErrFrame
	}
	record, err := account.Consumable(0, uint16(slot))
	if err != nil {
		return nil
	}
	instance := protocol.ReadUint32(record, 0)
	signature := append(bytes.Clone(payload[23:27]), payload[39:]...)
	applied, err := storage2_6(hub, hub.Store.InventoryManager().Consume, session.UID, session.Room.Serial, protocol.ReadUint32(payload, 19), instance, signature, session.ConsumeIntents[instance])
	delete(session.ConsumeIntents, instance)
	if err != nil {
		return nil
	}
	if applied {
		// The native sender already applies the item locally. Relay exactly once
		// after the transaction commits, never echo the effect to its sender.
		hub.broadcast(session.Room, message, session.UID)
	}
	account, err = storage2_1(hub, hub.Store.RoleManager().Snapshot, session.UID)
	if err != nil {
		return err
	}
	response := make([]byte, 34)
	protocol.WriteUint64(response, 0, session.UID)
	for _, offsets := range [][4]int{{27, 8, 18, 26}, {28, 12, 22, 30}} {
		item, err := account.Consumable(0, uint16(offsets[0]))
		if err != nil {
			continue
		}
		copy(response[offsets[1]:offsets[1]+4], item[:4])
		copy(response[offsets[2]:offsets[2]+4], item[9:13])
		copy(response[offsets[3]:offsets[3]+2], item[23:25])
	}
	session.sendGame(protocol.Message{ID: 4210, Payload: response})
	return nil
}
