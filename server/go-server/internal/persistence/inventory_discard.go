package persistence

import (
	"database/sql"
	"errors"
	"kungfu.local/server/internal/protocol"
	"time"
)

// Discard retires one complete instance (2130 has no quantity field).
// Keep its ID reserved: MAX(instance)+1 allocation must not reuse an ID that
// an old/repeated discard request can still reference.
func (m *InventoryManager) Discard(uid uint64, instance uint32) error {
	if uid == 0 || instance == 0 {
		return ErrDenied
	}
	tx, txCancel, err := beginTransaction(m.store.DB)
	defer txCancel()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var owner uint64
	if err = tx.QueryRow(`SELECT uid FROM accounts WHERE uid=? FOR UPDATE`, uid).Scan(&owner); err != nil {
		return err
	}
	if err = expireInventory(tx, uid, time.Now().Unix()); err != nil {
		return err
	}
	var item []byte
	err = tx.QueryRow(`SELECT record FROM inventory WHERE uid=? AND instance=? FOR UPDATE`, uid, instance).Scan(&item)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrDenied
	}
	if err != nil {
		return err
	}
	if !discardableItem(item, instance) {
		return ErrDenied
	}
	// Keep a usable replacement for essential clothing and weapons. This is
	// server policy; a forged request cannot bypass the warehouse UI checks.
	kind := item[4]
	if (kind >= 12 && kind <= 17) || kind == protocol.ItemWeapon {
		rows, err := tx.Query(`SELECT record FROM inventory WHERE uid=?`, uid)
		if err != nil {
			return err
		}
		replacement := false
		for rows.Next() {
			var r []byte
			if err = rows.Scan(&r); err != nil {
				rows.Close()
				return err
			}
			if usableItem(r) && r[4] == kind && protocol.ReadUint32(r, 0) != instance {
				replacement = true
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if !replacement {
			return ErrDenied
		}
	}
	protocol.WriteUint32(item, 19, 0xffffffff)
	result, err := tx.Exec(`UPDATE inventory SET record=? WHERE uid=? AND instance=?`, item, uid, instance)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrDenied
	}
	if _, err = tx.Exec(`DELETE FROM inventory_expirations WHERE uid=? AND instance=?`, uid, instance); err != nil {
		return err
	}
	return tx.Commit()
}

func discardableItem(r []byte, instance uint32) bool {
	if len(r) != protocol.InventoryRecordSize || instance == 0 || protocol.ReadUint32(r, 0) != instance || protocol.ReadUint32(r, 5) == 0 || protocol.ReadUint16(r, 17) != 0 || protocol.ReadUint32(r, 19) > 2 {
		return false
	}
	// Restrict to implemented physical inventory categories. Membership and
	// other special account entitlements need their own cancellation semantics.
	if len(Slots[r[4]]) != 0 {
		return true
	}
	switch r[4] {
	case protocol.ItemSuit:
		// A suit package is discardable even though it has no equipment slot.
		return true
	case 60, protocol.ItemExperienceCard, protocol.ItemWeaponSwitchCard:
		return protocol.ReadUint16(r, 23) > 0
	}
	return false
}
