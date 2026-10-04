package desktop

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestWeaponMetadataTypes(t *testing.T) {
	for index, name := range []string{"刀类", "剑类", "长柄", "拳套", "拳脚", "重型", "奇门"} {
		if got := weaponType(Item{Fields: []string{"25", "1", strconv.Itoa(index + 1)}}); got != name {
			t.Fatal(got, name)
		}
	}
	if weaponType(Item{}) != "未分类" || weaponType(Item{Fields: []string{"25", "1", "42"}}) != "类型 42" {
		t.Fatal("unknown type lost")
	}
}

func TestInstalledWeaponMetadata(t *testing.T) {
	client := os.Getenv("OPENKFO_WEAPON_TEST_CLIENT")
	if client == "" {
		t.Skip("client fixture required")
	}
	items, err := Catalog(client)
	if err != nil {
		t.Fatal(err)
	}
	a, err := loadArchive(filepath.Join(client, "Data/config.spf2"))
	if err != nil {
		t.Fatal(err)
	}
	info, err := inspect(a, items)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[int]Item{}
	for _, item := range items {
		if item.Kind == 25 {
			byID[int(item.ID)] = item
		}
	}
	icons := 0
	for _, w := range info.weapons {
		item := byID[w.ID]
		if w.Name != item.Name || w.Icon != item.Icon || w.Description != item.Description || w.Type != weaponType(item) {
			t.Fatalf("metadata mismatch %d", w.ID)
		}
		if w.Icon != "" {
			if _, err := os.Stat(w.Icon); err != nil {
				t.Fatal(err)
			}
			icons++
		}
	}
	if len(info.weapons) == 0 || icons == 0 {
		t.Fatal("missing weapons/icons")
	}
	t.Logf("weapons=%d valid_icons=%d", len(info.weapons), icons)
}

func TestLightWeaponListUsesStagePlaceholders(t *testing.T) {
	a, err := parseArchive(synthArchive(t, map[string][]byte{
		"itemact": []byte("id\tname\t2011\t2012\t3001\n100\t甲\t2001001\t0\t2001002\n101\t乙\t0\t2001003\t\n"),
	}))
	if err != nil {
		t.Fatal(err)
	}
	a.entries["itemact.txt"] = a.entries["itemact"]
	delete(a.entries, "itemact")
	items := []Item{
		{ID: 100, Kind: 25, Name: "甲", Icon: "甲.png", Description: "说明", Fields: []string{"25", "1", "2", "", "", "", "", "model.dff"}},
		{ID: 101, Kind: 25, Name: "乙", Fields: []string{"25", "1", "3", "", "", "", "", "model2.dff"}},
	}
	weapons, err := lightWeaponList(a, items)
	if err != nil {
		t.Fatal(err)
	}
	if len(weapons) != 2 || weapons[0].StageCount != 2 || len(weapons[0].Stages) != 2 {
		t.Fatalf("轻量列表阶段占位错误：%+v", weapons)
	}
	if weapons[0].Stages[0].State != "2011" || weapons[0].Stages[0].Action != "2001001" {
		t.Fatalf("阶段占位没有保留状态和动作：%+v", weapons[0].Stages[0])
	}
	if weapons[1].StageCount != 1 || weapons[1].Stages[0].State != "2012" {
		t.Fatalf("空动作列不应计入阶段：%+v", weapons[1])
	}
}

