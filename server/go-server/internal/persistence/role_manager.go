package persistence

import (
	"context"
	"kungfu.local/server/internal/protocol"
	"math"
	"time"
)

const MaxRoleLevel uint16 = 200

// LevelChange lets the enclosing business award crossed levels without recursively
// invoking RewardManager inside AddExp. The caller owns the locked profile/transaction.
type LevelChange struct{ Before, After uint16 }

func (m RoleManager) AddExp(profile []byte, amount uint32, growth RewardRules) (LevelChange, error) {
	if len(profile) != protocol.RoleProfileSize {
		return LevelChange{}, ErrDenied
	}
	before := ProfileLevel(profile)
	level := before
	xp := uint64(protocol.ReadUint32(profile, ExperienceOffset)) + uint64(amount)
	if growth.GrowthEnabled {
		var rest uint32
		level, rest = AdvanceLevel(level, xp, growth)
		xp = uint64(rest)
	}
	if xp > math.MaxInt32 {
		return LevelChange{}, ErrDenied
	}
	total := uint64(protocol.ReadUint32(profile, ExperienceOffset+4)) + uint64(amount)
	if total > math.MaxInt32 {
		total = math.MaxInt32
	}
	if growth.GrowthEnabled {
		protocol.WriteUint16(profile, LevelOffset, level)
	}
	protocol.WriteUint32(profile, ExperienceOffset, uint32(xp))
	protocol.WriteUint32(profile, ExperienceOffset+4, uint32(total))
	return LevelChange{before, level}, nil
}

// SetLevel is an explicit progression correction, not a level-up reward trigger.
// Caller supplies remaining experience; cumulative experience is preserved.
func (m RoleManager) SetLevel(profile []byte, level uint16, remainingExp uint32) error {
	if len(profile) != protocol.RoleProfileSize || level < 1 || level > MaxRoleLevel || remainingExp > math.MaxInt32 {
		return ErrDenied
	}
	protocol.WriteUint16(profile, LevelOffset, level)
	protocol.WriteUint32(profile, ExperienceOffset, remainingExp)
	return nil
}

func (m *RoleManager) Snapshot(uid uint64) (Account, error) {
	account := Account{UID: uid}
	if err := m.store.InventoryManager().ExpireInventory(uid); err != nil {
		return account, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := m.store.DB.QueryRowContext(ctx, `SELECT account,nickname,profile,gold,tickets FROM accounts WHERE uid=?`, uid).Scan(&account.Account, &account.Nickname, &account.Profile, &account.Gold, &account.Tickets)
	if err != nil {
		return account, err
	}
	rows, err := m.store.DB.QueryContext(ctx, `SELECT record FROM inventory WHERE uid=? ORDER BY instance`, uid)
	if err != nil {
		return account, err
	}
	defer rows.Close()
	for rows.Next() {
		var record []byte
		if err = rows.Scan(&record); err != nil {
			return account, err
		}
		if len(record) != 68 {
			return account, ErrDenied
		}
		if protocol.ReadUint32(record, 19) != 0xffffffff {
			account.Inventory = append(account.Inventory, record)
		}
	}
	if len(account.Profile) != 360 {
		return account, ErrDenied
	}
	if err = rows.Err(); err != nil {
		return account, err
	}
	if err = rows.Close(); err != nil {
		return account, err
	}
	if err = m.store.normalizeEquippedInventory(&account); err != nil {
		return account, err
	}
	return account, m.store.projectVIPInventory(&account)
}
