package persistence

import (
	"fmt"
	"github.com/go-sql-driver/mysql"
	"kungfu.local/server/internal/protocol"
	"math/bits"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestTreasureDrawMySQL(t *testing.T) {
	dsn := os.Getenv("OPENKFO_DEBUG_DSN")
	if dsn == "" {
		t.Skip("isolated database required")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil || !strings.HasPrefix(cfg.DBName, "openkfo_debug_treasure_draw_") {
		t.Fatal("isolated treasure database required")
	}
	s, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	uid := uint64(987654)
	a, err := NewAccountWithStarterCharacter(uid, "treasuredrawtest", "test123456")
	if err != nil {
		t.Fatal(err)
	}
	a.Gold = 100
	a.Tickets = 100
	if err = s.Create(a); err != nil {
		t.Fatal(err)
	}
	card := make([]byte, 68)
	card[4] = 75
	protocol.WriteUint32(card, 0, 123)
	protocol.WriteUint32(card, 5, 753001)
	protocol.WriteUint16(card, 23, 10)
	if _, err = s.DB.Exec("INSERT INTO inventory(uid,instance,record) VALUES(?,?,?)", uid, 123, card); err != nil {
		t.Fatal(err)
	}
	request := make([]byte, 30)
	request[0] = 1
	for i := 0; i < 6; i++ {
		protocol.WriteUint32(request, 2+i*4, uint32(i+1))
	}
	group := make([]TreasurePrize, 6)
	for i := range group {
		group[i] = TreasurePrize{Weight: 1, RewardBundle: RewardBundle{Tickets: 20}}
	}
	in := TreasureDrawInput{Operation: "one", Kind: 75, Cost: 1, Group: group, Request: request}
	var wg sync.WaitGroup
	results := make(chan TreasureDrawResult, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); r, e := s.DrawTreasure(uid, in); results <- r; errs <- e }()
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	fresh := 0
	winner := -1
	for r := range results {
		if !r.Replay {
			fresh++
		}
		if winner == -1 {
			winner = r.Winner
		}
		if winner != r.Winner {
			t.Fatal("retry rerolled")
		}
	}
	if fresh != 1 {
		t.Fatal("duplicate awards", fresh)
	}
	check := func(count, tickets uint32) {
		t.Helper()
		var raw []byte
		var balance uint32
		if e := s.DB.QueryRow("SELECT record FROM inventory WHERE uid=? AND instance=123", uid).Scan(&raw); e != nil {
			t.Fatal(e)
		}
		if e := s.DB.QueryRow("SELECT tickets FROM accounts WHERE uid=?", uid).Scan(&balance); e != nil {
			t.Fatal(e)
		}
		if uint32(protocol.ReadUint16(raw, 23)) != count || balance != tickets {
			t.Fatal("assets", count, balance)
		}
	}
	check(9, 120)
	in.Request = append([]byte(nil), request...)
	in.Request[0] = 2
	if _, e := s.DrawTreasure(uid, in); e == nil {
		t.Fatal("changed duplicate accepted")
	}
	check(9, 120)
	in.Operation = "insufficient"
	in.Cost = 10
	if _, e := s.DrawTreasure(uid, in); e == nil {
		t.Fatal("insufficient accepted")
	}
	check(9, 120)
	in.Operation = "invalid-item"
	in.Cost = 1
	for i := range group {
		group[i].RewardBundle = RewardBundle{Items: []uint32{42}}
	}
	if _, e := s.DrawTreasure(uid, in); e == nil {
		t.Fatal("missing definition accepted")
	}
	check(9, 120)
	item := make([]byte, 68)
	item[4] = 25
	protocol.WriteUint32(item, 5, 253037)
	protocol.WriteUint32(item, 13, 168)
	in.Definitions = []ItemDefinition{{Key: 42, Record: item, Days: 7}}
	catalog, err := s.RewardManager().RewardCatalog(in.Definitions)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for i := 0; i < len(catalog); i += 108 {
		if protocol.ReadUint32(catalog, i+9) == 42 {
			found = catalog[i+4] == 25 && protocol.ReadUint32(catalog, i+5) == 253037
		}
	}
	if !found {
		t.Fatal("missing native item display catalog")
	}
	in.Operation = "weapon"
	if _, e := s.DrawTreasure(uid, in); e != nil {
		t.Fatal(e)
	}
	check(8, 120)
	var expiry int
	if e := s.DB.QueryRow("SELECT COUNT(*) FROM inventory_expirations WHERE uid=? AND expires_at>UNIX_TIMESTAMP()+6*86400", uid).Scan(&expiry); e != nil || expiry != 1 {
		t.Fatal("weapon lifetime", expiry, e)
	}
	for i := range group {
		group[i].RewardBundle = RewardBundle{Gold: 25}
	}
	in.Operation = "gold"
	if _, e := s.DrawTreasure(uid, in); e != nil {
		t.Fatal(e)
	}
	check(7, 120)
	var gold uint32
	s.DB.QueryRow("SELECT gold FROM accounts WHERE uid=?", uid).Scan(&gold)
	if gold != 125 {
		t.Fatal("gold", gold)
	}
	for i := range group {
		group[i].RewardBundle = RewardBundle{Tickets: 1000000}
	}
	s.DB.Exec("UPDATE accounts SET tickets=2147483647 WHERE uid=?", uid)
	in.Operation = "overflow"
	if _, e := s.DrawTreasure(uid, in); e == nil {
		t.Fatal("overflow accepted")
	}
	check(7, 2147483647)
	var receipts int
	s.DB.QueryRow("SELECT COUNT(*) FROM treasure_draws WHERE uid=?", uid).Scan(&receipts)
	if receipts != 3 {
		t.Fatal(fmt.Sprint("unexpected receipts ", receipts))
	}
	protocol.WriteUint16(card, 23, 50)
	if _, err = s.DB.Exec("UPDATE inventory SET record=? WHERE uid=? AND instance=123", card, uid); err != nil {
		t.Fatal(err)
	}
	s.DB.Exec("UPDATE accounts SET tickets=100 WHERE uid=?", uid)
	for i := range group {
		group[i].RewardBundle = RewardBundle{Gold: 25}
	}
	in.Operation = "three-cards"
	in.Cost = 1
	in.Request = append([]byte(nil), request...)
	mask := uint32(0)
	for i := 0; i < 3; i++ {
		in.Request[1] = byte(i)
		r, e := s.DrawTreasure(uid, in)
		if e != nil {
			t.Fatal(i, e)
		}
		if mask&(1<<r.Winner) != 0 {
			t.Fatal("same prize awarded twice")
		}
		mask |= 1 << r.Winner
		again, e := s.DrawTreasure(uid, in)
		if e != nil || !again.Replay || again.Winner != r.Winner {
			t.Fatal("consecutive replay", e)
		}
	}
	if bits.OnesCount32(mask) != 3 {
		t.Fatal("three unique prizes missing")
	}
	check(44, 100)
	in.Request[1] = 3
	if _, err := s.DrawTreasure(uid, in); err == nil {
		t.Fatal("fourth ordinary draw accepted")
	}
	check(44, 100)

	// Festival uses its own tickets and permits all six cards, unlike ordinary.
	card[4] = 76
	protocol.WriteUint32(card, 5, 763001)
	protocol.WriteUint16(card, 23, 50)
	if _, err := s.DB.Exec("UPDATE inventory SET record=? WHERE uid=? AND instance=123", card, uid); err != nil {
		t.Fatal(err)
	}
	in.Kind = 76
	in.Operation = "festival-six"
	mask = 0
	for i := 0; i < 6; i++ {
		in.Request[1] = byte(i)
		r, err := s.DrawTreasure(uid, in)
		if err != nil {
			t.Fatal("festival draw", i, err)
		}
		if mask&(1<<r.Winner) != 0 {
			t.Fatal("festival duplicate prize")
		}
		mask |= 1 << r.Winner
		replay, err := s.DrawTreasure(uid, in)
		if err != nil || !replay.Replay {
			t.Fatal("festival replay", err)
		}
	}
	if mask != 63 {
		t.Fatal("festival six prizes missing")
	}
	check(29, 100)

}
