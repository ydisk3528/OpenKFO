package persistence

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"time"

	"kungfu.local/server/internal/protocol"
)

// RenewalQuote is a server-owned snapshot, not a deserialized request field.
// The network layer must bind it to a successfully delivered price list.
// This policy uses the configured ticket price without a renewal discount.
type RenewalQuote struct {
	Catalog [108]byte
	Days    uint32
}

type ItemRenewal struct {
	Tickets uint32
	Item    []byte
	Replay  bool
}

func validRenewalOffer(catalog, grant []byte, days uint32) bool {
	if len(catalog) != 108 || len(grant) != 68 || days == 0 || days > 3650 {
		return false
	}
	key, cost := protocol.ReadUint32(catalog, 0), protocol.ReadUint32(catalog, 38)
	return key != 0 && catalog[4] == protocol.ItemWeapon && protocol.ReadUint32(catalog, 9) == key && cost > 0 && cost <= 2147483647 && protocol.ReadUint32(catalog, 42) == cost && protocol.ReadUint32(catalog, 30) == 0 && protocol.ReadUint32(catalog, 34) == 0 && catalog[48] != 0 && catalog[46] == 0 && catalog[49] == 0 && catalog[13] == 0 && catalog[83] == 1 && protocol.ReadUint32(catalog, 77) == 0 && protocol.ReadUint32(catalog, 88) == 0 && grant[4] == catalog[4] && bytes.Equal(grant[5:9], catalog[5:9])
}

// Read display and duration together; RenewItem revalidates both under lock.
func (m *ShopManager) RenewalQuotes(kind byte, itemID uint32) ([]RenewalQuote, error) {
	if kind != protocol.ItemWeapon || itemID == 0 {
		return nil, nil
	}
	rows, err := m.store.DB.Query(`SELECT o.record,o.grant_record,l.days FROM offers o JOIN offer_lifetimes l ON l.catalog_key=o.catalog_key WHERE o.enabled=TRUE ORDER BY o.catalog_key LIMIT 4000`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []RenewalQuote
	for rows.Next() {
		var catalog, grant []byte
		var days uint32
		if err = rows.Scan(&catalog, &grant, &days); err != nil {
			return nil, err
		}
		if !validRenewalOffer(catalog, grant, days) || protocol.ReadUint32(catalog, 5) != itemID {
			continue
		}
		q := RenewalQuote{Days: days}
		copy(q.Catalog[:], catalog)
		result = append(result, q)
	}
	return result, rows.Err()
}

func (m *ShopManager) RenewItem(uid uint64, operation string, payload []byte, quote RenewalQuote) (out ItemRenewal, err error) {
	r, err := protocol.ParseRenewalRequest(payload)
	if err != nil || uid == 0 || len(operation) == 0 || len(operation) > 128 || r.Operation() != 105 || r.SenderUID() != uid || r.RecipientUID() != uid || r.InventoryInstance() == 0 {
		return out, ErrDenied
	}
	tx, txCancel, err := beginTransaction(m.store.DB)
	defer txCancel()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = tx.QueryRow(`SELECT tickets FROM accounts WHERE uid=? FOR UPDATE`, uid).Scan(&out.Tickets); err != nil {
		return out, err
	}
	hash := sha256.Sum256(payload)
	var previous []byte
	var instance uint32
	err = tx.QueryRow(`SELECT request_hash,instance FROM renewal_receipts WHERE uid=? AND operation_id=?`, uid, operation).Scan(&previous, &instance)
	if err == nil {
		if !bytes.Equal(previous, hash[:]) || instance != r.InventoryInstance() {
			return ItemRenewal{}, ErrDenied
		}
		// Return current inventory, not an old snapshot that could resurrect an
		// expired/deleted item or overwrite a subsequent upgrade in the client.
		if err = expireInventory(tx, uid, time.Now().Unix()); err != nil {
			return out, err
		}
		err = tx.QueryRow(`SELECT record FROM inventory WHERE uid=? AND instance=?`, uid, instance).Scan(&out.Item)
		if err != nil && err != sql.ErrNoRows {
			return out, err
		}
		out.Replay = true
		return out, tx.Commit()
	}
	if err != sql.ErrNoRows {
		return out, err
	}
	var catalog, grant []byte
	var days uint32
	err = tx.QueryRow(`SELECT o.record,o.grant_record,l.days FROM offers o JOIN offer_lifetimes l ON l.catalog_key=o.catalog_key WHERE o.catalog_key=? AND o.enabled=TRUE LOCK IN SHARE MODE`, r.CatalogKey()).Scan(&catalog, &grant, &days)
	if err != nil {
		return out, err
	}
	if len(catalog) != 108 || len(grant) != 68 || !bytes.Equal(catalog, quote.Catalog[:]) || days != quote.Days || days == 0 || days > 3650 {
		return out, ErrDenied
	}
	cost := protocol.ReadUint32(catalog, 38)
	if !validRenewalOffer(catalog, grant, days) || protocol.ReadUint32(catalog, 0) != r.CatalogKey() {
		return out, ErrDenied
	}
	if cost != r.QuotedAmount() || out.Tickets < cost {
		return out, ErrDenied
	}
	var current []byte
	if err = tx.QueryRow(`SELECT record FROM inventory WHERE uid=? AND instance=? FOR UPDATE`, uid, r.InventoryInstance()).Scan(&current); err != nil {
		return out, err
	}
	if len(current) != 68 || current[4] != catalog[4] || !bytes.Equal(current[5:9], catalog[5:9]) {
		return out, ErrDenied
	}
	out.Tickets -= cost
	if _, err = tx.Exec(`UPDATE accounts SET tickets=? WHERE uid=?`, out.Tickets, uid); err != nil {
		return out, err
	}
	out.Item, err = (InventoryManager{}).ExtendItem(tx, uid, r.InventoryInstance(), days, time.Now().Unix())
	if err != nil {
		return out, err
	}
	if _, err = tx.Exec(`INSERT INTO renewal_receipts(uid,operation_id,request_hash,instance) VALUES(?,?,?,?)`, uid, operation, hash[:], r.InventoryInstance()); err != nil {
		return out, err
	}
	return out, tx.Commit()
}
