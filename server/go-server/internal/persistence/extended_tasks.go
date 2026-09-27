package persistence

import (
	"database/sql"
	"encoding/json"
	"kungfu.local/server/internal/protocol"
)

type ExtendedTaskSnapshot struct {
	ClientHash string                     `json:"client_hash"`
	Rule       ExtendedTaskRule           `json:"rule"`
	Template   ExtendedTaskCatalogueEntry `json:"template"`
}

type ExtendedTaskState struct {
	Key      uint16
	Cycle    string
	State    byte
	Revision uint64
	Snapshot ExtendedTaskSnapshot
	Counts   [3]uint32
}

// Only the authenticated session supplies uid and the server's configured
// archive supplies hash. Native process pointers/state claims are not inputs.
// Daily cycles use the database's UTC date as explicit emulator policy;
// newbie tasks have one permanent cycle. Historical rows are retained.
func (s *TaskManager) ExtendedTaskTransition(uid uint64, hash string, action uint32, key uint16) (ExtendedTaskState, bool, error) {
	var result ExtendedTaskState
	kind := ""
	switch action {
	case 6051, 6081:
		kind = "daily"
	case 6052, 6082:
		kind = "newbie"
	default:
		return result, false, ErrDenied
	}
	if uid == 0 || key == 0 || hash == "" {
		return result, false, ErrDenied
	}
	tx, txCancel, err := beginTransaction(s.store.DB)
	defer txCancel()
	if err != nil {
		return result, false, err
	}
	defer tx.Rollback()
	var owner uint64
	if err = tx.QueryRow("SELECT uid FROM accounts WHERE uid=? FOR UPDATE", uid).Scan(&owner); err != nil {
		return result, false, err
	}
	var settings TaskSettings
	var data []byte
	if err = tx.QueryRow("SELECT revision,rules FROM task_rules WHERE id=1 FOR UPDATE").Scan(&settings.Revision, &data); err != nil {
		return result, false, err
	}
	if err = json.Unmarshal(data, &settings.Rules); err != nil {
		return result, false, err
	}
	if err = settings.Validate(); err != nil {
		return result, false, err
	}
	rules := settings.Rules.Extended
	if rules == nil || rules.ClientHash != hash {
		return result, false, ErrDenied
	}
	snapshot := ExtendedTaskSnapshot{ClientHash: hash}
	for _, r := range rules.Tasks {
		if r.ID == key && r.Kind == kind && r.Enabled {
			snapshot.Rule = r
		}
	}
	for _, r := range rules.Catalogue {
		if r.ID == key && r.Kind == kind {
			snapshot.Template = r
		}
	}
	if snapshot.Rule.ID == 0 || snapshot.Template.ID == 0 {
		return result, false, ErrDenied
	}
	cycle := ""
	if kind == "daily" {
		if err = tx.QueryRow("SELECT DATE_FORMAT(UTC_DATE(),'%Y-%m-%d')").Scan(&cycle); err != nil {
			return result, false, err
		}
	}
	result = ExtendedTaskState{Key: key, Cycle: cycle, State: 1, Revision: settings.Revision, Snapshot: snapshot}
	var frozen, counts []byte
	err = tx.QueryRow("SELECT state,rule_revision,rule_data,counts FROM extended_task_progress WHERE uid=? AND task_key=? AND cycle=?", uid, key, cycle).Scan(&result.State, &result.Revision, &frozen, &counts)
	if err != nil && err != sql.ErrNoRows {
		return result, false, err
	}
	if err == nil {
		if len(counts) != 12 || result.State < 1 || result.State > 4 {
			return result, false, ErrDenied
		}
		if err = json.Unmarshal(frozen, &result.Snapshot); err != nil {
			return result, false, err
		}
		if result.Snapshot.ClientHash != hash || result.Snapshot.Rule.ID != key || result.Snapshot.Rule.Kind != kind || result.Snapshot.Template.ID != key || result.Snapshot.Template.Kind != kind {
			return result, false, ErrDenied
		}
		frozenRules := ExtendedTaskRules{ClientHash: hash, Catalogue: []ExtendedTaskCatalogueEntry{result.Snapshot.Template}, Tasks: []ExtendedTaskRule{result.Snapshot.Rule}}
		if err = frozenRules.Validate(); err != nil {
			return result, false, err
		}
		for i := range result.Counts {
			result.Counts[i] = protocol.ReadUint32(counts, i*4)
		}
	}
	if result.State == 3 || result.State == 4 {
		return result, false, ErrDenied
	}
	target := byte(1)
	if action == 6051 || action == 6052 {
		target = 2
	}
	if result.State == target {
		return result, false, tx.Commit()
	}
	result.State = target
	result.Counts = [3]uint32{}
	result.Revision = settings.Revision
	result.Snapshot = snapshot
	data, err = json.Marshal(snapshot)
	if err != nil {
		return result, false, err
	}
	_, err = tx.Exec("INSERT INTO extended_task_progress(uid,task_key,cycle,state,rule_revision,rule_data,counts) VALUES(?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE state=VALUES(state),rule_revision=VALUES(rule_revision),rule_data=VALUES(rule_data),counts=VALUES(counts)", uid, key, cycle, target, result.Revision, data, make([]byte, 12))
	if err != nil {
		return result, false, err
	}
	if err = tx.Commit(); err != nil {
		return result, false, err
	}
	return result, true, nil
}
