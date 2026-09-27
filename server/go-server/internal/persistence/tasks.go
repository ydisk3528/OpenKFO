package persistence

import (
	"database/sql"
	"encoding/json"
	"kungfu.local/server/internal/protocol"
)

// Tasks uses only the authenticated UID. Client prefixes and completion claims
// are never used to select an account or supply the progress baseline.
// action=0 lists; 6050 accepts; 6080 cancels. Completion is a separate transaction.
func (s *TaskManager) Tasks(uid uint64, action uint32, key uint16) ([]protocol.TaskProgress, error) {
	rows, _, err := s.store.TaskManager().TaskTransition(uid, action, key)
	return rows, err
}

func (s *TaskManager) TaskTransition(uid uint64, action uint32, key uint16) ([]protocol.TaskProgress, bool, error) {
	changed := false
	if uid == 0 || (action != 0 && action != 6050 && action != 6080) || (action != 0 && key == 0) {
		return nil, false, ErrDenied
	}
	tx, txCancel, err := beginTransaction(s.store.DB)
	defer txCancel()
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	var profile []byte
	if err = tx.QueryRow("SELECT profile FROM accounts WHERE uid=? FOR UPDATE", uid).Scan(&profile); err != nil {
		return nil, false, err
	}
	if len(profile) != 360 {
		return nil, false, ErrDenied
	}
	var config TaskSettings
	var data []byte
	err = tx.QueryRow("SELECT revision,rules FROM task_rules WHERE id=1 FOR UPDATE").Scan(&config.Revision, &data)
	if err != nil && err != sql.ErrNoRows {
		return nil, false, err
	}
	if err == nil {
		if err = json.Unmarshal(data, &config.Rules); err != nil {
			return nil, false, err
		}
	}
	if err = config.Validate(); err != nil {
		return nil, false, err
	}
	if !config.Rules.Enabled {
		if action != 0 {
			return nil, false, ErrDenied
		}
		return []protocol.TaskProgress{}, false, tx.Commit()
	}
	rows, err := tx.Query("SELECT task_key,state,baseline FROM task_progress WHERE uid=?", uid)
	if err != nil {
		return nil, false, err
	}
	states := map[uint16]protocol.TaskProgress{}
	for rows.Next() {
		var r protocol.TaskProgress
		var baseline []byte
		if err = rows.Scan(&r.Key, &r.State, &baseline); err != nil {
			rows.Close()
			return nil, false, err
		}
		if len(baseline) != 116 || r.State < 1 || r.State > 3 {
			rows.Close()
			return nil, false, ErrDenied
		}
		for i := range r.ProfileBaseline {
			r.ProfileBaseline[i] = protocol.ReadUint32(baseline, i*4)
		}
		r.Unknown0 = protocol.ReadUint32(profile, 0)
		states[r.Key] = r
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, false, err
	}
	available := map[uint16]bool{}
	for _, r := range config.Rules.Tasks {
		if !r.Enabled {
			continue
		}
		hasParent, unlocked := false, false
		for _, parent := range config.Rules.Tasks {
			if parent.Next == r.ID {
				hasParent = true
				unlocked = unlocked || states[parent.ID].State == 3
			}
		}
		if !hasParent || unlocked || states[r.ID].State != 0 {
			available[r.ID] = true
		}
	}
	if action != 0 {
		if !available[key] {
			return nil, false, ErrDenied
		}
		current := states[key]
		if current.State == 3 {
			return nil, false, ErrDenied
		}
		if action == 6050 && current.State != 2 {
			var frozen TaskRule
			for _, r := range config.Rules.Tasks {
				if r.ID == key {
					frozen = r
					break
				}
			}
			ruleData, e := json.Marshal(frozen)
			if e != nil {
				return nil, false, e
			}
			if _, err = tx.Exec("INSERT INTO task_progress(uid,task_key,state,baseline,rule_revision,rule_data) VALUES(?,?,2,?,?,?) ON DUPLICATE KEY UPDATE state=2,baseline=VALUES(baseline),rule_revision=VALUES(rule_revision),rule_data=VALUES(rule_data)", uid, key, profile[129:245], config.Revision, ruleData); err != nil {
				return nil, false, err
			}
			current = protocol.TaskProgress{Key: key, State: 2, Unknown0: protocol.ReadUint32(profile, 0)}
			for i := range current.ProfileBaseline {
				current.ProfileBaseline[i] = protocol.ReadUint32(profile, 129+i*4)
			}
			states[key] = current
			changed = true
		}
		if action == 6080 && current.State == 2 {
			if _, err = tx.Exec("UPDATE task_progress SET state=1,baseline=?,rule_revision=0,rule_data=NULL WHERE uid=? AND task_key=?", make([]byte, 116), uid, key); err != nil {
				return nil, false, err
			}
			states[key] = protocol.TaskProgress{Key: key, State: 1, Unknown0: protocol.ReadUint32(profile, 0)}
			changed = true
		}
	}
	result := []protocol.TaskProgress{}
	for _, r := range config.Rules.Tasks {
		if available[r.ID] {
			state, ok := states[r.ID]
			if !ok {
				state = protocol.TaskProgress{Key: r.ID, State: 1, Unknown0: protocol.ReadUint32(profile, 0)}
			}
			result = append(result, state)
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, false, err
	}
	return result, changed, nil
}
