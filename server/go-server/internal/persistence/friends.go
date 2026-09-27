package persistence

import (
	"bytes"
	"context"
	"database/sql"
	"time"
)

// FriendManager owns directed native buddy lists, not reciprocal relationships.
type FriendManager struct{ store *Store }

func (s *Store) FriendManager() *FriendManager { return &FriendManager{store: s} }

const FriendLimit = 100 // SDFriend.dll's fixed native list capacity.

type Friend struct {
	UID     uint64
	Name    string
	Profile []byte
}

func validFriend(f Friend) bool {
	b := GBK(f.Name)
	n, err := DecodeGBK(b)
	return f.UID != 0 && len(b) > 0 && len(b) <= 20 && !bytes.ContainsRune(b, 0) && err == nil && n == f.Name && len(f.Profile) == 360 && (f.Profile[122] == 1 || f.Profile[122] == 2)
}

func (m *FriendManager) List(uid uint64) ([]Friend, error) {
	if uid == 0 {
		return nil, ErrDenied
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err := m.store.DB.QueryContext(ctx, `SELECT a.uid,a.nickname,a.profile FROM friends f JOIN accounts a ON a.uid=f.friend_uid WHERE f.uid=? ORDER BY a.uid LIMIT 101`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Friend
	for rows.Next() {
		var f Friend
		if err := rows.Scan(&f.UID, &f.Name, &f.Profile); err != nil {
			return nil, err
		}
		// A reset character is temporarily absent until it creates a role again.
		if validFriend(f) {
			out = append(out, f)
		}
	}
	if len(out) > FriendLimit {
		return nil, ErrDenied
	}
	return out, rows.Err()
}

// Lock both account rows to serialize add/remove and capacity checks even across
// server processes. The caller's authenticated UID is the only list owner.
func (m *FriendManager) Change(uid uint64, name string, add bool) (Friend, error) {
	var f Friend
	if uid == 0 || name == "" {
		return f, ErrDenied
	}
	rows, err := m.store.DB.Query(`SELECT uid,nickname,profile FROM accounts WHERE BINARY nickname=BINARY ? LIMIT 2`, name)
	if err != nil {
		return f, err
	}
	count := 0
	for rows.Next() {
		count++
		if err = rows.Scan(&f.UID, &f.Name, &f.Profile); err != nil {
			rows.Close()
			return f, err
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return f, err
	}
	if count != 1 || f.UID == uid || !validFriend(f) {
		return f, ErrDenied
	}
	tx, txCancel, err := beginTransaction(m.store.DB)
	defer txCancel()
	if err != nil {
		return f, err
	}
	defer tx.Rollback()
	// Canonical ordering also avoids opposite-direction adds deadlocking on
	// the foreign-key locks. Locking reads recheck a concurrent role reset.
	low, high := uid, f.UID
	if low > high {
		low, high = high, low
	}
	for _, id := range []uint64{low, high} {
		var current Friend
		if err = tx.QueryRow(`SELECT uid,nickname,profile FROM accounts WHERE uid=? FOR UPDATE`, id).Scan(&current.UID, &current.Name, &current.Profile); err != nil {
			return f, err
		}
		if !validFriend(current) {
			return f, ErrDenied
		}
		if id == f.UID {
			f = current
		}
	}
	if f.Name != name {
		return f, ErrDenied
	}
	var exists int
	err = tx.QueryRow(`SELECT 1 FROM friends WHERE uid=? AND friend_uid=?`, uid, f.UID).Scan(&exists)
	if err != nil && err != sql.ErrNoRows {
		return f, err
	}
	if add && exists == 0 {
		var size int
		if err = tx.QueryRow(`SELECT COUNT(*) FROM friends WHERE uid=?`, uid).Scan(&size); err != nil {
			return f, err
		}
		if size >= FriendLimit {
			return f, ErrDenied
		}
		_, err = tx.Exec(`INSERT INTO friends(uid,friend_uid) VALUES(?,?)`, uid, f.UID)
	} else if !add {
		_, err = tx.Exec(`DELETE FROM friends WHERE uid=? AND friend_uid=?`, uid, f.UID)
	}
	if err != nil {
		return f, err
	}
	return f, tx.Commit()
}
