package desktop

import (
	"strings"
	"testing"
)

// 命中属性克隆编号必须「认得出同一件事」：同一个 (武器, 状态, 旧属性号) 在任何
// 一次渲染里都要拿到同一个新号，否则连招限制里的被动编号会随别的武器的增删而漂移，
// 客户端按号匹配，永不命中（2026-09-27 实测：整套黑白名单因此全空转）。
func TestCloneIDsStayPutWhenOtherWeaponsChange(t *testing.T) {
	info := &inspection{
		weapons: []Weapon{
			{ID: 253300, Name: "王八拳", BuffIDs: []int{0}, Stages: []Stage{
				{Stage: 1, State: "2011", Action: "2001936", PropertyIDs: []string{"1830110"}, Supported: true},
				{Stage: 2021, State: "2021", Action: "2001934", PropertyIDs: []string{"1930210"}, Supported: true},
			}},
			{ID: 253700, Name: "混沌宇宙", BuffIDs: []int{0}, Stages: []Stage{
				{Stage: 1, State: "2011", Action: "2001500", PropertyIDs: []string{"3101"}, Supported: true},
			}},
		},
		properties: map[string][]*xmlNode{},
	}
	rule := func(stage int, ref string) Rule {
		return Rule{Stage: stage, Buff: 0, Level: 1, Duration: 3000,
			Properties: map[string]map[string]float64{ref: {"SkillDamage": 1}}}
	}

	clones := map[string]map[string]string{}
	first := map[string][]Rule{"253300": {rule(1, "1830110"), rule(2021, "1930210")}}
	if err := assignCloneIDs(info, first, clones); err != nil {
		t.Fatalf("首次分配失败：%v", err)
	}
	before := map[string]string{
		"stage1":    clones["253300"][cloneKey(1, "1830110")],
		"stage2021": clones["253300"][cloneKey(2021, "1930210")],
	}
	for label, id := range before {
		if id == "" {
			t.Fatalf("%s 没有分到编号", label)
		}
	}

	// 给另一把武器加规则（以及把它的状态排到前面）——旧实现会整体重排发号。
	grown := map[string][]Rule{
		"253300": first["253300"],
		"253700": {rule(1, "3101")},
	}
	if err := assignCloneIDs(info, grown, clones); err != nil {
		t.Fatalf("追加武器后分配失败：%v", err)
	}
	if got := clones["253300"][cloneKey(1, "1830110")]; got != before["stage1"] {
		t.Fatalf("旧武器的编号漂了：%s -> %s", before["stage1"], got)
	}
	if got := clones["253300"][cloneKey(2021, "1930210")]; got != before["stage2021"] {
		t.Fatalf("旧武器的编号漂了：%s -> %s", before["stage2021"], got)
	}
	if clones["253700"][cloneKey(1, "3101")] == "" {
		t.Fatal("新武器没有分到编号")
	}

	// 把新武器删掉也不能影响已有编号。
	if err := assignCloneIDs(info, first, clones); err != nil {
		t.Fatalf("删减后分配失败：%v", err)
	}
	if got := clones["253300"][cloneKey(1, "1830110")]; got != before["stage1"] {
		t.Fatalf("删掉别的武器后编号漂了：%s -> %s", before["stage1"], got)
	}
}

// 发号要跳过归档里已经被占用的号（基线里可能留着历史克隆节点），
// 并且要重新处理「钉住的号已被占用」这种情况 —— 否则会追加出重复的 SkillProId 节点。
func TestCloneIDAllocationReservesAllPinnedNumbers(t *testing.T) {
	info := &inspection{
		weapons: []Weapon{{ID: 253300, Name: "王八拳", BuffIDs: []int{0}, Stages: []Stage{
			{Stage: 1, State: "2011", Action: "2001936", PropertyIDs: []string{"1830110"}, Supported: true},
		}}},
		properties: map[string][]*xmlNode{},
	}
	plans := map[string][]Rule{"253300": {{Stage: 1, Buff: 0, Level: 1, Duration: 3000,
		Properties: map[string]map[string]float64{"1830110": {"SkillDamage": 1}}}}}
	clones := map[string]map[string]string{
		"253700": {cloneKey(1, "3101"): "900000000"},
	}
	if err := assignCloneIDs(info, plans, clones); err != nil {
		t.Fatalf("分配失败：%v", err)
	}
	if got := clones["253300"][cloneKey(1, "1830110")]; got != "900000001" {
		t.Fatalf("应当避开计划外的固定号，实际 %s", got)
	}
}

