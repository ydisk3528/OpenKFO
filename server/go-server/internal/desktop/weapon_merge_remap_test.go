package desktop

import (
	"strings"
	"testing"
)

// 导入时的换号：目标端已占用、且内容不同的号必须换新号；没占用、或内容完全一致
// （重复导入同一个包）则保持原号，保证幂等。
func TestPlanMergePropertyIDsRenumbersOnlyRealCollisions(t *testing.T) {
	target := `<SkillProperty>` +
		`<PropertyItem SkillProId="900000070" SkillDamage="1"></PropertyItem>` +
		`<PropertyItem SkillProId="2204196" SkillDamage="9"></PropertyItem>` +
		`</SkillProperty>`
	node := func(id, damage string) string {
		return `<PropertyItem SkillProId="` + id + `" SkillDamage="` + damage + `"></PropertyItem>`
	}
	manifest := &mergeManifest{Weapons: []mergeWeapon{{
		ID: 253300,
		SkillProperties: []string{
			node("900000070", "55"), // 撞号且内容不同 → 必须换
			node("900000071", "7"),  // 目标端没有 → 保留
			node("2204196", "9"),    // 撞号但内容一致 → 保留（幂等）
		},
	}}}

	remap, err := planMergePropertyIDs(target, manifest)
	if err != nil {
		t.Fatalf("规划失败：%v", err)
	}
	if len(remap) != 1 {
		t.Fatalf("只该换一个号，实际 %v", remap)
	}
	next, ok := remap["900000070"]
	if !ok {
		t.Fatalf("900000070 内容不同，必须换号：%v", remap)
	}
	if !strings.HasPrefix(next, "800000") {
		t.Fatalf("新号应当落在自建号段，实际 %q", next)
	}
	if want := freshMergePropertyID(map[string]bool{"900000070": true}); next != want {
		t.Fatalf("新号应当避开双方用号，实际 %q 期望 %q", next, want)
	}
	if _, changed := remap["900000071"]; changed {
		t.Fatal("目标端没有的号不该换")
	}
	if _, changed := remap["2204196"]; changed {
		t.Fatal("内容一致的号不该换（重复导入要幂等）")
	}
}

// 换号要同时改到动作块里的 skillproid（含 CustomStateSwitch 上的）和连招限制里的
// Skill / PrevSkill / CurSkill / ExceedSkillProID。
func TestRewritePropertyAndSkillReferences(t *testing.T) {
	remap := map[string]string{"900000070": "800000001"}
	block := `<AnmDesc id="196" skillproid="900000070">` +
		`<Anm id="1" skillproid="900000070"></Anm>` +
		`<CustomStateSwitch keycode="9" nextstate="2016" skillproid="900000070"></CustomStateSwitch>` +
		`</AnmDesc>`
	got := rewritePropertyRefs(block, remap)
	if strings.Contains(got, `"900000070"`) {
		t.Fatalf("动作块里的引用没换干净：%s", got)
	}
	if strings.Count(got, `"800000001"`) != 3 {
		t.Fatalf("应当换 3 处，实际 %s", got)
	}

	rule := `<MaxComboForSkill Skill="900000070" MaxCombo="2"/>` +
		`<MaxComboForSkill Skill="900000071" MaxCombo="1" ExceedSkillProID="900000070"/>` +
		`<BlackListItem PrevSkill="900000070" CurSkill="900000071"/>` +
		`<WhiteListItem PrevSkill="900000071" CurSkill="900000070"/>`
	rules := rewriteSkillNumbers(rule, remap)
	// 命中目标的引用共 4 处：MaxComboForSkill.Skill、ExceedSkillProID、黑名单 PrevSkill、白名单 CurSkill
	if strings.Count(rules, `"800000001"`) != 4 {
		t.Fatalf("连招限制里的 4 处引用都该换，实际：%s", rules)
	}
	if !strings.Contains(rules, `PrevSkill="800000001" CurSkill="900000071"`) {
		t.Fatalf("只换命中目标的号，别的号不能动：%s", rules)
	}
	// 属性节点自己的 SkillProId 也要换（同一个属性名，大小写不敏感）。
	node := rewritePropertyRefs(`<PropertyItem SkillProId="900000070" SkillDamage="1"></PropertyItem>`, remap)
	if !strings.Contains(node, `SkillProId="800000001"`) {
		t.Fatalf("属性节点自己的号没换：%s", node)
	}
	if rewritePropertyRefs(block, nil) != block {
		t.Fatal("空映射不该改动文本")
	}
}

