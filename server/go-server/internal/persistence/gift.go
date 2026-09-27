package persistence

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"kungfu.local/server/internal/protocol"
)

type GiftResult struct {
	Created         bool
	Recipient       uint64
	MailID, Balance uint32
}

// Gift debits the authenticated sender and durably creates a recipient-owned
// mail plus attachment in the same transaction. It never grants inventory yet.
func (s *MailManager) Gift(uid uint64, operation string, p []byte) (GiftResult, error) {
	var out GiftResult
	r, err := protocol.ParseGiftRequest(p)
	if err != nil || uid == 0 || len(operation) == 0 || len(operation) > 128 || r.Currency != 109 || r.Flag169 != 0 || r.CatalogField77 != 0 {
		return out, ErrDenied
	}
	// Name is mandatory in the native packet. UID is only a hint. Ambiguous
	// historical nicknames are rejected, never resolved to the first account.
	rows, err := s.store.DB.Query(`SELECT uid FROM accounts WHERE BINARY nickname=BINARY ? LIMIT 2`, r.RecipientName)
	if err != nil {
		return out, err
	}
	var ids []uint64
	for rows.Next() {
		var id uint64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return out, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(ids) != 1 || ids[0] == uid || (r.RecipientUID != 0 && r.RecipientUID != ids[0]) {
		return out, ErrDenied
	}
	target := ids[0]
	tx, txCancel, err := beginTransaction(s.store.DB)
	defer txCancel()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	first, second := uid, target
	if first > second {
		first, second = second, first
	}
	var name, recipientName string
	var balance uint32
	for _, id := range []uint64{first, second} {
		var n string
		var b uint32
		if err = tx.QueryRow(`SELECT nickname,tickets FROM accounts WHERE uid=? FOR UPDATE`, id).Scan(&n, &b); err != nil {
			return out, err
		}
		if id == uid {
			name, balance = n, b
		} else {
			recipientName = n
		}
	}
	if recipientName != r.RecipientName {
		return out, ErrDenied
	}
	hash := sha256.Sum256(p)
	var previous []byte
	err = tx.QueryRow(`SELECT request_hash,recipient,mail_id FROM gift_receipts WHERE uid=? AND operation_id=?`, uid, operation).Scan(&previous, &out.Recipient, &out.MailID)
	if err == nil {
		if !bytes.Equal(previous, hash[:]) {
			return GiftResult{}, ErrDenied
		}
		out.Balance = balance
		return out, tx.Commit()
	}
	if err != sql.ErrNoRows {
		return out, err
	}
	var pending int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM mailbox WHERE uid=? AND deleted=FALSE`, target).Scan(&pending); err != nil {
		return out, err
	}
	if pending >= 2048 {
		return out, ErrDenied
	}
	var catalog, item []byte
	err = tx.QueryRow(`SELECT record,grant_record FROM offers WHERE catalog_key=? AND enabled=TRUE LOCK IN SHARE MODE`, r.CatalogKey).Scan(&catalog, &item)
	if err != nil || len(catalog) != 108 || len(item) != 68 {
		return out, ErrDenied
	}
	cost := protocol.ReadUint32(catalog, 38)
	if cost == 0 || cost > 2147483647 || protocol.ReadUint32(catalog, 9) != r.CatalogKey || protocol.ReadUint32(catalog, 30) != 0 || protocol.ReadUint32(catalog, 42) != cost || catalog[48] == 0 || catalog[46] != 0 || catalog[49] != 0 || catalog[13] != 0 || catalog[83] != 1 || protocol.ReadUint32(catalog, 77) != 0 || protocol.ReadUint32(catalog, 88) != 0 || protocol.ReadUint16(item, 17) != 0 || item[4] != catalog[4] || protocol.ReadUint32(item, 5) != protocol.ReadUint32(catalog, 5) {
		return out, ErrDenied
	}
	percent, err := vipShopPercentTx(tx, uid)
	if err != nil {
		return out, err
	}
	cost, err = vipShopPrice(cost, percent)
	if err != nil {
		return out, err
	}
	if balance < cost || r.TicketPrice != cost {
		return out, ErrDenied
	}
	var days uint32
	err = tx.QueryRow(`SELECT days FROM offer_lifetimes WHERE catalog_key=? LOCK IN SHARE MODE`, r.CatalogKey).Scan(&days)
	if err != nil && err != sql.ErrNoRows {
		return out, err
	}
	if days > 3650 {
		return out, ErrDenied
	}
	// Validate text before charging; do not silently truncate the native message.
	record, err := (protocol.MailRecord{ID: 1, Title: "赠送道具", Sender: name, Body: r.Text}).Encode()
	if err != nil {
		return out, ErrDenied
	}
	detail, err := (protocol.MailDetail{MailKey: 1, Catalog: catalog}).Encode()
	if err != nil {
		return out, err
	}
	result, err := tx.Exec(`INSERT INTO mailbox(uid,list_record,detail_record) VALUES(?,?,?)`, target, record, detail)
	if err != nil {
		return out, err
	}
	mail, err := result.LastInsertId()
	if err != nil || mail <= 0 || mail > 0xffffffff {
		return out, ErrDenied
	}
	result, err = tx.Exec(`INSERT INTO mail_attachments(mail_id,uid,grant_record,expiry_days) VALUES(?,?,?,?)`, mail, target, item, days)
	if err != nil {
		return out, err
	}
	attachment, err := result.LastInsertId()
	if err != nil || attachment <= 0 || attachment > 0xffffffff {
		return out, ErrDenied
	}
	protocol.WriteUint32(record, 0, uint32(mail))
	protocol.WriteUint32(record, 331, uint32(attachment))
	protocol.WriteUint32(detail, 0, uint32(mail))
	protocol.WriteUint32(detail, 8, uint32(attachment))
	if _, err = tx.Exec(`UPDATE mailbox SET list_record=?,detail_record=? WHERE id=?`, record, detail, mail); err != nil {
		return out, err
	}
	balance -= cost
	if _, err = tx.Exec(`UPDATE accounts SET tickets=? WHERE uid=?`, balance, uid); err != nil {
		return out, err
	}
	if _, err = tx.Exec(`INSERT INTO gift_receipts(uid,operation_id,request_hash,recipient,mail_id,cost) VALUES(?,?,?,?,?,?)`, uid, operation, hash[:], target, mail, cost); err != nil {
		return out, err
	}
	out = GiftResult{Created: true, Recipient: target, MailID: uint32(mail), Balance: balance}
	return out, tx.Commit()
}
