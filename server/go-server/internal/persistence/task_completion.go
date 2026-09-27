package persistence

import (
	"database/sql"
	"encoding/json"
	"kungfu.local/server/internal/protocol"
)

type TaskAwards struct {
	AlreadyClaimed    bool
	NotificationRules TaskRules
	Keys              []uint16
	Profile           []byte
	Experience        uint32
	Gold              uint32
	GoldBalance       uint32
	Items             [][]byte
}

// Conditions without an authoritative statistic remain incomplete. In
// particular neither client-reported combo nor badge counters grant rewards.
func taskConditionsMet(rule TaskRule, profile, baseline []byte) bool {
	if len(profile) != 360 || len(baseline) != 116 || len(rule.Counters) != 29 || rule.MaxCombo != 0 {
		return false
	}
	delta := func(i int) uint64 {
		current, old := protocol.ReadUint32(profile, 129+i*4), protocol.ReadUint32(baseline, i*4)
		if current < old || current > 0x7fffffff || old > 0x7fffffff {
			return 0
		}
		return uint64(current - old)
	}
	hasCondition := rule.Matches > 0
	var matches uint64
	for _, i := range []int{1, 3, 5, 7} {
		matches += delta(i)
	}
	if matches < uint64(rule.Matches) {
		return false
	}
	for i, target := range rule.Counters {
		if target == 0 {
			continue
		}
		hasCondition = true
		if i < 1 || i > 8 || delta(i) < uint64(target) {
			return false
		}
	}
	return hasCondition
}

// CompleteTasks is driven by server-owned profile counters, never request
// progress values. State, account balances and reward receipts commit together.
func (s *TaskManager) CompleteTasks(uid uint64, growth RewardRules) (r TaskAwards, err error) {
	if uid == 0 {
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
	var cfg TaskSettings
	var data []byte
	err = tx.QueryRow("SELECT revision,rules FROM task_rules WHERE id=1 FOR UPDATE").Scan(&cfg.Revision, &data)
	if err == sql.ErrNoRows {
		return r, tx.Commit()
	}
	if err != nil {
		return r, err
	}
	if err = json.Unmarshal(data, &cfg.Rules); err != nil {
		return r, err
	}
	if err = cfg.Validate(); err != nil {
		return r, err
	}
	r.NotificationRules = cfg.Rules
	if !cfg.Rules.Enabled {
		return r, tx.Commit()
	}
	enabled := map[uint16]bool{}
	for _, rule := range cfg.Rules.Tasks {
		enabled[rule.ID] = rule.Enabled
	}
	rows, err := tx.Query("SELECT task_key,baseline,rule_revision,rule_data FROM task_progress WHERE uid=? AND state=2 ORDER BY task_key", uid)
	if err != nil {
		return r, err
	}
	type pending struct {
		key      uint16
		revision uint64
		rule     TaskRule
	}
	awards := []pending{}
	for rows.Next() {
		var a pending
		var baseline, frozen []byte
		if err = rows.Scan(&a.key, &baseline, &a.revision, &frozen); err != nil {
			rows.Close()
			return r, err
		}
		if !enabled[a.key] {
			continue
		}
		if err = json.Unmarshal(frozen, &a.rule); err != nil {
			rows.Close()
			return r, err
		}
		checked := a.rule
		checked.Next = 0
		if a.rule.ID != a.key || !a.rule.Enabled || (TaskSettings{Rules: TaskRules{Enabled: true, Tasks: []TaskRule{checked}}}).Validate() != nil {
			rows.Close()
			return r, ErrDenied
		}
		if taskConditionsMet(a.rule, r.Profile, baseline) {
			awards = append(awards, a)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return r, err
	}
	if len(awards) == 0 {
		return r, tx.Commit()
	}
	var xp, gold uint64
	for _, a := range awards {
		xp += uint64(a.rule.Experience)
		gold += uint64(a.rule.Gold)
	}
	if xp > 0x7fffffff || gold > 0x7fffffff || uint64(r.GoldBalance)+gold > 0xffffffff {
		return r, ErrDenied
	}
	r.Experience, r.Gold = uint32(xp), uint32(gold)
	r.GoldBalance, r.Items, err = (RewardManager{}).GrantProgressItems(tx, uid, r.Profile, uint64(r.GoldBalance), r.Experience, r.Gold, growth)
	if err != nil {
		return TaskAwards{}, err
	}
	if _, err = tx.Exec("UPDATE accounts SET profile=?,gold=? WHERE uid=?", r.Profile, r.GoldBalance, uid); err != nil {
		return r, err
	}
	for _, a := range awards {
		if a.rule.RewardCatalog != 0 {
			item, e := (RewardManager{}).GrantItem(tx, uid, a.rule.RewardCatalog)
			if e != nil {
				return TaskAwards{}, e
			}
			r.Items = append(r.Items, item)
		}
		if _, err = tx.Exec("UPDATE task_progress SET state=3 WHERE uid=? AND task_key=?", uid, a.key); err != nil {
			return r, err
		}
		if _, err = tx.Exec("INSERT INTO task_rewards(uid,task_key,rule_revision,experience,gold) VALUES(?,?,?,?,?)", uid, a.key, a.revision, a.rule.Experience, a.rule.Gold); err != nil {
			return r, err
		}
		r.Keys = append(r.Keys, a.key)
	}
	if err = tx.Commit(); err != nil {
		return TaskAwards{}, err
	}
	return r, nil
}
