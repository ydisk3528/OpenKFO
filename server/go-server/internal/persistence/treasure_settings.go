package persistence

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/big"
)

// Treasure settings are kept independently of shop prices and client resources.
// The native draw protocol must be connected before these pools award anything.
type TreasurePrize struct {
	Weight uint32 `json:"weight"`
	RewardBundle
}
type TreasurePool struct {
	Name       string          `json:"name"`
	TicketKind uint8           `json:"ticket_kind"`
	Cost       uint32          `json:"cost"`
	Prizes     []TreasurePrize `json:"prizes"`
}
type TreasureSettings struct {
	Revision uint64         `json:"revision"`
	Pools    []TreasurePool `json:"pools"`
}

func (a TreasureSettings) Validate() error {
	if len(a.Pools) > 2 {
		return fmt.Errorf("只支持百宝券和节日百宝券两个奖池")
	}
	seen := map[uint8]bool{}
	for _, p := range a.Pools {
		if (p.TicketKind != 75 && p.TicketKind != 76) || seen[p.TicketKind] || len(p.Name) == 0 || len(p.Name) > 120 || p.Cost < 1 || p.Cost > 999 || len(p.Prizes) > 500 {
			return fmt.Errorf("奖池类型、名称、消耗数量或奖励数量无效")
		}
		seen[p.TicketKind] = true
		for _, v := range p.Prizes {
			if v.Weight < 1 || v.Weight > 1000000 {
				return fmt.Errorf("奖励权重必须为1至1000000")
			}
			if err := v.RewardBundle.Validate(); err != nil {
				return fmt.Errorf("奖励金额或道具规格无效")
			}
			if len(v.Items) == 0 && v.Gold == 0 && v.Tickets == 0 {
				return fmt.Errorf("奖励不能为空")
			}
		}
	}
	return nil
}
func (s *Store) TreasureSettings() (TreasureSettings, error) {
	a := TreasureSettings{Pools: []TreasurePool{{Name: "百宝", TicketKind: 75, Cost: 1, Prizes: []TreasurePrize{}}, {Name: "节日百宝", TicketKind: 76, Cost: 1, Prizes: []TreasurePrize{}}}}
	var raw []byte
	err := s.DB.QueryRow("SELECT revision,rules FROM treasure_rules WHERE id=1").Scan(&a.Revision, &raw)
	if err == sql.ErrNoRows {
		return a, nil
	}
	if err != nil {
		return a, err
	}
	if err = json.Unmarshal(raw, &a.Pools); err != nil {
		return a, err
	}
	return a, a.Validate()
}
func (s *Store) SaveTreasureSettings(a TreasureSettings) (TreasureSettings, error) {
	if err := a.Validate(); err != nil {
		return a, err
	}
	for _, p := range a.Pools {
		for _, v := range p.Prizes {
			if err := s.RewardManager().validateBundleItems(v.RewardBundle); err != nil {
				return a, fmt.Errorf("奖池%s引用的道具不存在或规格无效: %w", p.Name, err)
			}
		}
	}
	raw, err := json.Marshal(a.Pools)
	if err != nil {
		return a, err
	}
	tx, cancel, err := beginTransaction(s.DB)
	defer cancel()
	if err != nil {
		return a, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT IGNORE INTO treasure_rules(id,revision,rules) VALUES(1,0,'[]')"); err != nil {
		return a, err
	}
	var revision uint64
	var before []byte
	if err = tx.QueryRow("SELECT revision,rules FROM treasure_rules WHERE id=1 FOR UPDATE").Scan(&revision, &before); err != nil {
		return a, err
	}
	if revision != a.Revision {
		return a, fmt.Errorf("百宝配置已被修改，请重新读取后保存")
	}
	if _, err = tx.Exec("UPDATE treasure_rules SET revision=revision+1,rules=? WHERE id=1", raw); err != nil {
		return a, err
	}
	if _, err = tx.Exec("INSERT INTO treasure_rules_audit(revision,before_data,after_data) VALUES(?,?,?)", revision+1, before, raw); err != nil {
		return a, err
	}
	if err = tx.Commit(); err != nil {
		return a, err
	}
	a.Revision++
	return a, nil
}

// RefreshSix samples six distinct pool entries uniformly. Weight is exclusively
// the winning weight within the displayed six; it does not affect appearance.
func (p TreasurePool) RefreshSix() ([]TreasurePrize, error) {
	if err := (TreasureSettings{Pools: []TreasurePool{p}}).Validate(); err != nil {
		return nil, err
	}
	if len(p.Prizes) < 6 {
		return nil, fmt.Errorf("总奖池至少需要6条奖励；超过6条时才能更换组合")
	}
	entries := append([]TreasurePrize(nil), p.Prizes...)
	for i := 0; i < 6; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(entries)-i)))
		if err != nil {
			return nil, err
		}
		j := i + int(n.Int64())
		entries[i], entries[j] = entries[j], entries[i]
	}
	return entries[:6], nil
}

// SelectTreasureWinner accepts only the actual displayed group, never the full pool.
// This helper does not debit tickets or award assets.
func SelectTreasureWinner(group []TreasurePrize) (int, error) {
	if len(group) != 6 {
		return 0, fmt.Errorf("百宝当前组必须正好6条奖励")
	}
	var total uint64
	for _, p := range group {
		if p.Weight < 1 || p.Weight > 1000000 {
			return 0, fmt.Errorf("中奖权重无效")
		}
		total += uint64(p.Weight)
	}
	n, err := rand.Int(rand.Reader, new(big.Int).SetUint64(total))
	if err != nil {
		return 0, err
	}
	return treasureWinnerAt(group, n.Uint64()), nil
}
func treasureWinnerAt(group []TreasurePrize, ticket uint64) int {
	for i, p := range group {
		if ticket < uint64(p.Weight) {
			return i
		}
		ticket -= uint64(p.Weight)
	}
	return -1
}
