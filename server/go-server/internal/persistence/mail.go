package persistence

import (
	"context"
	"database/sql"
	"kungfu.local/server/internal/protocol"
	"time"
)

// Mailbox holds server-generated wire snapshots, never client-submitted blobs.
// Delivery and attachment claims are deliberately separate transactions from viewing.
func (s *MailManager) Mailbox(uid uint64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err := s.store.DB.QueryContext(ctx, `SELECT id,list_record,is_read FROM mailbox WHERE uid=? AND deleted=FALSE ORDER BY id DESC LIMIT 2049`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []byte
	for rows.Next() {
		var id uint32
		var p []byte
		var read bool
		if err = rows.Scan(&id, &p, &read); err != nil {
			return nil, err
		}
		if len(p) != 339 || len(out)/339 >= 2048 {
			return nil, ErrDenied
		}
		protocol.WriteUint32(p, 0, id)
		state := uint32(0)
		if read {
			state = 1
		}
		protocol.WriteUint32(p, 327, state)
		out = append(out, p...)
	}
	return out, rows.Err()
}

func (s *MailManager) ReadMail(uid uint64, id uint32) ([]byte, error) {
	if uid == 0 || id == 0 {
		return nil, ErrDenied
	}
	tx, txCancel, err := beginTransaction(s.store.DB)
	defer txCancel()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var p []byte
	err = tx.QueryRow(`SELECT detail_record FROM mailbox WHERE id=? AND uid=? AND deleted=FALSE FOR UPDATE`, id, uid).Scan(&p)
	if err != nil {
		return nil, err
	}
	if len(p) != 136 {
		return nil, ErrDenied
	}
	if _, err = tx.Exec(`UPDATE mailbox SET is_read=TRUE WHERE id=? AND uid=?`, id, uid); err != nil {
		return nil, err
	}
	protocol.WriteUint32(p, 0, id)
	protocol.WriteUint32(p, 4, 1)
	return p, tx.Commit()
}

// Preserve the row and any claim receipt. A repeated delete by the same owner
// succeeds; another account cannot discover or delete the target mail.
func (s *MailManager) DeleteMail(uid uint64, id uint32) error {
	if uid == 0 || id == 0 {
		return ErrDenied
	}
	tx, txCancel, err := beginTransaction(s.store.DB)
	defer txCancel()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var deleted bool
	err = tx.QueryRow(`SELECT deleted FROM mailbox WHERE id=? AND uid=? FOR UPDATE`, id, uid).Scan(&deleted)
	if err == sql.ErrNoRows {
		return ErrDenied
	}
	if err != nil {
		return err
	}
	if !deleted {
		if _, err = tx.Exec(`UPDATE mailbox SET deleted=TRUE WHERE id=? AND uid=?`, id, uid); err != nil {
			return err
		}
	}
	return tx.Commit()
}
