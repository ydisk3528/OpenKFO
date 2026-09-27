package persistence

import (
	"bytes"
	"fmt"
	"github.com/go-sql-driver/mysql"
	"kungfu.local/server/internal/protocol"
	"os"
	"strings"
	"testing"
	"time"
)

func TestClothingMenuRepairDoesNotChangeItemTypeOrSlot(t *testing.T) {
	for _, kind := range []byte{12, 13, 14, 15, 16, 17, 20, 21} {
		r := make([]byte, 68)
		r[4] = kind
		protocol.WriteUint32(r, 5, 121002)
		protocol.WriteUint16(r, 17, 4)
		protocol.WriteUint16(r, 23, 1)
		protocol.WriteUint32(r, 13, 525600)
		protocol.WriteUint32(r, 19, 1)
		want := bytes.Clone(r)
		protocol.WriteUint16(want, 23, 0)
		normalizeClothingMenu(r)
		if !bytes.Equal(r, want) {
			t.Fatalf("kind %d altered unrelated fields", kind)
		}
		normalizeClothingMenu(r)
		if !bytes.Equal(r, want) {
			t.Fatal("not idempotent")
		}
	}
	for _, kind := range []byte{18, 25, 30, 64, 71, 74} {
		r := make([]byte, 68)
		r[4] = kind
		protocol.WriteUint16(r, 23, 99)
		want := bytes.Clone(r)
		normalizeClothingMenu(r)
		if !bytes.Equal(r, want) {
			t.Fatal("quantity/durability changed", kind)
		}
	}
}
func TestLegacyClothingRemainsVisibleWithoutCount(t *testing.T) {
	r := make([]byte, 68)
	r[4] = 12
	protocol.WriteUint16(r, 23, 1)
	normalizeClothingMenu(r)
	if protocol.ReadUint16(r, 23) != 0 || protocol.ReadUint32(r, 13) != 8760 {
		t.Fatal("legacy clothing hidden")
	}
}

func TestClothingMenuLegacyDatabase(t *testing.T) {
	dsn := os.Getenv("OPENKFO_DEBUG_DSN")
	if dsn == "" {
		t.Skip("independent debug DB required")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil || !strings.HasPrefix(cfg.DBName, "openkfo_debug_") {
		t.Fatal("independent DB required")
	}
	store, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.DB.Close()
	uid := uint64(time.Now().UnixMicro())
	a, err := NewAccountWithStarterCharacter(uid, fmt.Sprintf("cm%d", uid), "test123456")
	if err != nil {
		t.Fatal(err)
	}
	a.Inventory = nil
	for i, kind := range []byte{12, 18, 64, 30} {
		record := make([]byte, 68)
		protocol.WriteUint32(record, 0, uint32(i+1))
		record[4] = kind
		protocol.WriteUint32(record, 5, 121005)
		protocol.WriteUint32(record, 13, 8760)
		protocol.WriteUint16(record, 23, 1)
		a.Inventory = append(a.Inventory, record)
	}
	if err = store.Create(a); err != nil {
		t.Fatal(err)
	}
	defer func() {
		store.DB.Exec(`DELETE FROM inventory WHERE uid=?`, uid)
		store.DB.Exec(`DELETE FROM accounts WHERE uid=?`, uid)
	}()
	snapshot, err := store.RoleManager().Snapshot(uid)
	if err != nil {
		t.Fatal(err)
	}
	for i, record := range snapshot.Inventory {
		want := bytes.Clone(a.Inventory[i])
		if i == 0 {
			protocol.WriteUint16(want, 23, 0)
		}
		if !bytes.Equal(record, want) {
			t.Fatal("unexpected migration", i)
		}
		var saved []byte
		err = store.DB.QueryRow(`SELECT record FROM inventory WHERE uid=? AND instance=?`, uid, i+1).Scan(&saved)
		if err != nil || !bytes.Equal(saved, want) {
			t.Fatal("migration not persisted", err)
		}
	}
}
