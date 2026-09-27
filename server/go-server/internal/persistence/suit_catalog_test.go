package persistence

import (
	"encoding/json"
	"fmt"
	"github.com/go-sql-driver/mysql"
	"kungfu.local/server/internal/protocol"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAllConfiguredSuitsMySQL(t *testing.T) {
	dsn := os.Getenv("OPENKFO_DEBUG_DSN")
	if dsn == "" {
		t.Skip("independent debug database required")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil || !strings.HasPrefix(cfg.DBName, "openkfo_debug_") {
		t.Fatal("independent debug database required")
	}
	store, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.DB.Close()
	raw, err := os.ReadFile("../../../../config/suit-bundles.example.json")
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Bundles map[uint32][]uint32 `json:"suit_bundles"`
	}
	if err = json.Unmarshal(raw, &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog.Bundles) != 42 {
		t.Fatal("expected 42 ordinary suits")
	}
	for item, parts := range catalog.Bundles {
		for _, timed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/timed=%t", item, timed), func(t *testing.T) {
				uid := uint64(time.Now().UnixNano())
				account, e := NewAccountWithStarterCharacter(uid, fmt.Sprintf("s%d", uid), "test123456")
				if e != nil {
					t.Fatal(e)
				}
				source := make([]byte, protocol.InventoryRecordSize)
				protocol.WriteUint32(source, 0, 1)
				source[4] = protocol.ItemSuit
				protocol.WriteUint32(source, 5, item)
				protocol.WriteUint32(source, inventoryDurationOffset, 365*24)
				account.Inventory = [][]byte{source}
				if e = store.Create(account); e != nil {
					t.Fatal(e)
				}
				defer func() {
					store.DB.Exec("DELETE FROM inventory_expirations WHERE uid=?", uid)
					store.DB.Exec("DELETE FROM inventory WHERE uid=?", uid)
					store.DB.Exec("DELETE FROM accounts WHERE uid=?", uid)
				}()
				deadline := time.Now().Unix() + 86400
				if timed {
					if _, e = store.DB.Exec("INSERT INTO inventory_expirations(uid,instance,expires_at) VALUES(?,?,?)", uid, 1, deadline); e != nil {
						t.Fatal(e)
					}
				}
				granted, e := store.EquipmentManager().OpenSuit(uid, 1, catalog.Bundles)
				if e != nil {
					t.Fatal(e)
				}
				if len(granted) != len(parts) {
					t.Fatal("missing parts")
				}
				for i, record := range granted {
					if protocol.ReadUint32(record, 5) != parts[i] || protocol.ReadUint16(record, 17) == 0 {
						t.Fatal("incorrect part or equipment slot")
					}
					minutes := protocol.ReadUint32(record, inventoryDurationOffset)
					if (!timed && minutes != permanentDisplayMinutes) || (timed && (minutes < 1439 || minutes > 1440)) {
						t.Fatalf("wrong duration %d", minutes)
					}
					var count int
					if e = store.DB.QueryRow("SELECT COUNT(*) FROM inventory_expirations WHERE uid=? AND instance=? AND expires_at=?", uid, protocol.ReadUint32(record, 0), deadline).Scan(&count); e != nil {
						t.Fatal(e)
					}
					if (timed && count != 1) || (!timed && count != 0) {
						t.Fatal("expiry not inherited")
					}
				}
				if e = store.DB.QueryRow("SELECT record FROM inventory WHERE uid=? AND instance=1", uid).Scan(&source); e != nil {
					t.Fatal(e)
				}
				if usableItem(source) {
					t.Fatal("package still usable")
				}
				if _, e = store.EquipmentManager().OpenSuit(uid, 1, catalog.Bundles); e == nil {
					t.Fatal("duplicate grant on replay")
				}
			})
		}
	}
}
