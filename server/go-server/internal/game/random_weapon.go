package game

import (
	"bytes"
	"errors"
	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
	"log"
)

// Temporarily disabled until native start-of-battle synchronization is verified.
const randomWeaponsEnabled = false

func (s *Session) sendRandomWeaponOff() {
	// A27180 needs a non-null payload; clear mode before the zero-record UI
	// refresh so 8B44C0 does not display its failed-random-selection warning.
	s.sendGame(protocol.Message{ID: protocol.MsgRandomWeaponCancelled, Payload: []byte{0}})
	s.sendGame(protocol.Message{ID: protocol.MsgRandomWeaponResult, Payload: protocol.RandomWeaponRecord(nil)})
}

func (h *Hub) randomWeapon(s *Session, message protocol.Message) error {
	mode := s.RandomWeaponMode
	if message.ID == protocol.MsgRandomWeaponSet {
		if len(message.Payload) != 4 {
			return nil
		}
		mode = protocol.ReadUint32(message.Payload, 0)
		if mode > protocol.RandomWeaponAll {
			return nil
		}
	} else if len(message.Payload) != 0 {
		return nil
	}
	// 21428 is also emitted on room/UI transitions, not a request to cancel.
	// 21424 asks for saved equipment after 21422. This implementation applies
	// the owned selection directly, so it has no temporary loadout to restore.
	if message.ID == protocol.MsgRandomWeaponCancelAck || message.ID == protocol.MsgRandomWeaponEquipmentQuery {
		return nil
	}
	if !randomWeaponsEnabled {
		s.RandomWeaponMode = protocol.RandomWeaponOff
		s.sendRandomWeaponOff()
		if message.ID == protocol.MsgRandomWeaponSet {
			s.sendGame(notice("暂不支持随机武器"))
		}
		return nil
	}
	if s.Room != nil {
		member := s.Room.Members[s.UID]
		if s.Room.Stage != "room" || member == nil || member.Ready {
			s.restoreRandomWeaponUI()
			s.sendGame(notice("请取消准备后再调整随机武器。"))
			return nil
		}
	}
	return h.selectRandomWeapon(s, mode, message.ID == protocol.MsgRandomWeaponSet)
}

func (h *Hub) selectRandomWeapon(s *Session, mode uint32, reroll bool) error {
	record, err := storage2_4(h, h.Store.SelectRandomWeapon, s.UID, mode, h.Config.RandomWeaponTypes, reroll)
	if errors.Is(err, persistence.ErrDenied) {
		// Restore the actual persisted mode, since the native picker changes its
		// local mode before sending 21423. Do not grant a fallback weapon.
		s.restoreRandomWeaponUI()
		s.sendGame(notice("没有符合此随机类型的可用武器，请检查背包、有效期和第二武器槽。"))
		return nil
	}
	if err != nil {
		return err
	}
	s.RandomWeaponMode = mode
	if mode == protocol.RandomWeaponOff {
		s.sendRandomWeaponOff()
		return nil
	}
	account, err := storage2_1(h, h.Store.RoleManager().Snapshot, s.UID)
	if err != nil {
		return err
	}
	changed := !bytes.Equal(s.Inventory[protocol.ReadUint32(record, 0)], record)
	s.syncUnequippedInventory(account.Inventory)
	s.sendGame(protocol.RandomWeaponPreferences(s.UID, mode))
	s.sendGame(protocol.Message{ID: protocol.MsgRandomWeaponResult, Payload: protocol.RandomWeaponRecord(record)})
	if changed {
		h.broadcastEquipment(s, account)
	}
	log.Printf("random_weapon uid=%d mode=%d instance=%d item=%d reroll=%t", s.UID, mode, protocol.ReadUint32(record, 0), protocol.ReadUint32(record, protocol.InventoryItemIDOffset), reroll)
	return nil
}

// The picker changes local state optimistically. Restore both mode and icon on
// rejection without changing persisted inventory or rerolling the weapon.
func (s *Session) restoreRandomWeaponUI() {
	s.sendGame(protocol.RandomWeaponPreferences(s.UID, s.RandomWeaponMode))
	if s.RandomWeaponMode == protocol.RandomWeaponOff {
		s.sendRandomWeaponOff()
		return
	}
	for _, record := range s.Inventory {
		if len(record) == protocol.InventoryRecordSize && record[protocol.InventoryKindOffset] == protocol.ItemWeapon && protocol.ReadUint16(record, protocol.InventorySlotOffset) == protocol.SlotPrimaryWeapon {
			s.sendGame(protocol.Message{ID: protocol.MsgRandomWeaponResult, Payload: protocol.RandomWeaponRecord(record)})
			return
		}
	}
}
