package persistence

import (
	"encoding/json"
	"kungfu.local/server/internal/protocol"
	"os"
	"testing"
)

// Run against the prepared release configuration, without touching MySQL.
func TestReleaseExperiencePlan(t *testing.T) {
	path := os.Getenv("KK_REWARD_PLAN_TEST")
	if path == "" {
		t.Skip("release reward plan path required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var plan RewardSettings
	if err = json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	if err = plan.Rules.Validate(); err != nil {
		t.Fatal(err)
	}
	p := make([]byte, protocol.RoleProfileSize)
	if err = (RoleManager{}).SetLevel(p, 150, 0); err != nil {
		t.Fatal(err)
	}
	reached160 := 0
	for n := 1; n <= 200; n++ {
		level := ProfileLevel(p)
		if level == 200 {
			t.Fatalf("reached 200 before win %d", n)
		}
		if _, err = (RoleManager{}).AddExp(p, plan.Rules.AtLevel(level).WinExperience, plan.Rules); err != nil {
			t.Fatal(err)
		}
		if ProfileLevel(p) >= 160 && reached160 == 0 {
			reached160 = n
		}
	}
	if reached160 != 50 || ProfileLevel(p) != 200 {
		t.Fatalf("160 at %d wins, final level %d", reached160, ProfileLevel(p))
	}
}
