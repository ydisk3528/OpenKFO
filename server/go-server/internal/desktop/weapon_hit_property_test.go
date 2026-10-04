package desktop

import "testing"

func TestNormalizeHitPropertiesStripsMetadataValues(t *testing.T) {
	state := &weaponState{
		HitProperties: map[string]HitProperty{
			"10080012": {
				ID: "10080012", OwnerWeapon: "253451",
				Values: map[string]float64{
					"id": 10080012, "action": 1008001, "state": 1081,
					"owner_weapon": 253451, "SkillDamage": 2,
				},
			},
		},
	}
	if err := normalizeHitProperties(state); err != nil {
		t.Fatal(err)
	}
	values := state.HitProperties["10080012"].Values
	if len(values) != 1 || values["SkillDamage"] != 2 {
		t.Fatalf("canonical metadata was not removed: %#v", values)
	}
}

func TestNormalizeHitPropertiesSharesVariantAndRule(t *testing.T) {
	state := &weaponState{
		Drafts: map[string][]Rule{
			"253450": {{Stage: 1, Properties: map[string]map[string]float64{
				"910000001": {"SkillDamage": 3.3},
			}}},
		},
		Variants: map[string]map[int][]VariantEdit{
			"253450": {2011: {{Condition: 406, Segments: []VariantAnm{{
				Name: "2001001", SkillProID: "910000001", TemplateSkillProID: "100",
			}}}}},
		},
	}
	if err := normalizeHitProperties(state); err != nil {
		t.Fatal(err)
	}
	property, ok := state.HitProperties["910000001"]
	if !ok {
		t.Fatal("统一 map 未生成命中属性")
	}
	if property.TemplateID != "100" || property.Condition != 406 || property.State != 2011 {
		t.Fatalf("命中属性关联不完整：%+v", property)
	}
	if len(property.References) != 2 {
		t.Fatalf("分支和规则引用未合并：%+v", property.References)
	}
	if property.Values["SkillDamage"] != 3.3 {
		t.Fatalf("规则编辑值未进入统一对象：%+v", property.Values)
	}
}

func TestNormalizeHitPropertiesIsIdempotent(t *testing.T) {
	state := &weaponState{
		ExtraProperties: map[string]ExtraProperty{
			"800000001": {Template: "100", OwnerWeapon: "253450"},
		},
		Remaps: map[string]map[int]*StageRemap{
			"253450": {2011: {PropertyID: "800000001", Action: "2001001"}},
		},
	}
	if err := normalizeHitProperties(state); err != nil {
		t.Fatal(err)
	}
	if err := normalizeHitProperties(state); err != nil {
		t.Fatal(err)
	}
	property := state.HitProperties["800000001"]
	if len(property.References) != 1 {
		t.Fatalf("重复归一化产生重复引用：%+v", property.References)
	}
}

func TestRenameHitPropertyRefreshesAllReferences(t *testing.T) {
	const oldID = "910000001"
	const newID = "910000002"
	state := &weaponState{
		Drafts:          map[string][]Rule{"253450": {{Properties: map[string]map[string]float64{oldID: {"SkillDamage": 3}}}}},
		Applied:         map[string][]Rule{"253450": {{Properties: map[string]map[string]float64{oldID: {"SkillDamage": 4}}}}},
		ExtraProperties: map[string]ExtraProperty{oldID: {Template: "100"}},
		Remaps:          map[string]map[int]*StageRemap{"253450": {2011: {PropertyID: oldID}}},
		Variants:        map[string]map[int][]VariantEdit{"253450": {2011: {{Segments: []VariantAnm{{SkillProID: oldID}}}}}},
		HitProperties:   map[string]HitProperty{oldID: {ID: oldID, OwnerWeapon: "253450"}},
		PropertyClones: map[string]map[string]string{
			"253450": {"1|" + oldID: "900000001"},
			"253451": {"1|" + oldID: "900000002"},
		},
	}
	if err := renameHitProperty(state, oldID, newID); err != nil {
		t.Fatal(err)
	}
	if _, ok := state.HitProperties[oldID]; ok {
		t.Fatal("旧统一对象仍存在")
	}
	if _, ok := state.HitProperties[newID]; !ok {
		t.Fatal("新统一对象未生成")
	}
	if state.Drafts["253450"][0].Properties[newID]["SkillDamage"] != 3 || state.Applied["253450"][0].Properties[newID]["SkillDamage"] != 4 {
		t.Fatal("规则引用未同步")
	}
	if state.Remaps["253450"][2011].PropertyID != newID || state.Variants["253450"][2011][0].Segments[0].SkillProID != newID {
		t.Fatal("重映射或分支引用未同步")
	}
	if _, ok := state.ExtraProperties[newID]; !ok {
		t.Fatal("额外属性登记未同步")
	}
	if state.PropertyClones["253450"]["1|"+newID] != "900000001" || state.PropertyClones["253450"]["1|"+oldID] != "" || state.PropertyClones["253451"]["1|"+oldID] != "900000002" {
		t.Fatal("pinned clone mapping not migrated or unrelated owner changed")
	}
}

