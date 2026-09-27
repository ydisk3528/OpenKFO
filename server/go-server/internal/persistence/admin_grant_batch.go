package persistence

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"kungfu.local/server/internal/protocol"
	"regexp"
	"time"
)

type BatchGrantItem struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	Quantity  int    `json:"quantity"`
	Days      int    `json:"days"`
	Stackable bool   `json:"stackable"`
	Record    []byte `json:"record,omitempty"`
}

var batchIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func validateBatchItems(items []BatchGrantItem) error {
	if len(items) == 0 || len(items) > 100 {
		return ErrDenied
	}
	seen := map[string]bool{}
	total := 0
	for _, i := range items {
		if i.Key == "currency:ticket" {
			if seen[i.Key] || i.Quantity < 1 || i.Quantity > 2147483647 || len(i.Record) != 0 || i.Stackable {
				return fmt.Errorf("点券数量须为1–2147483647且不能重复")
			}
			seen[i.Key] = true
			continue
		}
		if len(i.Record) != 68 || i.Key != itemKey(i.Record) || seen[i.Key] || i.Quantity < 1 || i.Quantity > 999 || i.Days < 1 || i.Days > 3650 || protocol.ReadUint16(i.Record, 17) != 0 {
			return fmt.Errorf("道具清单无效或重复")
		}
		if i.Stackable && int(protocol.ReadUint16(i.Record, 23)) != i.Quantity {
			return ErrDenied
		}
		seen[i.Key] = true
		total += i.Quantity
	}
	if total > 5000 {
		return fmt.Errorf("每个用户每批最多5000件")
	}
	return nil
}

