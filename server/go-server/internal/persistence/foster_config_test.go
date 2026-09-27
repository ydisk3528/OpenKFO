package persistence

import (
	"math"
	"strings"
	"testing"

	"kungfu.local/server/internal/protocol"
)

func TestFosterPlanValidation(t *testing.T) {
	for _, scenario := range []string{"valid", "legacy-no-hp", "hp-count", "hp-zero", "hp-negative", "hp-nan", "hp-inf", "unknown-map", "duplicate-map", "wave-conflict", "hash", "template", "empty", "players", "capacity", "limits", "nan", "box", "names"} {
		t.Run(scenario, func(t *testing.T) {
			hash := strings.Repeat("a", 64)
			a := StageAccess{ClientHash: hash, PVEMaps: []uint32{8110}, Requirements: []StageTitleRequirement{{MapID: 8110, Name: "test"}}, FosterPlans: []FosterConfig{{MapID: 8110, ScriptHash: hash, RuntimeHash: hash, ConfigHash: hash, Templates: []string{" Monster", "Monster"}, Plan: protocol.FosterPlan{PlayerLimit: 6, GlobalLimit: 32, Groups: []protocol.FosterGroup{{SubLimit: 2, GroupLimit: 20, Block: 100, Spawns: []protocol.FosterSpawn{{Template: 0}}}}}}}}
			p := &a.FosterPlans[0]
			p.Plan.InitialHP = []float32{8, 75}
			switch scenario {
			case "legacy-no-hp":
				p.Plan.InitialHP = nil
			case "hp-count":
				p.Plan.InitialHP = []float32{8}
			case "hp-zero":
				p.Plan.InitialHP[0] = 0
			case "hp-negative":
				p.Plan.InitialHP[0] = -1
			case "hp-nan":
				p.Plan.InitialHP[0] = float32(math.NaN())
			case "hp-inf":
				p.Plan.InitialHP[0] = float32(math.Inf(1))
			case "unknown-map":
				p.MapID = 999
			case "duplicate-map":
				a.FosterPlans = append(a.FosterPlans, *p)
			case "wave-conflict":
				a.WavePlans = []StageWaveConfig{{MapID: 8110}}
			case "hash":
				p.ConfigHash = "missing"
			case "template":
				p.Plan.Groups[0].Spawns[0].Template = 2
			case "empty":
				p.Plan.Groups[0].Spawns = nil
			case "players":
				p.Plan.PlayerLimit = 7
			case "capacity":
				p.Plan.GlobalLimit = 101
			case "limits":
				p.Plan.Groups[0].SubLimit = 21
			case "nan":
				p.Plan.Groups[0].Spawns[0].Position[1] = float32(math.NaN())
			case "box":
				p.Plan.Groups[0].TriggerBox[0] = 1
			case "duplicate-block":
				p.Plan.Groups = append(p.Plan.Groups, p.Plan.Groups[0])
			case "names":
				p.Templates[1] = p.Templates[0]
			}
			if err := a.Validate(); (err == nil) != (scenario == "valid" || scenario == "legacy-no-hp") {
				t.Fatal("unexpected validation result", err)
			}
		})
	}
}
