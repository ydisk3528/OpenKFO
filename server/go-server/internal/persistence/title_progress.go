package persistence

import (
	"database/sql"
	"encoding/json"
	"slices"

	"kungfu.local/server/internal/protocol"
)

// Conditions use cumulative server-owned statistics, not client claims.
func titleCountersMet(profile []byte, rule TitleRule) bool {
	if len(profile) != 360 {
		return false
	}
	level := protocol.ReadUint16(profile, LevelOffset)
	if level > MaxRoleLevel {
		return false
	}
	// NewAccount's legacy zero field represents level 1 throughout growth.
	level = ProfileLevel(profile)
	if level < rule.MinPlayerLevel {
		return false
	}
	var matches, wins uint64
	for mode := 0; mode < 4; mode++ {
		m, w := protocol.ReadUint32(profile, 133+mode*8), protocol.ReadUint32(profile, 137+mode*8)
		if m > 0x7fffffff || w > m {
			return false
		}
		matches += uint64(m)
		wins += uint64(w)
	}
	return matches >= uint64(rule.Matches) && wins >= uint64(rule.Wins)
}

// Only the lowest enabled title above the current title is eligible. Existing
// pending offers freeze their choices; edits apply only to future grants.
func (s *TitleManager) AdvanceTitle(uid uint64, supported []byte, clientHash string) (bool, error) {
	if uid == 0 {
		return false, ErrDenied
	}
	if len(supported) == 0 {
		return false, nil
	}
	tx, txCancel, err := beginTransaction(s.store.DB)
	defer txCancel()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var profile, data []byte
	if err = tx.QueryRow("SELECT profile FROM accounts WHERE uid=? FOR UPDATE", uid).Scan(&profile); err != nil {
		return false, err
	}
	if len(profile) != 360 {
		return false, ErrDenied
	}
	var settings TitleSettings
	err = tx.QueryRow("SELECT revision,rules FROM title_rules WHERE id=1 FOR UPDATE").Scan(&settings.Revision, &data)
	if err == sql.ErrNoRows {
		return false, tx.Commit()
	}
	if err != nil {
		return false, err
	}
	if err = json.Unmarshal(data, &settings.Rules); err != nil {
		return false, err
	}
	if err = settings.Validate(); err != nil {
		return false, err
	}
	if settings.Rules.ClientHash != "" {
		supported = settings.Rules.SupportedLevels(clientHash)
	}
	if !settings.Rules.Enabled {
		return false, tx.Commit()
	}
	var pending bool
	if err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM title_rewards WHERE uid=? AND claimed_key IS NULL)", uid).Scan(&pending); err != nil {
		return false, err
	}
	if pending {
		return false, tx.Commit()
	}
	advanced := false
	for {
		var next *TitleRule
		for i := range settings.Rules.Titles {
			r := &settings.Rules.Titles[i]
			if r.Enabled && r.Level > profile[TitleLevelOffset] && (next == nil || r.Level < next.Level) {
				next = r
			}
		}
		if next == nil || !slices.Contains(supported, next.Level) || !titleCountersMet(profile, *next) {
			return advanced, tx.Commit()
		}
		if next.CompletedTask != 0 {
			var completed bool
			if err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM task_progress WHERE uid=? AND task_key=? AND state=3)", uid, next.CompletedTask).Scan(&completed); err != nil {
				return false, err
			}
			if !completed {
				return advanced, tx.Commit()
			}
		}
		if len(next.Choices) == 0 {
			profile[TitleLevelOffset] = next.Level
			if _, err = tx.Exec("UPDATE accounts SET profile=? WHERE uid=?", profile, uid); err != nil {
				return false, err
			}
			advanced = true
			continue
		}
		// Do not consume title advancement for an offer the native selector cannot
		// display. Unlisted weapons use the same display-only catalogue as tutorial.
		if _, err = weaponRewardCatalog(tx, next.Choices); err != nil {
			return false, err
		}
		if err = grantTitleChoices(tx, uid, next.Level, next.Choices); err != nil {
			return false, err
		}
		return true, tx.Commit()
	}
}
