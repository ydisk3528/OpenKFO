package desktop

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestWorkspaceRulesStripHitPropertyMetadata(t *testing.T) {
	state := weaponState{Drafts: map[string][]Rule{}}
	payload := map[string]any{"rules": []Rule{{Stage: 1081, Properties: map[string]map[string]float64{
		"10080012": {"action": 1008001, "state": 1081, "owner_weapon": 253451, "SkillDamage": 2},
	}}}}
	if err := mergeWorkspaceIntoState(payload, "253451", &state); err != nil {
		t.Fatal(err)
	}
	values := state.Drafts["253451"][0].Properties["10080012"]
	if len(values) != 1 || values["SkillDamage"] != 2 {
		t.Fatalf("metadata was not stripped: %#v", values)
	}
}

func TestWorkspaceHitPropertiesAreAuthoritativeForCurrentWeapon(t *testing.T) {
	state := weaponState{
		HitProperties: map[string]HitProperty{
			"910000001": {ID: "910000001", OwnerWeapon: "253450", Values: map[string]float64{"SkillDamage": 9}},
			"910000002": {ID: "910000002", OwnerWeapon: "253451", Values: map[string]float64{"SkillDamage": 8}},
		},
		Drafts:   map[string][]Rule{"253450": {{Stage: 1, Properties: map[string]map[string]float64{"910000001": {"SkillDamage": 9}}}}},
		Variants: map[string]map[int][]VariantEdit{"253450": {1: {{Segments: []VariantAnm{{SkillProID: "910000001", Name: "a"}}}}}},
	}
	payload := map[string]any{"hit_properties": map[string]HitProperty{
		"910000001": {ID: "910000001", Values: map[string]float64{"SkillDamage": 3.3}},
	}}
	if err := mergeWorkspaceIntoState(payload, "253450", &state); err != nil {
		t.Fatal(err)
	}
	if got := state.HitProperties["910000001"].Values["SkillDamage"]; got != 3.3 {
		t.Fatalf("当前武器新值未替换旧值：%v", got)
	}
	if _, ok := state.HitProperties["910000002"]; !ok {
		t.Fatal("别武器命中属性被误删")
	}
	if _, ok := state.HitProperties["910000001"]; !ok {
		t.Fatal("当前武器新对象被删除")
	}

	payload = map[string]any{"hit_properties": map[string]HitProperty{}}
	if err := mergeWorkspaceIntoState(payload, "253450", &state); err != nil {
		t.Fatal(err)
	}
	if _, ok := state.HitProperties["910000001"]; ok {
		t.Fatal("当前武器删除的 canonical 对象仍存在")
	}
	if len(state.Variants["253450"][1][0].Segments) != 1 || state.Variants["253450"][1][0].Segments[0].SkillProID != "" {
		t.Fatal("删除 canonical 对象未保留纯动画段")
	}
}

func TestWorkspaceHitPropertiesRejectForeignIncomingOwner(t *testing.T) {
	payload := map[string]any{"hit_properties": map[string]HitProperty{"910000001": {OwnerWeapon: "253451"}}}
	if err := mergeWorkspaceIntoState(payload, "253450", &weaponState{}); err == nil {
		t.Fatal("incoming owner 属于别武器时未拒绝")
	}
}

func TestWorkspaceVariantDamageStringRoundTrip(t *testing.T) {
	payload := map[string]any{"variants": map[string]any{"1": []any{map[string]any{
		"condition": 406, "segments": []any{map[string]any{"name": "a", "damage": "3.3"}},
	}}}}
	state := weaponState{}
	if err := mergeWorkspaceIntoState(payload, "253999", &state); err != nil {
		t.Fatal(err)
	}
	if got := state.Variants["253999"][1][0].Segments[0].Damage; got != 3.3 {
		t.Fatalf("workspace 字符串 damage 未转换：%v", got)
	}
}

func TestRenameMissingHitPropertyStillRewritesLegacyReferences(t *testing.T) {
	state := &weaponState{
		Drafts:   map[string][]Rule{"253450": {{Properties: map[string]map[string]float64{"old": {"SkillDamage": 1}}}}},
		Remaps:   map[string]map[int]*StageRemap{"253450": {1: {PropertyID: "old"}}},
		Variants: map[string]map[int][]VariantEdit{"253450": {1: {{Segments: []VariantAnm{{SkillProID: "old"}}}}}},
	}
	if err := renameHitProperty(state, "old", "new"); err != nil {
		t.Fatal(err)
	}
	if _, ok := state.Drafts["253450"][0].Properties["new"]; !ok || state.Remaps["253450"][1].PropertyID != "new" || state.Variants["253450"][1][0].Segments[0].SkillProID != "new" {
		t.Fatal("canonical 缺失时 legacy 引用未完整重命名")
	}
}

func TestWorkspaceVariantDeletionTombstoneIsPreserved(t *testing.T) {
	payload := map[string]any{"variants": map[string]any{"1": []any{map[string]any{
		"condition": 1071, "remove": true, "segments": []any{},
	}}}}
	state := weaponState{}
	if err := mergeWorkspaceIntoState(payload, "253521", &state); err != nil {
		t.Fatal(err)
	}
	edits := state.Variants["253521"][1]
	if len(edits) != 1 || !edits[0].Remove || edits[0].Condition != 1071 {
		t.Fatalf("删除 tombstone 未保留：%+v", edits)
	}
}

