package persistence

import (
	"bytes"
	"database/sql"
	"github.com/go-sql-driver/mysql"
	"kungfu.local/server/internal/protocol"
	"os"
	"strings"
	"testing"
)

func TestLevelGiftsIndependentDefinitionsLocalDatabase(t *testing.T) {
	dsn := os.Getenv("OPENKFO_DEBUG_DSN")
	if dsn == "" {
		t.Skip("independent debug DB required")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil || !strings.HasPrefix(cfg.DBName, "openkfo_debug_") {
		t.Fatal("not debug database")
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
		if _, e := db.Exec(q, args...); e != nil {
			t.Fatal(e)
		}
	}
	for _, q := range []string{
		"CREATE TEMPORARY TABLE accounts(uid BIGINT PRIMARY KEY,profile BLOB,gold INT DEFAULT 10,tickets INT DEFAULT 20)",
		"CREATE TEMPORARY TABLE offers(catalog_key INT PRIMARY KEY,grant_record BLOB,enabled BOOL)",
		"CREATE TEMPORARY TABLE offer_lifetimes(catalog_key INT PRIMARY KEY,days INT)",
		"CREATE TEMPORARY TABLE item_definitions(definition_key INT PRIMARY KEY,revision BIGINT,record BLOB,days INT) ENGINE=InnoDB",
		"CREATE TEMPORARY TABLE level_reward_receipts(uid BIGINT,level INT,items BLOB,PRIMARY KEY(uid,level)) ENGINE=InnoDB",
		"CREATE TEMPORARY TABLE inventory(uid BIGINT,instance INT UNSIGNED,record BLOB,PRIMARY KEY(uid,instance)) ENGINE=InnoDB",
		"CREATE TEMPORARY TABLE inventory_expirations(uid BIGINT,instance INT UNSIGNED,expires_at BIGINT,PRIMARY KEY(uid,instance)) ENGINE=InnoDB",
		"CREATE TEMPORARY TABLE battle_reward_rules(id INT PRIMARY KEY,revision BIGINT,rules BLOB) ENGINE=InnoDB",
	} {
		exec(q)
	}
	template := make([]byte, protocol.InventoryRecordSize)
	template[4] = protocol.ItemWeapon
	protocol.WriteUint32(template, 5, 250001)
	protocol.WriteUint32(template, 13, 24)
	exec("INSERT INTO offers VALUES(7,?,FALSE)", template)
	exec("INSERT INTO offer_lifetimes VALUES(7,1)")
	exec(seedDefinitionsSQL)
	exec("UPDATE offers SET grant_record=?", make([]byte, 68))
	exec("UPDATE offer_lifetimes SET days=99")
	exec(seedDefinitionsSQL)
	exec("DELETE FROM offers")
	exec("DELETE FROM offer_lifetimes")
	store := &Store{DB: db}
	defs, e := store.ItemManager().Definitions()
	if e != nil || len(defs) != 1 || defs[0].Days != 1 || !bytes.Equal(defs[0].Record, template) {
		t.Fatal("shop mutated definition", defs, e)
	}
	profile := make([]byte, protocol.RoleProfileSize)
	if e = (RoleManager{}).SetLevel(profile, 1, 90); e != nil {
		t.Fatal(e)
	}
	original := bytes.Clone(profile)
	exec("INSERT INTO accounts(uid,profile) VALUES(1,?)", profile)
	rules := RewardRules{GrowthEnabled: true, LevelGifts: []LevelGift{{Level: 2, Items: []uint32{7}, Gold: 20, Tickets: 10}, {Level: 3, Items: []uint32{8}, Gold: 30, Tickets: 15}}}.Normalized()
	for i := 0; i < int(MaxRoleLevel)-1; i++ {
		rules.Levels[i].NextExperience = 100
	}
	grant := func() (items [][]byte, e error) {
		tx, e := db.Begin()
		if e != nil {
			return nil, e
		}
		defer tx.Rollback()
		var uid, balance uint64
		if e = tx.QueryRow("SELECT uid,gold FROM accounts WHERE uid=1 FOR UPDATE").Scan(&uid, &balance); e != nil {
			return nil, e
		}
		var gold uint32
		gold, items, e = (RewardManager{}).GrantProgressItems(tx, uid, profile, balance, 120, 0, rules)
		if e != nil {
			return nil, e
		}
		if _, e = tx.Exec("UPDATE accounts SET profile=?,gold=? WHERE uid=1", profile, gold); e != nil {
			return nil, e
		}
		return items, tx.Commit()
	}
	if _, e = grant(); e == nil || !bytes.Equal(profile, original) {
		t.Fatal("missing second gift should fail without profile mutation")
	}
	for _, table := range []string{"inventory", "inventory_expirations", "level_reward_receipts"} {
		var n int
		if e = db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); e != nil || n != 0 {
			t.Fatal("partial reward committed", table, n, e)
		}
	}
	d := ItemDefinition{Key: 8, Record: template, Days: 2}
	saved, e := store.ItemManager().SaveDefinition(d)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = store.ItemManager().SaveDefinition(d); e == nil {
		t.Fatal("stale definition create overwrote record")
	}
	if saved.Revision != 1 {
		t.Fatal(saved)
	}
	items, e := grant()
	if e != nil || len(items) != 2 || ProfileLevel(profile) != 3 || protocol.ReadUint32(profile, ExperienceOffset) != 10 {
		t.Fatal("cross-level rewards", len(items), e)
	}
	if protocol.ReadUint32(items[0], 0) == protocol.ReadUint32(items[1], 0) {
		t.Fatal("duplicate instance")
	}
	profile = bytes.Clone(original) // Explicit level correction cannot repeat gifts.
	items, e = grant()
	if e != nil || len(items) != 0 {
		t.Fatal("level gifts repeated", len(items), e)
	}
	gold, tickets, e := store.WalletManager().Balances(1)
	if e != nil || gold != 60 || tickets != 45 {
		t.Fatal("level currencies repeated or lost", gold, tickets, e)
	}
	var n int
	if e = db.QueryRow("SELECT COUNT(*) FROM inventory_expirations").Scan(&n); e != nil || n != 2 {
		t.Fatal(n, e)
	}
	settings, e := store.RewardManager().SaveBattleRewards(0, rules)
	if e != nil {
		t.Fatal(e)
	}
	oldClient := rules
	oldClient.LevelGifts = nil
	settings, e = store.RewardManager().SaveBattleRewards(settings.Revision, oldClient)
	if e != nil || len(settings.Rules.LevelGifts) != 2 {
		t.Fatal("old GM erased gifts", e)
	}
}
func TestLevelGiftRules(t *testing.T) {
	for _, gifts := range [][]LevelGift{{{Level: 1, Items: []uint32{7}}}, {{Level: 201, Items: []uint32{7}}}, {{Level: 2, Items: []uint32{0}}}, {{Level: 2, Items: []uint32{7}}, {Level: 2, Items: []uint32{8}}}} {
		if validateLevelGifts(gifts) == nil {
			t.Fatal("invalid gift accepted", gifts)
		}
	}
}
