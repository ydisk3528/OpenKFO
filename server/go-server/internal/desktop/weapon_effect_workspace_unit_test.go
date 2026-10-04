package desktop

import (
	"reflect"
	"strings"
	"testing"
)

func TestEffectWorkspaceAuthority(t *testing.T) {
	const key = "253450"
	rows := []EffectRow{{EffectID: "100", File: "historical_resource"}}
	effects := []StageEffect{{Kind: "effect", EffectID: "100", Start: 2}}
	state := weaponState{EffectRows: map[string][]EffectRow{key: rows}, StageEffects: map[string]map[int][]StageEffect{key: {2011: effects, 2012: effects}}}
	for _, payload := range []map[string]any{{}, {"effect_rows": nil, "stage_effects": nil}, {"stage_effects": map[string][]StageEffect{}}} {
		if err := mergeWorkspaceIntoState(payload, key, &state); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(state.EffectRows[key], rows) || !reflect.DeepEqual(state.StageEffects[key][2011], effects) {
			t.Fatal("继承字段覆盖了作者编辑")
		}
	}
	if err := mergeWorkspaceIntoState(map[string]any{"effect_rows": []EffectRow{}, "stage_effects": map[string][]StageEffect{"2011": {}}}, key, &state); err != nil {
		t.Fatal(err)
	}
	if got, ok := state.EffectRows[key]; !ok || got == nil || len(got) != 0 {
		t.Fatal("空登记必须保留权威空数组")
	}
	if got, ok := state.StageEffects[key][2011]; !ok || got == nil || len(got) != 0 {
		t.Fatal("空状态特效必须保留删除标记")
	}
	if !reflect.DeepEqual(state.StageEffects[key][2012], effects) {
		t.Fatal("缺失状态应继承")
	}
}

func TestEffectValuesRejectUnsafeInput(t *testing.T) {
	for _, field := range []string{"id", "kind", "bind", "index", "break", "frame"} {
		t.Run(field, func(t *testing.T) {
			e := StageEffect{Kind: "effect", EffectID: "100", Start: 3}
			switch field {
			case "id":
				e.EffectID = `100"><Audio />`
			case "kind":
				e.Kind = "other"
			case "bind":
				e.BindType = `1" x="2`
			case "index":
				e.BindIndex = "-1"
			case "break":
				e.Break = "4294967296"
			case "frame":
				e.End = -1
			}
			if _, _, err := rewriteStageEffects(`<AnmDesc id="1"></AnmDesc>`, []StageEffect{e}); err == nil {
				t.Fatal("应用未拒绝无效属性")
			}
		})
	}
	for _, file := range []string{"../bad", `..\bad`, "a/../b", `C:\bad`, "/bad", `a" b="c`, "a\x00b", "a."} {
		if err := validateEffectRows("", []EffectRow{{EffectID: "100", File: file}}); err == nil {
			t.Fatalf("未拒绝路径 %q", file)
		}
	}
	if err := validateEffectRows("", []EffectRow{{EffectID: "100", File: "missing_native"}}); err != nil {
		t.Fatal(err)
	}
	if err := validateEffectRows("", []EffectRow{{EffectID: "100", File: "a"}, {EffectID: "100", File: "b"}}); err == nil {
		t.Fatal("未拒绝重复登记")
	}
}

func TestAppliedEffectRowsHistoricalResources(t *testing.T) {
	a, _ := variantFixture(t, map[string]string{"acteffect.xml": `<ActEffect><WeaponEffect ItemID="253451"><EffectFile EffectId="100" File="missing_native" /></WeaponEffect></ActEffect>`})
	client := t.TempDir()
	if err := validateAppliedEffectRows(a, client, []EffectRow{{EffectID: "100", File: "missing_native"}}); err != nil {
		t.Fatal(err)
	}
	if err := validateAppliedEffectRows(a, client, []EffectRow{{EffectID: "100", File: "missing_new"}}); err == nil {
		t.Fatal("新资源缺失未被拒绝")
	}
	if err := validateAppliedEffectRows(a, client, []EffectRow{{EffectID: "101", File: "missing_native"}}); err == nil {
		t.Fatal("新编号不能借历史路径绕过校验")
	}
}

func TestStageEffectsRejectAmbiguousBase(t *testing.T) {
	for _, text := range []string{
		`<AnmInfo><AnmDesc id="1"><Condition><Ustate id="406" /></Condition></AnmDesc></AnmInfo>`,
		`<AnmInfo><AnmDesc id="1"></AnmDesc><AnmDesc id="1"></AnmDesc></AnmInfo>`,
		`<AnmInfo><AnmDesc id="1"><Condition><Unknown /></Condition></AnmDesc></AnmInfo>`,
	} {
		a, _ := variantFixture(t, map[string]string{"animation/2001.xml": text})
		state := weaponState{StageEffects: map[string]map[int][]StageEffect{"253450": {2011: {}}}}
		if _, err := applyStageEffects(a, &state); err == nil {
			t.Fatal("不安全基础块未被拒绝")
		}
	}
}

