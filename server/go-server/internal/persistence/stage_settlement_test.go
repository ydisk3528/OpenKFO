package persistence

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
	"kungfu.local/server/internal/protocol"
)

func TestStageSettlementLocalDatabase(t *testing.T) {
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
	for _, q := range []string{
		`CREATE TEMPORARY TABLE accounts(uid BIGINT PRIMARY KEY,profile BLOB,gold INT UNSIGNED,tickets INT UNSIGNED) ENGINE=InnoDB`,
		`CREATE TEMPORARY TABLE counters(name VARCHAR(32) PRIMARY KEY,value BIGINT UNSIGNED) ENGINE=InnoDB`,
		`CREATE TEMPORARY TABLE battle_settlements(serial INT PRIMARY KEY,reports BLOB,result BLOB) ENGINE=InnoDB`,
		`CREATE TEMPORARY TABLE level_reward_receipts(uid BIGINT,level INT,items BLOB,PRIMARY KEY(uid,level)) ENGINE=InnoDB`,
		`CREATE TEMPORARY TABLE item_definitions(definition_key INT PRIMARY KEY,revision BIGINT,record BLOB,days INT) ENGINE=InnoDB`,
		`CREATE TEMPORARY TABLE inventory(uid BIGINT,instance INT UNSIGNED,record BLOB,PRIMARY KEY(uid,instance)) ENGINE=InnoDB`,
		`CREATE TEMPORARY TABLE inventory_expirations(uid BIGINT,instance INT UNSIGNED,expires_at BIGINT,PRIMARY KEY(uid,instance)) ENGINE=InnoDB`,
	} {
		exec(q)
	}
	exec("INSERT INTO counters VALUES('battle',10)")
	item := make([]byte, protocol.InventoryRecordSize)
	item[4] = protocol.ItemWeapon
	protocol.WriteUint32(item, 5, 253001)
	protocol.WriteUint16(item, 23, 1)
	exec("INSERT INTO item_definitions VALUES(12,1,?,1)", item)
	p := make([]byte, 360)
	protocol.WriteUint16(p, LevelOffset, 1)
	protocol.WriteUint32(p, ExperienceOffset, 90)
	exec("INSERT INTO accounts VALUES(1,?,10,0),(2,?,20,0)", p, p)
	m := (&Store{DB: db}).BattleManager()
	rules := RewardRules{}.Normalized()
	rules.GrowthEnabled = true
	for i := 0; i < int(MaxRoleLevel)-1; i++ {
		rules.Levels[i].NextExperience = 100
	}
	rules.StageRewards = []StageMapRewards{{MapID: 20051, Clear: StageReward{RewardBundle: RewardBundle{Gold: 7, Tickets: 2, Items: []uint32{12}}, Experience: 20}, Failed: StageReward{RewardBundle: RewardBundle{Gold: 1}}}}
	rules.LevelGifts = []LevelGift{{Level: 2, Gold: 5, Tickets: 3}}
	awards := []BattleReward{{UID: 2, Outcome: StageOutcomeClear, Gold: 7, Experience: 20}, {UID: 1, Outcome: StageOutcomeClear, Gold: 7, Experience: 20}}
	got, err := m.SettleStage(1, 20051, []byte("verified stage reports"), awards, rules)
	if err != nil {
		t.Fatal(err)
	}
	if awards[0].UID != 2 || awards[0].BattleMode != nil {
		t.Fatal("caller input mutated")
	}
	for _, r := range got {
		if len(r.Items) != 1 || protocol.ReadUint32(r.Items[0], 5) != 253001 {
			t.Fatal("stage item missing")
		}
		if r.Outcome != StageOutcomeClear || r.BattleMode == nil || *r.BattleMode != byte(protocol.StageAssault) || ProfileLevel(r.Profile) != 2 || protocol.ReadUint32(r.Profile, ExperienceOffset) != 10 || r.GoldBalance != uint32(r.UID)*10+12 {
			t.Fatal("stage progression wrong", r)
		}
		// No competitive counters in the profile may change.
		if !bytes.Equal(r.Profile[133:165], p[133:165]) {
			t.Fatal("competitive counters changed")
		}
		var tickets uint32
		if err := db.QueryRow("SELECT tickets FROM accounts WHERE uid=?", r.UID).Scan(&tickets); err != nil || tickets != 5 {
			t.Fatal("level gift missing", err, tickets)
		}
	}
	awards[0].Gold = 999
	rules.StageRewards[0].Clear.Gold = 999
	again, err := m.SettleStage(1, 20051, nil, awards, rules)
	if err != nil || !reflect.DeepEqual(got, again) {
		t.Fatal("replay did not return original settlement", err)
	}
	removed := rules
	removed.StageRewards = nil
	if replay, err := m.SettleStage(1, 20051, nil, awards, removed); err != nil || !reflect.DeepEqual(got, replay) {
		t.Fatal("removed configuration invalidated receipt", err)
	}
	if _, err = m.SettleStage(1, 20051, nil, awards[:1], rules); err == nil {
		t.Fatal("wrong party accepted replay")
	}
	if _, err = m.SettleBattle(1, nil, []BattleReward{{UID: 1, Outcome: "win"}}); err == nil {
		t.Fatal("stage receipt returned to PvP")
	}
	// Seed a competitive receipt to verify the reverse boundary without touching tasks.
	exec(`INSERT INTO battle_settlements VALUES(2,'[]','[{"uid":1,"outcome":"win"}]')`)
	if _, err = m.SettleStage(2, 20051, nil, awards, rules); err == nil {
		t.Fatal("PvP receipt returned to stage")
	}
	// A later participant's overflow must roll back the earlier account update.
	exec("UPDATE accounts SET gold=4294967295 WHERE uid=2")
	if _, err = m.SettleStage(3, 20051, nil, []BattleReward{{UID: 1, Outcome: StageOutcomeFailed, Gold: 1}, {UID: 2, Outcome: StageOutcomeFailed, Gold: 0xffffffff}}, rules); err == nil {
		t.Fatal("overflow accepted")
	}
	var gold, count int
	if err = db.QueryRow("SELECT gold FROM accounts WHERE uid=1").Scan(&gold); err != nil || gold != 22 {
		t.Fatal("partial stage reward committed", gold, err)
	}
	if err = db.QueryRow("SELECT COUNT(*) FROM battle_settlements WHERE serial=3").Scan(&count); err != nil || count != 0 {
		t.Fatal("failed stage receipt persisted", count, err)
	}
	t.Run("older GM preserves stage configuration", func(t *testing.T) {
		exec(`CREATE TEMPORARY TABLE battle_reward_rules(id INT PRIMARY KEY,revision BIGINT,rules BLOB) ENGINE=InnoDB`)
		data, err := json.Marshal(rules)
		if err != nil {
			t.Fatal(err)
		}
		exec("INSERT INTO battle_reward_rules VALUES(1,1,?)", data)
		legacy := RewardRules{LevelGifts: []LevelGift{}, Tutorial: &RewardBundle{}}.Normalized()
		rm := (&Store{DB: db}).RewardManager()
		saved, err := rm.SaveBattleRewards(1, legacy)
		if err != nil || len(saved.Rules.StageRewards) != 1 {
			t.Fatal("old GM erased stage rewards", err)
		}
		legacy.StageRewards = []StageMapRewards{}
		cleared, err := rm.SaveBattleRewards(saved.Revision, legacy)
		if err != nil || len(cleared.Rules.StageRewards) != 0 {
			t.Fatal("explicit empty stage rules not saved", err)
		}
	})
}

func TestStageSettlementRejectsCompetitiveInputs(t *testing.T) {
	m := (&Store{}).BattleManager() // Rejections must occur before DB access.
	for _, r := range []BattleReward{{Outcome: "win"}, {Outcome: StageOutcomeClear, HonourPeriod: 1}, {Outcome: StageOutcomeClear, HonourPoints: 1}} {
		if _, err := m.SettleStage(1, 20051, nil, []BattleReward{r}, RewardRules{}); err == nil {
			t.Fatal("competitive input accepted")
		}
	}
	for _, mode := range []protocol.RoomType{protocol.StageAssault, protocol.FosterMode} {
		v := byte(mode)
		if _, err := m.SettleBattle(1, nil, []BattleReward{{BattleMode: &v, UID: 1, Outcome: "win"}}); err == nil {
			t.Fatal("PVE accepted competitive settlement")
		}
	}
}
