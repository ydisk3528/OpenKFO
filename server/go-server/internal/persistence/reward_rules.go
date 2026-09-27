package persistence

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"kungfu.local/server/internal/protocol"
)

type LevelReward struct {
	Level          uint16 `json:"level"`
	NextExperience uint32 `json:"next_experience"`
	WinGold        uint32 `json:"win_gold"`
	LossGold       uint32 `json:"loss_gold"`
	DrawGold       uint32 `json:"draw_gold"`
	WinExperience  uint32 `json:"win_experience"`
	LossExperience uint32 `json:"loss_experience"`
	DrawExperience uint32 `json:"draw_experience"`
}

type RewardRules struct {
	StageRewards   []StageMapRewards `json:"stage_rewards"`
	Tutorial       *RewardBundle     `json:"tutorial_reward,omitempty"`
	LevelGifts     []LevelGift       `json:"level_gifts"`
	Drops          []DropRule        `json:"drops,omitempty"`
	GrowthEnabled  bool              `json:"growth_enabled"`
	Levels         []LevelReward     `json:"levels,omitempty"`
	WinGold        uint32            `json:"win_gold"`
	LossGold       uint32            `json:"loss_gold"`
	DrawGold       uint32            `json:"draw_gold"`
	WinExperience  uint32            `json:"win_experience"`
	LossExperience uint32            `json:"loss_experience"`
	DrawExperience uint32            `json:"draw_experience"`
}

type RewardSettings struct {
	Revision uint64      `json:"revision"`
	Rules    RewardRules `json:"rules"`
}

func (m *RewardManager) BattleRewards(fallback RewardRules) (RewardSettings, error) {
	result := RewardSettings{Rules: fallback}
	var data []byte
	err := m.store.DB.QueryRow("SELECT revision,rules FROM battle_reward_rules WHERE id=1").Scan(&result.Revision, &data)
	if err == sql.ErrNoRows {
		result.Rules = result.Rules.Normalized()
		return result, result.Rules.Validate()
	}
	if err != nil {
		return result, err
	}
	err = json.Unmarshal(data, &result.Rules)
	result.Rules = result.Rules.Normalized()
	if err == nil {
		err = result.Rules.Validate()
	}
	return result, err
}

func (m *RewardManager) SeedBattleRewards(rules RewardRules) error {
	rules = rules.Normalized()
	if err := rules.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(rules)
	if err != nil {
		return err
	}
	_, err = m.store.DB.Exec("INSERT IGNORE INTO battle_reward_rules(id,revision,rules) VALUES(1,1,?)", data)
	return err
}

