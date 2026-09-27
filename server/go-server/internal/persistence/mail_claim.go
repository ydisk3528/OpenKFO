package persistence

import (
	"database/sql"
	"kungfu.local/server/internal/protocol"
)

// ClaimMailItem grants one server-stored item. Currency/experience attachments
// need their own verified rules; never derive an award from the preview packet.
func (s *MailManager) ClaimMailItem(uid uint64, key uint32) ([]byte, error) {
	if uid == 0 || key == 0 {
		return nil, ErrDenied
	}
	tx, txCancel, err := beginTransaction(s.store.DB)
	defer txCancel()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var owner uint64
	if err = tx.QueryRow(`SELECT uid FROM accounts WHERE uid=? FOR UPDATE`, uid).Scan(&owner); err != nil {
		return nil, err
	}
	var mail uint32
	var item []byte
	var days uint32
	var instance sql.NullInt64
	err = tx.QueryRow(`SELECT mail_id,grant_record,expiry_days,claimed_instance FROM mail_attachments WHERE id=? AND uid=? FOR UPDATE`, key, uid).Scan(&mail, &item, &days, &instance)
	if err == sql.ErrNoRows {
		return nil, ErrDenied
	}
	if err != nil {
		return nil, err
	}
	var deleted, claimed bool
	err = tx.QueryRow(`SELECT deleted,claimed FROM mailbox WHERE id=? AND uid=? FOR UPDATE`, mail, uid).Scan(&deleted, &claimed)
	if err == sql.ErrNoRows {
		return nil, ErrDenied
	}
	if err != nil {
		return nil, err
	}
	if instance.Valid {
		if !claimed {
			return nil, ErrDenied
		}
		// A retry returns the current item, never resurrects a removed/consumed one.
		var current []byte
		err = tx.QueryRow(`SELECT record FROM inventory WHERE uid=? AND instance=?`, uid, instance.Int64).Scan(&current)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		return current, tx.Commit()
	}
	if deleted || claimed || len(item) != 68 || days > 3650 || protocol.ReadUint16(item, 17) != 0 {
		return nil, ErrDenied
	}
	item, err = (InventoryManager{}).AddItem(tx, uid, item, days)
	if err != nil {
		return nil, err
	}
	next := protocol.ReadUint32(item, 0)
	if _, err = tx.Exec(`UPDATE mail_attachments SET claimed_instance=? WHERE id=? AND uid=?`, next, key, uid); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(`UPDATE mailbox SET claimed=TRUE,is_read=TRUE WHERE id=? AND uid=?`, mail, uid); err != nil {
		return nil, err
	}
	return item, tx.Commit()
}
