package persistence

import (
	"database/sql"
	"kungfu.local/server/internal/protocol"
	"time"
)

// Deadlines are explicit server UTC Unix seconds, never inferred from the
// client's 365+ display value. No row means permanent ownership.
func expireInventory(tx *sql.Tx, uid uint64, now int64) error {
	rows, err := tx.Query(`SELECT i.instance,i.record FROM inventory i JOIN inventory_expirations e ON e.uid=i.uid AND e.instance=i.instance WHERE i.uid=? AND e.expires_at<=? AND e.processed=FALSE ORDER BY i.instance FOR UPDATE`, uid, now)
	if err != nil {
		return err
	}
	type item struct {
		id     uint32
		record []byte
	}
	var items []item
	for rows.Next() {
		var i item
		if err = rows.Scan(&i.id, &i.record); err != nil {
			rows.Close()
			return err
		}
		if len(i.record) != 68 {
			rows.Close()
			return ErrDenied
		}
		items = append(items, i)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, i := range items {
		protocol.WriteUint16(i.record, 17, 0)
		if protocol.ReadUint32(i.record, 19) != 0xffffffff {
			protocol.WriteUint32(i.record, 19, 2)
		}
		if _, err = tx.Exec(`UPDATE inventory SET record=? WHERE uid=? AND instance=?`, i.record, uid, i.id); err != nil {
			return err
		}
		if _, err = tx.Exec(`UPDATE inventory_expirations SET processed=TRUE WHERE uid=? AND instance=?`, uid, i.id); err != nil {
			return err
		}
	}
	return nil
}

func (m *InventoryManager) ExpireInventory(uid uint64) error {
	// The indexed fast path leaves permanent accounts read-only.
	var due bool
	if err := m.store.DB.QueryRow(`SELECT EXISTS(SELECT 1 FROM inventory_expirations WHERE uid=? AND expires_at<=? AND processed=FALSE)`, uid, time.Now().Unix()).Scan(&due); err != nil {
		return err
	}
	if !due {
		return nil
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
	return tx.Commit()
}

func usableItem(record []byte) bool {
	return len(record) == 68 && protocol.ReadUint32(record, 19) != 2 && protocol.ReadUint32(record, 19) != 0xffffffff
}

// IsExpiredInventoryRecord uses the persisted state, never the display duration.
func IsExpiredInventoryRecord(record []byte) bool {
	return len(record) == protocol.InventoryRecordSize && protocol.ReadUint32(record, 19) == inventoryExpired
}
