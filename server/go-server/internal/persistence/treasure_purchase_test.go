package persistence

import (
	"fmt"
	"github.com/go-sql-driver/mysql"
	"kungfu.local/server/internal/protocol"
	"os"
	"strings"
	"testing"
)

func TestTreasurePurchaseSpecsMySQL(t *testing.T) {
	dsn := os.Getenv("OPENKFO_DEBUG_DSN")
	if dsn == "" {
		t.Skip("independent database required")
	}
	cfg, e := mysql.ParseDSN(dsn)
	if e != nil || !strings.HasPrefix(cfg.DBName, "openkfo_debug_cardspec_") {
		t.Fatal("isolated cardspec database required")
	}
	s, e := Open(dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	uid := uint64(10002)
	a, e := NewAccountWithStarterCharacter(uid, "treasurespectest", "test123456")
	if e != nil {
		t.Fatal(e)
	}
	a.Tickets = 50000
	if e = s.Create(a); e != nil {
		t.Fatal(e)
	}
	total := uint16(0)
	for i, n := range []uint16{1, 10, 50, 100} {
		o := adminFixtureOffer(753001)
		o.Key += uint32(i)
		o.Category = 10
		o.Variant = 75
		o.Record[4] = 75
		o.Record[48] = 1
		o.Grant[4] = 75
		protocol.WriteUint32(o.Record, 0, o.Key)
		protocol.WriteUint32(o.Record, 9, o.Key)
		protocol.WriteUint32(o.Record, 26, uint32(n)*200)
		protocol.WriteUint32(o.Record, 38, uint32(n)*200)
		protocol.WriteUint32(o.Record, 42, uint32(n)*200)
		protocol.WriteUint32(o.Grant, 13, 0)
		protocol.WriteUint16(o.Grant, 23, n)
		if _, e = s.DB.Exec("INSERT INTO offers(catalog_key,category,variant,record,grant_record,enabled) VALUES(?,?,?,?,?,TRUE)", o.Key, o.Category, o.Variant, o.Record, o.Grant); e != nil {
			t.Fatal(e)
		}
		req := make([]byte, 169)
		protocol.WriteUint32(req, 0, 109)
		protocol.WriteUint64(req, 4, uid)
		protocol.WriteUint64(req, 54, uid)
		protocol.WriteUint32(req, 145, o.Key)
		protocol.WriteUint32(req, 157, uint32(n)*200)
		id := fmt.Sprintf("treasure-spec-%d", n)
		balance, item, _, e := s.ShopManager().Purchase(uid, id, req)
		total += n
		if e != nil || balance != 50000-uint32(total)*200 || protocol.ReadUint16(item, 23) != n {
			t.Fatalf("spec %d balance %d error %v", n, balance, e)
		}
		retry, again, _, e := s.ShopManager().Purchase(uid, id, req)
		if e != nil || retry != balance || protocol.ReadUint16(again, 23) != n {
			t.Fatal("retry duplicated purchase", e)
		}
	}
}
