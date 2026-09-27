package persistence

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

const MaxItemsPerLevelGift = 8
const MaxTotalLevelGiftItems = 150

// LevelGift applies on reaching Level, once per character. Empty configuration
// never invents rewards or retroactively grants to existing high-level players.
type LevelGift struct {
	Gold    uint32   `json:"gold"`
	Tickets uint32   `json:"tickets"`
	Level   uint16   `json:"level"`
	Items   []uint32 `json:"items"`
}

func validateLevelGifts(gifts []LevelGift) error {
	seen := map[uint16]bool{}
	total := 0
	for _, g := range gifts {
		if g.Level < 2 || g.Level > MaxRoleLevel || seen[g.Level] || (len(g.Items) == 0 && g.Gold == 0 && g.Tickets == 0) || len(g.Items) > MaxItemsPerLevelGift {
			return fmt.Errorf("升级礼包需不重复的2–200级，每级至少设置一项奖励，最多8件物品")
		}
		if err := (RewardBundle{Items: g.Items, Gold: g.Gold, Tickets: g.Tickets}).Validate(); err != nil {
			return err
		}
		seen[g.Level] = true
		total += len(g.Items)
		for _, key := range g.Items {
			if key == 0 {
				return ErrDenied
			}
		}
	}
	if total > MaxTotalLevelGiftItems {
		return fmt.Errorf("升级礼包合计最多150件物品")
	}
	return nil
}

// GrantProgressItems runs inside an account-locked business transaction. The
// caller must roll back on error and persist/notify only on success.
func (m RewardManager) GrantProgressItems(tx *sql.Tx, uid uint64, profile []byte, balance uint64, xp, gold uint32, rules RewardRules) (uint32, [][]byte, error) {
	if err := validateLevelGifts(rules.LevelGifts); err != nil {
		return 0, nil, err
	}
	before := ProfileLevel(profile)
	nextProfile := bytes.Clone(profile)
	next, err := m.GrantProgress(nextProfile, balance, xp, gold, rules)
	if err != nil {
		return 0, nil, err
	}
	items := [][]byte{}
	after := ProfileLevel(nextProfile)
	if after <= before {
		copy(profile, nextProfile)
		return next, items, nil
	}
	// Read crossed levels together: long multi-level jumps must not perform
	// a database round trip per empty level on a remote debug database.
	rows, err := tx.Query("SELECT level FROM level_reward_receipts WHERE uid=? AND level>? AND level<=?", uid, before, after)
	if err != nil {
		return 0, nil, err
	}
	claimed := map[uint16]bool{}
	for rows.Next() {
		var level uint16
		if err = rows.Scan(&level); err != nil {
			rows.Close()
			return 0, nil, err
		}
		claimed[level] = true
	}
	err = rows.Err()
	closeErr := rows.Close()
	if err != nil {
		return 0, nil, err
	}
	if closeErr != nil {
		return 0, nil, closeErr
	}
	values := []string{}
	args := []any{}
	for level := before + 1; level <= after; level++ {
		if claimed[level] {
			continue
		}
		awarded := [][]byte{}
		giftGold, giftTickets := uint32(0), uint32(0)
		for _, g := range rules.LevelGifts {
			if g.Level != level {
				continue
			}
			var part [][]byte
			next, part, err = m.GrantBundle(tx, uid, uint64(next), RewardBundle{Items: g.Items, Gold: g.Gold, Tickets: g.Tickets})
			if err != nil {
				return 0, nil, err
			}
			awarded = append(awarded, part...)
			giftGold = g.Gold
			giftTickets = g.Tickets
		}
		data, e := json.Marshal(map[string]any{"items": awarded, "gold": giftGold, "tickets": giftTickets})
		if e != nil {
			return 0, nil, e
		}
		values = append(values, "(?,?,?)")
		args = append(args, uid, level, data)
		items = append(items, awarded...)
	}
	if len(values) > 0 {
		if _, err = tx.Exec("INSERT INTO level_reward_receipts(uid,level,items) VALUES "+strings.Join(values, ","), args...); err != nil {
			return 0, nil, err
		}
	}
	copy(profile, nextProfile)
	return next, items, nil
}
