package persistence

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// Values use inventory hundredths, not currency or duration.
type TalismanUseRule struct {
	Item        uint32 `json:"item"`
	ActiveCost  uint16 `json:"active_cost"`
	PassiveCost uint16 `json:"passive_cost"`
}
type TalismanRules struct {
	Enabled bool                 `json:"enabled"`
	Uses    []TalismanUseRule    `json:"uses"`
	Repairs []TalismanRepairRule `json:"repairs"`
}
type TalismanSettings struct {
	Revision uint64        `json:"revision"`
	Rules    TalismanRules `json:"rules"`
}

func (a TalismanSettings) Validate() error {
	if len(a.Rules.Uses) > 4096 || len(a.Rules.Repairs) > 4096 || (a.Rules.Enabled && len(a.Rules.Uses)+len(a.Rules.Repairs) == 0) {
		return fmt.Errorf("法宝规则为空或超过4096条限制")
	}
	seen := map[uint32]bool{}
	for _, r := range a.Rules.Uses {
		if r.Item == 0 || seen[r.Item] {
			return fmt.Errorf("法宝使用规则物品无效或重复")
		}
		seen[r.Item] = true
	}
	seen = map[uint32]bool{}
	for _, r := range a.Rules.Repairs {
		if !r.Valid() || r.Item == r.Material || seen[r.Item] {
			return fmt.Errorf("法宝修理规则无效或重复")
		}
		seen[r.Item] = true
	}
	return nil
}
func (s *ItemManager) TalismanSettings() (TalismanSettings, error) {
	a := TalismanSettings{}
	var data []byte
	err := s.store.DB.QueryRow("SELECT revision,rules FROM talisman_rules WHERE id=1").Scan(&a.Revision, &data)
	if err == sql.ErrNoRows {
		return a, nil
	}
	if err != nil {
		return a, err
	}
	if err = json.Unmarshal(data, &a.Rules); err != nil {
		return a, err
	}
	return a, a.Validate()
}

func (s *ItemManager) SeedTalismanSettings(rules TalismanRules) error {
	if err := (TalismanSettings{Rules: rules}).Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(rules)
	if err != nil {
		return err
	}
	_, err = s.store.DB.Exec("INSERT IGNORE INTO talisman_rules(id,revision,rules) VALUES(1,1,?)", data)
	return err
}
func (s *ItemManager) SaveTalismanSettings(a TalismanSettings) (TalismanSettings, error) {
	if err := a.Validate(); err != nil {
		return TalismanSettings{}, err
	}
	if a.Rules.Uses == nil {
		a.Rules.Uses = []TalismanUseRule{}
	}
	if a.Rules.Repairs == nil {
		a.Rules.Repairs = []TalismanRepairRule{}
	}
	data, err := json.Marshal(a.Rules)
	if err != nil {
		return TalismanSettings{}, err
	}
	tx, txCancel, err := beginTransaction(s.store.DB)
	defer txCancel()
	if err != nil {
		return TalismanSettings{}, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT IGNORE INTO talisman_rules(id,revision,rules) VALUES(1,0,'{\"enabled\":false,\"uses\":[],\"repairs\":[]}')"); err != nil {
		return TalismanSettings{}, err
	}
	var revision uint64
	var before []byte
	if err = tx.QueryRow("SELECT revision,rules FROM talisman_rules WHERE id=1 FOR UPDATE").Scan(&revision, &before); err != nil {
		return TalismanSettings{}, err
	}
	if revision != a.Revision {
		return TalismanSettings{}, fmt.Errorf("法宝配置已被修改，请重新读取后保存")
	}
	if _, err = tx.Exec("UPDATE talisman_rules SET revision=revision+1,rules=? WHERE id=1", data); err != nil {
		return TalismanSettings{}, err
	}
	if _, err = tx.Exec("INSERT INTO talisman_rules_audit(revision,before_data,after_data) VALUES(?,?,?)", revision+1, before, data); err != nil {
		return TalismanSettings{}, err
	}
	if err = tx.Commit(); err != nil {
		return TalismanSettings{}, err
	}
	a.Revision++
	return a, nil
}
