package persistence

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"kungfu.local/server/internal/protocol"
)

// Run only on a fresh disposable database created for this test. Ordinary
// database integration tests use temporary tables and can share another DB.
func TestShopSharedReadsLocalDatabase(t *testing.T) {
	dsn := os.Getenv("OPENKFO_SHOP_CONCURRENCY_DSN")
	if dsn == "" {
		t.Skip("fresh independent database required")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil || !strings.HasPrefix(cfg.DBName, "openkfo_debug_shop_locks_") {
		t.Fatal("unsafe database")
	}
	s, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.DB.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, uid := range []uint64{1, 2} {
		name := "buyerone"
		if uid == 2 {
			name = "buyertwo"
		}
		a, err := NewAccountWithStarterCharacter(uid, name, "test123456")
		if err != nil {
			t.Fatal(err)
		}
		a.Inventory = nil
		a.Tickets = 1000
		if err = s.Create(a); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO vip_shop_rules(id,revision,rules) VALUES(1,1,'{"enabled":false,"silver":0,"gold":0,"platinum":0}')`)
	o := adminFixtureOffer(7)
	o.Record[48] = 1
	exec("INSERT INTO offers(catalog_key,category,variant,record,grant_record,enabled) VALUES(?,?,?,?,?,TRUE)", o.Key, o.Category, o.Variant, o.Record, o.Grant)
	exec("INSERT INTO offer_lifetimes(catalog_key,days) VALUES(?,1)", o.Key)
	held, cancel, err := beginTransaction(s.DB)
	defer cancel()
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback()
	var id uint64
	if err = held.QueryRow("SELECT uid FROM accounts WHERE uid=1 FOR UPDATE").Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err = vipShopPercentTx(held, 1); err != nil {
		t.Fatal(err)
	}
	var b []byte
	var days int
	if err = held.QueryRow("SELECT record FROM offers WHERE catalog_key=? LOCK IN SHARE MODE", o.Key).Scan(&b); err != nil {
		t.Fatal(err)
	}
	if err = held.QueryRow("SELECT days FROM offer_lifetimes WHERE catalog_key=? LOCK IN SHARE MODE", o.Key).Scan(&days); err != nil {
		t.Fatal(err)
	}
	// Merely viewing the shop must not wait for this account's writer.
	viewed := make(chan error, 1)
	go func() { _, e := s.ShopManager().VIPShopPercent(1); viewed <- e }()
	select {
	case err = <-viewed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("display blocked on account/config lock")
	}
	p := make([]byte, 169)
	protocol.WriteUint32(p, 0, 109)
	protocol.WriteUint64(p, 4, 2)
	protocol.WriteUint64(p, 54, 2)
	protocol.WriteUint32(p, 145, o.Key)
	protocol.WriteUint32(p, 157, 77)
	done := make(chan error, 1)
	go func() {
		balance, _, _, e := s.ShopManager().Purchase(2, "parallel", p)
		if e == nil && balance != 923 {
			e = ErrDenied
		}
		done <- e
	}()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("another buyer blocked by shared configuration")
	}
	// GM writers must still wait until transactions release the read locks.
	for _, q := range []string{"UPDATE vip_shop_rules SET revision=revision+1 WHERE id=1", "UPDATE offers SET enabled=FALSE WHERE catalog_key=?", "UPDATE offer_lifetimes SET days=2 WHERE catalog_key=?"} {
		ctx, stop := context.WithTimeout(context.Background(), 150*time.Millisecond)
		var args []any
		if strings.Contains(q, "?") {
			args = []any{o.Key}
		}
		_, e := s.DB.ExecContext(ctx, q, args...)
		stop()
		if e == nil {
			t.Fatalf("writer bypassed reader: %s", q)
		}
	}
	if err = held.Commit(); err != nil {
		t.Fatal(err)
	}
	if balance, _, _, e := s.ShopManager().Purchase(2, "parallel", p); e != nil || balance != 923 {
		t.Fatal("duplicate charged", balance, e)
	}
	var receipts int
	if err = s.DB.QueryRow("SELECT COUNT(*) FROM purchases WHERE uid=2").Scan(&receipts); err != nil || receipts != 1 {
		t.Fatal(receipts, err)
	}
	exec("UPDATE offers SET enabled=FALSE WHERE catalog_key=?", o.Key)
	if _, _, _, e := s.ShopManager().Purchase(2, "disabled", p); e == nil {
		t.Fatal("disabled offer sold")
	}
	var tickets int
	if err = s.DB.QueryRow("SELECT tickets FROM accounts WHERE uid=2").Scan(&tickets); err != nil || tickets != 923 {
		t.Fatal(tickets, err)
	}
}
