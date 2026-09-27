package persistence

import (
	"database/sql"
	"encoding/json"
	"github.com/go-sql-driver/mysql"
	"kungfu.local/server/internal/protocol"
	"os"
	"strings"
	"testing"
)

func TestTitleProgressLocalDatabase(t *testing.T) {
	dsn := os.Getenv("OPENKFO_DEBUG_DSN")
	if dsn == "" {
		t.Skip("independent debug DB required")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil || !strings.HasPrefix(cfg.DBName, "openkfo_debug_") {
		t.Fatal("refusing non-debug DB")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, e := db.Exec("CREATE TEMPORARY TABLE item_definitions(definition_key INT PRIMARY KEY,revision BIGINT,record BLOB,days INT) ENGINE=InnoDB"); e != nil {
		t.Fatal(e)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := db.Exec(q, args...); e != nil {
			t.Fatal(e)
		}
	}
	for _, q := range []string{
		`CREATE TEMPORARY TABLE accounts(uid BIGINT PRIMARY KEY,profile BLOB) ENGINE=InnoDB`,
		`CREATE TEMPORARY TABLE title_rewards(uid BIGINT,title_level TINYINT UNSIGNED,choices BLOB,claimed_key INT UNSIGNED NULL,claimed_instance INT UNSIGNED NULL,PRIMARY KEY(uid,title_level)) ENGINE=InnoDB`,
		`CREATE TEMPORARY TABLE offers(catalog_key INT PRIMARY KEY,record BLOB,grant_record BLOB,enabled BOOL) ENGINE=InnoDB`,
		`CREATE TEMPORARY TABLE offer_lifetimes(catalog_key INT PRIMARY KEY,days INT UNSIGNED) ENGINE=InnoDB`,
		`CREATE TEMPORARY TABLE inventory(uid BIGINT,instance INT UNSIGNED,record BLOB,PRIMARY KEY(uid,instance)) ENGINE=InnoDB`,
		`CREATE TEMPORARY TABLE inventory_expirations(uid BIGINT,instance INT UNSIGNED,expires_at BIGINT,PRIMARY KEY(uid,instance)) ENGINE=InnoDB`,
	} {
		exec(q)
	}
	for _, uid := range []int{1, 2} {
		exec("INSERT INTO accounts VALUES(?,?)", uid, make([]byte, 360))
	}
	for _, key := range []uint32{7, 8} {
		catalog, item := make([]byte, 108), make([]byte, 68)
		catalog[4], item[4] = 25, 25
		protocol.WriteUint32(catalog, 0, key)
		protocol.WriteUint32(catalog, 9, key)
		protocol.WriteUint32(catalog, 5, 250001)
		protocol.WriteUint32(item, 5, 250001)
		protocol.WriteUint32(item, 13, 24)
		exec("INSERT INTO offers VALUES(?,?,?,TRUE)", key, catalog, item)
		exec("INSERT INTO offer_lifetimes VALUES(?,1)", key)
		exec(seedDefinitionsSQL)
	}
	s := &Store{DB: db}

	exec(`CREATE TEMPORARY TABLE title_rules(id INT PRIMARY KEY,revision BIGINT,rules BLOB) ENGINE=InnoDB`)
	exec(`CREATE TEMPORARY TABLE task_progress(uid BIGINT,task_key INT,state INT) ENGINE=InnoDB`)
	rules := TitleRules{Enabled: true, Titles: []TitleRule{
		{Level: 2, Enabled: true, MinPlayerLevel: 1, Choices: []uint32{8}},
		{Level: 1, Enabled: true, MinPlayerLevel: 5, Matches: 10, Wins: 3, CompletedTask: 1001, Choices: []uint32{7}},
	}}
	save := func() { data, _ := json.Marshal(rules); exec("REPLACE INTO title_rules VALUES(1,1,?)", data) }
	save()
	profile := make([]byte, 360)
	protocol.WriteUint16(profile, LevelOffset, 5)
	protocol.WriteUint32(profile, 133, 10)
	protocol.WriteUint32(profile, 137, 3)
	exec("UPDATE accounts SET profile=? WHERE uid=1", profile)
	advance := func(want bool, supported []byte) {
		t.Helper()
		got, e := s.TitleManager().AdvanceTitle(1, supported, "")
		if e != nil || got != want {
			t.Fatal(got, e)
		}
	}
	advance(false, []byte{1, 2}) // Required task absent: must not skip to easier title 2.
	exec("INSERT INTO task_progress VALUES(2,1001,3),(1,1001,2)")
	advance(false, []byte{1, 2}) // Other account completion is irrelevant.
	exec("UPDATE task_progress SET state=3 WHERE uid=1")
	advance(false, []byte{2}) // Unsupported next title must not be skipped.
	rules.Enabled = false
	save()
	advance(false, []byte{1, 2})
	rules.Enabled = true
	save()
	var catalog []byte
	if e := db.QueryRow("SELECT record FROM offers WHERE catalog_key=7").Scan(&catalog); e != nil {
		t.Fatal(e)
	}
	conflict := append([]byte(nil), catalog...)
	protocol.WriteUint32(conflict, 5, 999999)
	exec("UPDATE offers SET record=? WHERE catalog_key=7", conflict)
	if _, e := s.TitleManager().AdvanceTitle(1, []byte{1, 2}, ""); e == nil {
		t.Fatal("unusable display catalogue consumed title advancement")
	}
	exec("UPDATE offers SET record=? WHERE catalog_key=7", catalog)
	exec("UPDATE offers SET enabled=FALSE WHERE catalog_key=7")
	// Reward definitions are independent of shop availability. A missing
	// definition must still fail atomically; an unsold reward remains valid.
	exec("DELETE FROM item_definitions WHERE definition_key=7")
	if _, e := s.TitleManager().AdvanceTitle(1, []byte{1, 2}, ""); e == nil {
		t.Fatal("missing reward definition accepted")
	}
	var unchanged []byte
	if e := db.QueryRow("SELECT profile FROM accounts WHERE uid=1").Scan(&unchanged); e != nil || unchanged[TitleLevelOffset] != 0 {
		t.Fatal("failed grant changed title", e)
	}
	exec(seedDefinitionsSQL)
	advance(true, []byte{1, 2})
	advance(false, []byte{1, 2})
	rules.Titles[1].Choices = []uint32{8}
	save()
	level, choices, e := s.TitleManager().PendingTitleReward(1)
	if e != nil || level != 1 || len(choices) != 1 || choices[0] != 7 {
		t.Fatal("pending choices changed", e)
	}
	if _, e = s.TitleManager().ClaimTitleReward(1, 1, 7); e != nil {
		t.Fatal(e)
	}
	advance(true, []byte{1, 2})
	level, choices, e = s.TitleManager().PendingTitleReward(1)
	if e != nil || level != 2 || len(choices) != 1 || choices[0] != 8 {
		t.Fatal("next title missing", e)
	}
	rules = TitleRules{Enabled: true, Titles: []TitleRule{
		{Level: 1, Enabled: true, MinPlayerLevel: 1},
		{Level: 2, Enabled: true, MinPlayerLevel: 1},
		{Level: 3, Enabled: true, MinPlayerLevel: 10},
	}}
	save()
	for i, want := range []bool{true, false} {
		changed, err := s.TitleManager().AdvanceTitle(2, []byte{1, 2, 3}, "")
		if err != nil || changed != want {
			t.Fatalf("no reward advance %d: %v %v", i, changed, err)
		}
	}
	var updatedProfile []byte
	if err := db.QueryRow("SELECT profile FROM accounts WHERE uid=2").Scan(&updatedProfile); err != nil || updatedProfile[TitleLevelOffset] != 2 {
		t.Fatal("no reward title missing", err)
	}
	var offers int
	if err := db.QueryRow("SELECT COUNT(*) FROM title_rewards WHERE uid=2").Scan(&offers); err != nil || offers != 0 {
		t.Fatal("empty reward offer created", err)
	}

}

func TestTitleCountersMet(t *testing.T) {
	p := make([]byte, 360)
	if !titleCountersMet(p, TitleRule{MinPlayerLevel: 1}) {
		t.Fatal("new account level zero must mean level one")
	}
	protocol.WriteUint16(p, LevelOffset, 201)
	if titleCountersMet(p, TitleRule{MinPlayerLevel: 1}) {
		t.Fatal("corrupt level accepted")
	}
	protocol.WriteUint16(p, LevelOffset, 5)
	protocol.WriteUint32(p, 133, 6)
	protocol.WriteUint32(p, 137, 2)
	protocol.WriteUint32(p, 141, 4)
	protocol.WriteUint32(p, 145, 1)
	r := TitleRule{MinPlayerLevel: 5, Matches: 10, Wins: 3}
	if !titleCountersMet(p, r) {
		t.Fatal("combined modes not counted")
	}
	if titleCountersMet(p[:359], r) {
		t.Fatal("short profile")
	}
	r.Matches = 11
	if titleCountersMet(p, r) {
		t.Fatal("insufficient matches")
	}
	r.Matches = 10
	r.Wins = 4
	if titleCountersMet(p, r) {
		t.Fatal("insufficient wins")
	}
	r.Wins = 3
	r.MinPlayerLevel = 6
	if titleCountersMet(p, r) {
		t.Fatal("insufficient level")
	}
	r.MinPlayerLevel = 5
	protocol.WriteUint32(p, 137, 7)
	if titleCountersMet(p, r) {
		t.Fatal("corrupt wins")
	}
}
