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

func TestWeaponSwitchStackMySQL(t *testing.T) {
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
		"CREATE TEMPORARY TABLE accounts(uid BIGINT PRIMARY KEY)", "INSERT INTO accounts VALUES(1)",
		"CREATE TEMPORARY TABLE inventory(uid BIGINT,instance BIGINT,record BLOB,PRIMARY KEY(uid,instance)) ENGINE=InnoDB",
		"CREATE TEMPORARY TABLE inventory_expirations(uid BIGINT,instance BIGINT,expires_at BIGINT,PRIMARY KEY(uid,instance)) ENGINE=InnoDB",
	} {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	card := make([]byte, 68)
	card[4] = protocol.ItemWeaponSwitchCard
	protocol.WriteUint32(card, 5, 743001)
	protocol.WriteUint16(card, 23, 1)
	original := bytes.Clone(card)
	grant := func(count uint16, days uint32, commit, wantError bool) []byte {
		t.Helper()
		tx, e := db.Begin()
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback()
		var uid uint64
		if e = tx.QueryRow("SELECT uid FROM accounts WHERE uid=1 FOR UPDATE").Scan(&uid); e != nil {
			t.Fatal(e)
		}
		input := bytes.Clone(card)
		protocol.WriteUint16(input, 23, count)
		got, e := (InventoryManager{}).AddItem(tx, uid, input, days)
		if (e != nil) != wantError {
			t.Fatalf("unexpected grant result: %v", e)
		}
		if !bytes.Equal(input[4:23], original[4:23]) {
			t.Fatal("template changed")
		}
		if commit && !wantError {
			if e = tx.Commit(); e != nil {
				t.Fatal(e)
			}
		}
		return got
	}
	first := grant(1, 0, true, false)
	second := grant(1, 0, true, false)
	if protocol.ReadUint32(first, 0) != protocol.ReadUint32(second, 0) || protocol.ReadUint16(second, 23) != 2 {
		t.Fatal("second purchase did not merge")
	}
	grant(3, 0, false, false) // Rolled back purchase must not increase the stack.
	grant(998, 0, true, true) // Refuse overflow without losing cards.
	timed := grant(1, 1, true, false)
	if protocol.ReadUint32(timed, 0) == protocol.ReadUint32(first, 0) {
		t.Fatal("timed card merged with permanent stack")
	}
	grant(1, 0, true, false)
	var record []byte
	if err = db.QueryRow("SELECT record FROM inventory WHERE uid=1 AND instance=?", protocol.ReadUint32(first, 0)).Scan(&record); err != nil {
		t.Fatal(err)
	}
	if protocol.ReadUint16(record, 23) != 3 {
		t.Fatal("rollback, overflow or expiry changed total")
	}
	var rows int
	if err = db.QueryRow("SELECT COUNT(*) FROM inventory").Scan(&rows); err != nil || rows != 2 {
		t.Fatal("unexpected inventory rows", rows, err)
	}
}