func TestWorkspaceVariantsAcceptIntegerStringCondition(t *testing.T) {
	payload := map[string]any{"variants": map[string]any{"1": []any{map[string]any{
		"condition": "406", "segments": []any{},
	}}}}
	state := weaponState{}
	if err := mergeWorkspaceIntoState(payload, "253999", &state); err != nil {
		t.Fatal(err)
	}
	if got := state.Variants["253999"][1][0].Condition; got != 406 {
		t.Fatalf("condition = %d, want 406", got)
	}
}

func TestWorkspaceVariantsRejectInvalidCondition(t *testing.T) {
	payload := map[string]any{"variants": map[string]any{"1": []any{map[string]any{
		"condition": "406x", "segments": []any{},
	}}}}
	if err := mergeWorkspaceIntoState(payload, "253999", &weaponState{}); err == nil {
		t.Fatal("invalid condition was accepted")
	}
}

func TestWorkspaceVariantJSONUsesIntegerCondition(t *testing.T) {
	var edit workspaceVariantEdit
	if err := json.Unmarshal([]byte(`{"condition":"406"}`), &edit); err != nil {
		t.Fatal(err)
	}
	if edit.Condition != 406 {
		t.Fatalf("condition = %d, want 406", edit.Condition)
	}
}

func TestWorkspaceRemapFieldsMergeAndFalseClearIsIgnored(t *testing.T) {
	key := "253999"
	state := weaponState{
		Remaps:          map[string]map[int]*StageRemap{key: {2011: {Action: "2001001", PropertyID: "100"}}},
		Cleared:         map[string]map[int]bool{key: {2012: true}},
		ExtraProperties: map[string]ExtraProperty{"800000001": {Template: "100"}},
	}
	payload := map[string]any{
		"remaps":           map[string]map[int]*StageRemap{key: {2011: {Action: "2001002", PropertyID: "101"}}},
		"cleared":          map[string]map[int]bool{key: {2012: false, 2013: true}},
		"extra_properties": map[string]ExtraProperty{"800000002": {Template: "101"}},
	}
	if err := mergeWorkspaceIntoState(payload, key, &state); err != nil {
		t.Fatal(err)
	}
	if state.Remaps[key][2011].Action != "2001002" || state.Cleared[key][2012] || !state.Cleared[key][2013] || len(state.ExtraProperties) != 2 {
		t.Fatalf("workspace remap fields merge error: %+v %+v %+v", state.Remaps, state.Cleared, state.ExtraProperties)
	}
}

func TestWorkspaceExtraPropertyOwnership(t *testing.T) {
	for _, test := range []struct {
		name      string
		incoming  ExtraProperty
		wantError bool
	}{
		{"旧工作区保留归属", ExtraProperty{Template: "100"}, false},
		{"相同登记幂等", ExtraProperty{Template: "100", OwnerWeapon: "253450"}, false},
		{"不能改变模板", ExtraProperty{Template: "101"}, true},
		{"不能改变归属", ExtraProperty{Template: "100", OwnerWeapon: "253451"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := weaponState{ExtraProperties: map[string]ExtraProperty{"800000001": {Template: "100", OwnerWeapon: "253450"}}}
			payload := map[string]any{"extra_properties": map[string]ExtraProperty{"800000001": test.incoming}}
			err := mergeWorkspaceIntoState(payload, "253450", &state)
			if (err != nil) != test.wantError {
				t.Fatalf("合并结果错误：%v", err)
			}
			if state.ExtraProperties["800000001"].OwnerWeapon != "253450" {
				t.Fatal("作者归属丢失")
			}
			encoded, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			var restored weaponState
			if err := json.Unmarshal(encoded, &restored); err != nil {
				t.Fatal(err)
			}
			if restored.ExtraProperties["800000001"].OwnerWeapon != "253450" {
				t.Fatal("归属未持久化")
			}
		})
	}
}

func TestWorkspaceComboRulesIgnoreEditorDrafts(t *testing.T) {
	key := "253999"
	committed := ComboRuleSet{Max: []ComboRuleMax{{Skill: "900000001", MaxCombo: "2"}}}
	payload := map[string]any{
		"combo_rule_info":        map[string]any{"rules": committed},
		"combo_rule_max_draft":   []ComboRuleMax{{Skill: "900000002", MaxCombo: "9"}},
		"combo_rule_black_draft": []ComboRuleLink{{Prev: "900000002", Cur: "900000003"}},
		"combo_rule_white_draft": []ComboRuleLink{{Prev: "900000003", Cur: "900000002"}},
	}
	state := weaponState{}
	if err := mergeWorkspaceIntoState(payload, key, &state); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(state.ComboRules[key], committed) {
		t.Fatalf("uncommitted editor drafts replaced saved rules: %+v", state.ComboRules[key])
	}
	payload["combo_rule_info"] = map[string]any{"rules": ComboRuleSet{}}
	if err := mergeWorkspaceIntoState(payload, key, &state); err != nil {
		t.Fatal(err)
	}
	if _, ok := state.ComboRules[key]; ok {
		t.Fatal("cleared rules restored from editor drafts")
	}
}
