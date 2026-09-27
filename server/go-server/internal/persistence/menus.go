package persistence

import (
	"bytes"
	"context"
	"sort"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/encoding/simplifiedchinese"
	"kungfu.local/server/internal/protocol"
)

func DecodeGBK(encoded []byte) (string, error) {
	decoded, err := simplifiedchinese.GBK.NewDecoder().Bytes(encoded)
	if err != nil {
		return "", err
	}
	text := string(decoded)
	if !bytes.Equal(GBK(text), encoded) {
		return "", ErrDenied
	}
	for _, character := range text {
		if unicode.IsControl(character) {
			return "", ErrDenied
		}
	}
	return text, nil
}

func (m *RoleManager) Rename(uid uint64, nickname string) (string, error) {
	encoded := GBK(nickname)
	if len(encoded) == 0 || len(encoded) > 20 || strings.TrimSpace(nickname) != nickname {
		return "", ErrDenied
	}
	if decoded, err := DecodeGBK(encoded); err != nil || decoded != nickname {
		return "", ErrDenied
	}
	if err := m.store.CheckText(nickname); err != nil {
		return "", err
	}
	transaction, transactionCancel, err := beginTransaction(m.store.DB)
	defer transactionCancel()
	if err != nil {
		return "", err
	}
	defer transaction.Rollback()
	// Existing accounts are locked in UID order to serialize exact-name changes.
	rows, err := transaction.Query(`SELECT uid,nickname FROM accounts ORDER BY uid FOR UPDATE`)
	if err != nil {
		return "", err
	}
	var oldName string
	for rows.Next() {
		var accountUID uint64
		var currentName string
		if err = rows.Scan(&accountUID, &currentName); err != nil {
			rows.Close()
			return "", err
		}
		if accountUID == uid {
			oldName = currentName
		} else if currentName == nickname {
			rows.Close()
			return "", ErrDenied
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", err
	}
	if oldName == "" {
		return "", ErrDenied
	}
	var profile []byte
	if err = transaction.QueryRow(`SELECT profile FROM accounts WHERE uid=?`, uid).Scan(&profile); err != nil {
		return "", err
	}
	if len(profile) != 360 {
		return "", ErrDenied
	}
	clear(profile[4:25])
	copy(profile[4:25], encoded)
	if _, err = transaction.Exec(`UPDATE accounts SET nickname=?,profile=? WHERE uid=?`, nickname, profile, uid); err != nil {
		return "", err
	}
	return oldName, transaction.Commit()
}

func (store *Store) Rankings(uid uint64, category byte) ([]byte, []byte, error) {
	offsets := [][]int{{249}, {137, 145, 153, 161}, {129}, {137}, {145}, {153}, {161}, {181}, {185}, {189}, {193}, {197}, {201}}
	if int(category) >= len(offsets) {
		return nil, nil, ErrDenied
	}
	entries, err := store.rankingEntries(category, func() ([]rankingEntry, error) { return store.loadRankings(category, offsets[category]) })
	if err != nil {
		return nil, nil, err
	}
	var directory []byte
	var own []byte
	for rank, account := range entries {
		if account.UID == uid {
			own = append([]byte{category}, protocol.Uint32Bytes(uint32(rank))...)
		}
		if rank >= 100 {
			continue
		}
		record := make([]byte, 27)
		copy(record[:21], account.Name)
		record[21] = byte(rank)
		protocol.WriteUint32(record, 22, uint32(account.Score))
		record[26] = category
		directory = append(directory, record...)
	}
	if own == nil {
		return nil, nil, ErrDenied
	}
	return directory, own, nil
}

func (store *Store) loadRankings(category byte, offsets []int) ([]rankingEntry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err := store.DB.QueryContext(ctx, `SELECT uid,nickname,profile FROM accounts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []rankingEntry
	for rows.Next() {
		var accountUID uint64
		var nickname string
		var profile []byte
		if err = rows.Scan(&accountUID, &nickname, &profile); err != nil {
			return nil, err
		}
		if len(profile) != 360 {
			return nil, ErrDenied
		}
		var score int32
		for _, offset := range offsets {
			score += int32(protocol.ReadUint32(profile, offset))
		}
		entries = append(entries, rankingEntry{accountUID, GBK(nickname), score})
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(entries, func(first, second int) bool {
		if entries[first].Score == entries[second].Score {
			return entries[first].UID < entries[second].UID
		}
		return entries[first].Score > entries[second].Score
	})
	return entries, nil
}