func TestCloneIDAllocationRejectsPinnedNumberCollision(t *testing.T) {
	info := &inspection{
		weapons: []Weapon{
			{ID: 253300, Name: "王八拳", BuffIDs: []int{0}, Stages: []Stage{
				{Stage: 1, State: "2011", Action: "2001936", PropertyIDs: []string{"1830110"}, Supported: true},
			}},
			{ID: 253700, Name: "混沌宇宙", BuffIDs: []int{0}, Stages: []Stage{
				{Stage: 1, State: "2011", Action: "2001500", PropertyIDs: []string{"3101"}, Supported: true},
			}},
		},
		properties: map[string][]*xmlNode{},
	}
	plans := map[string][]Rule{
		"253300": {{Stage: 1, Buff: 0, Level: 1, Duration: 3000,
			Properties: map[string]map[string]float64{"1830110": {"SkillDamage": 1}}}},
		"253700": {{Stage: 1, Buff: 0, Level: 1, Duration: 3000,
			Properties: map[string]map[string]float64{"3101": {"SkillDamage": 1}}}},
	}
	clones := map[string]map[string]string{
		"253300": {cloneKey(1, "1830110"): "900000000"},
		"253700": {cloneKey(1, "3101"): "900000000"},
	}
	if err := assignCloneIDs(info, plans, clones); err == nil || !strings.Contains(err.Error(), "900000000") {
		t.Fatalf("两个 active 输出使用同一固定号时应拒绝，实际错误：%v", err)
	}
	if got := clones["253300"][cloneKey(1, "1830110")]; got != "900000000" {
		t.Fatalf("重复校验失败后不应改写固定号，实际 %s", got)
	}
}

func TestCloneIDAllocationAllowsOneActiveAndOneInactivePinnedNumber(t *testing.T) {
	info := &inspection{
		weapons: []Weapon{{ID: 253300, Name: "王八拳", BuffIDs: []int{0}, Stages: []Stage{
			{Stage: 1, State: "2011", Action: "2001936", PropertyIDs: []string{"1830110"}, Supported: true},
		}}},
		properties: map[string][]*xmlNode{},
	}
	plans := map[string][]Rule{"253300": {{Stage: 1, Buff: 0, Level: 1, Duration: 3000,
		Properties: map[string]map[string]float64{"1830110": {"SkillDamage": 1}}}}}
	clones := map[string]map[string]string{
		"253300": {cloneKey(1, "1830110"): "900000000"},
		"253700": {cloneKey(1, "3101"): "900000000"},
	}
	if err := assignCloneIDs(info, plans, clones); err != nil {
		t.Fatalf("一个 active 加一个历史 inactive 不应失败：%v", err)
	}
	if got := clones["253300"][cloneKey(1, "1830110")]; got != "900000000" {
		t.Fatalf("active 固定号不应改写，实际 %s", got)
	}
}

func TestCloneIDAllocationSkipsOccupiedNumbers(t *testing.T) {
	occupied := []string{"900000000", "900000001", "900000002"}
	properties := map[string][]*xmlNode{}
	for _, id := range occupied {
		node, err := parseXML(`<PropertyItem SkillProId="` + id + `"/>`)
		if err != nil {
			t.Fatalf("构造节点失败：%v", err)
		}
		properties[id] = []*xmlNode{node}
	}
	info := &inspection{
		weapons: []Weapon{{ID: 253300, Name: "王八拳", BuffIDs: []int{0}, Stages: []Stage{
			{Stage: 1, State: "2011", Action: "2001936", PropertyIDs: []string{"1830110"}, Supported: true},
		}}},
		properties: properties,
	}
	plans := map[string][]Rule{"253300": {{Stage: 1, Buff: 0, Level: 1, Duration: 3000,
		Properties: map[string]map[string]float64{"1830110": {"SkillDamage": 1}}}}}

	clones := map[string]map[string]string{}
	if err := assignCloneIDs(info, plans, clones); err != nil {
		t.Fatalf("分配失败：%v", err)
	}
	if got := clones["253300"][cloneKey(1, "1830110")]; got != "900000003" {
		t.Fatalf("应当跳过已占用的号，拿到 %s", got)
	}

	// 钉住的号即使归档已经占用，也必须**沿用**：重新采集基线之后「上一轮的克隆
	// 节点已经在归档里」是常态，换号会让已经写进配置的连招限制立刻失效。
	// render 在这种情况下改成原位替换那个节点，不再追加第二条。
	pinned := map[string]map[string]string{"253300": {cloneKey(1, "1830110"): "900000001"}}
	if err := assignCloneIDs(info, plans, pinned); err != nil {
		t.Fatalf("沿用时分配失败：%v", err)
	}
	if got := pinned["253300"][cloneKey(1, "1830110")]; got != "900000001" {
		t.Fatalf("钉子号应当沿用，实际 %s", got)
	}
	// 新号仍然要避开已占用的号。
	fresh := map[string]map[string]string{"253300": {cloneKey(1, "1830110"): "900000001"}}
	plans2 := map[string][]Rule{"253300": {
		{Stage: 1, Buff: 0, Level: 1, Duration: 3000, Properties: map[string]map[string]float64{"1830110": {"SkillDamage": 1}}},
	}}
	if err := assignCloneIDs(info, plans2, fresh); err != nil {
		t.Fatalf("分配失败：%v", err)
	}
	if got := fresh["253300"][cloneKey(1, "1830110")]; got != "900000001" {
		t.Fatalf("沿用后不该再动，实际 %s", got)
	}
}

