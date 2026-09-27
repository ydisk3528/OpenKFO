package persistence

import (
	"database/sql"
	"encoding/json"
	"github.com/go-sql-driver/mysql"
	"kungfu.local/server/internal/protocol"
	"os"
	"strings"
	"testing"
	"time"
)

func TestTrainingClaimLocalDatabase(t *testing.T) {
	dsn := os.Getenv("OPENKFO_DEBUG_DSN")
	if dsn == "" {
		t.Skip("independent debug DB required")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil || !strings.HasPrefix(cfg.DBName, "openkfo_debug_") {
		t.Fatal("not independent debug DB")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`CREATE TEMPORARY TABLE level_reward_receipts(uid BIGINT,level INT,items BLOB,PRIMARY KEY(uid,level)) ENGINE=InnoDB`)
	exec(`CREATE TEMPORARY TABLE accounts(uid BIGINT PRIMARY KEY,profile BLOB NOT NULL,gold INT NOT NULL DEFAULT 0,tickets INT NOT NULL DEFAULT 0) ENGINE=InnoDB`)
	exec(`CREATE TEMPORARY TABLE training_ranks(uid BIGINT PRIMARY KEY,training_rank INT NOT NULL) ENGINE=InnoDB`)
	exec(`CREATE TEMPORARY TABLE training(uid BIGINT PRIMARY KEY,started BIGINT NULL) ENGINE=InnoDB`)
	exec(`CREATE TEMPORARY TABLE training_rules(id INT PRIMARY KEY,revision BIGINT NOT NULL,rules BLOB NOT NULL) ENGINE=InnoDB`)
	exec(`CREATE TEMPORARY TABLE training_claims(uid BIGINT NOT NULL,operation_id VARCHAR(128) NOT NULL,started BIGINT NOT NULL,training_rank INT NOT NULL,revision BIGINT NOT NULL,experience INT NOT NULL,PRIMARY KEY(uid,operation_id),UNIQUE KEY cycle(uid,started)) ENGINE=InnoDB`)
	rules := TrainingRules{Enabled: true}
	for i := uint32(0); i <= 8; i++ {
		rules.Levels = append(rules.Levels, TrainingRule{Level: i, XPPerHour: 100, XPCap: 250})
	}
	saveRules := func() {
		b, e := json.Marshal(rules)
		if e != nil {
			t.Fatal(e)
		}
		exec(`REPLACE INTO training_rules VALUES(1,7,?)`, b)
	}
	saveRules()
	profile := make([]byte, 360)
	protocol.WriteUint16(profile, LevelOffset, 1)
	exec(`INSERT INTO accounts(uid,profile) VALUES(1,?),(2,?)`, profile, profile)
	now := time.Now().Unix()
	exec(`INSERT INTO training VALUES(1,?),(2,?)`, now-7200, now-60)
	s := &Store{DB: db}
	growth := (RewardRules{}).Normalized()
	growth.GrowthEnabled = true
	for i := 0; i < int(MaxRoleLevel)-1; i++ {
		growth.Levels[i].NextExperience = 150
	}
	r, err := s.TrainingManager().ClaimTraining(1, "first", growth)
	if err != nil || r.Replay || r.Experience != 200 || ProfileLevel(r.Profile) != 2 || protocol.ReadUint32(r.Profile, ExperienceOffset) != 50 {
		t.Fatal(r, err)
	}
	retry, err := s.TrainingManager().ClaimTraining(1, "first", growth)
	if err != nil || !retry.Replay || retry.Experience != 200 {
		t.Fatal(retry, err)
	}
	if _, err = s.TrainingManager().ClaimTraining(1, "second", growth); err == nil {
		t.Fatal("claimed idle training")
	}
	if _, err = s.TrainingManager().ClaimTraining(2, "first", growth); err == nil {
		t.Fatal("claimed too early")
	}
	// A new cycle with a duplicate ledger cycle key must roll back XP and reset.
	exec(`UPDATE training SET started=? WHERE uid=1`, now-7200)
	if _, err = s.TrainingManager().ClaimTraining(1, "collision", growth); err == nil {
		t.Fatal("duplicate cycle accepted")
	}
	var current []byte
	var started sql.NullInt64
	if err = db.QueryRow(`SELECT profile FROM accounts WHERE uid=1`).Scan(&current); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT started FROM training WHERE uid=1`).Scan(&started); err != nil {
		t.Fatal(err)
	}
	if protocol.ReadUint32(current, ExperienceOffset) != 50 || !started.Valid {
		t.Fatal("failed receipt did not roll back")
	}
	rules.Enabled = false
	saveRules()
	if _, err = s.TrainingManager().ClaimTraining(1, "disabled", growth); err == nil {
		t.Fatal("disabled reward claimed")
	}
	retry, err = s.TrainingManager().ClaimTraining(1, "first", growth)
	if err != nil || !retry.Replay {
		t.Fatal("successful receipt lost after disable", err)
	}
	rules.Enabled = true
	saveRules()
	exec(`UPDATE training SET started=? WHERE uid=1`, now+3600)
	if _, err = s.TrainingManager().ClaimTraining(1, "future", growth); err == nil {
		t.Fatal("future start accepted")
	}
	exec(`UPDATE training SET started=? WHERE uid=1`, now-10800)
	r, err = s.TrainingManager().ClaimTraining(1, "next-cycle", growth)
	if err != nil || r.Experience != 250 {
		t.Fatal(r, err)
	}
	rank, e := s.TrainingManager().TrainingRank(1)
	if e != nil || rank != 0 {
		t.Fatal(rank, e)
	}
	exec(`INSERT INTO training_ranks VALUES(1,2)`)
	rank, e = s.TrainingManager().TrainingRank(1)
	if e != nil || rank != 2 {
		t.Fatal(rank, e)
	}
	exec(`DELETE FROM training_ranks WHERE uid=1`)
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM training_claims`).Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	for _, uid := range []uint64{0, 99} {
		if _, err = s.TrainingManager().ClaimTraining(uid, "invalid", growth); err == nil {
			t.Fatal("unknown account accepted")
		}
	}
	exec(`INSERT INTO training_ranks VALUES(1,9)`)
	if _, err = s.TrainingManager().ClaimTraining(1, "bad-rank", growth); err == nil {
		t.Fatal("invalid rank accepted")
	}
}
