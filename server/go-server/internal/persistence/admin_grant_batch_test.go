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

func TestGrantBatchResumeAtomicity(t *testing.T) {
	dsn := os.Getenv("OPENKFO_DEBUG_DSN")
	if dsn == "" {
		t.Skip("isolated debug DB required")
	}
	c, e := mysql.ParseDSN(dsn)
	if e != nil || !strings.HasPrefix(c.DBName, "openkfo_debug_") {
		t.Fatal("debug database required")
	}
	db, e := sql.Open("mysql", dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	for _, q := range []string{
		`CREATE TEMPORARY TABLE accounts(uid BIGINT UNSIGNED PRIMARY KEY,account VARCHAR(255),tickets BIGINT UNSIGNED NOT NULL DEFAULT 0)`,
		`INSERT INTO accounts(uid,account) VALUES(1,'one'),(2,'two')`,
		`CREATE TEMPORARY TABLE inventory(uid BIGINT UNSIGNED,instance BIGINT UNSIGNED,record BLOB,PRIMARY KEY(uid,instance)) ENGINE=InnoDB`,
		`CREATE TEMPORARY TABLE inventory_expirations(uid BIGINT UNSIGNED,instance BIGINT UNSIGNED,expires_at BIGINT,PRIMARY KEY(uid,instance)) ENGINE=InnoDB`,
		`CREATE TEMPORARY TABLE gm_grant_batches(id VARCHAR(64) CHARACTER SET ascii PRIMARY KEY,request_hash BINARY(32),items LONGBLOB,created TIMESTAMP DEFAULT CURRENT_TIMESTAMP) ENGINE=InnoDB`,
		`CREATE TEMPORARY TABLE gm_grant_recipients(batch_id VARCHAR(64) CHARACTER SET ascii,uid BIGINT UNSIGNED,account VARCHAR(255),state VARCHAR(16) DEFAULT 'pending',detail TEXT,delivered LONGBLOB,updated TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,PRIMARY KEY(batch_id,uid)) ENGINE=InnoDB`,
	} {
		if _, e = db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	s := &Store{DB: db}
	record := func(kind byte, id uint32, n uint16) []byte {
		b := make([]byte, 68)
		b[4] = kind
		protocol.WriteUint32(b, 5, id)
		protocol.WriteUint16(b, 23, n)
		return b
	}
	request := AdminRequest{Operation: "grant_batch_create", ID: "test-batch", All: true, BatchItems: []BatchGrantItem{
		{Key: "currency:ticket", Name: "点券", Quantity: 50000},
		{Key: "25:253013", Name: "weapon", Quantity: 2, Days: 365, Record: record(25, 253013, 0)},
		{Key: "74:743001", Name: "card", Quantity: 88, Days: 365, Stackable: true, Record: record(74, 743001, 88)},
	}}
	if _, e = s.adminGrantBatch(request); e != nil {
		t.Fatal(e)
	}
	// Creating the same batch after a new registration must retain its original audience.
	db.Exec(`INSERT INTO accounts(uid,account) VALUES(3,'later')`)
	if _, e = s.adminGrantBatch(request); e != nil {
		t.Fatal(e)
	}
	changed := request
	changed.All = false
	changed.UIDs = []uint64{3}
	if _, e = s.adminGrantBatch(changed); e == nil {
		t.Fatal("batch mutated")
	}
	for n := 0; n < 2; n++ {
		if _, e = s.adminGrantBatch(AdminRequest{Operation: "grant_batch_send_many", ID: request.ID, UIDs: []uint64{1}}); e != nil {
			t.Fatal(e)
		}
	}
	var balance int
	if e := db.QueryRow(`SELECT tickets FROM accounts WHERE uid=1`).Scan(&balance); e != nil || balance != 50000 {
		t.Fatalf("duplicate tickets: %d %v", balance, e)
	}
	var count int
	db.QueryRow(`SELECT COUNT(*) FROM inventory WHERE uid=1`).Scan(&count)
	if count != 3 {
		t.Fatal("replay duplicated or weapon quantity wrong", count)
	}
	full := record(74, 743001, 950)
	protocol.WriteUint32(full, 0, 1048576)
	db.Exec(`INSERT INTO inventory VALUES(2,1048576,?)`, full)
	if _, e = s.adminGrantBatch(AdminRequest{Operation: "grant_batch_send_many", ID: request.ID, UIDs: []uint64{1, 2}}); e != nil {
		t.Fatal(e)
	}
	db.QueryRow(`SELECT COUNT(*) FROM inventory WHERE uid=2`).Scan(&count)
	if count != 1 {
		t.Fatal("partial weapons committed despite card overflow")
	}
	var state string
	db.QueryRow(`SELECT state FROM gm_grant_recipients WHERE uid=2`).Scan(&state)
	if state != "failed" {
		t.Fatal("failure not persisted")
	}
	if e := db.QueryRow(`SELECT tickets FROM accounts WHERE uid=2`).Scan(&balance); e != nil || balance != 0 {
		t.Fatalf("partial tickets committed: %d %v", balance, e)
	}
	protocol.WriteUint16(full, 23, 900)
	db.Exec(`UPDATE inventory SET record=? WHERE uid=2`, full)
	if _, e = s.adminGrantBatch(AdminRequest{Operation: "grant_batch_send_many", ID: request.ID, UIDs: []uint64{1, 2}}); e != nil {
		t.Fatal(e)
	}
	out, e := s.grantBatchStatus(request.ID)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(out)
	var status struct{ Total, Success, Failed int }
	json.Unmarshal(raw, &status)
	if status.Total != 2 || status.Success != 2 || status.Failed != 0 {
		t.Fatal(string(raw))
	}
	db.QueryRow(`SELECT COUNT(*) FROM inventory WHERE uid=2`).Scan(&count)
	if count != 3 {
		t.Fatal("resume lost or duplicated items")
	}
	if e := db.QueryRow(`SELECT tickets FROM accounts WHERE uid=2`).Scan(&balance); e != nil || balance != 50000 {
		t.Fatalf("resume tickets: %d %v", balance, e)
	}
	cash := AdminRequest{Operation: "grant_batch_create", ID: "cash-only", UIDs: []uint64{3}, BatchItems: []BatchGrantItem{{Key: "currency:ticket", Name: "点券", Quantity: 123}}}
	if _, e := s.adminGrantBatch(cash); e != nil {
		t.Fatal(e)
	}
	for n := 0; n < 2; n++ {
		if e := s.sendGrantBatch(cash.ID, 3); e != nil {
			t.Fatal(e)
		}
	}
	if e := db.QueryRow(`SELECT tickets FROM accounts WHERE uid=3`).Scan(&balance); e != nil || balance != 123 {
		t.Fatalf("cash-only replay: %d %v", balance, e)
	}
}

func TestBatchTicketValidation(t *testing.T) {
	for _, n := range []int{0, -1, 2147483648} {
		if validateBatchItems([]BatchGrantItem{{Key: "currency:ticket", Quantity: n}}) == nil {
			t.Fatalf("accepted %d", n)
		}
	}
	ticket := BatchGrantItem{Key: "currency:ticket", Quantity: 50000}
	if err := validateBatchItems([]BatchGrantItem{ticket}); err != nil {
		t.Fatal(err)
	}
	if validateBatchItems([]BatchGrantItem{ticket, ticket}) == nil {
		t.Fatal("duplicate tickets accepted")
	}
}
