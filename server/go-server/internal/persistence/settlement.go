package persistence

import (
	"database/sql"
	"encoding/json"
	"kungfu.local/server/internal/protocol"
	"math"
	"sort"
)

// 4300 -> 9CD5B0 stores DWORDs at profile+F5/+F9. 9CD2D0 reads +F5
// for the experience delta. +F9 is txtTotalScore in native 7E138C.
const ExperienceOffset = 0xF5
const LevelOffset = 120

func ProfileLevel(profile []byte) uint16 {
	level := protocol.ReadUint16(profile, LevelOffset)
	if level < 1 {
		return 1
	}
	if level > MaxRoleLevel {
		return MaxRoleLevel
	}
	return level
}
func AdvanceLevel(level uint16, experience uint64, rules RewardRules) (uint16, uint32) {
	for level < MaxRoleLevel {
		cost := rules.AtLevel(level).NextExperience
		if cost == 0 || experience < uint64(cost) {
			break
		}
		experience -= uint64(cost)
		level++
	}
	if experience > math.MaxInt32 {
		experience = math.MaxInt32
	}
	return level, uint32(experience)
}

type BattleReward struct {
	TicketBalance  uint32   `json:"ticket_balance,omitempty"`
	StageMapID     uint32   `json:"stage_map_id,omitempty"`
	TaskClientHash string   `json:"-"` // Server archive identity, never client supplied.
	BattleMode     *byte    `json:"battle_mode,omitempty"`
	HonourPeriod   uint32   `json:"honour_period,omitempty"`
	HonourPoints   uint32   `json:"honour_points,omitempty"`
	StartLevel     uint16   `json:"start_level,omitempty"`
	Items          [][]byte `json:"items,omitempty"`
	UID            uint64   `json:"uid"`
	Outcome        string   `json:"outcome"`
	Gold           uint32   `json:"gold"`
	GoldBalance    uint32   `json:"gold_balance"`
	Experience     uint32   `json:"experience"`
	Profile        []byte   `json:"profile"`
}

// Commit the entire room once. The persisted response makes retries independent
// of subsequent reward configuration changes or process restarts.
func (m *BattleManager) SettleBattle(serial uint32, reports []byte, rewards []BattleReward, growth ...RewardRules) ([]BattleReward, error) {
	for _, r := range rewards {
		if r.Outcome == StageOutcomeClear || r.Outcome == StageOutcomeFailed {
			return nil, ErrDenied
		}
		if r.BattleMode != nil && (protocol.RoomType(*r.BattleMode) == protocol.StageAssault || protocol.RoomType(*r.BattleMode) == protocol.FosterMode) {
			return nil, ErrDenied
		}
	}
	return m.settleBattle(serial, reports, rewards, false, growth...)
}

const (
	StageOutcomeClear  = "stage_clear"
	StageOutcomeFailed = "stage_failed"
)

// SettleStage shares the room transaction and progression/item managers, but
// never awards competitive task counters, random PvP drops, or honour. The
// caller must validate native reports and the server's wave state beforehand.
func (m *BattleManager) SettleStage(serial uint32, mapID uint32, reports []byte, rewards []BattleReward, growth RewardRules) ([]BattleReward, error) {
	rewards = append([]BattleReward(nil), rewards...)
	for i := range rewards {
		r := &rewards[i]
		if r.BattleMode != nil && protocol.RoomType(*r.BattleMode) != protocol.StageAssault {
			return nil, ErrDenied
		}
		if (r.Outcome != StageOutcomeClear && r.Outcome != StageOutcomeFailed) || r.HonourPeriod != 0 || r.HonourPoints != 0 {
			return nil, ErrDenied
		}
		if i > 0 && r.Outcome != rewards[0].Outcome {
			return nil, ErrDenied
		}
		mode := byte(protocol.StageAssault)
		r.BattleMode = &mode
		r.TaskClientHash = ""
		r.StageMapID = mapID
	}
	return m.settleBattle(serial, reports, rewards, true, growth)
}

