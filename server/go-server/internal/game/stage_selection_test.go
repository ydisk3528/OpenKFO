package game

import (
	"bytes"
	"fmt"
	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
	"strings"
	"testing"
)

func TestStageSelectionWaitsForNativeLobby(t *testing.T) {
	h := NewHub(nil, Config{})
	s := &Session{}
	// The periodic inventory refresh may run after the lobby acknowledgement
	// but before its UI opens. It must not read or mark a delivered stage view.
	if err := h.refreshStageSelection(s); err != nil {
		t.Fatal(err)
	}
	if s.StageViewRequested || s.StageViewReady {
		t.Fatal("stage cache initialized before native lobby request")
	}
}

func TestStageSelectionRefreshAndRevocation(t *testing.T) {
	h, s, peer, _ := waitingRoomFixture()
	h.Config.Pools = map[string][]uint32{"0:2": {8110}}
	view := persistence.StagePlayerView{Configured: true, Catalogue: []uint32{8110, 8111}, Maps: []uint32{8110, 8111}}
	check := func(want []uint32) {
		t.Helper()
		out := roomOutputs(t, s, protocol.MsgStageSelectionReply)
		selection, e := protocol.ParseStageProgress(out[0].Payload)
		if e != nil {
			t.Fatal(e)
		}
		if len(selection.MapIDs) != len(want) {
			t.Fatal(selection, want)
		}
		for i := range want {
			if selection.MapIDs[i] != want[i] {
				t.Fatal(selection, want)
			}
		}
	}
	if err := h.sendStageSelection(s, view, true); err != nil {
		t.Fatal(err)
	}
	check([]uint32{8110})
	if err := h.sendStageSelection(s, view, false); err != nil {
		t.Fatal(err)
	}
	roomOutputs(t, s)
	view.ForcedMaps = []uint32{8111}
	if err := h.sendStageSelection(s, view, false); err != nil {
		t.Fatal(err)
	}
	check([]uint32{8110, 8111})
	view.Maps = nil
	view.ForcedMaps = nil
	if err := h.sendStageSelection(s, view, false); err != nil {
		t.Fatal(err)
	}
	check(nil)
	if err := h.sendStageSelection(s, view, false); err != nil {
		t.Fatal(err)
	}
	roomOutputs(t, s)
	roomOutputs(t, peer)
}

func TestStageSelectionIncludesPersistedPVEPlans(t *testing.T) {
	h, s, _, _ := waitingRoomFixture()
	hash := strings.Repeat("a", 64)
	h.Config.ConfigHash = hash
	h.Config.Pools = nil
	access := persistence.StageAccess{ClientHash: hash, PVEMaps: []uint32{8110, 9170, 8111}, Requirements: []persistence.StageTitleRequirement{{MapID: 8110, Name: "Foster"}, {MapID: 9170, Name: "Wave"}, {MapID: 8111, Name: "Missing"}},
		FosterPlans: []persistence.FosterConfig{{MapID: 8110, ScriptHash: hash, RuntimeHash: hash, ConfigHash: hash, Templates: []string{"Monster"}, Plan: protocol.FosterPlan{InitialHP: []float32{8}, PlayerLimit: 6, GlobalLimit: 32, Groups: []protocol.FosterGroup{{SubLimit: 2, GroupLimit: 20, Spawns: []protocol.FosterSpawn{{Template: 0, Direction: 2}}}}}}},
		WavePlans:   []persistence.StageWaveConfig{{MapID: 9170, ScriptHash: hash, RuntimeHash: hash, Templates: []string{"Monster"}, Variants: []StageWaveVariant{{MinPlayers: 2, MaxPlayers: 6, Waves: []StageWavePlan{{Monsters: map[uint32]uint32{0: 4}}}}}}}}
	view := persistence.StagePlayerView{Configured: true, Access: access, Catalogue: access.PVEMaps, Maps: access.PVEMaps}
	check := func(want ...uint32) {
		t.Helper()
		if err := h.sendStageSelection(s, view, true); err != nil {
			t.Fatal(err)
		}
		out := roomOutputs(t, s, protocol.MsgStageSelectionReply)
		actual, err := protocol.ParseStageProgress(out[0].Payload)
		if err != nil || len(actual.MapIDs) != len(want) {
			t.Fatal(actual, err, want)
		}
		for i, id := range want {
			if actual.MapIDs[i] != id {
				t.Fatal(actual, want)
			}
		}
	}
	check(8110, 9170)
	view.Maps = []uint32{9170}
	check(9170) // Player restrictions still apply.
	view.Access.Disabled = []uint32{9170}
	check()
	view.Access.Disabled = nil
	view.Access.ClientHash = strings.Repeat("b", 64)
	check()
	view.Access.ClientHash = hash
	view.Access.FosterPlans[0].Plan.PlayerLimit = 0
	check() // One invalid plan must still invalidate the whole access snapshot.
}

func TestStageAuxiliaryStateQuery(t *testing.T) {
	h, owner, _, _ := waitingRoomFixture()
	for _, phase := range []string{"lobby", "room"} {
		owner.game().Phase = phase
		if err := h.route(owner, owner.game(), protocol.Message{ID: protocol.MsgStageStateQuery}); err != nil {
			t.Fatal(err)
		}
		out := roomOutputs(t, owner, protocol.MsgStageStateReply)[0]
		if !bytes.Equal(out.Payload, make([]byte, 16)) {
			t.Fatal("invalid empty auxiliary state")
		}
	}
	if err := h.route(owner, owner.game(), protocol.Message{ID: protocol.MsgStageStateQuery, Payload: []byte{1}}); err != nil {
		t.Fatal(err)
	}
	roomOutputs(t, owner, 20150)
}

// Compare the complete catalogue with the former repeated-admission path.
func BenchmarkStageCatalogueValidation(b *testing.B) {
	hash := strings.Repeat("a", 64)
	config := Config{ConfigHash: hash}
	access := persistence.StageAccess{ClientHash: hash}
	for i := uint32(0); i < 50; i++ {
		id := 8100 + i
		access.PVEMaps = append(access.PVEMaps, id)
		access.Requirements = append(access.Requirements, persistence.StageTitleRequirement{MapID: id, Name: fmt.Sprint(id)})
		access.WavePlans = append(access.WavePlans, persistence.StageWaveConfig{MapID: id, ScriptHash: hash, RuntimeHash: hash, Templates: []string{"Monster"}, Variants: []StageWaveVariant{{MinPlayers: 2, MaxPlayers: 6, Waves: []StageWavePlan{{Monsters: map[uint32]uint32{0: 4}}}}}})
	}
	if err := access.Validate(); err != nil {
		b.Fatal(err)
	}
	view := persistence.StagePlayerView{Configured: true, Access: access, Maps: access.PVEMaps}
	b.Run("repeated_validation", func(b *testing.B) {
		for n := 0; n < b.N; n++ {
			ids := []uint32{}
			for _, plan := range access.WavePlans {
				for players := 1; players <= 8; players++ {
					if _, err := config.persistedStagePlan(access, plan.MapID, players); err == nil {
						ids = append(ids, plan.MapID)
						break
					}
				}
			}
			if _, err := (protocol.StageProgress{MapIDs: ids}).Encode(); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("single_validation", func(b *testing.B) {
		for n := 0; n < b.N; n++ {
			p, err := stageSelectionPayload(config, view)
			if err != nil {
				b.Fatal(err)
			}
			if n == 0 {
				expected, _ := (protocol.StageProgress{MapIDs: access.PVEMaps}).Encode()
				if !bytes.Equal(p, expected) {
					b.Fatal("catalogue changed")
				}
			}
		}
	})
}
