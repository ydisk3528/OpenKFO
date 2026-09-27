package persistence

import (
	"kungfu.local/server/internal/protocol"
	"time"
)

// ConsumeWeaponSwitchCard locks the account shared by equipment and GM writes.
// Switching the active hand is battle state; equipment slots stay unchanged.
func (m *InventoryManager) ConsumeWeaponSwitchCard(uid uint64) (uint32, error) {
	tx, txCancel, err := beginTransaction(m.store.DB)
	defer txCancel()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var owner uint64
	if err = tx.QueryRow(`SELECT uid FROM accounts WHERE uid=? FOR UPDATE`, uid).Scan(&owner); err != nil {
		return 0, err
	}
	if err = expireInventory(tx, uid, time.Now().Unix()); err != nil {
		return 0, err
	}
	rows, err := tx.Query(`SELECT record FROM inventory WHERE uid=? ORDER BY instance`, uid)
	if err != nil {
		return 0, err
	}
	var items [][]byte
	for rows.Next() {
		var p []byte
		if err = rows.Scan(&p); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	card, err := weaponSwitchCard(items)
	if err != nil {
		return 0, err
	}
	remaining := protocol.ReadUint16(card, 23) - 1
	protocol.WriteUint16(card, 23, remaining)
	if _, err = tx.Exec(`UPDATE inventory SET record=? WHERE uid=? AND instance=?`, card, uid, protocol.ReadUint32(card, 0)); err != nil {
		return 0, err
	}
	return uint32(remaining), tx.Commit()
}

func weaponSwitchCard(items [][]byte) ([]byte, error) {
	var card []byte
	var primary, secondary int
	for _, p := range items {
		if !usableItem(p) {
			continue
		}
		switch p[4] {
		case protocol.ItemWeapon:
			switch protocol.ReadUint16(p, 17) {
			case protocol.SlotPrimaryWeapon:
				primary++
			case protocol.SlotSecondaryWeapon:
				secondary++
			}
		case protocol.ItemWeaponSwitchCard:
			// Native 9CD8E0 updates the first type-74 stack, so ambiguous
			// multiple stacks must not silently consume a different one.
			if card != nil {
				return nil, ErrDenied
			}
			card = p
		}
	}
	if primary != 1 || secondary != 1 || card == nil || protocol.ReadUint16(card, 23) == 0 {
		return nil, ErrDenied
	}
	return card, nil
}
