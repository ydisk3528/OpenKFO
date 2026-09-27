package persistence

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// Unlocks are explicit server grants, not a claim that a map was cleared.
// They are scoped to the client catalogue; old-version grants cannot authorize
// a different map that reuses the same numeric ID in a new catalogue.
type StagePlayerUnlocks struct {
	UID        uint64   `json:"uid"`
	ClientHash string   `json:"client_hash"`
	Revision   uint64   `json:"revision"`
	Maps       []uint32 `json:"maps"`
}

func (p StagePlayerUnlocks) Validate() error {
	hash, err := hex.DecodeString(p.ClientHash)
	if p.UID == 0 || err != nil || len(hash) != 32 || strings.ToLower(p.ClientHash) != p.ClientHash || len(p.Maps) > 4096 {
		return ErrDenied
	}
	seen := map[uint32]bool{}
	for _, id := range p.Maps {
		if id == 0 || id > 0x7fffffff || seen[id] {
			return ErrDenied
		}
		seen[id] = true
	}
	return nil
}

func (s *Store) StagePlayerUnlocks(uid uint64, hash string) (StagePlayerUnlocks, error) {
	p := StagePlayerUnlocks{UID: uid, ClientHash: hash, Maps: []uint32{}}
	if err := p.Validate(); err != nil {
		return p, err
	}
	var data []byte
	err := s.DB.QueryRow("SELECT revision,maps FROM stage_player_unlocks WHERE uid=? AND client_hash=?", uid, hash).Scan(&p.Revision, &data)
	if err == sql.ErrNoRows {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	if err = json.Unmarshal(data, &p.Maps); err != nil {
		return p, err
	}
	return p, p.Validate()
}

// Save replaces one player's grant set with optimistic concurrency and an
// audit in the same transaction. Only privileged management calls may use it.
func (s *Store) SaveStagePlayerUnlocks(p StagePlayerUnlocks) (StagePlayerUnlocks, error) {
	if err := p.Validate(); err != nil {
		return p, err
	}
	if p.Maps == nil {
		p.Maps = []uint32{}
	}
	after, err := json.Marshal(p.Maps)
	if err != nil {
		return p, err
	}
	tx, txCancel, err := beginTransaction(s.DB)
	defer txCancel()
	if err != nil {
		return p, err
	}
	defer tx.Rollback()
	var account uint64
	if err = tx.QueryRow("SELECT uid FROM accounts WHERE uid=? FOR UPDATE", p.UID).Scan(&account); err != nil {
		return p, err
	}
	var config []byte
	var configRevision uint64
	if err = tx.QueryRow("SELECT revision,rules FROM stage_access WHERE id=1 FOR UPDATE").Scan(&configRevision, &config); err != nil {
		return p, err
	}
	access, err := decodeStageAccess(config, configRevision)
	if err != nil {
		return p, err
	}
	if access.ClientHash != p.ClientHash {
		return p, ErrDenied
	}
	known := map[uint32]bool{}
	for _, r := range access.Requirements {
		known[r.MapID] = true
	}
	for _, id := range p.Maps {
		if !known[id] {
			return p, fmt.Errorf("关卡不在当前目录中：%d", id)
		}
	}
	if _, err = tx.Exec("INSERT IGNORE INTO stage_player_unlocks(uid,client_hash,revision,maps) VALUES(?,?,0,'[]')", p.UID, p.ClientHash); err != nil {
		return p, err
	}
	var revision uint64
	var before []byte
	if err = tx.QueryRow("SELECT revision,maps FROM stage_player_unlocks WHERE uid=? AND client_hash=? FOR UPDATE", p.UID, p.ClientHash).Scan(&revision, &before); err != nil {
		return p, err
	}
	if revision != p.Revision {
		return p, fmt.Errorf("玩家解锁已被修改，请重新读取")
	}
	if _, err = tx.Exec("UPDATE stage_player_unlocks SET revision=revision+1,maps=? WHERE uid=? AND client_hash=?", after, p.UID, p.ClientHash); err != nil {
		return p, err
	}
	if _, err = tx.Exec("INSERT INTO stage_player_unlock_audit(uid,client_hash,revision,before_data,after_data) VALUES(?,?,?,?,?)", p.UID, p.ClientHash, revision+1, before, after); err != nil {
		return p, err
	}
	if err = tx.Commit(); err != nil {
		return p, err
	}
	p.Revision++
	return p, nil
}
