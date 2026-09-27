package persistence

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// All nonzero conditions must be met; rewards are explicit emulator policy.
type TitleRule struct {
	Level          byte     `json:"level"`
	Enabled        bool     `json:"enabled"`
	MinPlayerLevel uint16   `json:"min_player_level"`
	CompletedTask  uint16   `json:"completed_task"`
	Matches        uint32   `json:"matches"`
	Wins           uint32   `json:"wins"`
	Choices        []uint32 `json:"choices"`
}
type TitleRules struct {
	ClientHash string                `json:"client_hash,omitempty"`
	Catalogue  []TitleCatalogueEntry `json:"catalogue,omitempty"`
	Enabled    bool                  `json:"enabled"`
	Titles     []TitleRule           `json:"titles"`
}

type TitleCatalogueEntry struct {
	Level uint16 `json:"level"`
	Name  string `json:"name"`
}

func (r TitleRules) SupportedLevels(hash string) []byte {
	if hash == "" || r.ClientHash != hash {
		return nil
	}
	var levels []byte
	for _, entry := range r.Catalogue {
		if entry.Level > 0 && entry.Level <= 255 {
			levels = append(levels, byte(entry.Level))
		}
	}
	return levels
}

type TitleSettings struct {
	Revision uint64     `json:"revision"`
	Rules    TitleRules `json:"rules"`
}

func (a TitleSettings) Validate() error {
	if a.Rules.ClientHash != "" || len(a.Rules.Catalogue) > 0 {
		decoded, err := hex.DecodeString(a.Rules.ClientHash)
		if err != nil || len(decoded) != 32 || a.Rules.ClientHash != strings.ToLower(a.Rules.ClientHash) || len(a.Rules.Catalogue) == 0 || len(a.Rules.Catalogue) > 256 {
			return fmt.Errorf("称号目录必须绑定有效客户端SHA256")
		}
		levels := map[uint16]bool{}
		for _, e := range a.Rules.Catalogue {
			if e.Level > 255 || levels[e.Level] || strings.TrimSpace(e.Name) == "" || len(e.Name) > 256 {
				return fmt.Errorf("称号目录等级或名称无效")
			}
			levels[e.Level] = true
		}
		for _, r := range a.Rules.Titles {
			if r.Enabled && !levels[uint16(r.Level)] {
				return fmt.Errorf("启用称号不在客户端目录中")
			}
		}
	}
	if len(a.Rules.Titles) > 255 || (a.Rules.Enabled && len(a.Rules.Titles) == 0) {
		return fmt.Errorf("称号配置需要1至255条规则")
	}
	seen := map[byte]bool{}
	for _, r := range a.Rules.Titles {
		if r.Level == 0 || seen[r.Level] {
			return fmt.Errorf("称号等级必须非零且唯一")
		}
		seen[r.Level] = true
		if r.MinPlayerLevel > MaxRoleLevel || r.Matches > 0x7fffffff || r.Wins > 0x7fffffff {
			return fmt.Errorf("称号条件超过允许范围")
		}
		if r.Enabled && r.MinPlayerLevel == 0 && r.CompletedTask == 0 && r.Matches == 0 && r.Wins == 0 {
			return fmt.Errorf("启用称号必须配置达成条件")
		}
		if len(r.Choices) > 7 {
			return fmt.Errorf("称号最多支持7个候选商品，留空仅晋升称号")
		}
		keys := map[uint32]bool{}
		for _, key := range r.Choices {
			if key == 0 || keys[key] {
				return fmt.Errorf("候选商品编号必须非零且唯一")
			}
			keys[key] = true
		}
	}
	return nil
}

func (s *TitleManager) TitleSettings() (TitleSettings, error) {
	a := TitleSettings{}
	var data []byte
	err := s.store.DB.QueryRow("SELECT revision,rules FROM title_rules WHERE id=1").Scan(&a.Revision, &data)
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
func (s *TitleManager) SaveTitleSettings(a TitleSettings) (TitleSettings, error) {
	if err := a.Validate(); err != nil {
		return TitleSettings{}, err
	}
	if a.Rules.Titles == nil {
		a.Rules.Titles = []TitleRule{}
	}
	data, err := json.Marshal(a.Rules)
	if err != nil {
		return TitleSettings{}, err
	}
	tx, txCancel, err := beginTransaction(s.store.DB)
	defer txCancel()
	if err != nil {
		return TitleSettings{}, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT IGNORE INTO title_rules(id,revision,rules) VALUES(1,0,'{\"enabled\":false,\"titles\":[]}')"); err != nil {
		return TitleSettings{}, err
	}
	var revision uint64
	var before []byte
	if err = tx.QueryRow("SELECT revision,rules FROM title_rules WHERE id=1 FOR UPDATE").Scan(&revision, &before); err != nil {
		return TitleSettings{}, err
	}
	if revision != a.Revision {
		return TitleSettings{}, fmt.Errorf("称号配置已被修改，请重新读取后保存")
	}
	if a.Rules.Enabled {
		// Validate one catalogue for all active rules, not one full shop query
		// per title. Disabled rules can retain drafts or unavailable definitions.
		var choices []uint32
		seen := map[uint32]bool{}
		for _, rule := range a.Rules.Titles {
			if !rule.Enabled {
				continue
			}
			for _, key := range rule.Choices {
				if !seen[key] {
					choices = append(choices, key)
					seen[key] = true
				}
			}
		}
		if len(choices) != 0 {
			if _, err = weaponRewardCatalog(tx, choices); err != nil {
				return TitleSettings{}, fmt.Errorf("称号奖励武器展示目录无效，请检查物品定义与商城配置：%w", err)
			}
		}
	}
	if _, err = tx.Exec("UPDATE title_rules SET revision=revision+1,rules=? WHERE id=1", data); err != nil {
		return TitleSettings{}, err
	}
	if _, err = tx.Exec("INSERT INTO title_rules_audit(revision,before_data,after_data) VALUES(?,?,?)", revision+1, before, data); err != nil {
		return TitleSettings{}, err
	}
	if err = tx.Commit(); err != nil {
		return TitleSettings{}, err
	}
	a.Revision++
	return a, nil
}
