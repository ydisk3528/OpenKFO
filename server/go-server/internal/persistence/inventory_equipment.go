package persistence

import (
	"database/sql"
	"errors"
	"kungfu.local/server/internal/protocol"
	"time"
)

// Native warehouse 8B3919/8B3924 selects slot 37 for type 30 before 2080.
var Slots = map[byte][]uint16{protocol.ItemTop: {4}, protocol.ItemFace: {3}, protocol.ItemShoes: {7}, protocol.ItemHair: {2}, protocol.ItemPants: {6}, protocol.ItemGloves: {5}, 20: {10}, 21: {11}, protocol.ItemWeapon: {protocol.SlotPrimaryWeapon, protocol.SlotSecondaryWeapon}, protocol.ItemTalisman: {protocol.SlotPrimaryTalisman, protocol.SlotSecondaryTalisman}, protocol.ItemConsumable: {protocol.SlotPrimaryConsumable, protocol.SlotSecondaryConsumable}, protocol.ItemPhantomCard: {protocol.SlotPhantomCard}, protocol.ItemPersonalIcon: {protocol.SlotPersonalIcon}, protocol.ItemDecorativeTitle: {protocol.SlotDecorativeTitle}}

// Current native 660CE0 default-slot switch. Only include types whose explicit
// equipment paths are already supported here. Suits are consumed as packages,
// not equipped as a single clothing record.
func defaultEquipmentSlot(kind byte) uint16 {
	switch kind {
	case protocol.ItemTop:
		return 4
	case protocol.ItemFace:
		return 3
	case protocol.ItemShoes:
		return 7
	case protocol.ItemHair:
		return 2
	case protocol.ItemPants:
		return 6
	case protocol.ItemGloves:
		return 5
	case 20:
		return 10
	case 21:
		return 11
	case protocol.ItemWeapon:
		return protocol.SlotPrimaryWeapon
	case protocol.ItemPhantomCard:
		return protocol.SlotPhantomCard
	case protocol.ItemPersonalIcon:
		return protocol.SlotPersonalIcon
	case protocol.ItemDecorativeTitle:
		return protocol.SlotDecorativeTitle
	}
	return 0
}

func (m *EquipmentManager) Equip(uid uint64, instance uint32, slot uint16) ([]byte, error) {
	return m.store.EquipmentManager().equip(uid, instance, slot, false)
}

// EquipDefault resolves the warehouse's zero slot inside the ownership transaction.
// Zero still means unequip for Equip callers (2300).
func (m *EquipmentManager) EquipDefault(uid uint64, instance uint32, slot uint16) ([]byte, error) {
	return m.store.EquipmentManager().equip(uid, instance, slot, true)
}

func (m *EquipmentManager) equip(uid uint64, instance uint32, slot uint16, automatic bool) ([]byte, error) {
	transaction, transactionCancel, err := beginTransaction(m.store.DB)
	defer transactionCancel()
	if err != nil {
		return nil, err
	}
	defer transaction.Rollback()
	var owner uint64
	if err = transaction.QueryRow(`SELECT uid FROM accounts WHERE uid=? FOR UPDATE`, uid).Scan(&owner); err != nil {
		return nil, err
	}
	if err = expireInventory(transaction, uid, time.Now().Unix()); err != nil {
		return nil, err
	}
	record, err := equipInTransaction(transaction, uid, instance, slot, automatic)
	if err != nil {
		return nil, err
	}
	if record != nil {
		if _, err = transaction.Exec(`DELETE FROM random_weapon_settings WHERE uid=?`, uid); err != nil {
			return nil, err
		}
	}
	return record, transaction.Commit()
}

func equipInTransaction(transaction *sql.Tx, uid uint64, instance uint32, slot uint16, automatic bool) ([]byte, error) {
	var record []byte
	err := transaction.QueryRow(`SELECT record FROM inventory WHERE uid=? AND instance=?`, uid, instance).Scan(&record)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDenied
	}
	if err != nil {
		return nil, err
	}
	if len(record) != protocol.InventoryRecordSize {
		return nil, ErrDenied
	}
	if (automatic || slot != protocol.SlotUnequipped) && !usableItem(record) {
		return nil, ErrDenied
	}
	if automatic && slot == protocol.SlotUnequipped {
		slot = defaultEquipmentSlot(record[4])
		if slot == protocol.SlotUnequipped {
			return nil, ErrDenied
		}
	}
	if slot == protocol.SlotUnequipped && protocol.ReadUint16(record, 17) == 0 {
		return nil, nil
	}
	if slot != protocol.SlotUnequipped {
		allowed := false
		for _, allowedSlot := range Slots[record[4]] {
			allowed = allowed || allowedSlot == slot
		}
		if !allowed {
			return nil, ErrDenied
		}
		rows, err := transaction.Query(`SELECT instance,record FROM inventory WHERE uid=?`, uid)
		if err != nil {
			return nil, err
		}
		type change struct {
			instance uint32
			record   []byte
		}
		var edits []change
		for rows.Next() {
			var change change
			if err = rows.Scan(&change.instance, &change.record); err != nil {
				rows.Close()
				return nil, err
			}
			if len(change.record) == protocol.InventoryRecordSize && protocol.ReadUint16(change.record, 17) == slot {
				protocol.WriteUint16(change.record, 17, 0)
				edits = append(edits, change)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		for _, change := range edits {
			if _, err = transaction.Exec(`UPDATE inventory SET record=? WHERE uid=? AND instance=?`, change.record, uid, change.instance); err != nil {
				return nil, err
			}
		}
	}
	protocol.WriteUint16(record, 17, slot)
	prepareAppearanceAttachment(record)
	if err = activateEquipmentInTransaction(transaction, uid, record, time.Now().Unix()); err != nil {
		return nil, err
	}
	if _, err = transaction.Exec(`UPDATE inventory SET record=? WHERE uid=? AND instance=?`, record, uid, instance); err != nil {
		return nil, err
	}
	return record, nil
}

// Native A19AE0 selects the model part for clothing only when DWORD +9 is
// nonzero. Zero leaves the attachment name empty during live equip, although
// reopening the wardrobe rebuilds the appearance. Preserve nonzero values:
// other consumers of this field have not been fully reconstructed.
func prepareAppearanceAttachment(record []byte) {
	if len(record) != protocol.InventoryRecordSize || protocol.ReadUint16(record, protocol.InventorySlotOffset) == protocol.SlotUnequipped {
		return
	}
	switch record[protocol.InventoryKindOffset] {
	case protocol.ItemTop, protocol.ItemFace, protocol.ItemShoes, protocol.ItemHair, protocol.ItemPants, protocol.ItemGloves:
		if protocol.ReadUint32(record, protocol.InventoryAppearanceMarkerOffset) == 0 {
			protocol.WriteUint32(record, protocol.InventoryAppearanceMarkerOffset, protocol.AppearanceAttachmentPresent)
		}
	}
}
