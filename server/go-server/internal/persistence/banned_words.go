package persistence

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"kungfu.local/server/internal/moderation"
	"sync"
	"time"
)

var ErrBannedWord = errors.New(moderation.Notice)

type BannedWordsSettings struct {
	Revision uint64   `json:"revision"`
	Words    []string `json:"words"`
}
type wordCache struct {
	mu       sync.Mutex
	next     time.Time
	revision uint64
	filter   *moderation.Filter
}

var defaultWordFilter = moderation.Compile(moderation.DefaultWords())

func (s *Store) SeedBannedWords(words []string) error {
	clean, err := moderation.Clean(words)
	if err != nil {
		return err
	}
	data, err := json.Marshal(clean)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec("INSERT IGNORE INTO banned_words_config(id,revision,words) VALUES(1,1,?)", data)
	return err
}
func (s *Store) BannedWords() (BannedWordsSettings, error) {
	var r BannedWordsSettings
	var data []byte
	err := s.DB.QueryRow("SELECT revision,words FROM banned_words_config WHERE id=1").Scan(&r.Revision, &data)
	if err == sql.ErrNoRows {
		return BannedWordsSettings{Words: moderation.DefaultWords()}, nil
	}
	if err != nil {
		return r, err
	}
	err = json.Unmarshal(data, &r.Words)
	return r, err
}
func (s *Store) SaveBannedWords(r BannedWordsSettings) (BannedWordsSettings, error) {
	words, err := moderation.Clean(r.Words)
	if err != nil {
		return r, err
	}
	r.Words = words
	data, err := json.Marshal(words)
	if err != nil {
		return r, err
	}
	tx, txCancel, err := beginTransaction(s.DB)
	defer txCancel()
	if err != nil {
		return r, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT IGNORE INTO banned_words_config(id,revision,words) VALUES(1,0,'[]')"); err != nil {
		return r, err
	}
	var revision uint64
	if err = tx.QueryRow("SELECT revision FROM banned_words_config WHERE id=1 FOR UPDATE").Scan(&revision); err != nil {
		return r, err
	}
	if revision != r.Revision {
		return r, fmt.Errorf("违禁词已被其他管理员修改，请重新读取后保存")
	}
	if _, err = tx.Exec("UPDATE banned_words_config SET revision=revision+1,words=? WHERE id=1", data); err != nil {
		return r, err
	}
	if err = tx.Commit(); err != nil {
		return r, err
	}
	r.Revision++
	if s.wordFilter != nil {
		s.wordFilter.mu.Lock()
		s.wordFilter.next = time.Time{}
		s.wordFilter.mu.Unlock()
	}
	return r, nil
}
func (s *Store) CheckText(text string) error {
	f := defaultWordFilter
	if s != nil && s.wordFilter != nil {
		c := s.wordFilter
		c.mu.Lock()
		defer c.mu.Unlock()
		if time.Now().After(c.next) {
			var revision uint64
			err := s.DB.QueryRow("SELECT revision FROM banned_words_config WHERE id=1").Scan(&revision)
			if err != nil && err != sql.ErrNoRows {
				return err
			}
			if c.filter == nil || c.revision != revision {
				rules, err := s.BannedWords()
				if err != nil {
					return err
				}
				words, err := moderation.Clean(rules.Words)
				if err != nil {
					return err
				}
				c.filter = moderation.Compile(words)
				c.revision = rules.Revision
			}
			c.next = time.Now().Add(time.Second)
		}
		f = c.filter
	}
	if f.Contains(text) {
		return ErrBannedWord
	}
	return nil
}
