package persistence

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

type HonourSettings struct {
	Revision uint64      `json:"revision"`
	Rules    HonourRules `json:"rules"`
}

// Only the server knows the legacy runtime configuration. GM must not create an
// empty rules row before this import and thereby change historical period IDs.
func (s *Store) SeedHonourSettings(rules HonourRules) error {
	if err := rules.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(rules)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec("INSERT IGNORE INTO honour_rules(id,revision,rules) VALUES(1,1,?)", data)
	return err
}

func (s *Store) HonourSettings(fallback HonourRules) (HonourSettings, error) {
	r := HonourSettings{Rules: fallback}
	var data []byte
	err := s.DB.QueryRow("SELECT revision,rules FROM honour_rules WHERE id=1").Scan(&r.Revision, &data)
	if err == sql.ErrNoRows {
		return r, r.Rules.Validate()
	}
	if err != nil {
		return r, err
	}
	// Do not inherit missing fields from fallback once a stored config exists.
	r.Rules = HonourRules{}
	if err = json.Unmarshal(data, &r.Rules); err != nil {
		return r, err
	}
	return r, r.Rules.Validate()
}

func (s *Store) SaveHonourSettings(r HonourSettings) (HonourSettings, error) {
	if err := r.Rules.Validate(); err != nil {
		return HonourSettings{}, err
	}
	data, err := json.Marshal(r.Rules)
	if err != nil {
		return HonourSettings{}, err
	}
	tx, txCancel, err := beginTransaction(s.DB)
	defer txCancel()
	if err != nil {
		return HonourSettings{}, err
	}
	defer tx.Rollback()
	var revision uint64
	var before []byte
	if err = tx.QueryRow("SELECT revision,rules FROM honour_rules WHERE id=1 FOR UPDATE").Scan(&revision, &before); err != nil {
		if err == sql.ErrNoRows {
			return HonourSettings{}, fmt.Errorf("请先启动新版服务器导入原荣誉配置")
		}
		return HonourSettings{}, err
	}
	if revision != r.Revision {
		return HonourSettings{}, fmt.Errorf("荣誉配置已被修改，请重新读取后保存")
	}
	var old HonourRules
	if err = json.Unmarshal(before, &old); err != nil {
		return HonourSettings{}, err
	}
	// Period number is an index in this list and is persisted in honour_stats.
	// Deleting, renaming or reordering old entries would reinterpret history.
	if len(r.Rules.Periods) < len(old.Periods) {
		return HonourSettings{}, fmt.Errorf("历史期次不能删除")
	}
	for i, name := range old.Periods {
		if r.Rules.Periods[i] != name {
			return HonourSettings{}, fmt.Errorf("历史期次不能改名或调整顺序")
		}
	}
	if _, err = tx.Exec("UPDATE honour_rules SET revision=revision+1,rules=? WHERE id=1", data); err != nil {
		return HonourSettings{}, err
	}
	if _, err = tx.Exec("INSERT INTO honour_rules_audit(revision,before_data,after_data) VALUES(?,?,?)", revision+1, before, data); err != nil {
		return HonourSettings{}, err
	}
	if err = tx.Commit(); err != nil {
		return HonourSettings{}, err
	}
	r.Revision++
	return r, nil
}
