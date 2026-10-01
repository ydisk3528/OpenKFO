package persistence

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestRewardRulesPersistence(t *testing.T) {
	dsn := os.Getenv("KK_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("isolated MySQL required")
	}
	store, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.DB.Close()
	store.DB.SetMaxOpenConns(1)
	var name string
	if err = store.DB.QueryRow("SELECT DATABASE()").Scan(&name); err != nil || (!strings.HasPrefix(name, "openkfo_debug_") && name != "kungfu_game_test") {
		t.Fatal("isolated database required")
	}
	// Connection-local shadow table: never change the running server's rules.
	if _, err = store.DB.Exec("CREATE TEMPORARY TABLE battle_reward_rules(id TINYINT UNSIGNED PRIMARY KEY,revision BIGINT UNSIGNED NOT NULL,rules MEDIUMBLOB NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	initial := RewardRules{WinGold: 20, WinExperience: 10, LossGold: 10, LossExperience: 5, DrawGold: 10, DrawExperience: 5}
	if err = store.RewardManager().SeedBattleRewards(initial); err != nil {
		t.Fatal(err)
	}
	first, err := store.RewardManager().BattleRewards(RewardRules{})
	if err != nil || !reflect.DeepEqual(first.Rules, initial.Normalized()) || first.Revision != 1 {
		t.Fatalf("seed: %+v %v", first, err)
	}
	next := initial
	next.WinGold = 0
	next.LossExperience = 7
	saved, err := store.RewardManager().SaveBattleRewards(first.Revision, next)
	if err != nil || saved.Revision != 2 {
		t.Fatal(saved, err)
	}
	if _, err = store.RewardManager().SaveBattleRewards(first.Revision, initial); err == nil {
		t.Fatal("stale writer overwrote rules")
	}
	invalid := next
	invalid.DrawGold = 1000001
	if _, err = store.RewardManager().SaveBattleRewards(saved.Revision, invalid); err == nil {
		t.Fatal("out-of-range award accepted")
	}
	if err = store.RewardManager().SeedBattleRewards(initial); err != nil {
		t.Fatal(err)
	}
	current, err := store.RewardManager().BattleRewards(initial)
	if err != nil || !reflect.DeepEqual(current.Rules, next.Normalized()) || current.Revision != 2 {
		t.Fatal("restart or stale write reverted GM changes", current, err)
	}
}

func TestLargeExperienceKeepsCurrencyAndSignedBounds(t *testing.T) {
	r := RewardRules{}.Normalized()
	r.Levels[149].WinExperience = 200000000
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	r.Levels[149].WinGold = 1000001
	if r.Validate() == nil {
		t.Fatal("currency limit changed")
	}
	r.Levels[149].WinGold = 0
	r.Levels[149].WinExperience = 2147483648
	if r.Validate() == nil {
		t.Fatal("signed experience overflow accepted")
	}
}
