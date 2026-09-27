package persistence

import (
	"bytes"
	"database/sql"
	"errors"
	"kungfu.local/server/internal/protocol"
	"time"
)

var ErrTalismanQuota = errors.New("insufficient talisman quota")

// Cost is supplied by the server rule, never by the untrusted 4201 field.
type TalismanUse struct {
	Instance, Item, Kind uint32
	Slot, Cost           uint16
}

func (s *ItemManager) UseTalisman(uid uint64, operation string, use TalismanUse) (item []byte, applied bool, err error) {
	if uid == 0 || operation == "" || len(operation) > 128 || use.Instance == 0 || use.Item == 0 || (use.Slot != 37 && use.Slot != 38) || (use.Kind != 8291 && use.Kind != 8292) {
		return nil, false, ErrDenied
	}
	request := make([]byte, 16)
	protocol.WriteUint32(request, 0, use.Instance)
	protocol.WriteUint32(request, 4, use.Item)
	protocol.WriteUint32(request, 8, use.Kind)
	protocol.WriteUint16(request, 12, use.Slot)
	protocol.WriteUint16(request, 14, use.Cost)
	tx, txCancel, err := beginTransaction(s.store.DB)
	defer txCancel()
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	var owner uint64
	if err = tx.QueryRow(`SELECT uid FROM accounts WHERE uid=? FOR UPDATE`, uid).Scan(&owner); err != nil {
		return nil, false, err
	}
	var old []byte
	err = tx.QueryRow(`SELECT request FROM talisman_uses WHERE uid=? AND operation_id=?`, uid, operation).Scan(&old)
	replay := err == nil
	if err != nil && err != sql.ErrNoRows {
		return nil, false, err
	}
	// The fee is server policy, not event identity. Replays never charge again.
	if replay && (len(old) != len(request) || !bytes.Equal(old[:14], request[:14])) {
		return nil, false, ErrDenied
	}
	if err = tx.QueryRow(`SELECT record FROM inventory WHERE uid=? AND instance=? FOR UPDATE`, uid, use.Instance).Scan(&item); err != nil {
		return nil, false, err
	}
	if len(item) != 68 || item[4] != protocol.ItemTalisman || protocol.ReadUint32(item, 0) != use.Instance || protocol.ReadUint32(item, 5) != use.Item {
		return nil, false, ErrDenied
	}
	if !usableItem(item) || protocol.ReadUint16(item, 17) != use.Slot {
		return nil, false, ErrDenied
	}
	var expired bool
	if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM inventory_expirations WHERE uid=? AND instance=? AND expires_at<=?)`, uid, use.Instance, time.Now().Unix()).Scan(&expired); err != nil {
		return nil, false, err
	}
	if expired {
		return nil, false, ErrDenied
	}
	// A replay returns current quota; it must not undo later use or repair.
	if replay {
		return item, false, tx.Commit()
	}
	quota := protocol.ReadUint16(item, 23)
	if quota < use.Cost {
		return item, false, ErrTalismanQuota
	}
	protocol.WriteUint16(item, 23, quota-use.Cost)
	if _, err = tx.Exec(`UPDATE inventory SET record=? WHERE uid=? AND instance=?`, item, uid, use.Instance); err != nil {
		return nil, false, err
	}
	if _, err = tx.Exec(`INSERT INTO talisman_uses(uid,operation_id,request) VALUES(?,?,?)`, uid, operation, request); err != nil {
		return nil, false, err
	}
	if err = tx.Commit(); err != nil {
		return nil, false, err
	}
	return item, true, nil
}
