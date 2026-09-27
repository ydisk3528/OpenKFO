package persistence

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// Percent is the percentage paid, not the percentage taken off. Zero follows
// the native no-extra-discount branch. Values are emulator policy, not retail rules.
type VIPShopRules struct {
	Enabled  bool   `json:"enabled"`
	Silver   uint32 `json:"silver"`
	Gold     uint32 `json:"gold"`
	Platinum uint32 `json:"platinum"`
}
type VIPShopSettings struct {
	Revision uint64       `json:"revision"`
	Rules    VIPShopRules `json:"rules"`
}

func (a VIPShopSettings) Validate() error {
	if a.Rules.Silver > 100 || a.Rules.Gold > 100 || a.Rules.Platinum > 100 {
		return fmt.Errorf("VIP支付比例须为0至100；0不额外打折，80为支付原价80%%")
	}
	return nil
}
func (r VIPShopRules) Percent(kind uint32) uint32 {
	if !r.Enabled {
		return 0
	}
	switch kind {
	case 2:
		return r.Silver
	case 3:
		return r.Gold
	case 4:
		return r.Platinum
	default:
		return 0
	}
}

// Native 85D37B..85D3A8 uses signed DWORD multiplication then division by
// 100. Reject overflow or an accidental zero-priced paid item rather than
// letting server and client disagree. An absent currency remains zero.
func vipShopPrice(base, percent uint32) (uint32, error) {
	if base > 0x7fffffff || percent > 100 {
		return 0, ErrDenied
	}
	if base == 0 || percent == 0 {
		return base, nil
	}
	product := uint64(base) * uint64(percent)
	if product > 0x7fffffff || product < 100 {
		return 0, ErrDenied
	}
	return uint32(product / 100), nil
}

func (s *ShopManager) VIPShopSettings() (VIPShopSettings, error) {
	var a VIPShopSettings
	var data []byte
	err := s.store.DB.QueryRow("SELECT revision,rules FROM vip_shop_rules WHERE id=1").Scan(&a.Revision, &data)
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
func (s *ShopManager) SaveVIPShopSettings(a VIPShopSettings) (VIPShopSettings, error) {
	if err := a.Validate(); err != nil {
		return VIPShopSettings{}, err
	}
	data, err := json.Marshal(a.Rules)
	if err != nil {
		return VIPShopSettings{}, err
	}
	tx, txCancel, err := beginTransaction(s.store.DB)
	defer txCancel()
	if err != nil {
		return VIPShopSettings{}, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT IGNORE INTO vip_shop_rules(id,revision,rules) VALUES(1,0,'{\"enabled\":false,\"silver\":0,\"gold\":0,\"platinum\":0}')"); err != nil {
		return VIPShopSettings{}, err
	}
	var revision uint64
	var before []byte
	if err = tx.QueryRow("SELECT revision,rules FROM vip_shop_rules WHERE id=1 FOR UPDATE").Scan(&revision, &before); err != nil {
		return VIPShopSettings{}, err
	}
	if revision != a.Revision {
		return VIPShopSettings{}, fmt.Errorf("VIP商城配置已被修改，请重新读取后保存")
	}
	if _, err = tx.Exec("UPDATE vip_shop_rules SET revision=revision+1,rules=? WHERE id=1", data); err != nil {
		return VIPShopSettings{}, err
	}
	if _, err = tx.Exec("INSERT INTO vip_shop_rules_audit(revision,before_data,after_data) VALUES(?,?,?)", revision+1, before, data); err != nil {
		return VIPShopSettings{}, err
	}
	if err = tx.Commit(); err != nil {
		return VIPShopSettings{}, err
	}
	a.Revision++
	return a, nil
}
