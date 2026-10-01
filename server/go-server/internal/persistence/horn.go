package persistence

import (
	"bytes"
	"errors"
	"kungfu.local/server/internal/protocol"
	"time"
)

var ErrHornCards = errors.New("小喇叭卡数量不足")

func HornCost(kind uint32) uint16 {
	switch kind {
	case 2480:
		return 1
	case 2486:
		return 3
	case 2481:
		return 10
	}
	return 0
}

// Select only the actual card, and plan all stack changes before touching any.
func spendHornCards(items [][]byte, cost uint16) ([][]byte, error) {
	if cost != 1 && cost != 3 && cost != 10 {
		return nil, ErrDenied
	}
	left := uint32(cost)
	var changed [][]byte
	for _, p := range items {
		if !usableItem(p) || p[4] != 71 || protocol.ReadUint32(p, 5) != 713001 {
			continue
		}
		n := uint32(protocol.ReadUint16(p, 23))
		if n == 0 {
			continue
		}
		take := min(n, left)
		q := bytes.Clone(p)
		protocol.WriteUint16(q, 23, uint16(n-take))
		if n == take {
			protocol.WriteUint32(q, 19, 0xffffffff)
		} // Retain instance ID but remove depleted card from native inventory.
		changed = append(changed, q)
		left -= take
		if left == 0 {
			return changed, nil
		}
	}
	return nil, ErrHornCards
}

// Account lock is shared with shop/GM inventory writes. Audit and debit commit
// together; an interrupted dispatch remains visibly pending, never auto-replayed.
func (m *InventoryManager) ConsumeHorn(uid uint64, kind uint32, eventID, text string) ([][]byte, error) {
	cost := HornCost(kind)
	if cost == 0 || uid == 0 || len(eventID) != 32 {
		return nil, ErrDenied
	}
	tx, cancel, err := beginTransaction(m.store.DB)
	defer cancel()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var settings HornSettings
	if err = tx.QueryRow(`SELECT channel_enabled,realm_enabled,mood_enabled FROM horn_settings WHERE id=1 FOR SHARE`).Scan(&settings.ChannelEnabled, &settings.RealmEnabled, &settings.MoodEnabled); err != nil {
		return nil, err
	}
	if !settings.Allows(kind) {
		return nil, ErrHornDisabled
	}
	var owner uint64
	if err = tx.QueryRow(`SELECT uid FROM accounts WHERE uid=? FOR UPDATE`, uid).Scan(&owner); err != nil {
		return nil, err
	}
	if err = expireInventory(tx, uid, time.Now().Unix()); err != nil {
		return nil, err
	}
	rows, err := tx.Query(`SELECT record FROM inventory WHERE uid=? ORDER BY instance`, uid)
	if err != nil {
		return nil, err
	}
	var items [][]byte
	for rows.Next() {
		var p []byte
		if err = rows.Scan(&p); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	changed, err := spendHornCards(items, cost)
	if err != nil {
		return nil, err
	}
	for _, p := range changed {
		if _, err = tx.Exec(`UPDATE inventory SET record=? WHERE uid=? AND instance=?`, p, uid, protocol.ReadUint32(p, 0)); err != nil {
			return nil, err
		}
	}
	if _, err = tx.Exec(`INSERT INTO horn_events(id,uid,kind,cost,message) VALUES(?,?,?,?,?)`, eventID, uid, kind, cost, text); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return changed, nil
}
