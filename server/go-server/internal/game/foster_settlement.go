package game

import (
	"time"

	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
)

// Emulator settlement policy combines authenticated controller report, complete
// roster/context, script marker and reconciled spawn/health receipts. This is
// client-reported combat, not independent server simulation or original rewards.
func validateFosterFinish(r *Room, payload []byte) (string, error) {
	if r.FosterPlan == nil {
		return "", nil
	}
	_, reason, err := protocol.ParsePVEFinishReport(protocol.FosterMode, payload)
	if err != nil {
		return "", err
	}
	health, err := validateBattleReport(r, payload)
	if err != nil {
		return "", err
	}
	switch reason {
	case protocol.StageFinishScript:
		if r.fosterReceiptsComplete() {
			return persistence.StageOutcomeClear, nil
		}
	case protocol.StageFinishActorFlags:
		if r.FosterFinishReported {
			return "", nil
		}
		for _, hp := range health {
			if hp != 0 {
				return "", nil
			}
		}
		return persistence.StageOutcomeFailed, nil
	case protocol.StageFinishCounterZero:
		if pveDeadlineReached(r, time.Now()) {
			return persistence.StageOutcomeFailed, nil
		}
	}
	return "", nil
}
