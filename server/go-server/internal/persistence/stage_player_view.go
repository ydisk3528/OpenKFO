package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
)

// A policy projection, not proof that the server implements each PVE mode.
// Callers must still filter their supported map pools before game admission.
type StagePlayerView struct {
	Access                       StageAccess `json:"-"`
	Configured                   bool
	RuleRevision, UnlockRevision uint64
	Maps                         []uint32
	Catalogue                    []uint32
	ForcedMaps                   []uint32
}

// One repeatable-read snapshot prevents mixing an old catalogue with new
// grants or a changed title. clientHash must come from server configuration.
func (s *Store) StagePlayerView(uid uint64, clientHash string) (StagePlayerView, error) {
	out := StagePlayerView{Maps: []uint32{}}
	if err := (StagePlayerUnlocks{UID: uid, ClientHash: clientHash}).Validate(); err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), transactionTimeout)
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var profile []byte
	if err = tx.QueryRow("SELECT profile FROM accounts WHERE uid=?", uid).Scan(&profile); err != nil {
		return out, err
	}
	if len(profile) != 360 {
		return out, ErrDenied
	}
	var data []byte
	err = tx.QueryRow("SELECT revision,rules FROM stage_access WHERE id=1").Scan(&out.RuleRevision, &data)
	if err == sql.ErrNoRows {
		return out, tx.Commit()
	}
	if err != nil {
		return out, err
	}
	access, err := decodeStageAccess(data, out.RuleRevision)
	if err != nil {
		return out, err
	}
	if len(access.PVEMaps) == 0 {
		return out, tx.Commit()
	}
	if access.ClientHash != clientHash {
		return out, ErrDenied
	}
	out.Access = access
	out.Catalogue = append([]uint32(nil), access.PVEMaps...)
	var raw []byte
	grants := StagePlayerUnlocks{UID: uid, ClientHash: clientHash}
	err = tx.QueryRow("SELECT revision,maps FROM stage_player_unlocks WHERE uid=? AND client_hash=?", uid, clientHash).Scan(&out.UnlockRevision, &raw)
	if err != nil && err != sql.ErrNoRows {
		return out, err
	}
	if err == nil {
		if err = json.Unmarshal(raw, &grants.Maps); err != nil {
			return out, err
		}
		if err = grants.Validate(); err != nil {
			return out, err
		}
	}
	allowed := map[uint32]bool{}
	for _, id := range grants.Maps {
		allowed[id] = true
	}
	for _, id := range access.PVEMaps {
		if access.AllowsPlayer(id, profile[TitleLevelOffset], clientHash, allowed) {
			out.Maps = append(out.Maps, id)
			if access.ForceOpens(id) {
				out.ForcedMaps = append(out.ForcedMaps, id)
			}
		}
	}
	out.Configured = true
	return out, tx.Commit()
}