// 反向索引：规则里写的是克隆前的号（已应用状态）或过期克隆号（不再应用的状态），
// 都要能唯一地翻译到当前的号；同一个号可能对应两个目标时必须放弃翻译。
func TestComboRuleIDIndexTranslatesBothWays(t *testing.T) {
	weapon := Weapon{ID: 253300, Name: "王八拳", BuffIDs: []int{0}, Stages: []Stage{
		{Stage: 1, State: "2011", Action: "2001936", PropertyIDs: []string{"1830110"}},
		{Stage: 2021, State: "2021", Action: "2001934", PropertyIDs: []string{"1930210"}},
		{Stage: 2062, State: "2062", Action: "2001134361", PropertyIDs: []string{"1343610"}},
	}}
	applied := []Rule{{Stage: 1}, {Stage: 2021}}
	clones := map[string]string{
		cloneKey(1, "1830110"):    "900000010",
		cloneKey(2021, "1930210"): "900000011",
		// 2062 不再带规则，但留着上一轮的克隆号 ⇒ 规则写它时要翻回原号。
		cloneKey(2062, "1343610"): "900000012",
	}
	effective, reverse := comboRuleIDIndex(weapon, applied, clones)

	for _, id := range []string{"900000010", "900000011", "1343610"} {
		if !effective[id] {
			t.Fatalf("%s 应当是被客户端读到的有效编号", id)
		}
	}
	for _, stale := range []string{"1830110", "1930210"} {
		if effective[stale] {
			t.Fatalf("%s 已被克隆替换，不该再算有效", stale)
		}
	}
	if got := reverse["1830110"]; got != "900000010" {
		t.Fatalf("克隆前的号应当翻到克隆号，实际 %q", got)
	}
	if got := reverse["900000012"]; got != "1343610" {
		t.Fatalf("过期克隆号应当翻回原号，实际 %q", got)
	}

	// 同一个号落在两个状态上（共享块）：无法确定翻到哪一个，必须放弃。
	ambiguous := Weapon{ID: 1, BuffIDs: []int{0}, Stages: []Stage{
		{Stage: 1, State: "2011", Action: "a", PropertyIDs: []string{"700"}},
		{Stage: 2, State: "2012", Action: "b", PropertyIDs: []string{"700"}},
	}}
	_, reverse = comboRuleIDIndex(ambiguous, []Rule{{Stage: 1}, {Stage: 2}},
		map[string]string{cloneKey(1, "700"): "900000020", cloneKey(2, "700"): "900000021"})
	if _, ok := reverse["700"]; ok {
		t.Fatal("一对多的号不该出现在反向索引里")
	}
}

// 应用前把对得上的旧号就地翻译，翻不动的直接拒绝 —— 绝不能把「永不命中」的规则写进去。
func TestReconcileComboRulesRewritesStaleNumbers(t *testing.T) {
	info := &inspection{
		weapons: []Weapon{{ID: 253300, Name: "王八拳", BuffIDs: []int{0}, Stages: []Stage{
			{Stage: 1, State: "2011", Action: "2001936", PropertyIDs: []string{"1830110"}, Supported: true},
			{Stage: 2021, State: "2021", Action: "2001934", PropertyIDs: []string{"1930210"}, Supported: true},
		}}},
		properties: map[string][]*xmlNode{},
	}
	state := &weaponState{
		Applied: map[string][]Rule{"253300": {
			{Stage: 1, Buff: 0, Level: 1, Duration: 3000,
				Properties: map[string]map[string]float64{"1830110": {"SkillDamage": 1}}},
			{Stage: 2021, Buff: 0, Level: 1, Duration: 3000,
				Properties: map[string]map[string]float64{"1930210": {"SkillDamage": 1}}},
		}},
		ComboRules: map[string]ComboRuleSet{"253300": {Black: []ComboRuleLink{{Prev: "1830110", Cur: "1930210"}}}},
	}
	changed, err := reconcileComboRules(info, state)
	if err != nil {
		t.Fatalf("翻译失败：%v", err)
	}
	if !changed {
		t.Fatal("应当报告规则被改写")
	}
	clones := cloneMapOf(state)
	black := state.ComboRules["253300"].Black[0]
	wantPrev := clones["253300"][cloneKey(1, "1830110")]
	wantCur := clones["253300"][cloneKey(2021, "1930210")]
	if black.Prev != wantPrev || black.Cur != wantCur {
		t.Fatalf("翻译结果不对：%s -> %s，期望 %s -> %s", black.Prev, black.Cur, wantPrev, wantCur)
	}

	// 翻不动的号必须报错，并把编号列出来。
	state.ComboRules["253300"] = ComboRuleSet{
		Black: []ComboRuleLink{{Prev: black.Prev, Cur: "1234567"}},
	}
	if _, err := reconcileComboRules(info, state); err == nil {
		t.Fatal("未知编号应当拒绝写入")
	} else if !strings.Contains(err.Error(), "1234567") {
		t.Fatalf("错误信息应当点出问题编号，实际：%v", err)
	}
}

