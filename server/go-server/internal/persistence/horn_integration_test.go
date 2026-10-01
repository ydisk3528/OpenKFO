package persistence

import (
	"errors"
	"fmt"
	"github.com/go-sql-driver/mysql"
	"kungfu.local/server/internal/protocol"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHornDatabaseAtomicDebit(t *testing.T) {
	dsn := os.Getenv("OPENKFO_DEBUG_DSN")
	if dsn == "" {
		t.Skip("independent debug DB required")
	}
	c, e := mysql.ParseDSN(dsn)
	if e != nil || !strings.HasPrefix(c.DBName, "openkfo_debug_") {
		t.Fatal("refusing non-debug DB")
	}
	s, e := Open(dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	uid := uint64(time.Now().UnixMicro())
	a, e := NewAccountWithStarterCharacter(uid, fmt.Sprintf("horn%d", uid), "test123456")
	if e != nil {
		t.Fatal(e)
	}
	a.Inventory = [][]byte{hornCard(1, 10)}
	if e = s.Create(a); e != nil {
		t.Fatal(e)
	}
	defer func() {
		for _, table := range []string{"horn_events", "inventory_expirations", "inventory", "accounts"} {
			if _, e := s.DB.Exec("DELETE FROM "+table+" WHERE uid=?", uid); e != nil {
				t.Error(e)
			}
		}
	}()
	rules, e := s.HornSettings()
	if e != nil {
		t.Fatal(e)
	}
	original := rules
	defer func() {
		current, err := s.HornSettings()
		if err == nil {
			original.Revision = current.Revision
			_, err = s.SaveHornSettings(original)
		}
		if err != nil {
			t.Error(err)
		}
	}()
	rules.ChannelEnabled = true
	rules.RealmEnabled = false
	rules.MoodEnabled = true
	saved, e := s.SaveHornSettings(rules)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.SaveHornSettings(rules); e == nil {
		t.Fatal("stale configuration accepted")
	}
	if _, e = s.InventoryManager().ConsumeHorn(uid, 2486, fmt.Sprintf("%032x", uid), "关闭测试"); !errors.Is(e, ErrHornDisabled) {
		t.Fatal(e)
	}
	var untouched []byte
	if e = s.DB.QueryRow("SELECT record FROM inventory WHERE uid=? AND instance=1", uid).Scan(&untouched); e != nil || protocol.ReadUint16(untouched, 23) != 10 {
		t.Fatal("disabled horn charged", e)
	}
	saved.RealmEnabled = true
	if _, e = s.SaveHornSettings(saved); e != nil {
		t.Fatal(e)
	}
	m := s.InventoryManager()
	id := fmt.Sprintf("%032x", uid)
	if _, e = m.ConsumeHorn(uid, 2486, id, "全区测试"); e != nil {
		t.Fatal(e)
	}
	if _, e = m.ConsumeHorn(uid, 2486, id, "重复提交"); e == nil {
		t.Fatal("duplicate debit accepted")
	}
	var ok atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, e := m.ConsumeHorn(uid, 2480, fmt.Sprintf("%032x", uid+uint64(i)+1), "并发测试")
			if e == nil {
				ok.Add(1)
			} else if !errors.Is(e, ErrHornCards) {
				t.Error(e)
			}
		}(i)
	}
	wg.Wait()
	if ok.Load() != 7 {
		t.Fatal("overspend/underspend", ok.Load())
	}
	var record []byte
	if e = s.DB.QueryRow("SELECT record FROM inventory WHERE uid=? AND instance=1", uid).Scan(&record); e != nil {
		t.Fatal(e)
	}
	if protocol.ReadUint16(record, 23) != 0 {
		t.Fatal("count", record)
	}
	var count, cost int
	if e = s.DB.QueryRow("SELECT COUNT(*),SUM(cost) FROM horn_events WHERE uid=?", uid).Scan(&count, &cost); e != nil || count != 8 || cost != 10 {
		t.Fatal(count, cost, e)
	}
}
