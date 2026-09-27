package persistence

import (
	"database/sql"
	"encoding/json"
	"time"
)

type TrainingClaim struct {
	GoldBalance uint32
	Items       [][]byte
	Experience  uint32
	Profile     []byte
	Replay      bool
}

// ClaimTraining reads the rank from server-owned state, never the request.
// Resetting started and crediting experience commit with the durable receipt.
// The native 21007 handler requests 21002 to begin the next training cycle.
func (s *TrainingManager) ClaimTraining(uid uint64, operation string, growth RewardRules) (r TrainingClaim, err error) {
	if uid == 0 || len(operation) == 0 || len(operation) > 128 {
		return r, ErrDenied
	}
	if err = growth.Validate(); err != nil {
		return r, err
	}
	tx, txCancel, err := beginTransaction(s.store.DB)
	defer txCancel()
	if err != nil {
		return r, err
	}
	defer tx.Rollback()
	if err = tx.QueryRow("SELECT profile,gold FROM accounts WHERE uid=? FOR UPDATE", uid).Scan(&r.Profile, &r.GoldBalance); err != nil {
		return r, err
	}
	if len(r.Profile) != 360 {
		return r, ErrDenied
	}
	err = tx.QueryRow("SELECT experience FROM training_claims WHERE uid=? AND operation_id=?", uid, operation).Scan(&r.Experience)
	if err == nil {
		r.Replay = true
		return r, tx.Commit()
	}
	if err != sql.ErrNoRows {
		return r, err
	}
	var settings TrainingSettings
	var data []byte
	if err = tx.QueryRow("SELECT revision,rules FROM training_rules WHERE id=1").Scan(&settings.Revision, &data); err != nil {
		return r, err
	}
	if err = json.Unmarshal(data, &settings.Rules); err != nil {
		return r, err
	}
	if err = settings.Validate(); err != nil {
		return r, err
	}
	if !settings.Rules.Enabled {
		return r, ErrDenied
	}
	var rank uint32
	err = tx.QueryRow("SELECT training_rank FROM training_ranks WHERE uid=? FOR UPDATE", uid).Scan(&rank)
	if err != nil && err != sql.ErrNoRows {
		return r, err
	}
	if rank > 8 {
		return r, ErrDenied
	}
	var started sql.NullInt64
	if err = tx.QueryRow("SELECT started FROM training WHERE uid=? FOR UPDATE", uid).Scan(&started); err != nil {
		return r, err
	}
	now := time.Now().Unix()
	if !started.Valid || started.Int64 < 0 || started.Int64 > now || now-started.Int64 < 3600 {
		return r, ErrDenied
	}
	minutes := (now - started.Int64) / 60
	if minutes > 35791394 {
		minutes = 35791394
	}
	r.Experience = settings.Rules.Levels[rank].Award(uint32(minutes))
	if r.GoldBalance, r.Items, err = (RewardManager{}).GrantProgressItems(tx, uid, r.Profile, uint64(r.GoldBalance), r.Experience, 0, growth); err != nil {
		return r, err
	}
	if _, err = tx.Exec("UPDATE accounts SET profile=?,gold=? WHERE uid=?", r.Profile, r.GoldBalance, uid); err != nil {
		return r, err
	}
	if _, err = tx.Exec("UPDATE training SET started=NULL WHERE uid=?", uid); err != nil {
		return r, err
	}
	if _, err = tx.Exec("INSERT INTO training_claims(uid,operation_id,started,training_rank,revision,experience) VALUES(?,?,?,?,?,?)", uid, operation, started.Int64, rank, settings.Revision, r.Experience); err != nil {
		return r, err
	}
	return r, tx.Commit()
}

// Absent rank means the native unranked tier (0), independently of role level.
func (s *TrainingManager) TrainingRank(uid uint64) (uint32, error) {
	var rank uint32
	err := s.store.DB.QueryRow("SELECT COALESCE(t.training_rank,0) FROM accounts a LEFT JOIN training_ranks t ON t.uid=a.uid WHERE a.uid=?", uid).Scan(&rank)
	if err != nil {
		return 0, err
	}
	if rank > 8 {
		return 0, ErrDenied
	}
	return rank, nil
}

func (m *TrainingManager) Training(uid uint64, start bool) (uint32, bool, error) {
	if start {
		if _, err := m.store.DB.Exec(`INSERT IGNORE INTO training(uid) VALUES(?)`, uid); err != nil {
			return 0, false, err
		}
		if _, err := m.store.DB.Exec(`UPDATE training SET started=? WHERE uid=? AND started IS NULL`, time.Now().Unix(), uid); err != nil {
			return 0, false, err
		}
	}
	var started sql.NullInt64
	// A profile lookup must not create or start training for its target.
	// LEFT JOIN distinguishes an existing idle account from a missing UID.
	if err := m.store.DB.QueryRow(`SELECT t.started FROM accounts a LEFT JOIN training t ON t.uid=a.uid WHERE a.uid=?`, uid).Scan(&started); err != nil {
		return 0, false, err
	}
	if !started.Valid {
		return 0, false, nil
	}
	minutes := (time.Now().Unix() - started.Int64) / 60
	if minutes < 0 {
		minutes = 0
	}
	if minutes > 35791394 {
		minutes = 35791394
	}
	return uint32(minutes), true, nil
}