// 出厂自带的 PropertyItem 是自闭合写法（`... />`），编辑器克隆出来的是成对写法。
// 两种都必须被认出来：只认成对写法时，出厂节点既导不进包，也不会被当成「目标端
// 已存在」，于是导入会追加一条同号节点（同号两条 → 唯一性校验直接挂）。
func TestPropertyNodePatternMatchesBothForms(t *testing.T) {
	text := `<SkillProperty>` +
		`<PropertyItem SkillProId="809103" SkillDamage="1" />` +
		`<PropertyItem SkillProId="900000064" SkillDamage="2"></PropertyItem>` +
		`</SkillProperty>`

	selfClosing, ok := propertyNodeText(text, "809103")
	if !ok || !strings.Contains(selfClosing, "809103") {
		t.Fatalf("自闭合节点没被认出来：%q ok=%v", selfClosing, ok)
	}
	paired, ok := propertyNodeText(text, "900000064")
	if !ok || strings.Contains(paired, "809103") {
		t.Fatalf("成对节点没被认出来：%q ok=%v", paired, ok)
	}
	if _, ok := propertyNodeText(text, "111"); ok {
		t.Fatal("不存在的号不该被认出")
	}

	// 自闭合节点也要参与撞号判定：内容不同必须换号。
	manifest := &mergeManifest{Weapons: []mergeWeapon{{ID: 1,
		SkillProperties: []string{`<PropertyItem SkillProId="809103" SkillDamage="9" />`}}}}
	remap, err := planMergePropertyIDs(text, manifest)
	if err != nil {
		t.Fatalf("规划失败：%v", err)
	}
	if _, changed := remap["809103"]; !changed {
		t.Fatalf("自闭合节点撞号也必须换号：%v", remap)
	}

	// 内容一致则保持原号，重复导入保持幂等。
	manifest.Weapons[0].SkillProperties = []string{`<PropertyItem SkillProId="809103" SkillDamage="1" />`}
	remap, err = planMergePropertyIDs(text, manifest)
	if err != nil {
		t.Fatalf("规划失败：%v", err)
	}
	if len(remap) != 0 {
		t.Fatalf("内容一致不该换号：%v", remap)
	}
}

// 重复导入同一个包要幂等：上一次导入把 X 换成了 Y，这次 X 在目标端当然不存在，
// 必须认出「同内容的 Y」并复用，而不是再插一条（否则号会来回翻、节点越积越多）。
// 只在编辑器自己发的号段里找，免得把包里的节点接到出厂数据上。
func TestPlanMergePropertyIDsReusesRenumberedNode(t *testing.T) {
	// 上一次导入的结果：原来的 900000064 被换成了 800000041。
	target := `<SkillProperty>` +
		`<PropertyItem SkillProId="800000041" SkillDamage="55"></PropertyItem>` +
		`<PropertyItem SkillProId="809103" SkillDamage="7" />` +
		`</SkillProperty>`
	incomingNode := `<PropertyItem SkillProId="900000064" SkillDamage="55"></PropertyItem>`
	manifest := &mergeManifest{Weapons: []mergeWeapon{{ID: 1, SkillProperties: []string{incomingNode}}}}

	remap, err := planMergePropertyIDs(target, manifest)
	if err != nil {
		t.Fatalf("规划失败：%v", err)
	}
	if remap["900000064"] != "800000041" {
		t.Fatalf("应当复用上次换号留下的同内容节点：%v", remap)
	}

	// 出厂节点（号段更小）不参与复用：内容相同也保持原号，避免把节点接到出厂数据。
	donor := &mergeManifest{Weapons: []mergeWeapon{{ID: 1,
		SkillProperties: []string{`<PropertyItem SkillProId="700000001" SkillDamage="7" />`}}}}
	remap, err = planMergePropertyIDs(target, donor)
	if err != nil {
		t.Fatalf("规划失败：%v", err)
	}
	if len(remap) != 0 {
		t.Fatalf("出厂节点不该被复用：%v", remap)
	}
}

// 目标端本来就有个无关的同号节点（撞号、内容不同），而我们的节点上一轮已经以
// 别的号放进来了：再导同一个包必须复用那一条，不能因为「同号已被占用」就再分
// 一个新号（否则每次导入都多一条，号还会 041 → 042 → 043 一路翻下去）。
func TestPlanMergePropertyIDsReusesNodeWhenNumberIsTaken(t *testing.T) {
	target := `<SkillProperty>` +
		`<PropertyItem SkillProId="900000064" SkillDamage="1"></PropertyItem>` + // 目标端自己的，内容无关
		`<PropertyItem SkillProId="800000041" SkillDamage="55"></PropertyItem>` + // 上一轮导入我们的节点
		`</SkillProperty>`
	incomingNode := `<PropertyItem SkillProId="900000064" SkillDamage="55"></PropertyItem>`
	manifest := &mergeManifest{Weapons: []mergeWeapon{{ID: 1, SkillProperties: []string{incomingNode}}}}

	remap, err := planMergePropertyIDs(target, manifest)
	if err != nil {
		t.Fatalf("规划失败：%v", err)
	}
	if remap["900000064"] != "800000041" {
		t.Fatalf("撞号且内容不同时，应当复用同内容的自建节点：%v", remap)
	}
}
