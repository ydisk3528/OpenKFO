package persistence

import (
	"bytes"
	"os"
	"reflect"
	"testing"

	"kungfu.local/server/internal/protocol"
)

func shelfFixture(key uint32, category, kind byte, enabled bool) shelfOffer {
	o := adminFixtureOffer(key)
	o.Key, o.Category, o.Variant = key, category, kind
	o.Record[4], o.Grant[4] = kind, kind
	protocol.WriteUint32(o.Record, 0, key)
	protocol.WriteUint32(o.Record, 9, key)
	return shelfOffer{o.Offer, enabled}
}

func TestCompatibleShelvesKeepSaleRecords(t *testing.T) {
	for _, selector := range [][2]int{{252, 25}, {253, 25}, {10, 67}, {67, 67}, {19, 19}, {10, 30}} {
		category, variant := selector[0], selector[1]
		kinds := compatibleShelfKinds(category, variant)
		var source []shelfOffer
		for i, kind := range kinds {
			source = append(source, shelfFixture(uint32(i+1), 10, kind, true))
		}
		disabled := shelfFixture(99, 10, kinds[0], false)
		source = append(source, disabled)
		before := make([][]byte, len(source))
		for i := range source {
			before[i] = bytes.Clone(source[i].Record)
		}
		got := arrangeCompatibleShelf(category, variant, kinds, source)
		if len(got) != len(kinds) {
			t.Fatalf("%v: got %d rows", selector, len(got))
		}
		for _, o := range got {
			original := source[o.Key-1]
			if !reflect.DeepEqual(o, original.Offer) {
				t.Fatal("alias changed sale record")
			}
		}
		for i := range source {
			if !bytes.Equal(source[i].Record, before[i]) {
				t.Fatal("source mutated")
			}
		}
		if category == 10 && variant == 30 {
			continue
		}
		// Even an entirely disabled explicit shelf must suppress fallback.
		explicit := shelfFixture(100, byte(category), byte(variant), false)
		source = append(source, explicit)
		if got := arrangeCompatibleShelf(category, variant, kinds, source); len(got) != 0 {
			t.Fatal("disabled shelf resurrected")
		}
		source[len(source)-1].enabled = true
		if got := arrangeCompatibleShelf(category, variant, kinds, source); len(got) != 1 || got[0].Key != 100 {
			t.Fatal("explicit shelf changed")
		}
	}
	if compatibleShelfKinds(255, 25) != nil || compatibleShelfKinds(-1, 0) != nil || compatibleShelfKinds(10, 25) != nil {
		t.Fatal("existing selector intercepted")
	}
}

func TestCompatibleGroupOrderingAndDecorDedup(t *testing.T) {
	var source []shelfOffer
	for _, kind := range []byte{31, 64, 77, 20, 21, 79} {
		for i := 0; i < 18; i++ {
			source = append(source, shelfFixture(uint32(len(source)+1), 10, kind, true))
		}
	}
	got := arrangeCompatibleShelf(67, 67, compatibleShelfKinds(67, 67), source)
	if len(got) != len(source) {
		t.Fatal("overflow lost")
	}
	for i := 0; i < 48; i++ {
		if got[i].Grant[4] != []byte{64, 77, 31}[i/16] {
			t.Fatal("group order incorrect")
		}
	}
	decor := shelfFixture(200, 10, 20, true)
	explicit := decor
	explicit.Key, explicit.Variant, explicit.enabled = 201, 30, false
	got = arrangeCompatibleShelf(10, 30, compatibleShelfKinds(10, 30), []shelfOffer{explicit, decor})
	if len(got) != 0 {
		t.Fatal("disabled explicit accessory resurrected through alias")
	}
}

func TestCardShelfIncludesWeaponSwitchCard(t *testing.T) {
	source := []shelfOffer{shelfFixture(1, 10, 74, true), shelfFixture(2, 10, 74, false)}
	protocol.WriteUint32(source[0].Grant, 5, 743001)
	got := arrangeCompatibleShelf(67, 67, compatibleShelfKinds(67, 67), source)
	if len(got) != 1 || !reflect.DeepEqual(got[0], source[0].Offer) {
		t.Fatal("card shelf lost or changed weapon switch sale")
	}
}

