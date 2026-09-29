package persistence

import (
	"database/sql"
	"github.com/go-sql-driver/mysql"
	"os"
	"strings"
	"testing"
)

func TestTreasureValidation(t *testing.T) {
	valid := func() TreasureSettings {
		return TreasureSettings{Pools: []TreasurePool{{Name: "百宝", TicketKind: 75, Cost: 1, Prizes: []TreasurePrize{{Weight: 2, RewardBundle: RewardBundle{Items: []uint32{1}, Gold: 20, Tickets: 30}}}}}}
	}
	if err := valid().Validate(); err != nil {
		t.Fatal(err)
	}
	for _, edit := range []func(*TreasureSettings){
		func(a *TreasureSettings) { a.Pools[0].Prizes[0].Weight = 0 },
		func(a *TreasureSettings) { a.Pools[0].Cost = 0 },
		func(a *TreasureSettings) { a.Pools[0].TicketKind = 74 },
		func(a *TreasureSettings) { a.Pools = append(a.Pools, a.Pools[0]) },
		func(a *TreasureSettings) { a.Pools[0].Prizes[0].RewardBundle = RewardBundle{} },
		func(a *TreasureSettings) { a.Pools[0].Prizes[0].Tickets = 1000001 },
	} {
		a := valid()
		edit(&a)
		if a.Validate() == nil {
			t.Fatal("invalid settings accepted")
		}
	}
	if _, err := (&Store{}).Admin(AdminRequest{Operation: "treasure_save"}); err == nil {
		t.Fatal("missing payload")
	}
}
func TestTreasureSettingsDatabase(t *testing.T) {
	dsn := os.Getenv("OPENKFO_DEBUG_DSN")
	if dsn == "" {
		t.Skip("isolated debug DB required")
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
	for _, q := range []string{
		"CREATE TEMPORARY TABLE treasure_rules(id INT PRIMARY KEY,revision BIGINT,rules MEDIUMBLOB) ENGINE=InnoDB",
		"CREATE TEMPORARY TABLE treasure_rules_audit(revision BIGINT PRIMARY KEY,before_data MEDIUMBLOB,after_data MEDIUMBLOB) ENGINE=InnoDB",
		"CREATE TEMPORARY TABLE item_definitions(definition_key INT PRIMARY KEY,record BLOB,days INT) ENGINE=InnoDB",
	} {
		if _, e := db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	s := &Store{DB: db}
	a, err := s.TreasureSettings()
	if err != nil || len(a.Pools) != 2 {
		t.Fatal(a, err)
	}
	a.Pools[0].Prizes = []TreasurePrize{{Weight: 3, RewardBundle: RewardBundle{Tickets: 50}}, {Weight: 1, RewardBundle: RewardBundle{Gold: 100}}}
	b, err := s.SaveTreasureSettings(a)
	if err != nil || b.Revision != 1 {
		t.Fatal(b, err)
	}
	if _, err = s.SaveTreasureSettings(a); err == nil {
		t.Fatal("stale write accepted")
	}
	a = b
	a.Pools[0].Prizes[0].Items = []uint32{123}
	if _, err = s.SaveTreasureSettings(a); err == nil {
		t.Fatal("missing definition accepted")
	}
	got, err := s.TreasureSettings()
	if err != nil || got.Revision != 1 || len(got.Pools[0].Prizes[0].Items) != 0 {
		t.Fatal(got, err)
	}
	var n int
	if err = db.QueryRow("SELECT COUNT(*) FROM treasure_rules_audit").Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
}

func TestTreasureSixAndWeights(t *testing.T) {
	p := TreasurePool{Name: "百宝", TicketKind: 75, Cost: 1}
	for i := 1; i <= 20; i++ {
		p.Prizes = append(p.Prizes, TreasurePrize{Weight: uint32(i), RewardBundle: RewardBundle{Tickets: uint32(i)}})
	}
	for run := 0; run < 20; run++ {
		six, err := p.RefreshSix()
		if err != nil || len(six) != 6 {
			t.Fatal(six, err)
		}
		seen := map[uint32]bool{}
		for _, v := range six {
			if seen[v.Tickets] {
				t.Fatal("duplicate entry")
			}
			seen[v.Tickets] = true
		}
		win, err := SelectTreasureWinner(six)
		if err != nil || win < 0 || win >= 6 {
			t.Fatal(win, err)
		}
	}
	// Exhaust every ticket boundary: probabilities depend only on these six.
	group := p.Prizes[:6]
	counts := make([]int, 6)
	for ticket := uint64(0); ticket < 21; ticket++ {
		counts[treasureWinnerAt(group, ticket)]++
	}
	for i, n := range counts {
		if n != i+1 {
			t.Fatal(counts)
		}
	}
	if _, err := (TreasurePool{Name: "百宝", TicketKind: 75, Cost: 1, Prizes: p.Prizes[:5]}).RefreshSix(); err == nil {
		t.Fatal("short pool accepted")
	}
	if _, err := SelectTreasureWinner(p.Prizes); err == nil {
		t.Fatal("full pool used instead of displayed group")
	}
	if p.Prizes[0].Tickets != 1 {
		t.Fatal("refresh changed source pool")
	}
}
