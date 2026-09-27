package persistence

import (
	"database/sql"
	"errors"
	"kungfu.local/server/internal/protocol"
	"time"
)

const (
	inventoryMenuCountOffset        = 23
	inventoryDurationOffset         = 13
	inventoryStateOffset            = 19
	inventoryUnused          uint32 = 0
	inventoryActive          uint32 = 1
	inventoryExpired         uint32 = 2
	permanentDisplayMinutes  uint32 = 365 * 24 * 60
)

// Slot assignment is an activation, including starter equipment and old records.
// Never invent a deadline from the native display duration: no expiry row means
// permanent ownership. Quantity/durability and attachment keys stay untouched.
func activateEquippedItem(record []byte, deadline sql.NullInt64, now int64) bool {
	if len(record) != protocol.InventoryRecordSize || protocol.ReadUint16(record, protocol.InventorySlotOffset) == protocol.SlotUnequipped || protocol.ReadUint32(record, inventoryStateOffset) != inventoryUnused {
		return false
	}
	protocol.WriteUint32(record, inventoryStateOffset, inventoryActive)
	projectItemMinutes(record, deadline, now)
	return true
}

func projectItemMinutes(record []byte, deadline sql.NullInt64, now int64) {
	minutes := permanentDisplayMinutes
	if deadline.Valid {
		if deadline.Int64 <= now {
			minutes = 0
			protocol.WriteUint32(record, inventoryStateOffset, inventoryExpired)
			protocol.WriteUint16(record, protocol.InventorySlotOffset, protocol.SlotUnequipped)
		} else {
			minutes = uint32(min((uint64(deadline.Int64)-uint64(now)+59)/60, uint64(0x7fffffff)))
		}
	}
	protocol.WriteUint32(record, inventoryDurationOffset, minutes)
}

func activateEquipmentInTransaction(tx *sql.Tx, uid uint64, record []byte, now int64) error {
	normalizeClothingMenu(record)
	if protocol.ReadUint16(record, protocol.InventorySlotOffset) == protocol.SlotUnequipped {
		return nil
	}
	var deadline sql.NullInt64
	err := tx.QueryRow(`SELECT expires_at FROM inventory_expirations WHERE uid=? AND instance=?`, uid, protocol.ReadUint32(record, 0)).Scan(&deadline)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	activateEquippedItem(record, deadline, now)
	if protocol.ReadUint32(record, inventoryStateOffset) == inventoryActive {
		projectItemMinutes(record, deadline, now)
	}
	return nil
}

// Upgrade legacy equipped records under the same owner lock as equip/use. The
// caller has already closed its inventory cursor; no nested connection is held.
func (s *Store) normalizeEquippedInventory(a *Account) error {
	needed := false
	for _, record := range a.Inventory {
		if suitPackageNeedsRepair(record) || clothingMenuNeedsRepair(record) || (protocol.ReadUint16(record, protocol.InventorySlotOffset) != protocol.SlotUnequipped && protocol.ReadUint32(record, inventoryStateOffset) == inventoryUnused) {
			needed = true
			break
		}
	}
	if !needed {
		return nil
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var owner uint64
	if err = tx.QueryRow(`SELECT uid FROM accounts WHERE uid=? FOR UPDATE`, a.UID).Scan(&owner); err != nil {
		return err
	}
	for i, original := range a.Inventory {
		if !suitPackageNeedsRepair(original) && !clothingMenuNeedsRepair(original) && (protocol.ReadUint16(original, protocol.InventorySlotOffset) == protocol.SlotUnequipped || protocol.ReadUint32(original, inventoryStateOffset) != inventoryUnused) {
			continue
		}
		var record []byte
		err = tx.QueryRow(`SELECT record FROM inventory WHERE uid=? AND instance=?`, a.UID, protocol.ReadUint32(original, 0)).Scan(&record)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if len(record) != protocol.InventoryRecordSize {
			return ErrDenied
		}
		if err = activateEquipmentInTransaction(tx, a.UID, record, time.Now().Unix()); err != nil {
			return err
		}
		if _, err = tx.Exec(`UPDATE inventory SET record=? WHERE uid=? AND instance=?`, record, a.UID, protocol.ReadUint32(record, 0)); err != nil {
			return err
		}
		a.Inventory[i] = record
	}
	return tx.Commit()
}

// Native 8A3760 copies WORD record+23 to UI+20. 8B1700 selects the
// Equip menu only when that count is zero; nonzero enables Use instead.
// Clothing is not stackable. Do not touch consumable counts or pet durability.
func clothingMenuNeedsRepair(record []byte) bool {
	return len(record) == protocol.InventoryRecordSize && (record[protocol.InventoryKindOffset] >= protocol.ItemTop && record[protocol.InventoryKindOffset] <= protocol.ItemGloves || record[protocol.InventoryKindOffset] == 20 || record[protocol.InventoryKindOffset] == 21) && protocol.ReadUint16(record, inventoryMenuCountOffset) != 0
}
func suitPackageNeedsRepair(record []byte) bool {
	return len(record) == protocol.InventoryRecordSize && record[protocol.InventoryKindOffset] == protocol.ItemSuit && protocol.ReadUint32(record, inventoryStateOffset) <= inventoryActive && (protocol.ReadUint16(record, inventoryMenuCountOffset) == 0 || protocol.ReadUint16(record, protocol.InventorySlotOffset) != 0)
}
func normalizeClothingMenu(record []byte) {
	if suitPackageNeedsRepair(record) {
		protocol.WriteUint16(record, protocol.InventorySlotOffset, 0)
		if protocol.ReadUint16(record, inventoryMenuCountOffset) == 0 {
			protocol.WriteUint16(record, inventoryMenuCountOffset, 1)
		}
		return
	}

	if !clothingMenuNeedsRepair(record) {
		return
	}
	protocol.WriteUint16(record, inventoryMenuCountOffset, 0)
	// Legacy starters used count=1 with no display lifetime. Preserve warehouse
	// visibility using the existing permanent display convention, not a count.
	if protocol.ReadUint32(record, inventoryDurationOffset) == 0 {
		switch protocol.ReadUint32(record, inventoryStateOffset) {
		case inventoryUnused:
			protocol.WriteUint32(record, inventoryDurationOffset, permanentDisplayMinutes/60)
		case inventoryActive:
			protocol.WriteUint32(record, inventoryDurationOffset, permanentDisplayMinutes)
		}
	}
}
