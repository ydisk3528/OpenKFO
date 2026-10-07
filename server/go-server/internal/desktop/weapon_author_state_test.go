package desktop

import "testing"

func TestForgetWeaponAuthorStateKeepsForeignReferencesAndGlobalBuffs(t *testing.T) {
	state := &weaponState{
		Created:        map[string]Blueprint{"253011": {ID: 253011}, "253451": {ID: 253451}},
		Drafts:         map[string][]Rule{"253011": {{Stage: 1}}, "253451": {{Stage: 1}}},
		Applied:        map[string][]Rule{"253011": {{Stage: 1}}, "253451": {{Stage: 1}}},
		Remaps:         map[string]map[int]*StageRemap{"253011": {1: {PropertyID: "shared"}}, "253451": {1: {PropertyID: "shared"}}},
		Variants:       map[string]map[int][]VariantEdit{"253011": {1: {{Condition: 406}}}},
		Scopes:         map[string]map[int]map[string][]FrameSwitchAttr{"253011": {1: {"1": {{Key: "startframe", Value: "1"}}}}},
		StageEffects:   map[string]map[int][]StageEffect{"253011": {1: {{EffectID: "fx"}}}},
		EffectRows:     map[string][]EffectRow{"253011": {{EffectID: "fx"}}},
		PropertyClones: map[string]map[string]string{"253011": {"1|shared": "900000001"}},
		HitProperties: map[string]HitProperty{
			"shared": {ID: "shared", OwnerWeapon: "253011", References: []HitPropertyRef{{Weapon: "253011"}, {Weapon: "253451"}}},
		},
		ExtraProperties: map[string]ExtraProperty{"shared": {Template: "100", OwnerWeapon: "253011"}},
		UStates:         map[string]UStateEdit{"432": {Action: "upsert"}},
		LuaScripts:      map[string]map[string]string{buffLuaEntry: {"OnGetUstate_432": "return"}},
	}

	forgetWeaponAuthorState(state, "253011")
	if _, ok := state.Created["253011"]; ok || len(state.Variants["253011"]) != 0 || len(state.Scopes["253011"]) != 0 || len(state.StageEffects["253011"]) != 0 || len(state.EffectRows["253011"]) != 0 {
		t.Fatal("current weapon author state was not fully removed")
	}
	if state.Remaps["253451"][1].PropertyID != "shared" {
		t.Fatal("foreign weapon remap was cleared")
	}
	property, ok := state.HitProperties["shared"]
	if !ok || property.OwnerWeapon != "253451" || len(property.References) != 1 || property.References[0].Weapon != "253451" {
		t.Fatalf("shared hit property was deleted or not reassigned: %+v", state.HitProperties)
	}
	if _, ok := state.UStates["432"]; !ok || state.LuaScripts[buffLuaEntry]["OnGetUstate_432"] == "" {
		t.Fatal("global Buff/Lua state was touched")
	}
}