// 下拉框必须给「应用后」的号：已应用状态的选项要换成克隆号，其余保持原样。
func TestAppliedSkillOptionsRewritesAppliedStages(t *testing.T) {
	info := &inspection{
		weapons: []Weapon{{ID: 253300, Name: "王八拳", BuffIDs: []int{0}, Stages: []Stage{
			{Stage: 1, State: "2011", Action: "2001936", PropertyIDs: []string{"1830110"}, Supported: true},
			{Stage: 2062, State: "2062", Action: "2001134361", PropertyIDs: []string{"1343610"}, Supported: true},
		}}},
		properties: map[string][]*xmlNode{},
	}
	state := &weaponState{
		Applied: map[string][]Rule{"253300": {
			{Stage: 1, Buff: 0, Level: 1, Duration: 3000,
				Properties: map[string]map[string]float64{"1830110": {"SkillDamage": 1}}},
		}},
	}
	options := []SkillOption{
		{Skill: "1830110", State: "2011"},
		{Skill: "1343610", State: "2062"},
	}
	got := appliedSkillOptions(info, state, "253300", options)
	if got[0].Skill == "1830110" {
		t.Fatal("已应用状态的选项应当换成克隆号")
	}
	if !strings.HasPrefix(got[0].Skill, "900000") {
		t.Fatalf("克隆号应当落在 9000000xx，实际 %q", got[0].Skill)
	}
	if got[1].Skill != "1343610" {
		t.Fatalf("未应用的状态不该被改动，实际 %q", got[1].Skill)
	}

	// 预测不能落库：读一次目录不该钉住编号。
	if len(state.PropertyClones) != 0 {
		t.Fatalf("只读预测不该写入 state.PropertyClones，实际 %v", state.PropertyClones)
	}
	// 同一次 apply 会把两个号钉死，之后预测与落库一致。
	clones := cloneMapOf(state)
	if err := assignCloneIDs(info, state.Applied, clones); err != nil {
		t.Fatalf("分配失败：%v", err)
	}
	again := appliedSkillOptions(info, state, "253300",
		[]SkillOption{{Skill: "1830110", State: "2011"}})
	if again[0].Skill != clones["253300"][cloneKey(1, "1830110")] {
		t.Fatalf("预测与落库不一致：%q vs %q", again[0].Skill, clones["253300"][cloneKey(1, "1830110")])
	}
}

// 编辑器自己发的编号是九位（命中属性克隆号 900000000+ 起、新增节点 800000001+ 起），
// 保存期校验必须认 —— 否则「应用到游戏」后真正生效的号会被自己的校验拒掉。
func TestValidateComboRuleSetAcceptsEditorIssuedNumbers(t *testing.T) {
	set := ComboRuleSet{
		Max: []ComboRuleMax{
			{Skill: "900000026", MaxCombo: "1"},
			{Skill: "800000001", MaxCombo: "2"},
			{Skill: "1330310", MaxCombo: "3"},
		},
		Black: []ComboRuleLink{{Prev: "900000070", Cur: "900000077"}},
		White: []ComboRuleLink{{Prev: "900000105", Cur: "900000081"}},
	}
	if err := validateComboRuleSet(set); err != nil {
		t.Fatalf("编辑器自己发的九位编号应当被接受：%v", err)
	}
	bad := ComboRuleSet{Black: []ComboRuleLink{{Prev: "900000070", Cur: "1234567890"}}}
	if err := validateComboRuleSet(bad); err == nil {
		t.Fatal("十位编号应当被拒绝")
	}
}
