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

func TestCapeRecommendationMySQL(t *testing.T) {
	dsn := os.Getenv("OPENKFO_DEBUG_DSN")
	if dsn == "" {
		t.Skip("independent debug database required")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil || !strings.HasPrefix(cfg.DBName, "openkfo_debug_") {
		t.Fatal("independent database required")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, q := range []string{
		"CREATE TEMPORARY TABLE offers(catalog_key BIGINT PRIMARY KEY,category INT,variant INT,record BLOB,grant_record BLOB,enabled BOOL)",
		"CREATE TEMPORARY TABLE offer_recommendations(catalog_key BIGINT PRIMARY KEY,enabled BOOL)",
		"CREATE TEMPORARY TABLE offer_recommendation_order(catalog_key BIGINT PRIMARY KEY,priority INT)",
	} {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	var expected [][]byte
	for n, kind := range []byte{25, 21, 21, 21, 31} {
		o := adminFixtureOffer(uint32(n + 1))
		o.Variant = kind
		o.Record[4] = kind
		o.Grant[4] = kind
		recommended := n != 2
		o.Recommended = &recommended
		if kind != 31 {
			if err = validateAdminOffer(o); err != nil {
				t.Fatal(err)
			}
		}
		if _, err = db.Exec("INSERT INTO offers VALUES(?,?,?,?,?,?)", o.Key, o.Category, kind, o.Record, o.Grant, n != 3); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec("INSERT INTO offer_recommendations VALUES(?,?)", o.Key, recommended); err != nil {
			t.Fatal(err)
		}
		if n < 2 {
			expected = append(expected, o.Record)
		}
	}
	store := &Store{DB: db}
	rows, err := store.ShopManager().Offers(protocol.ShopCategoryRecommended, 25)
	if err != nil || len(rows) != 2 {
		t.Fatalf("wrong recommendation membership: %d %v", len(rows), err)
	}
	for n, r := range rows {
		if !bytes.Equal(r.Record, expected[n]) {
			t.Fatal("recommendation rewrote purchase fields")
		}
	}
	if _, err = db.Exec("INSERT INTO offer_recommendation_order VALUES(?,100),(?,200)", adminFixtureOffer(2).Key, adminFixtureOffer(3).Key); err != nil {
		t.Fatal(err)
	}
	rows, err = store.ShopManager().Offers(protocol.ShopCategoryRecommended, 25)
	if err != nil || len(rows) != 2 || rows[0].Key != adminFixtureOffer(2).Key || rows[1].Key != adminFixtureOffer(1).Key {
		t.Fatal("recommendation priority or membership incorrect", rows, err)
	}
	if !bytes.Equal(rows[0].Record, expected[1]) || !bytes.Equal(rows[1].Record, expected[0]) {
		t.Fatal("priority changed item purchase data")
	}
	rows, err = store.ShopManager().Offers(protocol.ShopCategoryRecommended, 21)
	if err != nil || len(rows) != 1 || rows[0].Grant[4] != 21 {
		t.Fatal("back accessory query lost type filter", err)
	}
}
