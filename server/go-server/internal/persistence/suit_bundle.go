package persistence

import (
	"database/sql"
	"errors"
	"kungfu.local/server/internal/protocol"
	"time"
)

// OpenSuit consumes an owned package and equips its server-configured parts
// atomically. A retired instance remains reserved, so replay cannot grant again.
func (m *EquipmentManager) OpenSuit(uid uint64, instance uint32, bundles map[uint32][]uint32) ([][]byte, error) {
	tx, txCancel, err := beginTransaction(m.store.DB)
	defer txCancel()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var owner uint64
	if err = tx.QueryRow(`SELECT uid FROM accounts WHERE uid=? FOR UPDATE`, uid).Scan(&owner); err != nil {
		return nil, err
	}
	if err = expireInventory(tx, uid, time.Now().Unix()); err != nil {
		return nil, err
	}
	var source []byte
	if err = tx.QueryRow(`SELECT record FROM inventory WHERE uid=? AND instance=?`, uid, instance).Scan(&source); err != nil {
		return nil, err
	}
	// Newly purchased legacy suit templates may still have count zero.
	// Normalize under the owner lock, just as the inventory snapshot does.
	normalizeClothingMenu(source)
	if !usableItem(source) || source[protocol.InventoryKindOffset] != protocol.ItemSuit || protocol.ReadUint16(source, inventoryMenuCountOffset) != 1 {
		return nil, ErrDenied
	}
	parts := bundles[protocol.ReadUint32(source, protocol.InventoryItemIDOffset)]
	if len(parts) == 0 {
		return nil, ErrDenied
	}
	seen := map[uint16]bool{}
	for _, item := range parts {
		kind := byte(item / 10000)
		slot := defaultEquipmentSlot(kind)
		if item/10000 < uint32(protocol.ItemTop) || item/10000 > uint32(protocol.ItemGloves) || slot == 0 || seen[slot] {
			return nil, ErrDenied
		}
		seen[slot] = true
	}
	var deadline sql.NullInt64
	err = tx.QueryRow(`SELECT expires_at FROM inventory_expirations WHERE uid=? AND instance=?`, uid, instance).Scan(&deadline)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var granted [][]byte
	for _, item := range parts {
		template := make([]byte, protocol.InventoryRecordSize)
		template[protocol.InventoryKindOffset] = byte(item / 10000)
		protocol.WriteUint32(template, protocol.InventoryItemIDOffset, item)
		protocol.WriteUint32(template, inventoryDurationOffset, permanentDisplayMinutes/60)
		added, e := m.store.InventoryManager().AddItem(tx, uid, template, 0)
		if e != nil {
			return nil, e
		}
		id := protocol.ReadUint32(added, 0)
		if deadline.Valid {
			if _, e = tx.Exec(`INSERT INTO inventory_expirations(uid,instance,expires_at) VALUES(?,?,?)`, uid, id, deadline.Int64); e != nil {
				return nil, e
			}
		}
		equipped, e := equipInTransaction(tx, uid, id, 0, true)
		if e != nil {
			return nil, e
		}
		granted = append(granted, equipped)
	}
	protocol.WriteUint16(source, protocol.InventorySlotOffset, 0)
	protocol.WriteUint16(source, inventoryMenuCountOffset, 0)
	protocol.WriteUint32(source, inventoryStateOffset, 0xffffffff)
	if _, err = tx.Exec(`UPDATE inventory SET record=? WHERE uid=? AND instance=?`, source, uid, instance); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(`DELETE FROM inventory_expirations WHERE uid=? AND instance=?`, uid, instance); err != nil {
		return nil, err
	}
	return granted, tx.Commit()
}
