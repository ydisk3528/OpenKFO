package desktop

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"kungfu.local/server/internal/protocol"
	"slices"
)

const fosterStreetEasyHash = "c227b5bf3dae47e461a2e59b3d0832065d83e52c3bd10f44f6dc6f72deb3c15f"
const fosterRuntimeHash = "0a083607cab1456c0976038f1e78658a208607355c015060a4492259cde5280f"

type FosterSpawn = protocol.FosterSpawn
type FosterGroup = protocol.FosterGroup
type FosterPlanPreview = protocol.FosterPlan

// Literal plans extracted from the 49 original mode-10 scripts. Each is bound
// to its exact script bytes and the traced runtime/template catalogue.
//go:embed foster_plans.json
var fosterPlansJSON []byte

func fosterPlanPreview(raw []byte, runtime string, catalogue *FosterTemplateCatalogue) (*FosterPlanPreview, error) {
	if runtime != fosterRuntimeHash || catalogue == nil || (catalogue.ConfigHash != fosterConfigHash && catalogue.ConfigHash != iceChapterConfigHash) {
		return nil, nil
	}
	var plans map[string]json.RawMessage
	if err := json.Unmarshal(fosterPlansJSON, &plans); err != nil {
		return nil, err
	}
	custom := digest(raw) == iceChapterScriptHash
	if custom && catalogue.ConfigHash != iceChapterConfigHash {
		return nil, nil
	}
	data, ok := plans[digest(raw)]
	if !ok {
		return nil, nil
	}
	var plan FosterPlanPreview
	if err := json.Unmarshal(data, &plan); err != nil {
		return nil, err
	}
	if len(catalogue.InitialHP) != len(catalogue.Names) {
		return nil, fmt.Errorf("Foster initial HP catalogue is missing")
	}
	// Adding the chapter guard shifts native sorted template indices. Preserve
	// every original plan by mapping its old index around the new catalogue entry.
	if !custom && catalogue.ConfigHash == iceChapterConfigHash {
		added := slices.Index(catalogue.Names, "ice_guard")
		if added < 0 {
			return nil, fmt.Errorf("chapter guard missing")
		}
		for gi := range plan.Groups {
			for si := range plan.Groups[gi].Spawns {
				sp := &plan.Groups[gi].Spawns[si]
				if sp.Template >= uint32(added) {
					sp.Template++
				}
			}
		}
	}
	plan.InitialHP = slices.Clone(catalogue.InitialHP)
	return &plan, plan.Validate(len(catalogue.Names))
}
