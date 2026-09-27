package persistence

import (
	"database/sql"
	"encoding/json"
	"kungfu.local/server/internal/protocol"
)

type TutorialReward struct {
	Profile       []byte
	Items         [][]byte
	Gold, Tickets uint32
	Replay        bool
	Choices       []uint32
	Catalog       []byte `json:"-"` // Display snapshot for this completion, not part of the receipt.
}

func (m *RewardManager) CompleteTutorial(uid uint64, clientHash string) (r TutorialReward, err error) {
	if uid == 0 {
		return r, ErrDenied
	}
	tx, txCancel, err := beginTransaction(m.store.DB)
	defer txCancel()
	if err != nil {
		return r, err
	}
	defer tx.Rollback()
	if err = tx.QueryRow("SELECT profile,gold,tickets FROM accounts WHERE uid=? FOR UPDATE", uid).Scan(&r.Profile, &r.Gold, &r.Tickets); err != nil {
		return r, err
	}
	if len(r.Profile) != protocol.RoleProfileSize {
		return r, ErrDenied
	}
	var exists bool
	if err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM tutorial_rewards WHERE uid=?)", uid).Scan(&exists); err != nil {
		return r, err
	}
	if exists || r.Profile[TitleLevelOffset] >= 2 {
		r.Replay = true
		if exists {
			r.Choices, r.Catalog, err = tutorialChoices(tx, uid)
			if err != nil {
				return r, err
			}
		}
		return r, tx.Commit()
	}
	var raw []byte
	var rules RewardRules
	err = tx.QueryRow("SELECT rules FROM battle_reward_rules WHERE id=1").Scan(&raw)
	if err != nil && err != sql.ErrNoRows {
		return r, err
	}
	if err == nil {
		if err = json.Unmarshal(raw, &rules); err != nil {
			return r, err
		}
	}
	if rules.Tutorial != nil {
		bundle := *rules.Tutorial
		bundle.Items = nil
		seen := map[uint32]bool{}
		for _, key := range rules.Tutorial.Items {
			d, e := readDefinition(tx, key)
			if e != nil {
				return r, e
			}
			if d.Record[4] == protocol.ItemWeapon {
				if seen[key] || len(r.Choices) == 7 {
					return r, ErrDenied
				}
				seen[key] = true
				r.Choices = append(r.Choices, key)
			} else {
				bundle.Items = append(bundle.Items, key)
			}
		}
		// Create the pending choice in the same transaction as completion.
		// No weapon enters inventory until authenticated 4126 chooses it.
		if len(r.Choices) > 0 {
			if r.Catalog, err = weaponRewardCatalog(tx, r.Choices); err != nil {
				return r, err
			}
			if err = grantTitleChoices(tx, uid, 2, r.Choices); err != nil {
				return r, err
			}
		}
		r.Gold, r.Items, err = m.GrantBundle(tx, uid, uint64(r.Gold), bundle)
		if err != nil {
			return r, err
		}
		r.Tickets += rules.Tutorial.Tickets
	}
	// Advance only configured, accepted tasks, in this same completion transaction.
	// The receipt above prevents replay; native condition numbers are not event IDs.
	if clientHash != "" {
		if err = advanceExtendedTasksTx(tx, uid, clientHash, func(state *ExtendedTaskState) bool {
			return advanceExtendedTaskConditions(state, func(event string) bool { return event == "tutorial_complete" })
		}); err != nil {
			return r, err
		}
	}
	r.Profile[TitleLevelOffset] = 2
	if _, err = tx.Exec("UPDATE accounts SET profile=?,gold=? WHERE uid=?", r.Profile, r.Gold, uid); err != nil {
		return r, err
	}
	raw, err = json.Marshal(r)
	if err != nil {
		return r, err
	}
	if _, err = tx.Exec("INSERT INTO tutorial_rewards(uid,reward) VALUES(?,?)", uid, raw); err != nil {
		return r, err
	}
	return r, tx.Commit()
}