func TestStageEffectsCloneAllSharedBranches(t *testing.T) {
	base := `<AnmDesc id="1"><Effect frame="0" effectid="100" /><Anm id="1" skillproid="100" /></AnmDesc>`
	branch := `<AnmDesc id="1"><Condition><Ustate id="406" /></Condition><Effect frame="0" effectid="200" /><Anm id="1" skillproid="100" /></AnmDesc>`
	a, _ := variantFixture(t, map[string]string{
		"itemact.txt":        "ID\tName\t2011\n253450\tx\t2001001\n253451\ty\t2001001\n",
		"animation/2001.xml": "<AnmInfo>" + base + branch + "</AnmInfo>",
		"skillproperty.xml":  `<SkillProperty><PropertyItem SkillProId="100"/><PropertyItem SkillProId="2001001"/></SkillProperty>`,
	})
	state := weaponState{StageEffects: map[string]map[int][]StageEffect{"253450": {2011: {{Kind: "effect", EffectID: "600120", Start: 3}}}}}
	edited, err := applyStageEffects(a, &state)
	if err != nil {
		t.Fatal(err)
	}
	table, _ := edited.text("itemact.txt")
	action, _ := itemactAction(table, "253450", 2011)
	if action == "2001001" {
		t.Fatal("共享基础块未克隆")
	}
	index, _, err := stageEffectIndex(edited)
	if err != nil {
		t.Fatal(err)
	}
	if len(index[actionKey(action)]) != 2 {
		t.Fatal("克隆丢失分支")
	}
	for _, b := range index[actionKey(action)] {
		if b.condition == "406" && retitleBlock(b.original, 1) != branch {
			t.Fatal("克隆分支引用或内容变化")
		}
	}
	text, _ := edited.text("animation/2001.xml")
	if !strings.Contains(text, base) || !strings.Contains(text, branch) {
		t.Fatal("共享源块变化")
	}
	properties, err := edited.xml("skillproperty.xml")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	properties.walk(func(n *xmlNode) {
		if n.tag == "PropertyItem" && n.get("SkillProId") == action {
			found = true
		}
	})
	if !found {
		t.Fatal("新动作缺少入口属性")
	}
}

func TestStageEffectsPreserveVariants(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(map[bool]string{false: "private", true: "shared"}[shared], func(t *testing.T) {
			table := "ID\tName\t2011\t2012\n253450\tx\t2001001\t2001002\n"
			if shared {
				table += "253451\ty\t2001001\t0\n"
			}
			a, items := variantFixture(t, map[string]string{"itemact.txt": table, "skillproperty.xml": `<SkillProperty><PropertyItem SkillProId="100"/><PropertyItem SkillProId="2001001"/></SkillProperty>`})
			state := weaponState{Variants: map[string]map[int][]VariantEdit{"253450": {2011: {{Condition: 406}}}}, StageEffects: map[string]map[int][]StageEffect{"253450": {2011: {{Kind: "effect", EffectID: "600120", Start: 3}}}}}
			branched, err := applyVariants(a, &state, items)
			if err != nil {
				t.Fatal(err)
			}
			if err = validateStageEffects(branched, "253450", state.StageEffects["253450"]); err != nil {
				t.Fatal(err)
			}
			edited, err := applyStageEffects(branched, &state)
			if err != nil {
				t.Fatal(err)
			}
			table, _ = edited.text("itemact.txt")
			action, _ := itemactAction(table, "253450", 2011)
			index, _, err := stageEffectIndex(edited)
			if err != nil {
				t.Fatal(err)
			}
			blocks := index[actionKey(action)]
			if len(blocks) != 2 {
				t.Fatalf("分支丢失: %d", len(blocks))
			}
			for _, b := range blocks {
				if b.condition == "" && !strings.Contains(b.original, "600120") {
					t.Fatal("基础块缺少特效")
				}
				if b.condition != "" && strings.Contains(b.original, "600120") {
					t.Fatal("条件分支被污染")
				}
			}
			if shared {
				other, _ := itemactAction(table, "253451", 2011)
				if other != "2001001" {
					t.Fatal("供体引用被修改")
				}
				for _, b := range index[actionKey(other)] {
					if strings.Contains(b.original, "600120") {
						t.Fatal("供体块被修改")
					}
				}
			}
			again, err := applyStageEffects(edited, &state)
			if err != nil {
				t.Fatal(err)
			}
			before, _ := edited.text("animation/2001.xml")
			after, _ := again.text("animation/2001.xml")
			if before != after {
				t.Fatal("重复应用不幂等")
			}
		})
	}
}
