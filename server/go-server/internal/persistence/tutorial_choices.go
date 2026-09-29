package persistence

import (
	"bytes"
	"database/sql"
	"encoding/json"

	"kungfu.local/server/internal/protocol"
)

// TutorialChoices is separate from automatic title advancement: title 2 is
// earned by completing the guide, even when ordinary title rules are disabled.
func (m *RewardManager) TutorialChoices(uid uint64) (choices []uint32, catalog []byte, err error) {
	tx, txCancel, err := beginTransaction(m.store.DB)
	defer txCancel()
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	choices, catalog, err = tutorialChoices(tx, uid)
	if err != nil {
		return nil, nil, err
	}
	return choices, catalog, tx.Commit()
}

// Completion retries and login recovery must restore the same persisted offer.
// Never rebuild it from a GM configuration that may have changed after earning it.
func tutorialChoices(tx *sql.Tx, uid uint64) (choices []uint32, catalog []byte, err error) {
	var data []byte
	err = tx.QueryRow(`SELECT t.choices FROM title_rewards t JOIN tutorial_rewards r ON r.uid=t.uid WHERE t.uid=? AND t.title_level=2 AND t.claimed_key IS NULL`, uid).Scan(&data)
	if err == sql.ErrNoRows {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if json.Unmarshal(data, &choices) != nil || len(choices) == 0 || len(choices) > 7 {
		return nil, nil, ErrDenied
	}
	catalog, err = weaponRewardCatalog(tx, choices)
	if err != nil {
		return nil, nil, err
	}
	return choices, catalog, nil
}

// Validate the same display catalogue before committing completion and when
// restoring a pending selection. A broken catalogue must not consume rewards.
func (m *RewardManager) WeaponChoiceCatalog(choices []uint32) ([]byte, error) {
	if len(choices) == 0 || len(choices) > 7 {
		return nil, ErrDenied
	}
	tx, txCancel, err := beginTransaction(m.store.DB)
	defer txCancel()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	catalog, err := weaponRewardCatalog(tx, choices)
	if err != nil {
		return nil, err
	}
	return catalog, tx.Commit()
}

func weaponRewardCatalog(tx *sql.Tx, choices []uint32) (catalog []byte, err error) {
	// GM validation can combine several independently bounded choice lists.
	if len(choices) == 0 {
		return nil, ErrDenied
	}
	defs := make([]ItemDefinition, 0, len(choices))
	for _, key := range choices {
		d, err := readDefinition(tx, key)
		if err != nil {
			return nil, err
		}
		if len(d.Record) != 68 || d.Record[4] != protocol.ItemWeapon {
			return nil, ErrDenied
		}
		defs = append(defs, d)
	}
	return rewardCatalog(tx, defs)
}

// RewardCatalog adds display-only entries for the exact snapshotted rewards.
func (m *RewardManager) RewardCatalog(defs []ItemDefinition) ([]byte, error) {
	tx, cancel, err := beginTransaction(m.store.DB)
	defer cancel()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	catalog, err := rewardCatalog(tx, defs)
	if err != nil {
		return nil, err
	}
	return catalog, tx.Commit()
}

func rewardCatalog(tx *sql.Tx, defs []ItemDefinition) (catalog []byte, err error) {
	// Native A27F60/85C5C0 appends 108-byte entries indexed by DWORD +9,
	// not +0. Keep ordinary entries and validate the same index the selector
	// uses before adding display-only definitions (never purchasable offers).
	rows, err := tx.Query("SELECT record FROM offers WHERE enabled=TRUE ORDER BY catalog_key LIMIT 4000")
	if err != nil {
		return nil, err
	}
	records := map[uint32][]byte{}
	order := []uint32{}
	for rows.Next() {
		var record []byte
		if err = rows.Scan(&record); err != nil {
			break
		}
		if len(record) != 108 {
			err = ErrDenied
			break
		}
		key := protocol.ReadUint32(record, 9)
		if _, ok := records[key]; ok {
			continue // Native keeps the first entry; later duplicates cannot repair it.
		}
		records[key] = record
		order = append(order, key)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if rowErr != nil {
		return nil, rowErr
	}
	seen := map[uint32]bool{}
	for _, d := range defs {
		key := d.Key
		if seen[key] {
			return nil, ErrDenied
		}
		seen[key] = true
		if key == 0 || len(d.Record) != 68 || protocol.ReadUint32(d.Record, 5) == 0 {
			return nil, ErrDenied
		}
		existing, ok := records[key]
		if ok {
			if existing[4] != d.Record[4] || !bytes.Equal(existing[5:9], d.Record[5:9]) {
				return nil, ErrDenied
			}
			continue
		}
		record := make([]byte, 108)
		protocol.WriteUint32(record, 0, key)
		copy(record[4:9], d.Record[4:9])
		protocol.WriteUint32(record, 9, key)
		protocol.WriteUint32(record, 22, d.Days*24)
		record[48], record[83] = 1, 1
		records[key] = record
		order = append(order, key)
	}
	for _, key := range order {
		catalog = append(catalog, records[key]...)
	}
	return catalog, nil
}
