package persistence

import (
	"fmt"
	"kungfu.local/server/internal/protocol"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestGrowthTable(t *testing.T) {
	r := (RewardRules{WinGold: 20, WinExperience: 10}).Normalized()
	if len(r.Levels) != int(MaxRoleLevel) || r.AtLevel(150).WinGold != 20 || r.GrowthEnabled {
		t.Fatal("legacy migration")
	}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	r.GrowthEnabled = true
	if r.Validate() == nil {
		t.Fatal("empty curve enabled")
	}
	for i := 0; i < int(MaxRoleLevel)-1; i++ {
		r.Levels[i].NextExperience = 100
	}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		level     uint16
		xp        uint64
		wantLevel uint16
		wantXP    uint32
	}{{1, 99, 1, 99}, {1, 100, 2, 0}, {1, 250, 3, 50}, {149, 250, 151, 50}, {150, 250, 152, 50}, {199, 250, 200, 150}, {200, 250, 200, 250}} {
		level, xp := AdvanceLevel(c.level, c.xp, r)
		if level != c.wantLevel || xp != c.wantXP {
			t.Fatalf("%+v -> %d/%d", c, level, xp)
		}
	}
	r.Levels[1].Level = 1
	if r.Validate() == nil {
		t.Fatal("duplicate accepted")
	}
}

func TestGrowthSettlementMySQL(t *testing.T) {
	dsn := os.Getenv("KK_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("isolated MySQL required")
	}
	store, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.DB.Close()
	var name string
	if err = store.DB.QueryRow("SELECT DATABASE()").Scan(&name); err != nil || (!strings.HasPrefix(name, "openkfo_debug_") && name != "kungfu_game_test") {
		t.Fatal("isolated database required")
	}
	uid := uint64(time.Now().UnixMilli())
	a, err := NewAccountWithStarterCharacter(uid, fmt.Sprintf("gr%d", uid), "test123456")
	if err != nil {
		t.Fatal(err)
	}
	protocol.WriteUint16(a.Profile, LevelOffset, 1)
	protocol.WriteUint32(a.Profile, ExperienceOffset, 90)
	if err = store.Create(a); err != nil {
		t.Fatal(err)
	}
	defer store.DB.Exec("DELETE FROM accounts WHERE uid=?", uid)
	serial, err := store.BattleManager().NextBattle()
	if err != nil {
		t.Fatal(err)
	}
	defer store.DB.Exec("DELETE FROM battle_settlements WHERE serial=?", serial)
	rules := RewardRules{}.Normalized()
	rules.GrowthEnabled = true
	for i := 0; i < int(MaxRoleLevel)-1; i++ {
		rules.Levels[i].NextExperience = 100
	}
	awards := []BattleReward{{UID: uid, Outcome: "win", Gold: 7, Experience: 220}}
	got, err := store.BattleManager().SettleBattle(serial, []byte("{}"), awards, rules)
	if err != nil {
		t.Fatal(err)
	}
	if ProfileLevel(got[0].Profile) != 4 || protocol.ReadUint32(got[0].Profile, ExperienceOffset) != 10 || got[0].GoldBalance != 7 {
		t.Fatalf("growth incorrect: %+v", got)
	}
	again, err := store.BattleManager().SettleBattle(serial, []byte("{}"), []BattleReward{{UID: uid, Outcome: "win", Gold: 999, Experience: 999}}, rules)
	if err != nil || !reflect.DeepEqual(got, again) {
		t.Fatal("duplicate settlement changed reward", err)
	}
	current, err := store.RoleManager().Snapshot(uid)
	if err != nil || current.Gold != 7 || ProfileLevel(current.Profile) != 4 {
		t.Fatal("growth not persisted atomically", err)
	}
}

func TestLevel200OneExperienceBoundary(t *testing.T) {
	rules := (RewardRules{GrowthEnabled: true}).Normalized()
	for i := 0; i < 199; i++ {
		rules.Levels[i].NextExperience = 30210
	}
	profile := make([]byte, 360)
	if err := (RoleManager{}).SetLevel(profile, 150, 30209); err != nil {
		t.Fatal(err)
	}
	change, err := (RoleManager{}).AddExp(profile, 1, rules)
	if err != nil || change.After != 151 || protocol.ReadUint32(profile, ExperienceOffset) != 0 {
		t.Fatal(change, err)
	}
	if err := (RoleManager{}).SetLevel(profile, 200, 0); err != nil {
		t.Fatal(err)
	}
	change, err = (RoleManager{}).AddExp(profile, 1000000, rules)
	if err != nil || change.After != 200 {
		t.Fatal("max level overflow", change, err)
	}
	if err := (RoleManager{}).SetLevel(profile, 201, 0); err == nil {
		t.Fatal("201 accepted")
	}
}
