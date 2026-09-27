package persistence

import (
	"bytes"
	"database/sql"
	"time"

	"kungfu.local/server/internal/protocol"
)

func (account Account) Consumable(instance uint32, slot uint16) ([]byte, error) {
	var found []byte
	for _, record := range account.Inventory {
		if !usableItem(record) || record[4] != protocol.ItemConsumable {
			continue
		}
		currentSlot := protocol.ReadUint16(record, 17)
		if currentSlot != 27 && currentSlot != 28 {
			continue
		}
		if (instance != 0 && protocol.ReadUint32(record, 0) == instance) || (instance == 0 && slot == currentSlot) {
			if found != nil {
				return nil, ErrDenied
			}
			found = record
		}
	}
	if found == nil {
		return nil, ErrDenied
	}
	return found, nil
}

func (m *InventoryManager) Consume(uid uint64, battle, sequence, instance uint32, signature []byte, intent bool) (bool, error) {
	if len(signature) != 40 {
		return false, ErrDenied
	}
	transaction, transactionCancel, err := beginTransaction(m.store.DB)
	defer transactionCancel()
	if err != nil {
		return false, err
	}
	defer transaction.Rollback()
	var owner uint64
	if err = transaction.QueryRow(`SELECT uid FROM accounts WHERE uid=? FOR UPDATE`, uid).Scan(&owner); err != nil {
		return false, err
	}
	var previousInstance uint32
	var previousSignature []byte
	err = transaction.QueryRow(`SELECT instance,signature FROM consumption_events WHERE uid=? AND battle=? AND sequence=?`, uid, battle, sequence).Scan(&previousInstance, &previousSignature)
	if err == nil {
		if previousInstance != instance || !bytes.Equal(previousSignature, signature) {
			return false, ErrDenied
		}
		return false, transaction.Commit()
	}
	if err != sql.ErrNoRows {
		return false, err
	}
	if !intent {
		return false, ErrDenied
	}
	if err = expireInventory(transaction, uid, time.Now().Unix()); err != nil {
		return false, err
	}
	var record []byte
	if err = transaction.QueryRow(`SELECT record FROM inventory WHERE uid=? AND instance=?`, uid, instance).Scan(&record); err != nil {
		return false, err
	}
	if !usableItem(record) || record[4] != protocol.ItemConsumable {
		return false, ErrDenied
	}
	slot := protocol.ReadUint16(record, 17)
	quantity := protocol.ReadUint16(record, 23)
	if (slot != 27 && slot != 28) || quantity == 0 {
		return false, ErrDenied
	}
	protocol.WriteUint16(record, 23, quantity-1)
	if _, err = transaction.Exec(`UPDATE inventory SET record=? WHERE uid=? AND instance=?`, record, uid, instance); err != nil {
		return false, err
	}
	if _, err = transaction.Exec(`INSERT INTO consumption_events VALUES(?,?,?,?,?)`, uid, battle, sequence, instance, signature); err != nil {
		return false, err
	}
	if err := transaction.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
