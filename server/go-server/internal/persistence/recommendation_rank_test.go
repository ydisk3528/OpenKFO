package persistence

import (
	"bytes"
	"fmt"
	"github.com/go-sql-driver/mysql"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRecommendationRankAdminMySQL(t *testing.T) {
	dsn := os.Getenv("OPENKFO_DEBUG_DSN")
	if dsn == "" {
		t.Skip("isolated database required")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil || !strings.HasPrefix(cfg.DBName, "openkfo_debug_") {
		t.Fatal("isolated database required")
	}
	store, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.DB.Close()
	prefix := fmt.Sprintf("rank-%d", time.Now().UnixNano())
	offer := adminFixtureOffer(999981)
	defer func() {
		for _, table := range []string{"offer_recommendation_order", "offer_recommendations", "offer_lifetimes", "offers"} {
			store.DB.Exec("DELETE FROM "+table+" WHERE catalog_key=?", offer.Key)
		}
		store.DB.Exec("DELETE FROM item_definitions WHERE definition_key=?", offer.Key)
		store.DB.Exec("DELETE FROM desktop_admin_operations WHERE id LIKE ?", prefix+"%")
	}()
	if _, err = store.Admin(AdminRequest{Operation: "shop_save", ID: prefix + "-save", Offers: []AdminOffer{offer}}); err != nil {
		t.Fatal(err)
	}
	req := AdminRequest{Operation: "shop_rank", ID: prefix + "-pin", Keys: []string{"25:999981"}, PinRecommended: true}
	first, err := store.Admin(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Admin(req); err != nil {
		t.Fatal("retry failed", err)
	}
	rows, err := store.ShopManager().Offers(255, 25)
	if err != nil || len(rows) == 0 || rows[0].Key != offer.Key {
		t.Fatal("pin failed", err, first)
	}
	if !bytes.Equal(rows[0].Record, offer.Record) {
		t.Fatal("price/duration changed")
	}
	if _, err = store.Admin(AdminRequest{Operation: "shop_rank", ID: prefix + "-reset", Keys: req.Keys, RecommendationPriority: 0}); err != nil {
		t.Fatal(err)
	}
	var priority int
	store.DB.QueryRow("SELECT priority FROM offer_recommendation_order WHERE catalog_key=?", offer.Key).Scan(&priority)
	if priority != 0 {
		t.Fatal("reset failed")
	}
	if _, err = store.Admin(AdminRequest{Operation: "shop_rank", ID: prefix + "-bad", Keys: req.Keys, RecommendationPriority: -1}); err == nil {
		t.Fatal("negative priority accepted")
	}
}
