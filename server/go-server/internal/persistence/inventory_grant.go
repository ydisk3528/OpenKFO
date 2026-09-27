package persistence

import (
	"bytes"
	"database/sql"
	"kungfu.local/server/internal/protocol"
	"math"
	"time"
)

// deliverInventoryItem inserts a server-validated item snapshot. Callers MUST
// hold the account row lock and record their durable entitlement in the SAME
// transaction. It does not commit or notify the client. Do not pass wire data.
// Permanent weapon switch cards reuse an identical stack. Other item kinds and
// timed grants retain their existing lifetime and instance semantics.
func (m InventoryManager) AddItem(tx *sql.Tx, uid uint64, template []byte, days uint32) ([]byte, error) {
	if uid == 0 || len(template) != protocol.InventoryRecordSize || protocol.ReadUint16(template, 17) != 0 || days > 3650 {
		return nil, ErrDenied
	}
	if template[4] == protocol.ItemWeaponSwitchCard && days == 0 {
		item, err := mergeWeaponSwitchGrant(tx, uid, template)
		if err != nil || item != nil {
			return item, err
		}
	}
	var next uint64
	if err := tx.QueryRow("SELECT COALESCE(MAX(instance),1048575)+1 FROM inventory WHERE uid=?", uid).Scan(&next); err != nil {
		return nil, err
	}
	if next == 0 || next > math.MaxUint32 {
		return nil, ErrDenied
	}
	item := bytes.Clone(template)
	protocol.WriteUint32(item, 0, uint32(next))
	if _, err := tx.Exec("INSERT INTO inventory(uid,instance,record) VALUES(?,?,?)", uid, next, item); err != nil {
		return nil, err
	}
	if days > 0 {
		if _, err := tx.Exec("INSERT INTO inventory_expirations(uid,instance,expires_at) VALUES(?,?,?)", uid, next, time.Now().Unix()+int64(days)*86400); err != nil {
			return nil, err
		}
	}
	return item, nil
}

func mergeWeaponSwitchGrant(tx *sql.Tx, uid uint64, template []byte) ([]byte, error) {
	count := uint32(protocol.ReadUint16(template, 23))
	if !usableItem(template) || count == 0 || count > 999 {
		return nil, ErrDenied
	}
	rows, err := tx.Query(`SELECT i.record FROM inventory i LEFT JOIN inventory_expirations x ON x.uid=i.uid AND x.instance=i.instance WHERE i.uid=? AND x.instance IS NULL ORDER BY i.instance`, uid)
	if err != nil {
		return nil, err
	}
	var existing []byte
	for rows.Next() {
		var item []byte
		if err = rows.Scan(&item); err != nil {
			rows.Close()
			return nil, err
		}
		// Ignore only identity and quantity; preserve all other item properties.
		if len(item) == 68 && bytes.Equal(item[4:23], template[4:23]) && bytes.Equal(item[25:], template[25:]) {
			existing = item
			break
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil || existing == nil {
		return nil, err
	}
	count += uint32(protocol.ReadUint16(existing, 23))
	if count > 999 {
		return nil, ErrDenied
	}
	protocol.WriteUint16(existing, 23, uint16(count))
	_, err = tx.Exec(`UPDATE inventory SET record=? WHERE uid=? AND instance=?`, existing, uid, protocol.ReadUint32(existing, 0))
	return existing, err
}