func TestDeleteHitPropertyClearsAllLegacyReferences(t *testing.T) {
	const id = "910000001"
	state := &weaponState{
		Drafts: map[string][]Rule{
			"253450": {{Stage: 1, Properties: map[string]map[string]float64{id: {"SkillDamage": 3}}}},
		},
		Applied: map[string][]Rule{
			"253450": {{Stage: 1, Properties: map[string]map[string]float64{id: {"SkillDamage": 4}}}},
		},
		ExtraProperties: map[string]ExtraProperty{id: {Template: "100"}},
		Remaps: map[string]map[int]*StageRemap{
			"253450": {2011: {PropertyID: id}},
		},
		Variants: map[string]map[int][]VariantEdit{
			"253450": {2011: {{Condition: 406, Segments: []VariantAnm{{SkillProID: id}}}}},
		},
		HitProperties: map[string]HitProperty{id: {ID: id}},
	}
	deleteHitProperty(state, id)
	if _, ok := state.HitProperties[id]; ok {
		t.Fatal("统一命中属性未删除")
	}
	if _, ok := state.ExtraProperties[id]; ok {
		t.Fatal("旧 extra_properties 引用未删除")
	}
	if state.Remaps["253450"][2011].PropertyID != "" {
		t.Fatal("重映射引用未清理")
	}
	if len(state.Drafts["253450"][0].Properties) != 0 || len(state.Applied["253450"][0].Properties) != 0 {
		t.Fatal("规则引用未清理")
	}
	if len(state.Variants["253450"][2011][0].Segments) != 1 || state.Variants["253450"][2011][0].Segments[0].SkillProID != "" {
		t.Fatal("删除命中属性时应保留纯动作分支段")
	}
}

func TestProjectHitPropertiesToLegacyFiltersExactState(t *testing.T) {
	state := &weaponState{
		Drafts: map[string][]Rule{"253450": {{Stage: 1}, {Stage: 2}}},
		HitProperties: map[string]HitProperty{
			"910000001": {ID: "910000001", OwnerWeapon: "253450", Values: map[string]float64{"SkillDamage": 3}, References: []HitPropertyRef{{Weapon: "253450", State: 1}}},
			"910000002": {ID: "910000002", OwnerWeapon: "253450", Values: map[string]float64{"SkillDamage": 4}, References: []HitPropertyRef{{Weapon: "253450", State: 2}}},
		},
	}
	projectHitPropertiesToLegacy(state)
	if len(state.Drafts["253450"][0].Properties) != 1 || state.Drafts["253450"][0].Properties["910000001"]["SkillDamage"] != 3 {
		t.Fatalf("状态1投影错误：%+v", state.Drafts)
	}
	if len(state.Drafts["253450"][1].Properties) != 1 || state.Drafts["253450"][1].Properties["910000002"]["SkillDamage"] != 4 {
		t.Fatalf("状态2投影错误：%+v", state.Drafts)
	}
}