func (s *Store) ensureGrantBatches() error {
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS gm_grant_batches(id VARCHAR(64) CHARACTER SET ascii PRIMARY KEY, request_hash BINARY(32) NOT NULL, items LONGBLOB NOT NULL, created TIMESTAMP DEFAULT CURRENT_TIMESTAMP) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS gm_grant_recipients(batch_id VARCHAR(64) CHARACTER SET ascii NOT NULL, uid BIGINT UNSIGNED NOT NULL, account VARCHAR(255) NOT NULL, state VARCHAR(16) NOT NULL DEFAULT 'pending', detail TEXT NOT NULL, delivered LONGBLOB NULL, updated TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP, PRIMARY KEY(batch_id,uid)) ENGINE=InnoDB`,
	} {
		if _, e := s.DB.Exec(q); e != nil {
			return e
		}
	}
	return nil
}

func (s *Store) adminGrantBatch(r AdminRequest) (any, error) {
	if e := s.ensureGrantBatches(); e != nil {
		return nil, e
	}
	if r.Operation == "grant_batch_list" {
		rows, e := s.DB.Query(`SELECT b.id,CAST(b.created AS CHAR),COUNT(*),SUM(p.state='success'),SUM(p.state='failed') FROM gm_grant_batches b JOIN gm_grant_recipients p ON p.batch_id=b.id GROUP BY b.id,b.created ORDER BY b.created DESC,b.id DESC LIMIT 200`)
		if e != nil {
			return nil, e
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var id, created string
			var total, success, failed int
			if e = rows.Scan(&id, &created, &total, &success, &failed); e != nil {
				return nil, e
			}
			out = append(out, map[string]any{"id": id, "created": created, "total": total, "success": success, "failed": failed})
		}
		return out, rows.Err()
	}
	if !batchIDPattern.MatchString(r.ID) {
		return nil, fmt.Errorf("批次编号无效")
	}
	if r.Operation == "grant_batch_create" {
		if e := s.createGrantBatch(r); e != nil {
			return nil, e
		}
	}
	if r.Operation == "grant_batch_send" || r.Operation == "grant_batch_send_many" {
		uids := r.UIDs
		if r.Operation == "grant_batch_send" {
			uids = []uint64{r.UID}
		}
		if len(uids) < 1 || len(uids) > 20 {
			return nil, fmt.Errorf("每次发放须为1–20个用户")
		}
		seen := map[uint64]bool{}
		for _, uid := range uids {
			if uid == 0 || seen[uid] {
				return nil, fmt.Errorf("用户编号无效或重复")
			}
			seen[uid] = true
		}
		for _, uid := range uids {
			if e := s.sendGrantBatch(r.ID, uid); e != nil {
				// Never overwrite success after a lost commit acknowledgement.
				_, recordErr := s.DB.Exec(`UPDATE gm_grant_recipients SET state='failed',detail=? WHERE batch_id=? AND uid=? AND state<>'success'`, e.Error(), r.ID, uid)
				if recordErr != nil {
					return nil, e
				}
			}
		}
	}

	return s.grantBatchStatus(r.ID)
}

func (s *Store) createGrantBatch(r AdminRequest) error {
	if e := validateBatchItems(r.BatchItems); e != nil {
		return e
	}
	if (r.All && len(r.UIDs) > 0) || (!r.All && len(r.UIDs) == 0) {
		return fmt.Errorf("请选择指定用户或全部用户")
	}
	request, _ := json.Marshal(struct {
		Items []BatchGrantItem
		All   bool
		UIDs  []uint64
	}{r.BatchItems, r.All, r.UIDs})
	hash := sha256.Sum256(request)
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	items, _ := json.Marshal(r.BatchItems)
	if _, e = tx.Exec(`INSERT IGNORE INTO gm_grant_batches(id,request_hash,items) VALUES(?,?,?)`, r.ID, hash[:], items); e != nil {
		return e
	}
	var old []byte
	if e = tx.QueryRow(`SELECT request_hash FROM gm_grant_batches WHERE id=? FOR UPDATE`, r.ID).Scan(&old); e != nil {
		return e
	}
	if !bytes.Equal(old, hash[:]) {
		return fmt.Errorf("批次已存在，不能修改名单或道具")
	}
	var count int
	if e = tx.QueryRow(`SELECT COUNT(*) FROM gm_grant_recipients WHERE batch_id=?`, r.ID).Scan(&count); e != nil {
		return e
	}
	if count > 0 {
		return tx.Commit()
	}
	if r.All {
		_, e = tx.Exec(`INSERT INTO gm_grant_recipients(batch_id,uid,account,detail) SELECT ?,uid,account,'' FROM accounts`, r.ID)
	} else {
		seen := map[uint64]bool{}
		for _, uid := range r.UIDs {
			if uid == 0 || seen[uid] {
				return fmt.Errorf("用户编号无效或重复")
			}
			seen[uid] = true
			var account string
			if e = tx.QueryRow(`SELECT account FROM accounts WHERE uid=?`, uid).Scan(&account); e != nil {
				return e
			}
			if _, e = tx.Exec(`INSERT INTO gm_grant_recipients(batch_id,uid,account,detail) VALUES(?,?,?,'')`, r.ID, uid, account); e != nil {
				return e
			}
		}
	}
	if e != nil {
		return e
	}
	if e = tx.QueryRow(`SELECT COUNT(*) FROM gm_grant_recipients WHERE batch_id=?`, r.ID).Scan(&count); e != nil {
		return e
	}
	if count == 0 {
		return fmt.Errorf("没有可发放用户")
	}
	return tx.Commit()
}

func (s *Store) grantBatchStatus(id string) (any, error) {
	var raw []byte
	if e := s.DB.QueryRow(`SELECT items FROM gm_grant_batches WHERE id=?`, id).Scan(&raw); e != nil {
		return nil, e
	}
	var items []BatchGrantItem
	if e := json.Unmarshal(raw, &items); e != nil {
		return nil, e
	}
	rows, e := s.DB.Query(`SELECT uid,account,state,detail,CAST(updated AS CHAR) FROM gm_grant_recipients WHERE batch_id=? ORDER BY uid`, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	recipients := []map[string]any{}
	success, failed, each, tickets := 0, 0, 0, 0
	for _, i := range items {
		if i.Key == "currency:ticket" {
			tickets += i.Quantity
			continue
		}
		each += i.Quantity
	}
	for rows.Next() {
		var uid uint64
		var account, state, detail, updated string
		if e = rows.Scan(&uid, &account, &state, &detail, &updated); e != nil {
			return nil, e
		}
		if state == "success" {
			success++
		}
		if state == "failed" {
			failed++
		}
		recipients = append(recipients, map[string]any{"uid": uid, "account": account, "state": state, "detail": detail, "updated": updated})
	}
	return map[string]any{"id": id, "items": items, "recipients": recipients, "total": len(recipients), "success": success, "failed": failed, "delivered_quantity": success * each, "each_quantity": each, "each_tickets": tickets, "delivered_tickets": int64(success) * int64(tickets)}, rows.Err()
}

func (s *Store) sendGrantBatch(id string, uid uint64) error {
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var state string
	if e = tx.QueryRow(`SELECT state FROM gm_grant_recipients WHERE batch_id=? AND uid=? FOR UPDATE`, id, uid).Scan(&state); e != nil {
		return e
	}
	if state == "success" {
		return tx.Commit()
	}
	var locked uint64
	if e = tx.QueryRow(`SELECT uid FROM accounts WHERE uid=? FOR UPDATE`, uid).Scan(&locked); e != nil {
		return e
	}
	var raw []byte
	if e = tx.QueryRow(`SELECT items FROM gm_grant_batches WHERE id=?`, id).Scan(&raw); e != nil {
		return e
	}
	var items []BatchGrantItem
	if e = json.Unmarshal(raw, &items); e != nil {
		return e
	}
	if e = validateBatchItems(items); e != nil {
		return e
	}
	delivered := [][]byte{}
	for _, i := range items {
		if i.Key == "currency:ticket" {
			var before uint64
			if e = tx.QueryRow(`SELECT tickets FROM accounts WHERE uid=?`, uid).Scan(&before); e != nil {
				return e
			}
			after := before + uint64(i.Quantity)
			if after > 2147483647 {
				return fmt.Errorf("点券余额超过上限，本用户本批次未发放")
			}
			if _, e = tx.Exec(`UPDATE accounts SET tickets=? WHERE uid=?`, after, uid); e != nil {
				return e
			}
			continue
		}
		if i.Stackable {
			rows, err := tx.Query(`SELECT i.record FROM inventory i LEFT JOIN inventory_expirations x ON x.uid=i.uid AND x.instance=i.instance WHERE i.uid=? AND (x.expires_at IS NULL OR x.expires_at>?) ORDER BY i.instance`, uid, time.Now().Unix())
			if err != nil {
				return err
			}
			var existing []byte
			for rows.Next() {
				var b []byte
				if e = rows.Scan(&b); e != nil {
					rows.Close()
					return e
				}
				if len(b) == 68 && itemKey(b) == i.Key && usableItem(b) {
					existing = b
					break
				}
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return e
			}
			if existing != nil {
				n := int(protocol.ReadUint16(existing, 23)) + i.Quantity
				if n > 999 {
					return fmt.Errorf("%s 添加后超过999，本用户全部道具未发放", i.Name)
				}
				protocol.WriteUint16(existing, 23, uint16(n))
				if _, e = tx.Exec(`UPDATE inventory SET record=? WHERE uid=? AND instance=?`, existing, uid, protocol.ReadUint32(existing, 0)); e != nil {
					return e
				}
				delivered = append(delivered, existing)
				continue
			}
		}
		copies := i.Quantity
		if i.Stackable {
			copies = 1
		}
		for n := 0; n < copies; n++ {
			item, err := s.InventoryManager().AddItem(tx, uid, i.Record, 0)
			if err != nil {
				return err
			}
			delivered = append(delivered, item)
		}
	}
	raw, e = json.Marshal(delivered)
	if e != nil {
		return e
	}
	if _, e = tx.Exec(`UPDATE gm_grant_recipients SET state='success',detail='',delivered=? WHERE batch_id=? AND uid=?`, raw, id, uid); e != nil {
		return e
	}
	return tx.Commit()
}
