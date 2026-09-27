package persistence

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrAccountBanned = errors.New("account banned")

const accountBanSchema = `CREATE TABLE IF NOT EXISTS account_bans(uid BIGINT UNSIGNED PRIMARY KEY, enabled BOOLEAN NOT NULL, expires_at BIGINT NOT NULL, reason VARCHAR(500) NOT NULL, generation BIGINT UNSIGNED NOT NULL, FOREIGN KEY(uid) REFERENCES accounts(uid)) ENGINE=InnoDB`
const accountBanAuditSchema = `CREATE TABLE IF NOT EXISTS account_ban_audit(id VARCHAR(100) CHARACTER SET ascii PRIMARY KEY, uid BIGINT UNSIGNED NOT NULL, request_data BLOB NOT NULL, before_data BLOB NOT NULL, after_data BLOB NOT NULL, created TIMESTAMP DEFAULT CURRENT_TIMESTAMP, INDEX(uid,created)) ENGINE=InnoDB`

type AccountBan struct {
	Enabled    bool   `json:"enabled"`
	ExpiresAt  int64  `json:"expires_at"`
	Reason     string `json:"reason"`
	Generation uint64 `json:"generation"`
}

func (b AccountBan) Active(now int64) bool {
	return b.Enabled && (b.ExpiresAt == 0 || b.ExpiresAt > now)
}

func (store *Store) AccountBan(uid uint64) (AccountBan, error) {
	var b AccountBan
	err := store.DB.QueryRow(`SELECT enabled,expires_at,reason,generation FROM account_bans WHERE uid=?`, uid).Scan(&b.Enabled, &b.ExpiresAt, &b.Reason, &b.Generation)
	if err == sql.ErrNoRows {
		err = nil
	}
	return b, err
}

func validateAccountBan(r AdminRequest, now int64) error {
	if r.UID == 0 || len(r.ID) == 0 || len(r.ID) > 100 || strings.TrimSpace(r.Reason) == "" || len([]rune(r.Reason)) > 500 || r.ExpiresAt == nil || *r.ExpiresAt < 0 {
		return fmt.Errorf("请选择账号并填写原因及有效期限")
	}
	if r.Enabled && *r.ExpiresAt != 0 && *r.ExpiresAt <= now {
		return fmt.Errorf("封禁结束时间必须晚于当前时间")
	}
	if !r.Enabled && *r.ExpiresAt != 0 {
		return fmt.Errorf("解封期限必须为零")
	}
	return nil
}

func (store *Store) SaveAccountBan(r AdminRequest) (any, error) {
	if err := validateAccountBan(r, time.Now().Unix()); err != nil {
		return nil, err
	}
	tx, txCancel, err := beginTransaction(store.DB)
	defer txCancel()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Serialize changes per account and commit the audit with the actual ban.
	var uid uint64
	if err = tx.QueryRow(`SELECT uid FROM accounts WHERE uid=? FOR UPDATE`, r.UID).Scan(&uid); err != nil {
		return nil, err
	}
	request, _ := json.Marshal(struct {
		UID       uint64
		Enabled   bool
		ExpiresAt int64
		Reason    string
	}{r.UID, r.Enabled, *r.ExpiresAt, r.Reason})
	var prior, after []byte
	err = tx.QueryRow(`SELECT request_data,after_data FROM account_ban_audit WHERE id=?`, r.ID).Scan(&prior, &after)
	if err == nil {
		if string(prior) != string(request) {
			return nil, fmt.Errorf("操作编号已用于其他请求")
		}
		return json.RawMessage(after), nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	var before AccountBan
	err = tx.QueryRow(`SELECT enabled,expires_at,reason,generation FROM account_bans WHERE uid=?`, uid).Scan(&before.Enabled, &before.ExpiresAt, &before.Reason, &before.Generation)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	next := AccountBan{Enabled: r.Enabled, ExpiresAt: *r.ExpiresAt, Reason: r.Reason, Generation: before.Generation}
	if next.Enabled {
		next.Generation++
	}
	if _, err = tx.Exec(`INSERT INTO account_bans(uid,enabled,expires_at,reason,generation) VALUES(?,?,?,?,?) ON DUPLICATE KEY UPDATE enabled=VALUES(enabled),expires_at=VALUES(expires_at),reason=VALUES(reason),generation=VALUES(generation)`, uid, next.Enabled, next.ExpiresAt, next.Reason, next.Generation); err != nil {
		return nil, err
	}
	beforeJSON, _ := json.Marshal(before)
	after, _ = json.Marshal(next)
	if _, err = tx.Exec(`INSERT INTO account_ban_audit(id,uid,request_data,before_data,after_data) VALUES(?,?,?,?,?)`, r.ID, uid, request, beforeJSON, after); err != nil {
		return nil, err
	}
	return next, tx.Commit()
}

func (store *Store) UsersList() (any, error) {
	rows, err := store.DB.Query(`SELECT a.uid,a.account,a.nickname,a.gold,a.tickets,COALESCE(b.enabled,FALSE),COALESCE(b.expires_at,0),COALESCE(b.reason,'') FROM accounts a LEFT JOIN account_bans b ON b.uid=a.uid ORDER BY a.uid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []map[string]any{}
	for rows.Next() {
		var uid uint64
		var account, nickname string
		var gold, tickets uint64
		var b AccountBan
		if err = rows.Scan(&uid, &account, &nickname, &gold, &tickets, &b.Enabled, &b.ExpiresAt, &b.Reason); err != nil {
			return nil, err
		}
		result = append(result, map[string]any{"uid": uid, "account": account, "nickname": nickname, "gold": gold, "tickets": tickets, "banned": b.Active(time.Now().Unix()), "expires_at": b.ExpiresAt, "reason": b.Reason})
	}
	return result, rows.Err()
}

func (store *Store) AccountBanHistory(uid uint64) (any, error) {
	rows, err := store.DB.Query(`SELECT id,CAST(created AS CHAR),before_data,after_data FROM account_ban_audit WHERE uid=? ORDER BY created DESC,id DESC LIMIT 100`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []map[string]any{}
	for rows.Next() {
		var id, created string
		var before, after []byte
		if err = rows.Scan(&id, &created, &before, &after); err != nil {
			return nil, err
		}
		result = append(result, map[string]any{"id": id, "created": created, "before": json.RawMessage(before), "after": json.RawMessage(after)})
	}
	return result, rows.Err()
}
