package persistence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"kungfu.local/server/internal/protocol"
	"time"
)

func (m *ShopManager) Purchase(uid uint64, operationID string, request []byte) (uint32, []byte, []byte, error) {
	if len(request) != 169 || len(operationID) == 0 || len(operationID) > 128 {
		return 0, nil, nil, ErrDenied
	}
	transaction, transactionCancel, err := beginTransaction(m.store.DB)
	defer transactionCancel()
	if err != nil {
		return 0, nil, nil, err
	}
	defer transaction.Rollback()
	var gold, tickets uint32
	if err = transaction.QueryRow(`SELECT gold,tickets FROM accounts WHERE uid=? FOR UPDATE`, uid).Scan(&gold, &tickets); err != nil {
		return 0, nil, nil, err
	}
	requestHash := sha256.Sum256(request)
	var previousHash, previousItem, previousCatalog []byte
	var previousBalance uint32
	err = transaction.QueryRow(`SELECT request_hash,balance,item_record,catalog_record FROM purchases WHERE uid=? AND operation_id=?`, uid, operationID).Scan(&previousHash, &previousBalance, &previousItem, &previousCatalog)
	if err == nil {
		if !bytes.Equal(previousHash, requestHash[:]) {
			return 0, nil, nil, ErrDenied
		}
		var currentItem []byte
		lookupErr := transaction.QueryRow(`SELECT record FROM inventory WHERE uid=? AND instance=?`, uid, protocol.ReadUint32(previousItem, 0)).Scan(&currentItem)
		if lookupErr != nil && lookupErr != sql.ErrNoRows {
			return 0, nil, nil, lookupErr
		}
		previousItem = currentItem
		if err = transaction.Commit(); err != nil {
			return 0, nil, nil, err
		}
		if protocol.ReadUint32(previousCatalog, 30) > 0 {
			previousBalance = gold
		} else {
			previousBalance = tickets
		}
		return previousBalance, previousItem, previousCatalog, nil
	}
	if err != sql.ErrNoRows {
		return 0, nil, nil, err
	}
	var catalog, item []byte
	err = transaction.QueryRow(`SELECT record,grant_record FROM offers WHERE catalog_key=? AND enabled=TRUE LOCK IN SHARE MODE`, protocol.ReadUint32(request, 145)).Scan(&catalog, &item)
	if err != nil || len(catalog) != 108 || len(item) != 68 {
		return 0, nil, nil, ErrDenied
	}
	goldPrice, ticketPrice := protocol.ReadUint32(catalog, 30), protocol.ReadUint32(catalog, 38)
	if protocol.ReadUint32(catalog, 9) != protocol.ReadUint32(request, 145) || item[4] != catalog[4] || protocol.ReadUint32(item, 5) != protocol.ReadUint32(catalog, 5) || protocol.ReadUint16(item, 17) != 0 {
		return 0, nil, nil, ErrDenied
	}
	if catalog[48] == 0 || catalog[46] != 0 || catalog[49] != 0 || catalog[13] != 0 || catalog[83] != 1 || protocol.ReadUint32(catalog, 88) != 0 || protocol.ReadUint32(catalog, 77) != 0 || (goldPrice == 0) == (ticketPrice == 0) || goldPrice > 2147483647 || ticketPrice > 2147483647 || protocol.ReadUint32(catalog, 34) != goldPrice || protocol.ReadUint32(catalog, 42) != ticketPrice {
		return 0, nil, nil, ErrDenied
	}
	currencyCode, balance, price := uint32(109), tickets, ticketPrice
	percent, err := vipShopPercentTx(transaction, uid)
	if err != nil {
		return 0, nil, nil, err
	}
	goldPrice, err = vipShopPrice(goldPrice, percent)
	if err != nil {
		return 0, nil, nil, err
	}
	ticketPrice, err = vipShopPrice(ticketPrice, percent)
	if err != nil {
		return 0, nil, nil, err
	}
	price = ticketPrice
	column := "tickets"
	if goldPrice > 0 {
		currencyCode, balance, price = 111, gold, goldPrice
		column = "gold"
	}
	if protocol.ReadUint32(request, 0) != currencyCode || protocol.ReadUint64(request, 4) != uid || protocol.ReadUint64(request, 54) != uid || protocol.ReadUint32(request, 149) != goldPrice || protocol.ReadUint32(request, 157) != ticketPrice || protocol.ReadUint32(request, 153) != 0 || protocol.ReadUint32(request, 161) != 0 || protocol.ReadUint32(request, 165) != 0 || balance < price {
		return 0, nil, nil, ErrDenied
	}
	var lifetime uint32
	err = transaction.QueryRow(`SELECT days FROM offer_lifetimes WHERE catalog_key=? LOCK IN SHARE MODE`, protocol.ReadUint32(request, 145)).Scan(&lifetime)
	if err != nil && err != sql.ErrNoRows {
		return 0, nil, nil, err
	}
	if lifetime > 3650 {
		return 0, nil, nil, ErrDenied
	}
	balance -= price
	if _, err = transaction.Exec(`UPDATE accounts SET `+column+`=? WHERE uid=?`, balance, uid); err != nil {
		return 0, nil, nil, err
	}
	item, err = (InventoryManager{}).AddItem(transaction, uid, item, lifetime)
	if err != nil {
		return 0, nil, nil, err
	}
	if _, err = transaction.Exec(`INSERT INTO purchases(uid,operation_id,request_hash,balance,item_record,catalog_record) VALUES(?,?,?,?,?,?)`, uid, operationID, requestHash[:], balance, item, catalog); err != nil {
		return 0, nil, nil, err
	}
	return balance, item, catalog, transaction.Commit()
}

func (m *ShopManager) Offers(category, variant int) ([]Offer, error) {
	if kinds := compatibleShelfKinds(category, variant); kinds != nil {
		return m.compatibleOffers(category, variant, kinds)
	}
	query := `SELECT catalog_key,category,variant,record,grant_record FROM offers WHERE enabled=TRUE`
	args := []any{}
	if category == protocol.ShopCategoryRecommended {
		// The native recommendation tab always requests 255/25, even for
		// non-weapon recommendations. Preserve each item's actual type and ID.
		if variant == protocol.ItemWeapon {
			query += ` AND variant IN (?,21)`
		} else {
			query += ` AND variant=?`
		}
		query += ` AND EXISTS(SELECT 1 FROM offer_recommendations f WHERE f.catalog_key=offers.catalog_key AND f.enabled=TRUE)`
		args = append(args, variant)
	} else if category >= 0 {
		query += ` AND category=? AND variant=?`
		args = append(args, category, variant)
	}
	if category == protocol.ShopCategoryRecommended {
		query += ` ORDER BY COALESCE((SELECT p.priority FROM offer_recommendation_order p WHERE p.catalog_key=offers.catalog_key),0) DESC, catalog_key LIMIT 4000`
	} else {
		query += ` ORDER BY catalog_key LIMIT 4000`
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err := m.store.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var offers []Offer
	for rows.Next() {
		var offer Offer
		if err = rows.Scan(&offer.Key, &offer.Category, &offer.Variant, &offer.Record, &offer.Grant); err != nil {
			return nil, err
		}
		if len(offer.Record) != 108 || len(offer.Grant) != 68 {
			return nil, ErrDenied
		}
		offers = append(offers, offer)
	}
	return offers, rows.Err()
}
