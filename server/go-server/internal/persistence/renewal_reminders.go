package persistence

import (
	"kungfu.local/server/internal/protocol"
	"time"
)

// RenewalReminders projects expired owned weapons, never an entitlement to buy.
// Unknown prefix words are zero; the known discount field is 100 (full price).
func (m *ShopManager) RenewalReminders(uid uint64) (records []protocol.RenewalRecord, err error) {
	if uid == 0 {
		return nil, ErrDenied
	}
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
	now := time.Now().Unix()
	if err = expireInventory(tx, uid, now); err != nil {
		return nil, err
	}
	rows, err := tx.Query(`SELECT o.record,o.grant_record,l.days FROM offers o JOIN offer_lifetimes l ON l.catalog_key=o.catalog_key WHERE o.enabled=TRUE ORDER BY o.catalog_key LIMIT 4000`)
	if err != nil {
		return nil, err
	}
	byItem := map[uint32][]byte{}
	for rows.Next() {
		var catalog, grant []byte
		var days uint32
		if err = rows.Scan(&catalog, &grant, &days); err != nil {
			break
		}
		if validRenewalOffer(catalog, grant, days) {
			id := protocol.ReadUint32(catalog, 5)
			if _, ok := byItem[id]; !ok {
				byItem[id] = catalog
			}
		}
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if rowErr != nil {
		return nil, rowErr
	}
	rows, err = tx.Query(`SELECT i.instance,i.record FROM inventory i JOIN inventory_expirations e ON e.uid=i.uid AND e.instance=i.instance LEFT JOIN renewal_reminders r ON r.uid=i.uid AND r.instance=i.instance WHERE i.uid=? AND e.expires_at<=? AND (r.ignored_deadline IS NULL OR r.ignored_deadline<>e.expires_at) ORDER BY i.instance LIMIT 4000`, uid, now)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var instance uint32
		var item []byte
		if err = rows.Scan(&instance, &item); err != nil {
			break
		}
		if len(item) != 68 || item[4] != protocol.ItemWeapon || protocol.ReadUint32(item, 0) != instance || protocol.ReadUint32(item, 19) != 2 {
			continue
		}
		catalog, ok := byItem[protocol.ReadUint32(item, 5)]
		if !ok {
			continue
		}
		var r protocol.RenewalRecord
		protocol.WriteUint32(r.Raw[:], 8, instance)
		protocol.WriteUint32(r.Raw[:], 12, 2)
		copy(r.Raw[16:], catalog)
		r.Raw[70] = 100
		records = append(records, r)
	}
	rowErr = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if rowErr != nil {
		return nil, rowErr
	}
	return records, tx.Commit()
}

// Hide only this deadline's reminder. A later expiry is a new reminder.
func (m *ShopManager) IgnoreRenewalReminder(uid uint64, instance uint32) error {
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
	var deadline int64
	if err = tx.QueryRow(`SELECT e.expires_at FROM inventory_expirations e JOIN inventory i ON i.uid=e.uid AND i.instance=e.instance WHERE e.uid=? AND e.instance=? FOR UPDATE`, uid, instance).Scan(&deadline); err != nil {
		return err
	}
	if deadline > time.Now().Unix() {
		return ErrDenied
	}
	if _, err = tx.Exec(`INSERT INTO renewal_reminders(uid,instance,ignored_deadline) VALUES(?,?,?) ON DUPLICATE KEY UPDATE ignored_deadline=VALUES(ignored_deadline)`, uid, instance, deadline); err != nil {
		return err
	}
	return tx.Commit()
}
