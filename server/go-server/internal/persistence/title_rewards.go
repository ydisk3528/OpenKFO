package persistence

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"kungfu.local/server/internal/protocol"
)

const TitleLevelOffset = 123

// Only one pending offer may exist for an authenticated account. An empty
// result means no offer; corrupt or ambiguous stored data must never be sent.
func (s *TitleManager) PendingTitleReward(uid uint64) (byte, []uint32, error) {
	if uid == 0 {
		return 0, nil, ErrDenied
	}
	rows, err := s.store.DB.Query("SELECT title_level,choices FROM title_rewards WHERE uid=? AND claimed_key IS NULL ORDER BY title_level LIMIT 2", uid)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	var level byte
	var choices []uint32
	for rows.Next() {
		if level != 0 {
			return 0, nil, ErrDenied
		}
		var data []byte
		if err = rows.Scan(&level, &data); err != nil {
			return 0, nil, err
		}
		if level == 0 || json.Unmarshal(data, &choices) != nil || len(choices) == 0 || len(choices) > 7 {
			return 0, nil, ErrDenied
		}
		seen := map[uint32]bool{}
		for _, key := range choices {
			if key == 0 || seen[key] {
				return 0, nil, ErrDenied
			}
			seen[key] = true
		}
	}
	return level, choices, rows.Err()
}

// Called only by a trusted award policy/admin, never by the 4126 request.
// One outstanding choice avoids ambiguities in the native claim (no title ID).
func (s *TitleManager) GrantTitleChoices(uid uint64, level byte, choices []uint32) error {
	tx, txCancel, err := beginTransaction(s.store.DB)
	defer txCancel()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = grantTitleChoices(tx, uid, level, choices); err != nil {
		return err
	}
	return tx.Commit()
}

func grantTitleChoices(tx *sql.Tx, uid uint64, level byte, choices []uint32) error {
	if uid == 0 || level == 0 || len(choices) == 0 || len(choices) > 7 {
		return ErrDenied
	}
	seen := map[uint32]bool{}
	for _, key := range choices {
		if key == 0 || seen[key] {
			return ErrDenied
		}
		seen[key] = true
	}
	data, err := json.Marshal(choices)
	if err != nil {
		return err
	}
	var profile, previous []byte
	if err = tx.QueryRow("SELECT profile FROM accounts WHERE uid=? FOR UPDATE", uid).Scan(&profile); err != nil {
		return err
	}
	if len(profile) != 360 {
		return ErrDenied
	}
	err = tx.QueryRow("SELECT choices FROM title_rewards WHERE uid=? AND title_level=?", uid, level).Scan(&previous)
	if err == nil {
		if !bytes.Equal(previous, data) {
			return ErrDenied
		}
		return nil
	}
	if err != sql.ErrNoRows {
		return err
	}
	if level <= profile[TitleLevelOffset] {
		return ErrDenied
	}
	var pending bool
	if err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM title_rewards WHERE uid=? AND claimed_key IS NULL)", uid).Scan(&pending); err != nil {
		return err
	}
	if pending {
		return ErrDenied
	}
	// Choices refer to independent reward definitions, including unsold items.
	for _, key := range choices {
		if _, err = readDefinition(tx, key); err != nil {
			return err
		}
	}
	profile[TitleLevelOffset] = level
	if _, err = tx.Exec("UPDATE accounts SET profile=? WHERE uid=?", profile, uid); err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO title_rewards(uid,title_level,choices) VALUES(?,?,?)", uid, level, data); err != nil {
		return err
	}
	return nil
}

// announcedLevel is bound by the server to the offer sent to this session.
// 4126 contains only a catalogue key and cannot authorize a title or account.
func (s *TitleManager) ClaimTitleReward(uid uint64, announcedLevel byte, key uint32) ([]byte, error) {
	if uid == 0 || announcedLevel == 0 || key == 0 {
		return nil, ErrDenied
	}
	tx, txCancel, err := beginTransaction(s.store.DB)
	defer txCancel()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var owner uint64
	if err = tx.QueryRow("SELECT uid FROM accounts WHERE uid=? FOR UPDATE", uid).Scan(&owner); err != nil {
		return nil, err
	}
	var data []byte
	var claimed, instance sql.NullInt64
	err = tx.QueryRow("SELECT choices,claimed_key,claimed_instance FROM title_rewards WHERE uid=? AND title_level=? FOR UPDATE", uid, announcedLevel).Scan(&data, &claimed, &instance)
	if err != nil {
		return nil, err
	}
	if claimed.Valid {
		if uint64(claimed.Int64) != uint64(key) || !instance.Valid {
			return nil, ErrDenied
		}
		var item []byte
		err = tx.QueryRow("SELECT record FROM inventory WHERE uid=? AND instance=?", uid, instance.Int64).Scan(&item)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		if err == nil && (len(item) != 68 || uint64(protocol.ReadUint32(item, 0)) != uint64(instance.Int64)) {
			return nil, ErrDenied
		}
		return item, tx.Commit() // Never recreate an item removed after claiming.
	}
	if instance.Valid {
		return nil, ErrDenied
	}
	var choices []uint32
	if err = json.Unmarshal(data, &choices); err != nil {
		return nil, err
	}
	if len(choices) == 0 || len(choices) > 7 {
		return nil, ErrDenied
	}
	allowed := false
	seen := map[uint32]bool{}
	for _, candidate := range choices {
		if candidate == 0 || seen[candidate] {
			return nil, ErrDenied
		}
		seen[candidate] = true
		allowed = allowed || candidate == key
	}
	if !allowed {
		return nil, ErrDenied
	}
	item, err := (RewardManager{}).GrantItem(tx, uid, key)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec("UPDATE title_rewards SET claimed_key=?,claimed_instance=? WHERE uid=? AND title_level=?", key, protocol.ReadUint32(item, 0), uid, announcedLevel); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return item, nil
}
