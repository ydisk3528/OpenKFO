package persistence

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"kungfu.local/server/internal/protocol"
	"math/big"
	"time"
)

type RandomWeaponState struct{ Mode, Instance uint32 }

func (s *Store) RandomWeapon(uid uint64) (state RandomWeaponState, err error) {
	err = s.DB.QueryRow(`SELECT mode,instance FROM random_weapon_settings WHERE uid=?`, uid).Scan(&state.Mode, &state.Instance)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return
}

func randomWeaponEligible(record []byte, mode uint32, types map[uint32]uint32) bool {
	if !usableItem(record) || record[protocol.InventoryKindOffset] != protocol.ItemWeapon {
		return false
	}
	slot := protocol.ReadUint16(record, protocol.InventorySlotOffset)
	if slot != protocol.SlotUnequipped && slot != protocol.SlotPrimaryWeapon {
		return false
	}
	item := protocol.ReadUint32(record, protocol.InventoryItemIDOffset)
	if item == 0 || item > ^uint32(0)/protocol.RandomWeaponAppearanceScale {
		return false
	}
	kind := types[item]
	return kind >= 1 && kind < protocol.RandomWeaponAll && (mode == protocol.RandomWeaponAll || mode == kind)
}

// Selection and equip share the account lock and commit. No grants or synthetic
// inventory instances: candidates are read from this owner's inventory only.
func (s *Store) SelectRandomWeapon(uid uint64, mode uint32, types map[uint32]uint32, reroll bool) ([]byte, error) {
	if mode > protocol.RandomWeaponAll {
		return nil, ErrDenied
	}
	tx, txCancel, err := beginTransaction(s.DB)
	defer txCancel()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var owner uint64
	if err = tx.QueryRow(`SELECT uid FROM accounts WHERE uid=? FOR UPDATE`, uid).Scan(&owner); err != nil {
		return nil, err
	}
	if mode == protocol.RandomWeaponOff {
		_, err = tx.Exec(`DELETE FROM random_weapon_settings WHERE uid=?`, uid)
		if err != nil {
			return nil, err
		}
		return nil, tx.Commit()
	}
	if err = expireInventory(tx, uid, time.Now().Unix()); err != nil {
		return nil, err
	}
	var previous RandomWeaponState
	err = tx.QueryRow(`SELECT mode,instance FROM random_weapon_settings WHERE uid=?`, uid).Scan(&previous.Mode, &previous.Instance)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	rows, err := tx.Query(`SELECT record FROM inventory WHERE uid=? ORDER BY instance`, uid)
	if err != nil {
		return nil, err
	}
	var candidates [][]byte
	var current []byte
	for rows.Next() {
		var record []byte
		if err = rows.Scan(&record); err != nil {
			rows.Close()
			return nil, err
		}
		if randomWeaponEligible(record, mode, types) {
			candidates = append(candidates, record)
			if previous.Mode == mode && previous.Instance == protocol.ReadUint32(record, 0) && protocol.ReadUint16(record, protocol.InventorySlotOffset) == protocol.SlotPrimaryWeapon {
				current = record
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, ErrDenied
	}
	if current == nil || reroll {
		index, err := rand.Int(rand.Reader, big.NewInt(int64(len(candidates))))
		if err != nil {
			return nil, err
		}
		current, err = equipInTransaction(tx, uid, protocol.ReadUint32(candidates[index.Int64()], 0), protocol.SlotPrimaryWeapon, false)
		if err != nil {
			return nil, err
		}
	}
	_, err = tx.Exec(`INSERT INTO random_weapon_settings(uid,mode,instance) VALUES(?,?,?) ON DUPLICATE KEY UPDATE mode=VALUES(mode),instance=VALUES(instance)`, uid, mode, protocol.ReadUint32(current, 0))
	if err != nil {
		return nil, err
	}
	return current, tx.Commit()
}