func TestDetailProjectionStateKeepsOtherPlansOutOfProjection(t *testing.T) {
	state := &weaponState{
		Drafts:       map[string][]Rule{"100": {{Stage: 1}}, "101": {{Stage: 2}}},
		Applied:      map[string][]Rule{"100": {{Stage: 1}}, "101": {{Stage: 2}}},
		Remaps:       map[string]map[int]*StageRemap{"100": {2011: {}}, "101": {2012: {}}},
		Variants:     map[string]map[int][]VariantEdit{"100": {2011: {}}, "101": {2012: {}}},
		StageEffects: map[string]map[int][]StageEffect{"100": {2011: {}}, "101": {2012: {}}},
	}
	projected, err := detailProjectionState(state, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(projected.Remaps) != 1 || projected.Remaps["100"] == nil || len(projected.Remaps["101"]) != 0 {
		t.Fatalf("详情 remap 投影泄漏其它武器：%v", projected.Remaps)
	}
	if len(projected.Variants) != 2 || len(projected.StageEffects) != 1 {
		t.Fatalf("详情应保留全局分支编号占用并裁剪特效：variants=%v effects=%v", projected.Variants, projected.StageEffects)
	}
	if len(projected.Drafts) != 2 || len(projected.Applied) != 2 {
		t.Fatalf("详情投影不应丢失全局方案：drafts=%v applied=%v", projected.Drafts, projected.Applied)
	}
	if len(state.Remaps) != 2 || len(state.Variants) != 2 {
		t.Fatal("详情投影修改了原始状态")
	}
}

func TestWeaponDetailReusesComboResponseShapes(t *testing.T) {
	a, err := parseArchive(synthArchive(t, map[string][]byte{
		"delayact": []byte(`<ItemList><Item WeaponTypeId = "100" OldState="2011" NewState="2012" KeyInput="1" StartPart="1"/></ItemList><KeyInputList></KeyInputList>`),
		"rules":    []byte(`<ComboRuleList><ComboRule Weapon="100"><MaxComboForSkill Skill="2001001" MaxCombo="3"/></ComboRule></ComboRuleList>`),
	}))
	if err != nil {
		t.Fatal(err)
	}
	a.entries["delayacttable.xml"] = a.entries["delayact"]
	a.entries["comborule.xml"] = a.entries["rules"]
	info := &inspection{weapons: []Weapon{{ID: 100, Stages: []Stage{{State: "2011", Label: "第一段", Action: "2001001"}}}}, blocks: map[string][]block{}, properties: map[string][]*xmlNode{}}
	state := &weaponState{}
	chain := weaponComboChainView(a, info, state, 100, []string{"910000100"}, "rev")
	for _, key := range []string{"weapon", "chain", "dead_ends", "frame_switches", "frame_switches_saved", "counters", "counters_saved", "block_elements", "block_elements_saved", "variants", "variant_occupied_ids", "variant_skillpro_min", "variant_skillpro_max", "variants_saved", "variant_bases", "block_element_groups", "frame_keys", "keys", "revision"} {
		if _, ok := chain[key]; !ok {
			t.Fatalf("chain_info 缺少旧响应字段 %q：%v", key, chain)
		}
	}
	if chain["variant_occupied_ids"].([]string)[0] != "910000100" {
		t.Fatal("chain_info 没有复用详情阶段的 variant 占用结果")
	}
	rules := weaponComboRuleView(a, info, state, 100, "rev")
	for _, key := range []string{"weapon", "rules", "shipped", "official", "overridden", "editable", "reason", "skills", "unknown_skills", "revision"} {
		if _, ok := rules[key]; !ok {
			t.Fatalf("combo_rule_info 缺少旧响应字段 %q：%v", key, rules)
		}
	}
	if rules["weapon"] != "100" || rules["revision"] != "rev" {
		t.Fatalf("combo_rule_info 基础字段错误：%v", rules)
	}
}

func TestAppliedSkillOptionsKeepsGlobalPlansWithFilteredInfo(t *testing.T) {
	info := &inspection{
		weapons:    []Weapon{{ID: 100, BuffIDs: []int{0}, Allowed: map[string][]int{"SkillDamage": {0, 1}}, Stages: []Stage{{Stage: 1, State: "2011", Action: "2001001", PropertyIDs: []string{"1830110"}, Supported: true}}}},
		blocks:     map[string][]block{},
		properties: map[string][]*xmlNode{"1830110": {{}}},
	}
	state := &weaponState{Applied: map[string][]Rule{
		"100": {{Stage: 1, Buff: 0, Level: 1, Duration: 3000, Properties: map[string]map[string]float64{"1830110": {"SkillDamage": 1}}}},
		"101": {{Stage: 1, Buff: 0, Level: 1, Duration: 3000}},
	}}
	options := appliedSkillOptions(info, state, "100", []SkillOption{{Skill: "1830110", State: "2011"}})
	if len(options) != 1 || options[0].Skill == "1830110" {
		t.Fatalf("过滤详情不应因其它武器 Applied 规则失败：%v", options)
	}
}
