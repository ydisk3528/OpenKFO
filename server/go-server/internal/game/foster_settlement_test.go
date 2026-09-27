package game

import (
	"testing"

	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
)

func TestFosterFinishPolicy(t *testing.T) {
	for _, scenario := range []string{"clear", "partial-plan", "no-spawns", "early", "live-monster", "missing-marker", "peer", "old-context", "failed", "live-player-failure", "unknown-reason"} {
		t.Run(scenario, func(t *testing.T) {
			_, owner, peer, _ := combatFixture()
			r := owner.Room
			r.Request[46] = byte(protocol.FosterMode)
			r.FosterPlan = &protocol.FosterPlan{Groups: []protocol.FosterGroup{{Spawns: []protocol.FosterSpawn{{}}}}}
			r.FosterSpawned, r.FosterRetired = []int{1}, []int{1}
			r.FosterFinishReported = true
			p := settlementReport(r)
			reason := uint16(1)
			sender := owner
			want := persistence.StageOutcomeClear
			wantError := false
			switch scenario {
			case "partial-plan":
				r.FosterPlan.Groups = []protocol.FosterGroup{{Spawns: make([]protocol.FosterSpawn, 100)}, {Spawns: make([]protocol.FosterSpawn, 44)}}
				r.FosterSpawned, r.FosterRetired = []int{25, 0}, []int{25, 0}
			case "no-spawns":
				r.FosterSpawned, r.FosterRetired = []int{0}, []int{0}
				want = ""
			case "early":
				r.FosterSpawned[0] = 0
				want = ""
			case "live-monster":
				r.FosterRetired[0] = 0
				r.PVEActors = map[uint64]pveActor{42: {active: true, maximumHP: 8, reportedHP: 1}}
				want = ""
			case "missing-marker":
				r.FosterFinishReported = false
				want = ""
			case "peer":
				sender = peer
			case "old-context":
				protocol.WriteUint32(p, 71, r.Serial+1)
				wantError = true
			case "failed":
				reason = 2
				r.FosterFinishReported = false
				want = persistence.StageOutcomeFailed
			case "live-player-failure":
				reason = 2
				r.FosterFinishReported = false
				want = ""
			case "unknown-reason":
				reason = 3
				want = ""
			}
			for _, m := range r.Members {
				protocol.WriteUint16(p, int(m.Slot)*87+65, reason)
				if scenario == "failed" {
					protocol.WriteUint16(p, int(m.Slot)*87+2, 0)
				}
			}
			out, err := validateStageFinish(r, p)
			if (err != nil) != wantError || !wantError && out != want {
				t.Fatal("wrong Foster finish policy", out, err)
			}
			accepted, err := recordStageFinish(sender, p)
			if (err != nil) != wantError || accepted != (want != "" && !wantError && sender == owner) {
				t.Fatal("wrong controller/report acceptance", accepted, err)
			}
			if !accepted && (r.Stage != "battle" || len(r.Reports) != 0) {
				t.Fatal("unaccepted report changed settlement")
			}
		})
	}
}
