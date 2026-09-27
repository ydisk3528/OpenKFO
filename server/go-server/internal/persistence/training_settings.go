package persistence

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// TrainingRules are server policy, not recovered official reward values.
type TrainingRule struct {
	Level     uint32 `json:"level"`
	XPPerHour uint32 `json:"xp_per_hour"`
	XPCap     uint32 `json:"xp_cap"`
}
type TrainingRules struct {
	Enabled bool           `json:"enabled"`
	Levels  []TrainingRule `json:"levels"`
}
type TrainingSettings struct {
	Revision uint64        `json:"revision"`
	Rules    TrainingRules `json:"rules"`
}

func (a TrainingSettings) Validate() error {
	if len(a.Rules.Levels) > 9 || (a.Rules.Enabled && len(a.Rules.Levels) != 9) {
		return fmt.Errorf("启用名侠奖励需要完整的0至8级配置")
	}
	for i, r := range a.Rules.Levels {
		if r.Level != uint32(i) || r.XPPerHour > 0x7fffffff || r.XPCap > 0x7fffffff {
			return fmt.Errorf("名侠等级必须从0连续，经验不得超过有符号32位范围")
		}
		if r.XPPerHour == 0 && r.XPCap != 0 {
			return fmt.Errorf("每小时经验为0时累计经验上限也必须为0")
		}
		if r.XPPerHour != 0 && r.XPCap > (0x7fffffff/r.XPPerHour)*r.XPPerHour {
			return fmt.Errorf("经验上限过高，客户端整小时乘法会溢出，请降低经验上限")
		}
	}
	return nil
}

// Award uses wide arithmetic. The caller supplies server-measured time only.
func (r TrainingRule) Award(minutes uint32) uint32 {
	amount := uint64(minutes/60) * uint64(r.XPPerHour)
	if amount > uint64(r.XPCap) {
		return r.XPCap
	}
	return uint32(amount)
}
func (s *TrainingManager) TrainingSettings() (TrainingSettings, error) {
	a := TrainingSettings{}
	var data []byte
	err := s.store.DB.QueryRow("SELECT revision,rules FROM training_rules WHERE id=1").Scan(&a.Revision, &data)
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
func (s *TrainingManager) SaveTrainingSettings(a TrainingSettings) (TrainingSettings, error) {
	if err := a.Validate(); err != nil {
		return TrainingSettings{}, err
	}
	if a.Rules.Levels == nil {
		a.Rules.Levels = []TrainingRule{}
	}
	data, err := json.Marshal(a.Rules)
	if err != nil {
		return TrainingSettings{}, err
	}
	tx, txCancel, err := beginTransaction(s.store.DB)
	defer txCancel()
	if err != nil {
		return TrainingSettings{}, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT IGNORE INTO training_rules(id,revision,rules) VALUES(1,0,'{\"enabled\":false,\"levels\":[]}')"); err != nil {
		return TrainingSettings{}, err
	}
	var revision uint64
	var before []byte
	if err = tx.QueryRow("SELECT revision,rules FROM training_rules WHERE id=1 FOR UPDATE").Scan(&revision, &before); err != nil {
		return TrainingSettings{}, err
	}
	if revision != a.Revision {
		return TrainingSettings{}, fmt.Errorf("名侠配置已被修改，请重新读取后保存")
	}
	if _, err = tx.Exec("UPDATE training_rules SET revision=revision+1,rules=? WHERE id=1", data); err != nil {
		return TrainingSettings{}, err
	}
	if _, err = tx.Exec("INSERT INTO training_rules_audit(revision,before_data,after_data) VALUES(?,?,?)", revision+1, before, data); err != nil {
		return TrainingSettings{}, err
	}
	if err = tx.Commit(); err != nil {
		return TrainingSettings{}, err
	}
	a.Revision++
	return a, nil
}