func (m *BattleManager) settleBattle(serial uint32, reports []byte, rewards []BattleReward, stage bool, growth ...RewardRules) ([]BattleReward, error) {
	if serial == 0 || len(rewards) == 0 || len(rewards) > 8 {
		return nil, ErrDenied
	}
	if len(growth) > 0 {
		if err := growth[0].Validate(); err != nil {
			return nil, err
		}
	}
	sort.Slice(rewards, func(i, j int) bool { return rewards[i].UID < rewards[j].UID })
	tx, txCancel, err := beginTransaction(m.store.DB)
	defer txCancel()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Lock the persistent battle counter to serialize settlement creation even
	// if duplicate requests come from separate service processes.
	var counter uint64
	if err = tx.QueryRow("SELECT value FROM counters WHERE name='battle' FOR UPDATE").Scan(&counter); err != nil {
		return nil, err
	}
	if uint64(serial) > counter {
		return nil, ErrDenied
	}
	var previous []byte
	err = tx.QueryRow("SELECT result FROM battle_settlements WHERE serial=?", serial).Scan(&previous)
	if err == nil {
		var saved []BattleReward
		if err = json.Unmarshal(previous, &saved); err != nil {
			return nil, err
		}
		if stage && len(saved) != len(rewards) {
			return nil, ErrDenied
		}
		for i, r := range saved {
			isStage := r.BattleMode != nil && protocol.RoomType(*r.BattleMode) == protocol.StageAssault
			if isStage != stage {
				return nil, ErrDenied
			}
			if stage && r.UID != rewards[i].UID {
				return nil, ErrDenied
			}
			if stage && r.StageMapID != rewards[i].StageMapID {
				return nil, ErrDenied
			}
		}
		return saved, tx.Commit()
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	// Lock every participant before the shared task rules. Otherwise a task
	// action on a later participant can hold its account while waiting for
	// rules already held by this settlement, creating a lock-order cycle.
	for i, r := range rewards {
		if r.UID == 0 || (i > 0 && rewards[i-1].UID == r.UID) {
			return nil, ErrDenied
		}
		var owner uint64
		if err = tx.QueryRow("SELECT uid FROM accounts WHERE uid=? FOR UPDATE", r.UID).Scan(&owner); err != nil {
			return nil, err
		}
	}
	for i := range rewards {
		r := &rewards[i]
		if stage {
			award, ok := growth[0].StageReward(r.StageMapID, r.Outcome)
			if !ok {
				return nil, ErrDenied
			}
			r.Gold, r.Experience = award.Gold, award.Experience
		}
		var gold uint64
		if err = tx.QueryRow("SELECT profile,gold FROM accounts WHERE uid=? FOR UPDATE", r.UID).Scan(&r.Profile, &gold); err != nil {
			return nil, err
		}
		if len(r.Profile) != 360 || gold+uint64(r.Gold) > math.MaxUint32 {
			return nil, ErrDenied
		}
		if !stage {
			addTaskBattleCounters(r.Profile, r.BattleMode, r.Outcome, len(rewards))
			if err = advanceExtendedTaskBattleTx(tx, *r, len(rewards)); err != nil {
				return nil, err
			}
		}
		startLevel := ProfileLevel(r.Profile)
		if r.StartLevel != 0 {
			startLevel = r.StartLevel
		}
		if startLevel > MaxRoleLevel {
			return nil, ErrDenied
		}
		r.Items = nil
		if stage {
			award, ok := growth[0].StageReward(r.StageMapID, r.Outcome)
			if !ok {
				return nil, ErrDenied
			}
			bundle := award.RewardBundle
			bundle.Gold = 0 // Gold and XP go through the shared progression below.
			var stageItems [][]byte
			_, stageItems, err = (RewardManager{}).GrantBundle(tx, r.UID, gold, bundle)
			if err != nil {
				return nil, err
			}
			r.Items = append(r.Items, stageItems...)
		}
		if !stage && len(growth) > 0 {
			if err = awardDrops(tx, r, startLevel, growth[0].Drops); err != nil {
				return nil, err
			}
		}
		if r.Gold > math.MaxInt32 {
			return nil, ErrDenied
		}
		rules := RewardRules{}
		if len(growth) > 0 {
			rules = growth[0]
		}
		if r.Outcome == "unconfirmed" {
			rules.GrowthEnabled = false
		}
		var gifts [][]byte
		r.GoldBalance, gifts, err = (RewardManager{}).GrantProgressItems(tx, r.UID, r.Profile, gold, r.Experience, r.Gold, rules)
		r.Items = append(r.Items, gifts...)
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec("UPDATE accounts SET gold=?,profile=? WHERE uid=?", r.GoldBalance, r.Profile, r.UID); err != nil {
			return nil, err
		}
		if stage {
			if err = tx.QueryRow("SELECT tickets FROM accounts WHERE uid=?", r.UID).Scan(&r.TicketBalance); err != nil {
				return nil, err
			}
		}
		if err = addHonour(tx, *r); err != nil {
			return nil, err
		}
	}
	data, err := json.Marshal(rewards)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec("INSERT INTO battle_settlements(serial,reports,result) VALUES(?,?,?)", serial, reports, data); err != nil {
		return nil, err
	}
	return rewards, tx.Commit()
}

func (m *BattleManager) NextBattle() (uint32, error) {
	transaction, transactionCancel, err := beginTransaction(m.store.DB)
	defer transactionCancel()
	if err != nil {
		return 0, err
	}
	defer transaction.Rollback()
	var serial uint64
	if err = transaction.QueryRow(`SELECT value FROM counters WHERE name='battle' FOR UPDATE`).Scan(&serial); err != nil {
		return 0, err
	}
	if serial >= 0xffffffff {
		return 0, ErrDenied
	}
	serial++
	if _, err = transaction.Exec(`UPDATE counters SET value=? WHERE name='battle'`, serial); err != nil {
		return 0, err
	}
	return uint32(serial), transaction.Commit()
}
