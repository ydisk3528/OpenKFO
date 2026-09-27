package persistence

import (
	"database/sql"
	"encoding/json"
	"kungfu.local/server/internal/protocol"
	"time"
)

// TalismanRepairRule is an explicit server policy, not a recovered official price.
// Capacity uses the inventory uint16 hundredths field, not seconds or gold.
type TalismanRepairRule struct {
	Item     uint32 `json:"item"`
	Material uint32 `json:"material"`
	Quantity uint16 `json:"quantity"`
	Capacity uint16 `json:"capacity"`
}

func (r TalismanRepairRule) Valid() bool {
	return r.Item != 0 && r.Material != 0 && r.Quantity != 0 && r.Capacity != 0
}

func (s *ItemManager) RepairTalisman(uid uint64, operation string, instance uint32, rule TalismanRepairRule) ([]byte, error) {
	return s.store.ItemManager().repairTalisman(uid, operation, instance, rule, nil)
}
func (s *ItemManager) RepairTalismanConfigured(uid uint64, operation string, instance uint32, rule TalismanRepairRule, revision uint64) ([]byte, error) {
	if revision == 0 {
		return nil, ErrDenied
	}
	return s.store.ItemManager().repairTalisman(uid, operation, instance, rule, &revision)
}
func (s *ItemManager) repairTalisman(uid uint64, operation string, instance uint32, rule TalismanRepairRule, revision *uint64) ([]byte, error) {
	if uid == 0 || instance == 0 || operation == "" || len(operation) > 128 || !rule.Valid() {
		return nil, ErrDenied
	}
	tx, txCancel, err := beginTransaction(s.store.DB)
	defer txCancel()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var owner uint64
	if err = tx.QueryRow(`SELECT uid FROM accounts WHERE uid=? FOR UPDATE`, uid).Scan(&owner); err != nil {
		return nil, err
	}
	var oldInstance, oldMaterial, oldQuantity, oldCapacity uint32
	err = tx.QueryRow(`SELECT instance,material,quantity,capacity FROM talisman_repairs WHERE uid=? AND operation_id=?`, uid, operation).Scan(&oldInstance, &oldMaterial, &oldQuantity, &oldCapacity)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	replay := err == nil
	if replay && (oldInstance != instance || oldMaterial != rule.Material || oldQuantity != uint32(rule.Quantity) || oldCapacity != uint32(rule.Capacity)) {
		return nil, ErrDenied
	}
	var item []byte
	if err = tx.QueryRow(`SELECT record FROM inventory WHERE uid=? AND instance=? FOR UPDATE`, uid, instance).Scan(&item); err != nil {
		return nil, err
	}
	if len(item) != 68 || item[4] != protocol.ItemTalisman || protocol.ReadUint32(item, 0) != instance || protocol.ReadUint32(item, 5) != rule.Item {
		return nil, ErrDenied
	}
	// Return current state on retry; never refill again after intervening use.
	if replay {
		return item, tx.Commit()
	}
	if revision != nil {
		var current uint64
		var data []byte
		if err = tx.QueryRow("SELECT revision,rules FROM talisman_rules WHERE id=1 FOR UPDATE").Scan(&current, &data); err != nil {
			return nil, err
		}
		var settings TalismanSettings
		if err = json.Unmarshal(data, &settings.Rules); err != nil {
			return nil, err
		}
		if current != *revision || !settings.Rules.Enabled || settings.Validate() != nil {
			return nil, ErrDenied
		}
		matched := false
		for _, candidate := range settings.Rules.Repairs {
			if candidate == rule {
				matched = true
				break
			}
		}
		if !matched {
			return nil, ErrDenied
		}
	}

	if !usableItem(item) || protocol.ReadUint16(item, 23) >= rule.Capacity {
		return nil, ErrDenied
	}
	var expired bool
	if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM inventory_expirations WHERE uid=? AND instance=? AND expires_at<=?)`, uid, instance, time.Now().Unix()).Scan(&expired); err != nil {
		return nil, err
	}
	if expired {
		return nil, ErrDenied
	}
	rows, err := tx.Query(`SELECT i.instance,i.record FROM inventory i LEFT JOIN inventory_expirations e ON e.uid=i.uid AND e.instance=i.instance WHERE i.uid=? AND (e.expires_at IS NULL OR e.expires_at>?) ORDER BY i.instance`, uid, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	type change struct {
		instance uint32
		record   []byte
	}
	var edits []change
	left := uint32(rule.Quantity)
	for rows.Next() {
		var c change
		if err = rows.Scan(&c.instance, &c.record); err != nil {
			rows.Close()
			return nil, err
		}
		p := c.record
		if left == 0 || !usableItem(p) || p[4] != 60 || protocol.ReadUint32(p, 5) != rule.Material || protocol.ReadUint16(p, 17) != 0 {
			continue
		}
		count := uint32(protocol.ReadUint16(p, 23))
		take := count
		if take > left {
			take = left
		}
		if take == 0 {
			continue
		}
		left -= take
		protocol.WriteUint16(p, 23, uint16(count-take))
		edits = append(edits, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if left != 0 {
		return nil, ErrDenied
	}
	for _, c := range edits {
		if protocol.ReadUint16(c.record, 23) == 0 {
			_, err = tx.Exec(`DELETE FROM inventory WHERE uid=? AND instance=?`, uid, c.instance)
		} else {
			_, err = tx.Exec(`UPDATE inventory SET record=? WHERE uid=? AND instance=?`, c.record, uid, c.instance)
		}
		if err != nil {
			return nil, err
		}
	}
	protocol.WriteUint16(item, 23, rule.Capacity)
	if _, err = tx.Exec(`UPDATE inventory SET record=? WHERE uid=? AND instance=?`, item, uid, instance); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(`INSERT INTO talisman_repairs(uid,operation_id,instance,material,quantity,capacity) VALUES(?,?,?,?,?,?)`, uid, operation, instance, rule.Material, rule.Quantity, rule.Capacity); err != nil {
		return nil, err
	}
	return item, tx.Commit()
}
