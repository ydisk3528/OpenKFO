package persistence

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// Weapon upgrade values are explicit emulator policy, not original game rules.
type WeaponLevel struct {
	Level          uint32 `json:"level"`
	ScoreThreshold uint32 `json:"score_threshold"`
	Gold           uint32 `json:"gold"`
	DisplayOdds    uint32 `json:"display_odds"`
	Unknown16      byte   `json:"unknown_16"`
	AttackBonusRaw uint32 `json:"attack_bonus_raw"`
}
type WeaponRules struct {
	Enabled bool          `json:"enabled"`
	Levels  []WeaponLevel `json:"levels"`
}
type WeaponSettings struct {
	Revision uint64      `json:"revision"`
	Rules    WeaponRules `json:"rules"`
}

func (a WeaponSettings) Validate() error {
	if len(a.Rules.Levels) > 256 || (a.Rules.Enabled && len(a.Rules.Levels) < 2) {
		return fmt.Errorf("启用武器升级需要2至256条等级配置")
	}
	for i, r := range a.Rules.Levels {
		if r.Level != uint32(i) || r.ScoreThreshold == 0 || r.DisplayOdds > 100 {
			return fmt.Errorf("武器等级须从0连续，熟练度须为正数，成功率须为0至100")
		}
	}
	return nil
}
func (s *ItemManager) WeaponSettings() (WeaponSettings, error) {
	a := WeaponSettings{}
	var data []byte
	err := s.store.DB.QueryRow("SELECT revision,rules FROM weapon_rules WHERE id=1").Scan(&a.Revision, &data)
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

func (s *ItemManager) SeedWeaponSettings(rules WeaponRules) error {
	if err := (WeaponSettings{Rules: rules}).Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(rules)
	if err != nil {
		return err
	}
	_, err = s.store.DB.Exec("INSERT IGNORE INTO weapon_rules(id,revision,rules) VALUES(1,1,?)", data)
	return err
}
func (s *ItemManager) SaveWeaponSettings(a WeaponSettings) (WeaponSettings, error) {
	if err := a.Validate(); err != nil {
		return WeaponSettings{}, err
	}
	if a.Rules.Levels == nil {
		a.Rules.Levels = []WeaponLevel{}
	}
	data, err := json.Marshal(a.Rules)
	if err != nil {
		return WeaponSettings{}, err
	}
	tx, txCancel, err := beginTransaction(s.store.DB)
	defer txCancel()
	if err != nil {
		return WeaponSettings{}, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT IGNORE INTO weapon_rules(id,revision,rules) VALUES(1,0,'{\"enabled\":false,\"levels\":[]}')"); err != nil {
		return WeaponSettings{}, err
	}
	var revision uint64
	var before []byte
	if err = tx.QueryRow("SELECT revision,rules FROM weapon_rules WHERE id=1 FOR UPDATE").Scan(&revision, &before); err != nil {
		return WeaponSettings{}, err
	}
	if revision != a.Revision {
		return WeaponSettings{}, fmt.Errorf("武器升级配置已被修改，请重新读取后保存")
	}
	if _, err = tx.Exec("UPDATE weapon_rules SET revision=revision+1,rules=? WHERE id=1", data); err != nil {
		return WeaponSettings{}, err
	}
	if _, err = tx.Exec("INSERT INTO weapon_rules_audit(revision,before_data,after_data) VALUES(?,?,?)", revision+1, before, data); err != nil {
		return WeaponSettings{}, err
	}
	if err = tx.Commit(); err != nil {
		return WeaponSettings{}, err
	}
	a.Revision++
	return a, nil
}
