package persistence

import (
	"bytes"
	"context"
	"database/sql"
	"kungfu.local/server/internal/protocol"
	"time"
)

func activeVIPCard(r []byte) bool {
	return len(r) == 68 && r[4] == protocol.ItemVIPCard && protocol.ReadUint32(r, 19) == 1 && protocol.ReadUint32(r, 5) >= 730001 && protocol.ReadUint32(r, 5) <= 730003
}

// Use only server-projected inventory (Snapshot expires cards before returning).
func (a Account) VIPKind() uint32 {
	kind := uint32(1)
	for _, r := range a.Inventory {
		if activeVIPCard(r) && protocol.ReadUint32(r, 13) > 0 {
			candidate := protocol.ReadUint32(r, 5) - 730001 + 2
			if candidate > kind {
				kind = candidate
			}
		}
	}
	return kind
}

// Only the outgoing snapshot changes. Persistent card records and UTC deadlines
// remain authoritative; reconnects cannot reset a membership's duration.
func projectVIPMinutes(records [][]byte, deadlines map[uint32]int64, now int64) {
	for i, r := range records {
		if !activeVIPCard(r) {
			continue
		}
		r = bytes.Clone(r)
		minutes := uint32(525600)
		if end, ok := deadlines[protocol.ReadUint32(r, 0)]; ok {
			if end <= now {
				minutes = 0
				protocol.WriteUint32(r, 19, 2)
				protocol.WriteUint16(r, 17, 0)
			} else {
				remaining := uint64(end) - uint64(now)
				value := (remaining + 59) / 60
				if value > 0x7fffffff {
					value = 0x7fffffff
				}
				minutes = uint32(value)
			}
		}
		protocol.WriteUint32(r, 13, minutes)
		records[i] = r
	}
}

func (s *Store) projectVIPInventory(a *Account) error {
	hasVIP := false
	for _, r := range a.Inventory {
		if activeVIPCard(r) || (len(r) == protocol.InventoryRecordSize && len(Slots[r[protocol.InventoryKindOffset]]) != 0 && protocol.ReadUint32(r, inventoryStateOffset) == inventoryActive) {
			hasVIP = true
			break
		}
	}
	if !hasVIP {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err := s.DB.QueryContext(ctx, `SELECT instance,expires_at FROM inventory_expirations WHERE uid=?`, a.UID)
	if err != nil {
		return err
	}
	defer rows.Close()
	deadlines := map[uint32]int64{}
	for rows.Next() {
		var id uint32
		var end int64
		if err = rows.Scan(&id, &end); err != nil {
			return err
		}
		deadlines[id] = end
	}
	if err = rows.Err(); err != nil {
		return err
	}
	projectVIPMinutes(a.Inventory, deadlines, time.Now().Unix())
	for _, r := range a.Inventory {
		if len(r) == protocol.InventoryRecordSize && len(Slots[r[protocol.InventoryKindOffset]]) != 0 && protocol.ReadUint32(r, inventoryStateOffset) == inventoryActive {
			end, finite := deadlines[protocol.ReadUint32(r, 0)]
			projectItemMinutes(r, sql.NullInt64{Int64: end, Valid: finite}, time.Now().Unix())
		}
	}
	return nil
}

type VIPMembership struct {
	Kind      uint32 `json:"kind"`
	ExpiresAt *int64 `json:"expires_at"`
}

// Highest active tier wins. Within that tier permanent ownership wins, otherwise
// use the latest deadline. This is deterministic server policy, not the native
// inventory-order-dependent display algorithm. Never infer a UTC deadline from
// the client's remaining-minute field.
func (v *VIPMembership) include(record []byte, deadline sql.NullInt64, now int64) error {
	if len(record) != 68 {
		return ErrDenied
	}
	if record[4] != protocol.ItemVIPCard || protocol.ReadUint32(record, 19) != 1 || (deadline.Valid && deadline.Int64 <= now) {
		return nil
	}
	id := protocol.ReadUint32(record, 5)
	if id < 730001 || id > 730003 {
		return nil
	}
	kind := id - 730001 + 2
	if kind < v.Kind {
		return nil
	}
	if kind == v.Kind && (v.ExpiresAt == nil || (deadline.Valid && deadline.Int64 <= *v.ExpiresAt)) {
		return nil
	}
	v.Kind = kind
	v.ExpiresAt = nil
	if deadline.Valid {
		end := deadline.Int64
		v.ExpiresAt = &end
	}
	return nil
}

func (s *Store) VIPMembership(uid uint64) (VIPMembership, error) {
	v := VIPMembership{Kind: 1}
	rows, err := s.DB.Query(`SELECT i.record,e.expires_at FROM inventory i LEFT JOIN inventory_expirations e ON e.uid=i.uid AND e.instance=i.instance WHERE i.uid=? ORDER BY i.instance`, uid)
	if err != nil {
		return v, err
	}
	defer rows.Close()
	now := time.Now().Unix()
	for rows.Next() {
		var record []byte
		var deadline sql.NullInt64
		if err = rows.Scan(&record, &deadline); err != nil {
			return v, err
		}
		if err = v.include(record, deadline, now); err != nil {
			return v, err
		}
	}
	return v, rows.Err()
}

// Called only inside the existing audited, idempotent GM transaction. Each new
// operation creates one card; retrying that operation returns its prior receipt.
func grantVIP(tx *sql.Tx, uid uint64, kind uint32, deadline, now int64) (uint32, error) {
	if uid == 0 || kind < 2 || kind > 4 || (deadline != 0 && (deadline <= now || deadline > now+10*366*24*3600)) {
		return 0, ErrDenied
	}
	var owner uint64
	if err := tx.QueryRow(`SELECT uid FROM accounts WHERE uid=? FOR UPDATE`, uid).Scan(&owner); err != nil {
		return 0, err
	}
	var next uint64
	if err := tx.QueryRow(`SELECT COALESCE(MAX(instance),1048575)+1 FROM inventory WHERE uid=?`, uid).Scan(&next); err != nil {
		return 0, err
	}
	if next > 0xffffffff {
		return 0, ErrDenied
	}
	r := make([]byte, 68)
	protocol.WriteUint32(r, 0, uint32(next))
	r[4] = 73
	protocol.WriteUint32(r, 5, 730001+kind-2)
	minutes := uint32(525600) // Native 365+ display; permanence is no deadline row.
	if deadline != 0 {
		minutes = uint32((deadline - now + 59) / 60)
	}
	protocol.WriteUint32(r, 13, minutes)
	protocol.WriteUint32(r, 19, 1)
	if _, err := tx.Exec(`INSERT INTO inventory(uid,instance,record) VALUES(?,?,?)`, uid, next, r); err != nil {
		return 0, err
	}
	if deadline != 0 {
		if _, err := tx.Exec(`INSERT INTO inventory_expirations(uid,instance,expires_at) VALUES(?,?,?)`, uid, next, deadline); err != nil {
			return 0, err
		}
	}
	return uint32(next), nil
}