func (m *RewardManager) SaveBattleRewards(revision uint64, rules RewardRules) (RewardSettings, error) {
	// Older GM clients omit this field; omission must not erase existing gifts.
	if rules.LevelGifts == nil || rules.Tutorial == nil || rules.StageRewards == nil {
		old, err := m.BattleRewards(RewardRules{})
		if err != nil {
			return RewardSettings{}, err
		}
		if rules.LevelGifts == nil {
			rules.LevelGifts = old.Rules.LevelGifts
		}
		if rules.Tutorial == nil {
			rules.Tutorial = old.Rules.Tutorial
		}
		if rules.StageRewards == nil {
			rules.StageRewards = old.Rules.StageRewards
		}
	}

	rules = rules.Normalized()
	if err := rules.Validate(); err != nil {
		return RewardSettings{}, err
	}
	for _, drop := range rules.Drops {
		var record []byte
		if err := m.store.DB.QueryRow("SELECT record FROM item_definitions WHERE definition_key=?", drop.CatalogKey).Scan(&record); err != nil || !validDropItem(record) {
			return RewardSettings{}, fmt.Errorf("掉落商品 %d 不存在或不是有效的未装备武器", drop.CatalogKey)
		}
	}
	if rules.Tutorial != nil {
		if err := m.validateBundleItems(*rules.Tutorial); err != nil {
			return RewardSettings{}, err
		}
		weapons := map[uint32]bool{}
		var choices []uint32
		for _, key := range rules.Tutorial.Items {
			var record []byte
			if err := m.store.DB.QueryRow("SELECT record FROM item_definitions WHERE definition_key=?", key).Scan(&record); err != nil {
				return RewardSettings{}, err
			}
			if len(record) != protocol.InventoryRecordSize {
				return RewardSettings{}, ErrDenied
			}
			if record[4] == protocol.ItemWeapon {
				if weapons[key] || len(weapons) == 7 {
					return RewardSettings{}, fmt.Errorf("新手奖励最多配置7件不重复的武器，玩家任选一件")
				}
				weapons[key] = true
				choices = append(choices, key)
			}
		}
		// Reject an unusable native selector while the GM is saving, rather
		// than discovering its missing/conflicting catalogue at guide completion.
		if len(choices) > 0 {
			tx, txCancel, err := beginTransaction(m.store.DB)
			defer txCancel()
			if err != nil {
				return RewardSettings{}, err
			}
			_, err = weaponRewardCatalog(tx, choices)
			tx.Rollback()
			if err != nil {
				return RewardSettings{}, fmt.Errorf("新手奖励武器展示目录无效，请检查物品定义与商城配置：%w", err)
			}
		}
	}
	for _, g := range rules.LevelGifts {
		if err := m.validateBundleItems(RewardBundle{Items: g.Items, Gold: g.Gold, Tickets: g.Tickets}); err != nil {
			return RewardSettings{}, err
		}
	}
	for _, stage := range rules.StageRewards {
		for _, reward := range []StageReward{stage.Clear, stage.Failed} {
			if err := m.validateBundleItems(reward.RewardBundle); err != nil {
				return RewardSettings{}, err
			}
		}
	}
	data, err := json.Marshal(rules)
	if err != nil {
		return RewardSettings{}, err
	}
	var result sql.Result
	if revision == 0 {
		result, err = m.store.DB.Exec("INSERT IGNORE INTO battle_reward_rules(id,revision,rules) VALUES(1,1,?)", data)
	} else {
		result, err = m.store.DB.Exec("UPDATE battle_reward_rules SET rules=?,revision=revision+1 WHERE id=1 AND revision=?", data, revision)
	}
	if err != nil {
		return RewardSettings{}, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return RewardSettings{}, err
	}
	if n != 1 {
		return RewardSettings{}, fmt.Errorf("奖励配置已被修改，请重新读取后保存")
	}
	return RewardSettings{Revision: revision + 1, Rules: rules}, nil
}

// Expand old settings without changing amounts or enabling a made-up curve.
func (r RewardRules) Normalized() RewardRules {
	if len(r.Levels) == 0 {
		for level := 1; level <= int(MaxRoleLevel); level++ {
			r.Levels = append(r.Levels, LevelReward{Level: uint16(level), WinGold: r.WinGold, LossGold: r.LossGold, DrawGold: r.DrawGold, WinExperience: r.WinExperience, LossExperience: r.LossExperience, DrawExperience: r.DrawExperience})
		}
	}
	return r
}
func (r RewardRules) Validate() error {
	if err := validateStageRewards(r.StageRewards); err != nil {
		return err
	}
	if r.Tutorial != nil {
		if err := r.Tutorial.Validate(); err != nil {
			return err
		}
	}
	if err := validateLevelGifts(r.LevelGifts); err != nil {
		return err
	}
	if err := validateDrops(r.Drops); err != nil {
		return err
	}
	if len(r.Levels) != 150 && len(r.Levels) != int(MaxRoleLevel) {
		return fmt.Errorf("必须包含完整的150级旧表或200级新表")
	}
	for i, row := range r.Levels {
		if int(row.Level) != i+1 {
			return fmt.Errorf("等级必须从1开始连续且不能重复")
		}
		for _, v := range []uint32{row.WinGold, row.LossGold, row.DrawGold, row.WinExperience, row.LossExperience, row.DrawExperience} {
			if v > 1000000 {
				return fmt.Errorf("第 %d 级单局奖励须为 0–1000000", row.Level)
			}
		}
		if row.NextExperience > 2147483647 || (int(row.Level) == len(r.Levels) && row.NextExperience != 0) {
			return fmt.Errorf("升级经验超出范围或 满级升级经验不为 0")
		}
		if r.GrowthEnabled && int(row.Level) < len(r.Levels) && row.NextExperience == 0 {
			return fmt.Errorf("启用升级前必须填写所有非满级的升级经验")
		}
	}
	return nil
}
func (r RewardRules) AtLevel(level uint16) LevelReward {
	if level < 1 {
		level = 1
	}
	r = r.Normalized()
	if int(level) > len(r.Levels) {
		level = uint16(len(r.Levels))
	}
	return r.Levels[int(level)-1]
}
