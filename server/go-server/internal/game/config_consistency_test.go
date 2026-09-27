package game

import (
	"errors"
	"kungfu.local/server/internal/persistence"
	"strings"
	"testing"
)

func TestStageConfigurationConsistency(t *testing.T) {
	c := Config{ConfigHash: strings.Repeat("a", 64)}
	for _, hash := range []string{"", c.ConfigHash} {
		if err := c.ValidateStageAccess(persistence.StageAccess{ClientHash: hash}); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.ValidateStageAccess(persistence.StageAccess{ClientHash: strings.Repeat("b", 64)}); !errors.Is(err, ErrMapConfigMismatch) {
		t.Fatal("mismatch not distinguished", err)
	}
}
