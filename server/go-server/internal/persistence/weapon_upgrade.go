package persistence

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"math/big"
	"time"

	"kungfu.local/server/internal/protocol"
)

// WeaponUpgradeRule is a server policy, not a recovered official formula.
// Each attempt spends the current level's score and gold. Failure keeps level.
type WeaponUpgradeRule struct{ Score, Gold, Odds uint32 }
type WeaponUpgradeResult struct {
	Item    []byte
	Gold    uint32
	Success bool
}

func (s *ItemManager) UpgradeWeapon(uid uint64, operation string, instance uint32, rules []WeaponUpgradeRule) (result WeaponUpgradeResult, err error) {
	return s.store.ItemManager().upgradeWeapon(uid, operation, instance, rules, nil)
}

// The revision is bound to the table sent to this authenticated session.
// Check it under the same transaction lock as charging; never trust client prices.
func (s *ItemManager) UpgradeWeaponConfigured(uid uint64, operation string, instance uint32, revision uint64) (WeaponUpgradeResult, error) {
	if revision == 0 {
		return WeaponUpgradeResult{}, ErrDenied
	}
	return s.store.ItemManager().upgradeWeapon(uid, operation, instance, nil, &revision)
}

func (s *ItemManager) upgradeWeapon(uid uint64, operation string, instance uint32, rules []WeaponUpgradeRule, revision *uint64) (result WeaponUpgradeResult, err error) {
	if len(operation) == 0 || len(operation) > 128 || instance == 0 || (revision == nil && (len(rules) < 2 || len(rules) > 256)) {
		return result, ErrDenied
	}
	for _, r := range rules {
		if r.Score == 0 || r.Odds > 100 {
			return result, ErrDenied
		}
	}
	tx, txCancel, err := beginTransaction(s.store.DB)
	defer txCancel()
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if err = tx.QueryRow(`SELECT gold FROM accounts WHERE uid=? FOR UPDATE`, uid).Scan(&result.Gold); err != nil {
		return result, err
	}
	var previousInstance uint32
	err = tx.QueryRow(`SELECT instance,success FROM weapon_upgrades WHERE uid=? AND operation_id=?`, uid, operation).Scan(&previousInstance, &result.Success)
	if err != nil && err != sql.ErrNoRows {
		return result, err
	}
	replay := err == nil
	if replay && previousInstance != instance {
		return result, ErrDenied
	}
	if !replay && revision != nil {
		var settings WeaponSettings
		var data []byte
		if err = tx.QueryRow("SELECT revision,rules FROM weapon_rules WHERE id=1 FOR UPDATE").Scan(&settings.Revision, &data); err != nil {
			return result, err
		}
		if err = json.Unmarshal(data, &settings.Rules); err != nil {
			return result, err
		}
		if settings.Revision != *revision || !settings.Rules.Enabled || settings.Validate() != nil {
			return result, ErrDenied
		}
		rules = make([]WeaponUpgradeRule, len(settings.Rules.Levels))
		for i, row := range settings.Rules.Levels {
			rules[i] = WeaponUpgradeRule{Score: row.ScoreThreshold, Gold: row.Gold, Odds: row.DisplayOdds}
		}
	}
	if err = tx.QueryRow(`SELECT record FROM inventory WHERE uid=? AND instance=? FOR UPDATE`, uid, instance).Scan(&result.Item); err != nil {
		return result, err
	}
	if len(result.Item) != 68 || protocol.ReadUint32(result.Item, 0) != instance {
		return result, ErrDenied
	}
	if replay {
		return result, tx.Commit()
	}
	if !usableItem(result.Item) || result.Item[4] != protocol.ItemWeapon {
		return result, ErrDenied
	}
	var expired bool
	if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM inventory_expirations WHERE uid=? AND instance=? AND expires_at<=?)`, uid, instance, time.Now().Unix()).Scan(&expired); err != nil {
		return result, err
	}
	if expired {
		return result, ErrDenied
	}
	level, score := protocol.ReadUint32(result.Item, 43), protocol.ReadUint32(result.Item, 47)
	if uint64(level)+1 >= uint64(len(rules)) {
		return result, ErrDenied
	}
	rule := rules[level]
	if score < rule.Score || result.Gold < rule.Gold {
		return result, ErrDenied
	}
	roll, err := rand.Int(rand.Reader, big.NewInt(100))
	if err != nil {
		return result, err
	}
	result.Success = uint32(roll.Uint64()) < rule.Odds
	before := append([]byte(nil), result.Item...)
	protocol.WriteUint32(result.Item, 47, score-rule.Score)
	if result.Success {
		protocol.WriteUint32(result.Item, 43, level+1)
	}
	result.Gold -= rule.Gold
	if _, err = tx.Exec(`UPDATE accounts SET gold=? WHERE uid=?`, result.Gold, uid); err != nil {
		return result, err
	}
	if _, err = tx.Exec(`UPDATE inventory SET record=? WHERE uid=? AND instance=?`, result.Item, uid, instance); err != nil {
		return result, err
	}
	if _, err = tx.Exec(`INSERT INTO weapon_upgrades(uid,operation_id,instance,success,cost,score_cost,odds,roll,before_record,after_record) VALUES(?,?,?,?,?,?,?,?,?,?)`, uid, operation, instance, result.Success, rule.Gold, rule.Score, rule.Odds, roll.Uint64(), before, result.Item); err != nil {
		return result, err
	}
	return result, tx.Commit()
}