// Opt in to a disposable database; only connection-local temporary tables are used.
func TestCompatibleShelvesMySQL(t *testing.T) {
	dsn := os.Getenv("KK_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("isolated MySQL required")
	}
	s, err := OpenExisting(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	s.DB.SetMaxOpenConns(1)
	s.DB.SetMaxIdleConns(1)
	s.DB.SetConnMaxLifetime(0)
	var name string
	if err = s.DB.QueryRow("SELECT DATABASE()").Scan(&name); err != nil || name != "kungfu_game_test" {
		t.Fatal("isolated database required")
	}
	for _, ddl := range []string{
		`CREATE TEMPORARY TABLE offers(catalog_key INT UNSIGNED PRIMARY KEY,category TINYINT UNSIGNED,variant TINYINT UNSIGNED,record VARBINARY(108),grant_record VARBINARY(68),enabled BOOLEAN)`,
		`CREATE TEMPORARY TABLE offer_recommendations(catalog_key INT UNSIGNED PRIMARY KEY,enabled BOOLEAN)`,
		`CREATE TEMPORARY TABLE offer_recommendation_order(catalog_key BIGINT PRIMARY KEY,priority INT)`,
		`CREATE TEMPORARY TABLE offer_lifetimes(catalog_key INT UNSIGNED PRIMARY KEY,days INT UNSIGNED)`,
	} {
		if _, err = s.DB.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	insert := func(o shelfOffer) {
		t.Helper()
		if _, err := s.DB.Exec(`INSERT INTO offers VALUES(?,?,?,?,?,?)`, o.Key, o.Category, o.Variant, o.Record, o.Grant, o.enabled); err != nil {
			t.Fatal(err)
		}
	}
	weapon := shelfFixture(1, 10, 25, true)
	insert(weapon)
	insert(shelfFixture(2, 10, 25, false))
	if _, err = s.DB.Exec(`INSERT INTO offer_lifetimes VALUES(1,7)`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`INSERT INTO offer_recommendations VALUES(1,TRUE)`); err != nil {
		t.Fatal(err)
	}
	for _, category := range []int{10, 252, 253, 255} {
		got, err := s.ShopManager().Offers(category, 25)
		if err != nil || len(got) != 1 || !reflect.DeepEqual(got[0], weapon.Offer) {
			t.Fatalf("category %d: %v %v", category, got, err)
		}
	}
	insert(shelfFixture(3, 252, 25, false))
	if got, err := s.ShopManager().Offers(252, 25); err != nil || len(got) != 0 {
		t.Fatal("explicit disabled shelf", got, err)
	}
	if _, err = s.DB.Exec(`UPDATE offers SET enabled=FALSE WHERE catalog_key=1`); err != nil {
		t.Fatal(err)
	}
	if got, err := s.ShopManager().Offers(253, 25); err != nil || len(got) != 0 {
		t.Fatal("disabled canonical offer", got, err)
	}
	var count, days int
	if err = s.DB.QueryRow(`SELECT COUNT(*) FROM offers`).Scan(&count); err != nil || count != 3 {
		t.Fatal("read created duplicates", count, err)
	}
	if err = s.DB.QueryRow(`SELECT days FROM offer_lifetimes WHERE catalog_key=1`).Scan(&days); err != nil || days != 7 {
		t.Fatal("expiry policy changed", days, err)
	}
}

func TestTreasureTicketsOnItemsShelf(t *testing.T) {
	kinds := compatibleShelfKinds(67, 67)
	source := []shelfOffer{shelfFixture(1, 10, 75, true), shelfFixture(2, 10, 76, true)}
	got := arrangeCompatibleShelf(67, 67, kinds, source)
	if len(got) != 2 || got[0].Grant[4] != 75 || got[1].Grant[4] != 76 {
		t.Fatal("treasure tickets missing", got)
	}
}
