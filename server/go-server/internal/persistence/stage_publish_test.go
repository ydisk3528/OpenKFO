package persistence

import (
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// All touched tables are connection-local temporary tables; no account or
// running server configuration is changed by this test.
func TestStagePublishTransaction(t *testing.T) {
	dsn := os.Getenv("OPENKFO_STAGE_TEST_DSN")
	if dsn == "" {
		t.Skip("temporary-table database test not configured")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	for _, query := range []string{
		`CREATE TEMPORARY TABLE stage_access(id INT PRIMARY KEY,revision BIGINT UNSIGNED NOT NULL,rules MEDIUMBLOB NOT NULL) ENGINE=InnoDB`,
		`CREATE TEMPORARY TABLE stage_access_audit(revision BIGINT UNSIGNED PRIMARY KEY,before_data MEDIUMBLOB,after_data MEDIUMBLOB) ENGINE=InnoDB`,
		`CREATE TEMPORARY TABLE stage_player_unlocks(uid BIGINT UNSIGNED,client_hash CHAR(64),revision BIGINT UNSIGNED,maps MEDIUMBLOB,PRIMARY KEY(uid,client_hash)) ENGINE=InnoDB`,
		`CREATE TEMPORARY TABLE stage_player_unlock_audit(uid BIGINT UNSIGNED,client_hash CHAR(64),revision BIGINT UNSIGNED,before_data MEDIUMBLOB,after_data MEDIUMBLOB,PRIMARY KEY(uid,client_hash,revision)) ENGINE=InnoDB`,
	} {
		if _, err = db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	oldHash, newHash := strings.Repeat("a", 64), strings.Repeat("b", 64)
	before := `{"client_hash":"` + oldHash + `","requirements_enabled":true,"requirements":[{"map_id":1201,"name":"training","title_level":1}],"disabled_maps":[],"future_field":{"keep":true}}`
	if _, err = db.Exec("INSERT INTO stage_access VALUES(1,4,?)", before); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO stage_player_unlocks VALUES(99,?,7,'[1201]')", oldHash); err != nil {
		t.Fatal(err)
	}
	s := &Store{DB: db}
	for _, commit := range []bool{false, true} {
		tx, e := s.BeginStageRebind(oldHash, newHash)
		if e != nil {
			t.Fatal(e)
		}
		// Give an incorrectly cancelled BeginTx context time to auto-rollback.
		time.Sleep(20 * time.Millisecond)
		if commit {
			err = tx.Commit()
		} else {
			err = tx.Rollback()
		}
		if err != nil {
			t.Fatal(err)
		}
		a, e := s.StageAccess()
		if e != nil {
			t.Fatal(e)
		}
		wantHash, wantRevision, wantRows := oldHash, uint64(4), 0
		if commit {
			wantHash, wantRevision, wantRows = newHash, 5, 1
		}
		if a.ClientHash != wantHash || a.Revision != wantRevision {
			t.Fatal(a)
		}
		var rows int
		if err = db.QueryRow("SELECT COUNT(*) FROM stage_player_unlocks WHERE client_hash=?", newHash).Scan(&rows); err != nil || rows != wantRows {
			t.Fatal(rows, err)
		}
	}
	var after []byte
	if err = db.QueryRow("SELECT rules FROM stage_access WHERE id=1").Scan(&after); err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	json.Unmarshal(after, &fields)
	if string(fields["future_field"]) != `{"keep":true}` {
		t.Fatal(string(after))
	}
	if tx, e := s.BeginStageRebind(oldHash, strings.Repeat("c", 64)); e == nil {
		tx.Rollback()
		t.Fatal("stale binding accepted")
	}
}
