package persistence

import (
	"bytes"
	"fmt"
	"kungfu.local/server/internal/protocol"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCharacterCreationLocalDatabase(t *testing.T) {
	dsn := os.Getenv("OPENKFO_DEBUG_DSN")
	if dsn == "" {
		t.Skip("independent local debug database required")
	}
	store, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.DB.Close()
	var name string
	if err = store.DB.QueryRow("SELECT DATABASE()").Scan(&name); err != nil || !strings.HasPrefix(name, "openkfo_debug_") {
		t.Fatal("refusing non-debug database")
	}
	base := uint64(time.Now().UnixMicro())
	var uids []uint64
	defer func() {
		for _, uid := range uids {
			for _, table := range []string{"character_creations", "inventory", "accounts"} {
				if _, err := store.DB.Exec("DELETE FROM "+table+" WHERE uid=?", uid); err != nil {
					t.Error(err)
				}
			}
		}
	}()
	for i := uint64(0); i < 3; i++ {
		a, err := NewAccountWithStarterCharacter(base+i, fmt.Sprintf("cr%d", base+i), "test123456")
		if err != nil {
			t.Fatal(err)
		}
		if i < 2 {
			a.Profile = make([]byte, 360)
			a.Inventory = nil
		}
		if err = store.Create(a); err != nil {
			t.Fatal(err)
		}
		uids = append(uids, a.UID)
	}
	p, c := characterFixture()
	clear(p[:21])
	copy(p, []byte(fmt.Sprintf("r%d", base)))
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := store.RoleManager().CreateCharacter(base, p, c); results <- err }()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal("identical retry", err)
		}
	}
	a, err := store.RoleManager().Snapshot(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Inventory) != 7 || a.Profile[122] != 2 || a.Profile[124] != 13 {
		t.Fatal("incomplete committed character")
	}
	var count int
	if err = store.DB.QueryRow("SELECT COUNT(*) FROM character_creations WHERE uid=?", base).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate receipt")
	}
	changed := bytes.Clone(p)
	changed[22] = 14
	if _, err = store.RoleManager().CreateCharacter(base, changed, c); err == nil {
		t.Fatal("changed retry overwrote character")
	}
	if _, err = store.RoleManager().CreateCharacter(base+1, p, c); err == nil {
		t.Fatal("duplicate nickname")
	}
	rejected, err := store.RoleManager().Snapshot(base + 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(rejected.Inventory) != 0 || protocol.ReadUint32(rejected.Profile, 0) != 0 {
		t.Fatal("rejected create left partial state")
	}
	clear(p[:21])
	copy(p, []byte(fmt.Sprintf("r%d", base+2)))
	before, err := store.RoleManager().Snapshot(base + 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.RoleManager().CreateCharacter(base+2, p, c); err == nil {
		t.Fatal("existing character overwritten")
	}
	after, err := store.RoleManager().Snapshot(base + 2)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before.Profile, after.Profile) || !bytes.Equal(before.InventoryBytes(), after.InventoryBytes()) {
		t.Fatal("existing character changed")
	}
}
