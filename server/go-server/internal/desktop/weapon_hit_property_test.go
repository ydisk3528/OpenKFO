package desktop

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
)

func TestHitPropertyAcceptsLegacyStringBuff(t *testing.T) {
	var property HitProperty
	if err := json.Unmarshal([]byte(`{"id":"1008011","buff":"0","values":{"SkillDamage":2}}`), &property); err != nil {
		t.Fatalf("legacy string buff should decode: %v", err)
	}
	if property.ID != "1008011" || property.Buff != 0 || property.Values["SkillDamage"] != 2 {
		t.Fatalf("legacy string buff decoded incorrectly: %+v", property)
	}
	raw, err := json.Marshal(property)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"id":"1008011","values":{"SkillDamage":2}}` {
		t.Fatalf("canonical JSON should emit numeric/int-compatible metadata: %s", raw)
	}
}

func TestWorkspaceAcceptsLegacyStringBuff(t *testing.T) {
	state := &weaponState{}
	payload := map[string]any{
		"hit_properties": map[string]any{
			"1008011": map[string]any{
				"id":     "1008011",
				"buff":   "0",
				"values": map[string]any{"SkillDamage": 2},
			},
		},
	}
	if err := mergeWorkspaceIntoState(payload, "253011", state); err != nil {
		t.Fatalf("workspace with legacy string buff should merge: %v", err)
	}
	property, ok := state.HitProperties["1008011"]
	if !ok || property.Buff != 0 || property.Values["SkillDamage"] != 2 {
		t.Fatalf("workspace legacy string buff merged incorrectly: %+v", state.HitProperties)
	}
}

func TestHitPropertyAcceptsLegacyStringValues(t *testing.T) {
	var property HitProperty
	if err := json.Unmarshal([]byte(`{"id":"1008011","values":{"SkillDamage":"2","UStateLastCycle":"2500","RepulseTarget":0}}`), &property); err != nil {
		t.Fatalf("legacy string values should decode: %v", err)
	}
	if property.Values["SkillDamage"] != 2 || property.Values["UStateLastCycle"] != 2500 || property.Values["RepulseTarget"] != 0 {
		t.Fatalf("legacy string values decoded incorrectly: %#v", property.Values)
	}
	state := &weaponState{HitProperties: map[string]HitProperty{"1008011": property}}
	if err := normalizeHitProperties(state); err != nil {
		t.Fatal(err)
	}
	if _, exists := state.HitProperties["1008011"].Values["UStateLastCycle"]; exists {
		t.Fatalf("action metadata must be removed during normalization: %#v", state.HitProperties["1008011"].Values)
	}
}

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

func TestNormalizeHitPropertiesMergesSparseCanonicalValues(t *testing.T) {
	state := &weaponState{
		Drafts: map[string][]Rule{
			"253011": {{Stage: 2041, Properties: map[string]map[string]float64{
				"900000533": {
					"SkillDamage":   8,
					"RepulseTarget": 1,
					"StandHurt":     42,
				},
			}}},
		},
		HitProperties: map[string]HitProperty{
			"900000533": {
				ID: "900000533", OwnerWeapon: "253011", State: 2041,
				Values: map[string]float64{"RepulseTarget": 0},
			},
			"910000106": {
				ID: "910000106", OwnerWeapon: "253011", State: 2041,
				Values: map[string]float64{"SkillDamage": 0},
			},
		},
		Variants: map[string]map[int][]VariantEdit{
			"253011": {2041: {{Condition: 406, Segments: []VariantAnm{{
				Name: "600180", AnmID: "2", SkillProID: "910000106", TemplateSkillProID: "900000533",
			}}}}},
		},
	}
	if err := normalizeHitProperties(state); err != nil {
		t.Fatal(err)
	}
	base := state.HitProperties["900000533"].Values
	if base["SkillDamage"] != 8 || base["RepulseTarget"] != 0 || base["StandHurt"] != 42 {
		t.Fatalf("原属性字段级合并错误：%+v", base)
	}
	variant := state.HitProperties["910000106"].Values
	if variant["SkillDamage"] != 0 {
		t.Fatalf("分支显式零伤害丢失：%+v", variant)
	}
	if value, exists := variant["RepulseTarget"]; !exists || value != 0 || variant["StandHurt"] != 42 {
		t.Fatalf("分支未继承最新 canonical 模板字段：%+v", variant)
	}
	property := state.HitProperties["910000106"]
	if property.TemplateID != "900000533" || property.Condition != 406 || property.SegmentID != "2" || property.Action != "600180" || property.Source != "variant" || !property.Preallocated || len(property.References) != 1 {
		t.Fatalf("稀疏 canonical 覆盖丢失分支元数据：%+v", property)
	}
	before, err := json.Marshal(state.HitProperties)
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 3; attempt++ {
		if err := normalizeHitProperties(state); err != nil {
			t.Fatal(err)
		}
		after, err := json.Marshal(state.HitProperties)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(before) {
			t.Fatalf("继承归一化不幂等：之前 %s，之后 %s", before, after)
		}
	}
}

func TestNormalizeHitPropertiesTemplateChains(t *testing.T) {
	cases := []struct {
		name       string
		properties map[string]HitProperty
		want       map[string]map[string]float64
	}{
		{
			name: "多级模板与逐字段零值覆盖",
			properties: map[string]HitProperty{
				"root":    {Values: map[string]float64{"SkillDamage": 8, "RepulseTarget": 1, "StandHurt": 42}},
				"middle":  {TemplateID: "root", Values: map[string]float64{"RepulseTarget": 0}},
				"leaf":    {TemplateID: "middle", Values: map[string]float64{"SkillDamage": 0}},
				"sibling": {TemplateID: "middle"},
			},
			want: map[string]map[string]float64{
				"root":    {"SkillDamage": 8, "RepulseTarget": 1, "StandHurt": 42},
				"middle":  {"SkillDamage": 8, "RepulseTarget": 0, "StandHurt": 42},
				"leaf":    {"SkillDamage": 0, "RepulseTarget": 0, "StandHurt": 42},
				"sibling": {"SkillDamage": 8, "RepulseTarget": 0, "StandHurt": 42},
			},
		},
		{
			name: "循环模板与最近节点优先",
			properties: map[string]HitProperty{
				"a":    {TemplateID: "b", Values: map[string]float64{"SkillDamage": 0}},
				"b":    {TemplateID: "c", Values: map[string]float64{"SkillDamage": 9, "RepulseTarget": 0}},
				"c":    {TemplateID: "a", Values: map[string]float64{"StandHurt": 42}},
				"leaf": {TemplateID: "c"},
			},
			want: map[string]map[string]float64{
				"a":    {"SkillDamage": 0, "RepulseTarget": 0, "StandHurt": 42},
				"b":    {"SkillDamage": 9, "RepulseTarget": 0, "StandHurt": 42},
				"c":    {"SkillDamage": 0, "RepulseTarget": 0, "StandHurt": 42},
				"leaf": {"SkillDamage": 0, "RepulseTarget": 0, "StandHurt": 42},
			},
		},
		{
			name: "自身循环与缺失模板",
			properties: map[string]HitProperty{
				"self":    {TemplateID: "self", Values: map[string]float64{"SkillDamage": 0}},
				"missing": {TemplateID: "absent", Values: map[string]float64{"RepulseTarget": 0}},
			},
			want: map[string]map[string]float64{
				"self":    {"SkillDamage": 0},
				"missing": {"RepulseTarget": 0},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var baseline string
			for attempt := 0; attempt < 20; attempt++ {
				state := &weaponState{HitProperties: map[string]HitProperty{}}
				for id, property := range tc.properties {
					state.HitProperties[id] = property
				}
				for pass := 0; pass < 3; pass++ {
					if err := normalizeHitProperties(state); err != nil {
						t.Fatal(err)
					}
					for id, want := range tc.want {
						if got := state.HitProperties[id].Values; !reflect.DeepEqual(got, want) {
							t.Fatalf("第 %d 次归一化 %s 字段错误：得到 %v，期望 %v", pass+1, id, got, want)
						}
					}
					raw, err := json.Marshal(state.HitProperties)
					if err != nil {
						t.Fatal(err)
					}
					if baseline == "" {
						baseline = string(raw)
					} else if string(raw) != baseline {
						t.Fatalf("模板继承不确定或不幂等：得到 %s，期望 %s", raw, baseline)
					}
				}
			}
		})
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
			"253450": {2011: {{Condition: 406, Segments: []VariantAnm{{
				Name: "600180", SkillProID: id, TemplateSkillProID: "100", Damage: 3, DamagePresent: true,
			}}}}},
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
	segment := state.Variants["253450"][2011][0].Segments[0]
	if segment.Name != "600180" || segment.TemplateSkillProID != "" || segment.Damage != 0 || segment.DamagePresent {
		t.Fatalf("删除命中属性后动画或伤害元数据错误：%+v", segment)
	}
	raw, err := json.Marshal(segment)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if _, exists := fields["damage"]; exists {
		t.Fatalf("删除命中属性后仍序列化伤害覆盖：%s", raw)
	}
}

func TestProjectHitPropertiesToLegacyFiltersExactState(t *testing.T) {
	state := &weaponState{
		Drafts: map[string][]Rule{"253450": {{Stage: 1}, {Stage: 2}}},
		HitProperties: map[string]HitProperty{
			"900000001": {ID: "900000001", OwnerWeapon: "253450", Values: map[string]float64{"SkillDamage": 3}, References: []HitPropertyRef{{Weapon: "253450", State: 1}}},
			"910000001": {ID: "910000001", OwnerWeapon: "253450", Values: map[string]float64{"SkillDamage": 5}, References: []HitPropertyRef{{Weapon: "253450", State: 1, Condition: 406, Kind: "variant"}}},
			"900000002": {ID: "900000002", OwnerWeapon: "253450", Values: map[string]float64{"SkillDamage": 4}, References: []HitPropertyRef{{Weapon: "253450", State: 2}}},
		},
	}
	projectHitPropertiesToLegacy(state)
	if len(state.Drafts["253450"][0].Properties) != 1 || state.Drafts["253450"][0].Properties["900000001"]["SkillDamage"] != 3 {
		t.Fatalf("状态1投影错误：%+v", state.Drafts)
	}
	if _, exists := state.Drafts["253450"][0].Properties["910000001"]; exists {
		t.Fatalf("分支命中属性不应投影到基础规则：%+v", state.Drafts["253450"][0].Properties)
	}
	if len(state.Drafts["253450"][1].Properties) != 1 || state.Drafts["253450"][1].Properties["900000002"]["SkillDamage"] != 4 {
		t.Fatalf("状态2投影错误：%+v", state.Drafts)
	}
}

func TestProjectHitPropertiesToLegacyPreservesRuleParameters(t *testing.T) {
	state := &weaponState{
		Drafts: map[string][]Rule{"253011": {{Stage: 2071, Buff: 432, Level: 4, Duration: 8000}}},
		HitProperties: map[string]HitProperty{
			"900000549": {
				ID: "900000549", OwnerWeapon: "253011", State: 2071,
				Buff: 16, Values: map[string]float64{"SkillDamage": 20},
				References: []HitPropertyRef{{Weapon: "253011", State: 2071}},
			},
		},
	}
	projectHitPropertiesToLegacy(state)
	rule := state.Drafts["253011"][0]
	if rule.Buff != 432 || rule.Level != 4 || rule.Duration != 8000 {
		t.Fatalf("命中属性元数据不应覆盖规则参数：%+v", rule)
	}
}

func TestProjectHitPropertiesToLegacyFiltersActionValues(t *testing.T) {
	state := &weaponState{
		Drafts: map[string][]Rule{"253011": {{Stage: 2071}}},
		HitProperties: map[string]HitProperty{
			"900000549": {
				ID: "900000549", OwnerWeapon: "253011", State: 2071,
				Values:     map[string]float64{"SkillDamage": 2, "UStateLastCycle": 2500},
				References: []HitPropertyRef{{Weapon: "253011", State: 2071}},
			},
		},
	}
	projectHitPropertiesToLegacy(state)
	values := state.Drafts["253011"][0].Properties["900000549"]
	if values["SkillDamage"] != 2 {
		t.Fatalf("合法命中字段丢失：%+v", values)
	}
	if _, exists := values["UStateLastCycle"]; exists {
		t.Fatalf("动作参数不应投影到命中规则：%+v", values)
	}
}

func TestWeapon253011HitInheritanceRealArchive(t *testing.T) {
	installed := os.Getenv("OPENKFO_WEAPON_TEST_CLIENT")
	if installed == "" {
		t.Skip("set OPENKFO_WEAPON_TEST_CLIENT for real archive inheritance regression")
	}
	original, err := os.ReadFile(configPath(installed))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		after, err := os.ReadFile(configPath(installed))
		if err != nil || !bytes.Equal(original, after) {
			t.Errorf("production archive changed: %v", err)
		}
	})
	source, err := parseArchive(original)
	if err != nil {
		t.Fatal(err)
	}
	items, err := catalog(installed, false, false)
	if err != nil {
		t.Fatal(err)
	}
	info, err := inspect(source, items)
	if err != nil {
		t.Fatal(err)
	}
	weapon := findWeapon(info, 253011)
	if weapon == nil {
		t.Fatal("real archive lacks weapon 253011")
	}
	var stage Stage
	for _, candidate := range weapon.Stages {
		if candidate.State == "2041" {
			stage = candidate
			break
		}
	}
	if len(stage.Hits) == 0 || stage.Hits[0].ID != "900000533" {
		t.Fatalf("real first hit differs from reported reproduction: %+v", stage)
	}
	values := map[string]float64{}
	for key, text := range stage.Hits[0].Values {
		value, err := strconv.ParseFloat(text, 64)
		if err != nil {
			t.Fatalf("real field %s=%q: %v", key, text, err)
		}
		values[key] = value
	}
	t.Logf("real first hit %s: damage=%v (historical report=8), %d editable fields; original SHA256=%s", stage.Hits[0].ID, values["SkillDamage"], len(values), digest(original))
	if fixture := os.Getenv("OPENKFO_HIT_UI_FIXTURE"); fixture != "" {
		raw, err := json.Marshal(map[string]any{"weapon": weapon, "fields": propertyFields, "variant_bases": variantBases(info, "253011")})
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(fixture, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if text := os.Getenv("OPENKFO_HIT_TEST_DAMAGE"); text != "" {
		value, err := strconv.ParseFloat(text, 64)
		if err != nil {
			t.Fatal(err)
		}
		values["SkillDamage"] = value
		t.Logf("isolated workspace damage override=%v; source archive remains unchanged", value)
	}
	bases := variantBases(info, "253011")[2041]
	if len(bases) == 0 {
		t.Fatal("real base segments missing")
	}
	var segment VariantAnm
	for _, row := range bases {
		if row["skillproid"] == "900000533" {
			raw, err := json.Marshal(row)
			if err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(raw, &segment); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if segment.Name == "" {
		t.Fatal("first real hit segment missing")
	}
	segment.SkillProID, segment.TemplateSkillProID = "910000106", "900000533"
	segment.Damage, segment.DamagePresent = 0, true
	branchValues := copyFloatMap(values)
	branchValues["SkillDamage"] = 0
	payload := map[string]any{
		"weapon": map[string]any{"id": 253011},
		"rules":  []Rule{{Stage: stage.Stage, Level: 1, Duration: 3000, Properties: map[string]map[string]float64{"900000533": values, "910000106": branchValues}}},
		"hit_properties": map[string]HitProperty{
			"900000533": {ID: "900000533", OwnerWeapon: "253011", State: 2041, Values: values},
			"910000106": {ID: "910000106", OwnerWeapon: "253011", State: 2041, TemplateID: "900000533", Values: branchValues},
		},
		"variants": map[string][]VariantEdit{"2041": {{Condition: 406, Segments: []VariantAnm{segment}}}},
	}
	folder, client := t.TempDir(), t.TempDir()
	if err = os.MkdirAll(filepath.Join(client, "Data"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(configPath(client), original, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(folder, "baseline.spf2"), original, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = saveWeaponWorkspace(folder, 253011, payload); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadWeaponWorkspace(folder, 253011)
	if err != nil {
		t.Fatal(err)
	}
	state := &weaponState{Drafts: map[string][]Rule{}, Applied: map[string][]Rule{}, PropertyClones: map[string]map[string]string{"253011": {cloneKey(stage.Stage, "900000533"): "900000533"}}}
	if err = mergeWorkspaceIntoState(loaded["payload"].(map[string]any), "253011", state); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(state.HitProperties["900000533"].Values, values) || !reflect.DeepEqual(state.HitProperties["910000106"].Values, branchValues) {
		t.Fatalf("workspace full values lost: %+v", state.HitProperties)
	}
	state.Applied["253011"] = state.Drafts["253011"]
	entry := &clientBaseline{Directory: client, File: "baseline.spf2", SourceHash: digest(original), AppliedHash: digest(original)}
	var first []byte
	for pass := 0; pass < 2; pass++ {
		prepared, err := prepareClient(entry, folder, state, state.Applied, info)
		if err != nil {
			t.Fatal(err)
		}
		out, err := parseArchive(prepared.Data)
		if err != nil {
			t.Fatal(err)
		}
		if err = out.verify(); err != nil {
			t.Fatal(err)
		}
		if pass == 0 {
			strictChangedXML(t, source, out, changedArchiveEntries(t, source, out))
		}
		finalItems, err := itemsFromText(client, mustArchiveText(t, out, "item.txt"), false, false)
		if err != nil {
			t.Fatal(err)
		}
		finalInfo, err := inspect(out, finalItems)
		if err != nil {
			t.Fatal(err)
		}
		finalWeapon := findWeapon(finalInfo, 253011)
		if finalWeapon == nil {
			t.Fatal("final weapon missing")
		}
		var finalStage Stage
		for _, candidate := range finalWeapon.Stages {
			if candidate.State == "2041" {
				finalStage = candidate
				break
			}
		}
		foundBase, foundBranch := false, false
		for _, hit := range finalStage.Hits {
			if hit.Variant == "" {
				foundBase = true
			} else if hit.Variant == "406" {
				foundBranch = true
			} else {
				continue
			}
			want := values
			if hit.Variant == "406" {
				want = branchValues
			}
			for key, expected := range want {
				actual, err := strconv.ParseFloat(hit.Values[key], 64)
				if err != nil || actual != expected {
					t.Fatalf("final hit %s field %s=%q, want %v", hit.ID, key, hit.Values[key], expected)
				}
			}
			t.Logf("pass %d final hit=%s condition=%s fields=%d damage=%s", pass+1, hit.ID, hit.Variant, len(hit.Values), hit.Values["SkillDamage"])
		}
		if !foundBase || !foundBranch {
			t.Fatalf("base or branch missing: %+v", finalStage.Hits)
		}
		if pass == 0 {
			first = append([]byte(nil), prepared.Data...)
		} else if !bytes.Equal(first, prepared.Data) {
			t.Fatal("repeated apply changed archive bytes")
		}
		if err = commitClient(prepared, folder); err != nil {
			t.Fatal(err)
		}
	}
}
