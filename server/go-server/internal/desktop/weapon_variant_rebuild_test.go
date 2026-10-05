package desktop

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// 纯单元：重建分支段必须保住卡帧、正确编号、继承子元素，纯动作段不挂号。
//
// 这几条正是「多段命中看起来像同一段动画反复播」的根因所在 —— 早期实现删光
// <Anm> 再从零造，replaytimes 丢了、id 写成动画名、<AttackScope> 全没。
func TestBuildVariantRebuildsSegmentsFaithfully(t *testing.T) {
	const template = `<PropertyItem SkillProId="5210110" SkillDamage="7" />`
	alloc := &variantAllocator{
		occupied: map[string]bool{"5210110": true},
		sources:  map[string]string{"5210110": template},
		template: template,
		nextID:   variantSkillProPrefix,
		usage:    map[string]int{},
		updates:  map[string]float64{},
		retired:  map[string]bool{},
	}
	// 无条件块：4 段，第 2 段有命中属性，第 1 段带一个 AttackScope 子节点。
	base := `<AnmDesc id = "521011">` +
		`<Anm id="1" name="600180" startframe="0" endframe="14"><AttackScope centerx="0" length="12" /></Anm>` +
		`<Anm id="2" name="600180" startframe="15" endframe="18" skillproid="5210110" />` +
		`<Anm id="3" name="600180" startframe="19" endframe="32" />` +
		`<Anm id="4" name="600180" startframe="33" endframe="99" />` +
		`</AnmDesc>`

	segments := []VariantAnm{
		{Name: "600180", Start: 0, End: 14},                                    // 纯动作段
		{Name: "600180", Start: 14, End: 15, SkillProID: "5210110", Damage: 4}, // 克隆原生号
		{Name: "600180", Start: 15, End: 15, ReplayTimes: 2},                   // 卡帧段
		{Name: "600180", Start: 15, End: 16, SkillProID: "5210110"},            // 克隆原生号
	}
	built, props, err := buildVariant(base, "521011", "406", segments, map[string]bool{}, alloc)
	if err != nil {
		t.Fatal(err)
	}

	// 条件必须排在块内最前（在第一个 <Anm> 之前）。
	condAt, anmAt := strings.Index(built, "<Condition>"), strings.Index(built, "<Anm ")
	if condAt < 0 || anmAt < 0 || condAt > anmAt {
		t.Fatalf("条件没排在最前：%.80s", built)
	}
	node := mustParse(t, built)
	if conditionUstate(node) != "406" {
		t.Fatalf("条件号不对：%q", conditionUstate(node))
	}
	anms := []*xmlNode{}
	for _, child := range node.children {
		if child.tag == "Anm" {
			anms = append(anms, child)
		}
	}
	if len(anms) != 4 {
		t.Fatalf("应当重建 4 段，得到 %d", len(anms))
	}
	// 1) <Anm id> 必须是片断序号，绝不能是动画名。
	for index, anm := range anms {
		if got := strings.TrimSpace(anm.get("id")); got != strconv.Itoa(index+1) {
			t.Fatalf("第 %d 段 id = %q，期望 %d（不能写成动画名）", index+1, got, index+1)
		}
		if got := strings.TrimSpace(anm.get("name")); got != "600180" {
			t.Fatalf("第 %d 段 name = %q", index+1, got)
		}
	}
	// 2) 卡帧必须写回（第 3 段），其余段不能带 replaytimes。
	if got := strings.TrimSpace(anms[2].get("replaytimes")); got != "2" {
		t.Fatalf("卡帧段 replaytimes = %q，期望 2", got)
	}
	for _, index := range []int{0, 1, 3} {
		if got := strings.TrimSpace(anms[index].get("replaytimes")); got != "" {
			t.Fatalf("第 %d 段不该有 replaytimes，得到 %q", index+1, got)
		}
	}
	// 3) 子元素必须继承（第 1 段的 AttackScope）。
	found := false
	anms[0].walk(func(n *xmlNode) {
		if n.tag == "AttackScope" {
			found = true
		}
	})
	if !found {
		t.Fatal("重建段丢了 AttackScope 子节点")
	}
	// 4) 纯动作段（1、3）不挂命中属性；有来源的段（2、4）分配新号。
	if got := strings.TrimSpace(anms[0].get("skillproid")); got != "" {
		t.Fatalf("纯动作段不该有 skillproid，得到 %q", got)
	}
	if got := strings.TrimSpace(anms[2].get("skillproid")); got != "" {
		t.Fatalf("卡帧段不该有 skillproid，得到 %q", got)
	}
	if len(props) != 2 {
		t.Fatalf("应当只分配 2 个新号，得到 %d", len(props))
	}
	for index, prop := range props {
		if prop.newID != strconv.Itoa(variantSkillProPrefix+index) {
			t.Fatalf("第 %d 个新号 = %s", index+1, prop.newID)
		}
		if prop.templateID != "5210110" {
			t.Fatalf("克隆源应当是 5210110，得到 %q", prop.templateID)
		}
	}
	// 5) 带伤害的那一段必须把覆写值带上。
	if !props[0].hasOverride || props[0].damage != 4 {
		t.Fatalf("第 2 段的伤害覆写丢了：%+v", props[0])
	}
	if props[1].hasOverride {
		t.Fatalf("没填伤害的段不该覆写：%+v", props[1])
	}
}

// 纯单元：把一段拆成三段时，三段都必须继承原段的子节点（命中盒）与独立的片断号。
//
// 模板若按**下标**取，只有拆出来的第一段能拿到带 <AttackScope> 的那一份，后两段
// 是空壳 —— 动画照播但打不中人。253450（狂暴·紫金八面锤）的 406 分支就是这样
// 第一次落地时少了两个命中盒。模板必须按帧窗口重叠挑。
func TestBuildVariantSplitsInheritHitBox(t *testing.T) {
	const template = `<PropertyItem SkillProId="5210110" SkillDamage="7" />`
	alloc := &variantAllocator{
		occupied: map[string]bool{"5210110": true},
		sources:  map[string]string{"5210110": template},
		template: template,
		nextID:   variantSkillProPrefix,
		usage:    map[string]int{},
		updates:  map[string]float64{},
		retired:  map[string]bool{},
	}
	base := `<AnmDesc id = "521011">` +
		`<Anm id="1" name="600180" startframe="0" endframe="14" />` +
		`<Anm id="2" name="600180" startframe="15" endframe="18" skillproid="5210110">` +
		`<AttackScope centerx="0" length="12" /></Anm>` +
		`<Anm id="3" name="600180" startframe="19" endframe="32" />` +
		`</AnmDesc>`
	// 一段（15-18）拆成三段，其余段原样。
	segments := []VariantAnm{
		{Name: "600180", Start: 0, End: 14},
		{Name: "600180", Start: 14, End: 15, SkillProID: "5210110", Damage: 4.7},
		{Name: "600180", Start: 16, End: 16, SkillProID: "5210110", Damage: 4.7},
		{Name: "600180", Start: 17, End: 18, SkillProID: "5210110", Damage: 4.7},
		{Name: "600180", Start: 19, End: 32},
	}
	built, props, err := buildVariant(base, "521011", "406", segments, map[string]bool{}, alloc)
	if err != nil {
		t.Fatal(err)
	}
	if len(props) != 3 {
		t.Fatalf("应当分配 3 个新号，得到 %d", len(props))
	}
	node := mustParse(t, built)
	anms := []*xmlNode{}
	for _, child := range node.children {
		if child.tag == "Anm" {
			anms = append(anms, child)
		}
	}
	if len(anms) != 5 {
		t.Fatalf("应当重建 5 段，得到 %d", len(anms))
	}
	seen := map[string]bool{}
	for index, anm := range anms {
		if id := strings.TrimSpace(anm.get("id")); id == "" || seen[id] {
			t.Fatalf("第 %d 段片断号重复或缺失：%q", index+1, id)
		} else {
			seen[id] = true
		}
	}
	ids := map[string]bool{}
	for _, index := range []int{1, 2, 3} {
		found := false
		anms[index].walk(func(n *xmlNode) {
			if n.tag == "AttackScope" {
				found = true
			}
		})
		if !found {
			t.Fatalf("第 %d 段（拆出来的命中段）丢了 <AttackScope>", index+1)
		}
		id := strings.TrimSpace(anms[index].get("skillproid"))
		if id == "" || ids[id] {
			t.Fatalf("第 %d 段的命中属性号异常：%q", index+1, id)
		}
		ids[id] = true
	}
}

// 纯单元：号就是被替换掉那一块里的号时，原样保留（原生号也不重铸）。
func TestBuildVariantKeepsPreallocatedNumbers(t *testing.T) {
	const template = `<PropertyItem SkillProId="5210111" SkillDamage="7" RepulseTarget="1" />`
	alloc := &variantAllocator{
		occupied: map[string]bool{"5210111": true},
		sources:  map[string]string{"5210111": template},
		template: template,
		nextID:   variantSkillProPrefix,
		usage:    map[string]int{},
		updates:  map[string]float64{},
		retired:  map[string]bool{},
	}
	base := `<AnmDesc id="521011"><Anm id="1" name="600180" startframe="0" endframe="4" /></AnmDesc>`
	segments := []VariantAnm{{
		Name:               "600180",
		Start:              0,
		End:                4,
		SkillProID:         "910000123",
		TemplateSkillProID: "5210111",
		Damage:             3.3,
	}}
	built, props, err := buildVariant(base, "521011", "406", segments, map[string]bool{}, alloc)
	if err != nil {
		t.Fatal(err)
	}
	if len(props) != 1 || props[0].newID != "910000123" || props[0].templateID != "5210111" {
		t.Fatalf("预分配 ID或模板源错误：%+v", props)
	}
	if strings.Contains(built, `skillproid="910000000"`) || !strings.Contains(built, `skillproid="910000123"`) {
		t.Fatalf("动作段没有保留预分配 ID：%s", built)
	}
	if got := alloc.nextID; got != variantSkillProPrefix {
		t.Fatalf("预分配 ID不应消耗 nextID，得到 %d", got)
	}
	if !alloc.occupied["910000123"] {
		t.Fatal("预分配 ID未登记为占用")
	}
}

func TestBuildVariantPreservesExplicitZeroDamage(t *testing.T) {
	const template = `<PropertyItem SkillProId="5210111" SkillDamage="8" RepulseTarget="1" />`
	alloc := &variantAllocator{
		occupied: map[string]bool{},
		reserved: map[string]bool{},
		sources:  map[string]string{"5210111": template},
		template: template,
		nextID:   variantSkillProPrefix,
		usage:    map[string]int{},
		updates:  map[string]float64{},
		retired:  map[string]bool{},
	}
	base := `<AnmDesc id="521011"><Anm id="1" name="600180" startframe="0" endframe="4" /></AnmDesc>`
	segments := []VariantAnm{{
		Name: "600180", Start: 0, End: 4,
		SkillProID: "910000106", TemplateSkillProID: "5210111",
		Damage: 0, DamagePresent: true,
	}}
	built, props, err := buildVariant(base, "521011", "406", segments, map[string]bool{}, alloc)
	if err != nil {
		t.Fatal(err)
	}
	if len(props) != 1 || !props[0].hasOverride || props[0].damage != 0 {
		t.Fatalf("显式零伤害没有保留 presence：props=%+v", props)
	}
	if !strings.Contains(built, `skillproid="910000106"`) {
		t.Fatalf("分支没有挂最终命中属性号：%s", built)
	}
	if err := alloc.record(props); err != nil {
		t.Fatal(err)
	}
	if len(alloc.added) != 1 || !strings.Contains(alloc.added[0], `SkillDamage="0"`) {
		t.Fatalf("显式零伤害没有写入克隆属性：%v", alloc.added)
	}
}

// 纯单元：号就是被替换掉那一块里的号时，原样保留（原生号也不重铸）。
func TestBuildVariantKeepsReusableNumbers(t *testing.T) {
	alloc := &variantAllocator{
		occupied: map[string]bool{},
		sources:  map[string]string{},
		nextID:   variantSkillProPrefix,
		usage:    map[string]int{},
		updates:  map[string]float64{},
		retired:  map[string]bool{},
	}
	base := `<AnmDesc id = "521011"><Anm id="1" name="600180" startframe="0" endframe="14" /></AnmDesc>`
	segments := []VariantAnm{{Name: "600180", Start: 0, End: 14, SkillProID: "5210111"}}
	built, props, err := buildVariant(base, "521011", "406", segments, map[string]bool{"5210111": true}, alloc)
	if err != nil {
		t.Fatal(err)
	}
	if len(props) != 1 || !props[0].reuse || props[0].newID != "5210111" {
		t.Fatalf("应当原样复用 5210111，得到 %+v", props)
	}
	node := mustParse(t, built)
	for _, child := range node.children {
		if child.tag == "Anm" && strings.TrimSpace(child.get("skillproid")) != "5210111" {
			t.Fatalf("skillproid 被改动：%q", child.get("skillproid"))
		}
	}
	if got := alloc.nextID; got != variantSkillProPrefix {
		t.Fatalf("不该消耗新号，nextID = %d", got)
	}
}

// 端到端：分支编辑必须幂等（不换号、不堆孤儿），移除段/分支要回收自建号。
func TestApplyVariantsIsIdempotentAndReclaims(t *testing.T) {
	installed := os.Getenv("OPENKFO_WEAPON_TEST_CLIENT")
	if installed == "" {
		t.Skip("set OPENKFO_WEAPON_TEST_CLIENT for installed resource validation")
	}
	raw, err := os.ReadFile(configPath(installed))
	if err != nil {
		t.Fatal(err)
	}
	client := t.TempDir()
	if err = os.MkdirAll(filepath.Join(client, "Data"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(configPath(client), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := loadArchive(configPath(client))
	if err != nil {
		t.Fatal(err)
	}
	itemText, err := a.text("item.txt")
	if err != nil {
		t.Fatal(err)
	}
	items, err := itemsFromText(client, itemText, true, true)
	if err != nil {
		t.Fatal(err)
	}
	info, err := inspect(a, items)
	if err != nil {
		t.Fatal(err)
	}

	weaponKey, stageNum, action := "", 0, ""
	for _, w := range info.weapons {
		for _, st := range w.Stages {
			if !stageNeedsSplit(st.State) {
				continue
			}
			base, ok := pickBlock(actionVariants(info, st.Action))
			if !ok {
				continue
			}
			hasSkill := false
			base.node.walk(func(n *xmlNode) {
				if n.tag == "Anm" && strings.TrimSpace(n.get("skillproid")) != "" {
					hasSkill = true
				}
			})
			if hasSkill {
				weaponKey, stageNum, action = strconv.Itoa(w.ID), mustAtoi(t, strings.TrimSpace(st.State)), st.Action
				break
			}
		}
		if weaponKey != "" {
			break
		}
	}
	if weaponKey == "" {
		t.Skip("客户端里没有可用的样本")
	}

	base, _ := pickBlock(actionVariants(info, action))
	source := ""
	base.node.walk(func(n *xmlNode) {
		if source == "" && n.tag == "Anm" {
			source = strings.TrimSpace(n.get("skillproid"))
		}
	})
	if source == "" {
		t.Skip("样本没有可用的命中属性来源")
	}

	const condition = 193
	segments := []VariantAnm{
		{Name: "600180", Start: 0, End: 5, SkillProID: source, Damage: 3},
		{Name: "600180", Start: 5, End: 5, ReplayTimes: 2},
	}
	edit := func(a *archive, segs []VariantAnm) *archive {
		t.Helper()
		state := &weaponState{Variants: map[string]map[int][]VariantEdit{
			weaponKey: {stageNum: {{Condition: condition, Segments: segs}}},
		}}
		out, err := applyVariants(a, state, items)
		if err != nil {
			t.Fatalf("应用分支失败：%v", err)
		}
		return out
	}

	first := edit(a, segments)
	ownCount := func(src *archive) int {
		t.Helper()
		text, err := src.text("skillproperty.xml")
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, id := range variantSkillProIDPattern.FindAllStringSubmatch(text, -1) {
			if number, convErr := strconv.Atoi(strings.TrimSpace(id[1])); convErr == nil && number >= variantSkillProPrefix {
				count++
			}
		}
		return count
	}
	// 回读分支段：模拟 UI 把**已分配的自建号**原样带回。前端若把号丢掉（退回原生号），
	// 后端就只能另分新号 —— 那样永远不幂等，正是本轮要修的问题。
	readBack := func(src *archive) []VariantAnm {
		t.Helper()
		read, err := inspect(src, items)
		if err != nil {
			t.Fatal(err)
		}
		for _, variant := range actionVariants(read, action) {
			if variant.condition != strconv.Itoa(condition) {
				continue
			}
			out := make([]VariantAnm, 0, 4)
			for _, entry := range variantSegmentMaps(read, variant.node) {
				segment := VariantAnm{
					Name:  entry["name"].(string),
					Start: entry["start"].(int),
					End:   entry["end"].(int),
				}
				if value, ok := entry["replay_times"].(int); ok {
					segment.ReplayTimes = value
				}
				if value, ok := entry["skillproid"].(string); ok {
					segment.SkillProID = value
				}
				if value, ok := entry["anm_id"].(string); ok {
					segment.AnmID = value
				}
				if value, ok := entry["damage"].(float64); ok {
					segment.Damage = value
				}
				out = append(out, segment)
			}
			return out
		}
		return nil
	}
	returned := readBack(first)
	if len(returned) != len(segments) {
		t.Fatalf("回读分支段失败：期望 %d 段，得到 %d", len(segments), len(returned))
	}
	if returned[0].SkillProID < strconv.Itoa(variantSkillProPrefix) {
		t.Fatalf("回读应当带回自建号，得到 %q", returned[0].SkillProID)
	}
	if returned[1].ReplayTimes != 2 || returned[1].SkillProID != "" {
		t.Fatalf("回读丢了卡帧/纯动作段信息：%+v", returned[1])
	}

	baseline := ownCount(a)
	if after := ownCount(first); after != baseline+1 {
		t.Fatalf("应当只新增 1 条自建命中属性：%d → %d", baseline, after)
	}

	// 原样再存一次（回读结果 = 号没变、伤害没变）：条目数不变、字节一致。
	second := edit(first, returned)
	if after := ownCount(second); after != baseline+1 {
		t.Fatalf("重复保存堆出了孤儿条目：%d → %d", baseline+1, after)
	}
	if !bytesEqualArchive(t, first, second, true) {
		t.Fatal("同样的输入重复保存应当逐字节一致")
	}

	// 改伤害：原地覆写，不新增条目。
	updated := append([]VariantAnm(nil), returned...)
	updated[0].Damage = 9
	third := edit(second, updated)
	if after := ownCount(third); after != baseline+1 {
		t.Fatalf("改伤害不该新增条目：%d → %d", baseline+1, after)
	}
	if bytesEqualArchive(t, second, third, false) {
		t.Fatal("改伤害应当产生变化")
	}
	// 只清掉第 1 段的命中属性：那 1 条自建号必须被回收。
	cleared := append([]VariantAnm(nil), updated...)
	cleared[0].SkillProID = ""
	fourth := edit(third, cleared)
	if after := ownCount(fourth); after != baseline {
		t.Fatalf("清空命中属性后应回收自建条目：期望 %d，得到 %d", baseline, after)
	}
	// 删分支：同样回收。
	state := &weaponState{Variants: map[string]map[int][]VariantEdit{
		weaponKey: {stageNum: {{Condition: condition, Remove: true}}},
	}}
	fifth, err := applyVariants(first, state, items)
	if err != nil {
		t.Fatalf("删分支失败：%v", err)
	}
	if after := ownCount(fifth); after != baseline {
		t.Fatalf("删分支后应回收自建条目：期望 %d，得到 %d", baseline, after)
	}
}

// bytesEqualArchive 比「落盘时会得到的字节」。
//
// 不能直接比 a.data：replace() 是追加式写入（新副本挂到数据区尾部，旧副本留在
// 文件里），所以同一个逻辑内容的归档每 replace 一次就胖一圈。真实流程落盘前会走
// compactArchive 收敛，这里照做，比的才是逻辑内容。
//
// report 为真时，若两侧不等会顺带打出逐条目差异，便于定位是哪份文件变脏了。
func bytesEqualArchive(t *testing.T, left, right *archive, report bool) bool {
	t.Helper()
	leftData, err := compactArchive(left.data)
	if err != nil {
		t.Fatal(err)
	}
	rightData, err := compactArchive(right.data)
	if err != nil {
		t.Fatal(err)
	}
	if string(leftData) == string(rightData) {
		return true
	}
	if !report {
		return false
	}
	// 逐条目定位差异，便于排查。
	leftEntries, rightEntries := map[string]string{}, map[string]string{}
	collect := func(a *archive, into map[string]string) {
		for name := range a.entries {
			if text, readErr := a.text(name); readErr == nil {
				into[name] = text
			}
		}
	}
	collect(left, leftEntries)
	collect(right, rightEntries)
	for name, value := range leftEntries {
		other, ok := rightEntries[name]
		switch {
		case !ok:
			t.Logf("条目 %s 只存在于左侧", name)
		case other != value:
			t.Logf("条目 %s 内容不同（左 %d 字节 / 右 %d 字节）", name, len(value), len(other))
		}
	}
	return false
}

// 合成归档包含真实路径，可用于完整 handler 回归。
func variantFixture(t *testing.T, overrides map[string]string) (*archive, []Item) {
	t.Helper()
	texts := map[string]string{
		"item.txt":           "25\t253450\t6\t测试\n",
		"itemact.txt":        "ID\tName\t2011\t2012\n253450\t测试\t2001001\t2001002\n",
		"ustate.xml":         "<UState />",
		"skillproperty.xml":  `<SkillProperty><PropertyItem SkillProId="100" SkillDamage="7" TargetSelf="0" TargetEnemy="1" /></SkillProperty>`,
		"animation/2001.xml": `<AnmInfo><AnmDesc id="1"><Anm id="1" name="600180" startframe="0" endframe="18" skillproid="100"><AttackScope length="12" /></Anm></AnmDesc><AnmDesc id="2"><Anm id="1" name="600180" startframe="0" endframe="18" skillproid="100" /></AnmDesc></AnmInfo>`,
		"delayacttable.xml":  `<DelayActTable />`,
	}
	for key, value := range overrides {
		texts[key] = value
	}
	// 由现有合成归档生成有效的长路径索引，不依赖客户端资源。
	names := make([]string, 0, len(texts))
	for name := range texts {
		names = append(names, name)
	}
	sort.Strings(names)
	entries := map[string][]byte{}
	for index, name := range names {
		encoded, err := encodeText(texts[name])
		if err != nil {
			t.Fatal(err)
		}
		entries[fmt.Sprintf("f%06d", index)] = encoded
	}
	data := synthArchive(t, entries)
	treeAt, tableAt := int(little.Uint32(data[40:])), int(little.Uint32(data[44:]))
	tree := make([]byte, 4+len(names)*12)
	little.PutUint32(tree, uint32(len(names)))
	for index, name := range names {
		position := 4 + index*12
		for len(name) > 8 {
			copy(tree[position:position+8], name[:8])
			little.PutUint32(tree[position+8:], uint32(len(tree)))
			position = len(tree) + 4
			tree = append(tree, make([]byte, 16)...)
			little.PutUint32(tree[position-4:], 1)
			name = name[8:]
		}
		copy(tree[position:position+8], name)
		little.PutUint32(tree[position+8:], uint32(0xff000000|index))
	}
	data = append(append(append([]byte(nil), data[:treeAt]...), tree...), data[tableAt:]...)
	little.PutUint32(data[44:], uint32(treeAt+len(tree)))
	little.PutUint32(data[48:], uint32(treeAt+len(tree)+len(names)*8))
	a, err := parseArchive(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.verify(); err != nil {
		t.Fatal(err)
	}
	return a, []Item{{ID: 253450, Kind: 25, Name: "测试"}}
}

func TestVariantAllocatorBoundaryAndFullUsage(t *testing.T) {
	a, _ := variantFixture(t, map[string]string{
		"animation/9999.xml": `<AnmInfo><Anm skillproid = '910000000' /><Anm skillproid="910999999">`,
	})
	alloc, err := newVariantAllocator(a)
	if err != nil {
		t.Fatal(err)
	}
	id, err := alloc.next()
	if err != nil || id != "910000001" {
		t.Fatalf("悬空号未避让：%s %v", id, err)
	}
	alloc.nextID = variantSkillProLimit - 1
	delete(alloc.occupied, "910999999")
	id, err = alloc.next()
	if err != nil || id != "910999999" {
		t.Fatalf("最后一号不可用：%s %v", id, err)
	}
	if _, err = alloc.next(); err == nil {
		t.Fatal("耗尽应返回错误")
	}
	if !alloc.isSelfMade("910500000") {
		t.Fatal("历史百万号段丢失")
	}
	base := `<AnmDesc id="1"><Anm id="1" name="600180" startframe="0" endframe="1" skillproid="100" /></AnmDesc>`
	if _, _, err := buildVariant(base, "1", "406", nil, nil, alloc); err == nil {
		t.Fatal("buildVariant 未传递耗尽错误")
	}
}

func TestVariantExplicitReservationConflictAndLegacy(t *testing.T) {
	a, items := variantFixture(t, nil)
	state := &weaponState{Variants: map[string]map[int][]VariantEdit{"253450": {2011: {{Condition: 406, Segments: []VariantAnm{
		{Name: "600180", Start: 0, End: 5, SkillProID: "100"},
		{Name: "600180", Start: 6, End: 10, SkillProID: "910000000", TemplateSkillProID: "100"},
	}}}}}}
	out, err := applyVariants(a, state, items)
	if err != nil {
		t.Fatal(err)
	}
	text, _ := out.text("animation/2001.xml")
	if !strings.Contains(text, `skillproid="910000001"`) || !strings.Contains(text, `skillproid="910000000"`) {
		t.Fatalf("自动分配抢显式号：%s", text)
	}
	state.Variants["253450"][2012] = []VariantEdit{{Condition: 407, Segments: []VariantAnm{{Name: "600180", SkillProID: "910000000", TemplateSkillProID: "100"}}}}
	if _, err = applyVariants(a, state, items); err == nil {
		t.Fatal("跨状态重复显式号未拒绝")
	}
	delete(state.Variants["253450"], 2012)
	state.Variants["253450"][2011][0].Segments = []VariantAnm{{Name: "600180", SkillProID: "910000000"}}
	legacy, err := applyVariants(out, state, items)
	if err != nil {
		t.Fatal(err)
	}
	// 换一个新条件：历史 91 来源必须仍能作为模板克隆。
	state.Variants["253450"][2011][0].Condition = 408
	if _, err = applyVariants(legacy, state, items); err != nil {
		t.Fatalf("旧91模板不兼容：%v", err)
	}
	collision, _ := variantFixture(t, map[string]string{"animation/9999.xml": `<AnmInfo><Anm skillproid="910000000" /></AnmInfo>`})
	state.Variants["253450"][2011][0].Segments[0].TemplateSkillProID = "100"
	if _, err = applyVariants(collision, state, items); err == nil {
		t.Fatal("全库悬空碰撞未拒绝")
	}
	if err = validateVariantCurrentIDs(collision, "253450", state.Variants["253450"]); err == nil {
		t.Fatal("当前包碰撞未拒绝")
	}
}

func TestVariantPureReplayAndHistoricalExplicitID(t *testing.T) {
	a, items := variantFixture(t, nil)
	state := &weaponState{Variants: map[string]map[int][]VariantEdit{"253450": {2011: {{Condition: 406, Segments: []VariantAnm{{Name: "600180", Start: 5, End: 5, ReplayTimes: 3}}}}}}}
	out, err := applyVariants(a, state, items)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := a.text("skillproperty.xml")
	after, _ := out.text("skillproperty.xml")
	if before != after {
		t.Fatal("纯卡帧分支不应分配属性")
	}
	text, _ := out.text("animation/2001.xml")
	block, ok := currentConditionalBlock(text, "1", "406")
	if !ok || strings.Contains(block, "skillproid") || !strings.Contains(block, `replaytimes="3"`) {
		t.Fatalf("纯卡帧分支错误：%s", block)
	}
	state.Variants["253450"][2011][0].Segments = []VariantAnm{{Name: "600180", SkillProID: "910999999", TemplateSkillProID: "100"}}
	out, err = applyVariants(a, state, items)
	if err != nil {
		t.Fatal(err)
	}
	after, _ = out.text("skillproperty.xml")
	if !strings.Contains(after, `SkillProId="910999999"`) {
		t.Fatal("历史号段最后一个显式号丢失")
	}
	state.Variants["253450"][2011][0].Segments[0].SkillProID = "911000000"
	if _, err := applyVariants(a, state, items); err == nil {
		t.Fatal("越界最终号未拒绝")
	}
}

func TestVariantProjectionReassignsConflictingDraftID(t *testing.T) {
	a, items := variantFixture(t, map[string]string{
		"animation/2001.xml": `<AnmInfo><AnmDesc id="1"><Anm id="1" name="600180" skillproid="100" /></AnmDesc><AnmDesc id="2"><Condition><Ustate id="407" /></Condition><Anm id="1" name="600180" skillproid="910000000" /></AnmDesc><AnmDesc id="2"><Anm id="1" name="600180" skillproid="100" /></AnmDesc></AnmInfo>`,
	})
	state := &weaponState{Variants: map[string]map[int][]VariantEdit{
		"253450": {2011: {{Condition: 406, Segments: []VariantAnm{{Name: "600180", Start: 0, End: 5, SkillProID: "910000000", TemplateSkillProID: "100"}}}}},
	}}
	if _, err := applyVariants(a, state, items, a); err == nil {
		t.Fatal("严格应用必须拒绝当前包已占用的预分配号")
	}
	out, err := applyVariantsProjection(a, state, items, nil, a)
	if err != nil {
		t.Fatalf("详情投影不应因冲突草稿号失败：%v", err)
	}
	text, err := out.text("animation/2001.xml")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, `skillproid="910000000"`) && strings.Count(text, `skillproid="910000000"`) > 1 {
		t.Fatal("投影仍重复使用已占用的预分配号")
	}
	if !strings.Contains(text, `skillproid="910000001"`) {
		t.Fatalf("投影没有为冲突草稿分配临时空闲号：%s", text)
	}
}

func TestVariantAnmDamageAcceptsNumberStringAndEmpty(t *testing.T) {
	for _, input := range []string{`{"name":"a","damage":1.25}`, `{"name":"a","damage":"2.5"}`, `{"name":"a","damage":""}`} {
		var segment VariantAnm
		if err := json.Unmarshal([]byte(input), &segment); err != nil {
			t.Fatalf("兼容 damage 输入失败 %s：%v", input, err)
		}
	}
	var segment VariantAnm
	if err := json.Unmarshal([]byte(`{"damage":"not-a-number"}`), &segment); err == nil {
		t.Fatal("非法 damage 字符串未拒绝")
	}
}

func TestReassignVariantIDsReservesWholeState(t *testing.T) {
	current, _ := variantFixture(t, map[string]string{"animation/9999.xml": `<AnmInfo><Anm skillproid="910000000" /></AnmInfo>`})
	state := &weaponState{
		HitProperties:   map[string]HitProperty{"910000001": {ID: "910000001"}},
		ExtraProperties: map[string]ExtraProperty{"910000002": {Template: "100"}},
		Remaps:          map[string]map[int]*StageRemap{"253450": {2011: {PropertyID: "910000003"}}},
		Drafts:          map[string][]Rule{"253450": {{Stage: 1, Properties: map[string]map[string]float64{"910000004": {"SkillDamage": 1}}}}},
		Variants:        map[string]map[int][]VariantEdit{"253450": {2011: {{Condition: 406, Segments: []VariantAnm{{SkillProID: "910000000", TemplateSkillProID: "100"}}}}}},
	}
	replacements, err := reassignConflictingVariantIDs(current, state)
	if err != nil {
		t.Fatal(err)
	}
	if replacements["910000000"] != "910000005" {
		t.Fatalf("重分配未跳过全部 workspace ID：%v", replacements)
	}
}

func TestReassignVariantIDsChecksCandidateAndPreservesOwnership(t *testing.T) {
	const baseAnimation = `<AnmInfo><AnmDesc id="1"><Anm id="1" name="600180" startframe="0" endframe="18" skillproid="100" /></AnmDesc></AnmInfo>`
	const branch = `<AnmDesc id="1"><Condition><Ustate id="406" /></Condition><Anm id="1" name="600180" startframe="0" endframe="18" skillproid="910000000" /></AnmDesc>`
	const properties = `<SkillProperty><PropertyItem SkillProId="100" SkillDamage="7" /><PropertyItem SkillProId="910000000" SkillDamage="2" /></SkillProperty>`
	for _, mode := range []string{"candidate-only", "candidate-shared", "owned"} {
		t.Run(mode, func(t *testing.T) {
			liveOverrides := map[string]string{}
			if mode != "candidate-only" {
				liveOverrides["animation/2001.xml"] = strings.Replace(baseAnimation, "</AnmInfo>", branch+"</AnmInfo>", 1)
				liveOverrides["skillproperty.xml"] = properties
			}
			current, _ := variantFixture(t, liveOverrides)
			candidateOverrides := map[string]string{"skillproperty.xml": properties}
			if mode != "candidate-only" {
				candidateOverrides["animation/2001.xml"] = liveOverrides["animation/2001.xml"]
			}
			if mode != "owned" {
				candidateOverrides["animation/9999.xml"] = `<AnmInfo><Anm skillproid="910000000" /><Anm skillproid="910000001" /></AnmInfo>`
			}
			candidate, items := variantFixture(t, candidateOverrides)
			state := &weaponState{
				HitProperties: map[string]HitProperty{"910000000": {ID: "910000000", OwnerWeapon: "253450", State: 2011, Condition: 406, Values: map[string]float64{"SkillDamage": 3}}},
				Drafts:        map[string][]Rule{"253450": {{Stage: 1, Properties: map[string]map[string]float64{"910000000": {"SkillDamage": 3}}}}},
				Variants:      map[string]map[int][]VariantEdit{"253450": {2011: {{Condition: 406, Segments: []VariantAnm{{Name: "600180", Start: 0, End: 18, SkillProID: "910000000", TemplateSkillProID: "100"}}}}}},
			}
			replacements, err := reassignConflictingVariantIDs(current, state, candidate)
			if err != nil {
				t.Fatal(err)
			}
			want := "910000002"
			if mode == "owned" {
				want = "910000000"
				if len(replacements) != 0 {
					t.Fatalf("exclusive branch number changed: %v", replacements)
				}
			} else if replacements["910000000"] != want {
				t.Fatalf("candidate collision not reassigned: %v", replacements)
			}
			if state.HitProperties[want].ID != want || state.Drafts["253450"][0].Properties[want]["SkillDamage"] != 3 || state.Variants["253450"][2011][0].Segments[0].SkillProID != want {
				t.Fatal("canonical property, rule and segment IDs drifted")
			}
			out, err := applyVariants(candidate, state, items, current)
			if err != nil {
				t.Fatalf("strict candidate apply failed: %v", err)
			}
			if err := out.verify(); err != nil {
				t.Fatal(err)
			}
			if mode != "owned" && mustArchiveText(t, out, "animation/9999.xml") != candidateOverrides["animation/9999.xml"] {
				t.Fatal("other action references changed")
			}
			if again, err := reassignConflictingVariantIDs(out, state, candidate); err != nil || len(again) != 0 {
				t.Fatalf("repeat apply changed number: %v: %v", again, err)
			}
			repeated, err := applyVariants(candidate, state, items, out)
			if err != nil || !bytesEqualArchive(t, out, repeated, true) {
				t.Fatalf("repeat candidate rebuild is not idempotent: %v", err)
			}
		})
	}
}

func TestReassignVariantIDsRecognizesRenderedBranch(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(strconv.FormatBool(shared), func(t *testing.T) {
			currentOverrides := map[string]string{
				"skillproperty.xml":  `<SkillProperty><PropertyItem SkillProId="100" /><PropertyItem SkillProId="910000000" /><PropertyItem SkillProId="900000123" /></SkillProperty>`,
				"animation/2001.xml": `<AnmInfo><AnmDesc id="1"><Anm id="1" name="600180" skillproid="100" /></AnmDesc><AnmDesc id="1"><Condition><Ustate id="406" /></Condition><Anm id="1" name="600180" skillproid="900000123" /></AnmDesc></AnmInfo>`,
			}
			if shared {
				currentOverrides["animation/9999.xml"] = `<AnmInfo><Anm skillproid="900000123" /></AnmInfo>`
			}
			current, _ := variantFixture(t, currentOverrides)
			candidate, items := variantFixture(t, nil)
			state := &weaponState{
				HitProperties:  map[string]HitProperty{"910000000": {ID: "910000000", OwnerWeapon: "253450"}},
				PropertyClones: map[string]map[string]string{"253450": {"1|910000000": "900000123"}},
				Variants:       map[string]map[int][]VariantEdit{"253450": {2011: {{Condition: 406, Segments: []VariantAnm{{Name: "600180", SkillProID: "910000000", TemplateSkillProID: "100"}}}}}},
			}
			if err := validateVariantCurrentIDs(current, "253450", state.Variants["253450"], state); (err != nil) != shared {
				t.Fatalf("rendered ownership guard shared=%t: %v", shared, err)
			}
			replacements, err := reassignConflictingVariantIDs(current, state, candidate)
			if err != nil {
				t.Fatal(err)
			}
			if shared {
				if replacements["910000000"] != "910000001" || state.PropertyClones["253450"]["1|910000001"] != "900000123" {
					t.Fatalf("shared clone not reassigned with pinned mapping: %v", replacements)
				}
			} else if len(replacements) != 0 {
				t.Fatalf("exclusive rendered branch number changed: %v", replacements)
			}
			if _, err := applyVariants(candidate, state, items, current); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestVariantPropertyCollisionAndSharedRetirement(t *testing.T) {
	state := &weaponState{Variants: map[string]map[int][]VariantEdit{"253450": {2011: {{Condition: 406, Segments: []VariantAnm{{Name: "600180", SkillProID: "910500000", TemplateSkillProID: "100"}}}}}}}
	propertyOnly, items := variantFixture(t, map[string]string{"skillproperty.xml": `<SkillProperty><PropertyItem SkillProId="100" /><PropertyItem SkillProId="910500000" /></SkillProperty>`})
	if _, err := applyVariants(propertyOnly, state, items); err == nil {
		t.Fatal("仅注册号的碰撞未拒绝")
	}
	if err := validateVariantCurrentIDs(propertyOnly, "253450", state.Variants["253450"]); err == nil {
		t.Fatal("当前包仅注册号的碰撞未拒绝")
	}
	animation := `<AnmInfo><AnmDesc id="1"><Anm id="1" name="600180" skillproid="100" /></AnmDesc><AnmDesc id="1"><Condition><Ustate id="406" /></Condition><Anm id="1" name="600180" skillproid="910500000" /><Anm id="2" name="600180" skillproid="910500000" /></AnmDesc></AnmInfo>`
	for _, shared := range []bool{false, true} {
		overrides := map[string]string{"animation/2001.xml": animation, "skillproperty.xml": `<SkillProperty><PropertyItem SkillProId="100" /><PropertyItem SkillProId="910500000" /></SkillProperty>`}
		if shared {
			overrides["animation/9999.xml"] = `<AnmInfo><Anm skillproid="910500000" /></AnmInfo>`
		}
		a, items := variantFixture(t, overrides)
		if shared {
			if _, err := applyVariants(a, state, items); err == nil {
				t.Fatal("自己的号也被其他动作引用，仍允许显式覆盖")
			}
		}
		deletion := &weaponState{Variants: map[string]map[int][]VariantEdit{"253450": {2011: {{Condition: 406, Remove: true}}}}}
		out, err := applyVariants(a, deletion, items)
		if err != nil {
			t.Fatal(err)
		}
		// 删除同一条件第二次也必须成功，避免刷新后的 workspace 因
		// 当前 archive 已无目标块而被错误拒绝。
		out, err = applyVariants(out, deletion, items)
		if err != nil {
			t.Fatalf("重复删除不应失败：%v", err)
		}
		properties, _ := out.text("skillproperty.xml")
		if strings.Contains(properties, "910500000") != shared {
			t.Fatalf("重复引用回收错误 shared=%v：%s", shared, properties)
		}
	}
}

func TestVariantRoundTripBaselineAndNative(t *testing.T) {
	a, items := variantFixture(t, nil)
	state := &weaponState{Variants: map[string]map[int][]VariantEdit{"253450": {2011: {{Condition: 406, Segments: []VariantAnm{
		{Name: "600180", AnmID: "1", Start: 0, End: 5, SkillProID: "100"},
		{Name: "600180", AnmID: "2", Start: 5, End: 5, ReplayTimes: 2},
	}}}}}}
	first, err := applyVariants(a, state, items)
	if err != nil {
		t.Fatal(err)
	}
	info, err := inspect(first, items)
	if err != nil {
		t.Fatal(err)
	}
	branches := comboVariants(info, "253450", state.Variants["253450"])
	raw, _ := json.Marshal(branches[2011][0]["segments"])
	var segments []VariantAnm
	if err := json.Unmarshal(raw, &segments); err != nil {
		t.Fatal(err)
	}
	if segments[0].TemplateSkillProID != "100" || segments[1].SkillProID != "" || segments[1].ReplayTimes != 2 {
		t.Fatalf("回读元数据丢失：%+v", segments)
	}
	legacyRebuilt, err := applyVariants(a, state, items, first)
	if err != nil {
		t.Fatal(err)
	}
	if !bytesEqualArchive(t, first, legacyRebuilt, true) {
		t.Fatal("旧格式草稿从当前包重建发生编号漂移")
	}
	state.Variants["253450"][2011][0].Segments = segments
	rebuilt, err := applyVariants(a, state, items, first)
	if err != nil {
		t.Fatal(err)
	}
	if !bytesEqualArchive(t, first, rebuilt, true) {
		t.Fatal("回原始基线重建不幂等")
	}
	repeated, err := applyVariants(first, state, items)
	if err != nil {
		t.Fatal(err)
	}
	if !bytesEqualArchive(t, first, repeated, true) {
		t.Fatal("已应用包重复保存不幂等")
	}
	state.Variants["253450"][2011][0] = VariantEdit{Condition: 406, Remove: true}
	removed, err := applyVariants(first, state, items)
	if err != nil {
		t.Fatal(err)
	}
	props, _ := removed.text("skillproperty.xml")
	if strings.Contains(props, "910000000") {
		t.Fatal("删除未回收")
	}
	alloc, _ := newVariantAllocator(removed)
	if id, err := alloc.next(); err != nil || id != "910000000" {
		t.Fatalf("删除后无法复用：%s %v", id, err)
	}
	native, nativeItems := variantFixture(t, map[string]string{"animation/2001.xml": `<AnmInfo><AnmDesc id="1"><Anm id="1" name="600180" startframe="0" endframe="10" skillproid="100" /></AnmDesc><AnmDesc id="1"><Condition><Ustate id="406" /></Condition><Anm id="1" name="600180" startframe="0" endframe="10" skillproid="100" /></AnmDesc></AnmInfo>`})
	state.Variants["253450"][2011][0] = VariantEdit{Condition: 406, Segments: []VariantAnm{{Name: "600180", AnmID: "1", SkillProID: "910000123", TemplateSkillProID: "100"}}}
	nativeOut, err := applyVariants(native, state, nativeItems)
	if err != nil {
		t.Fatal(err)
	}
	text, _ := nativeOut.text("animation/2001.xml")
	if strings.Contains(text, "910000123") {
		t.Fatal("原生已有分支被重铸")
	}
}

func TestVariant253450StageHitsFour(t *testing.T) {
	a, items := variantFixture(t, nil)
	state := &weaponState{Variants: map[string]map[int][]VariantEdit{"253450": {2011: {{Condition: 406, Segments: []VariantAnm{
		{Name: "600180", Start: 0, End: 5, SkillProID: "910000100", TemplateSkillProID: "100", Damage: 4.7},
		{Name: "600180", Start: 5, End: 5, ReplayTimes: 2},
		{Name: "600180", Start: 6, End: 10, SkillProID: "910000101", TemplateSkillProID: "100", Damage: 4.7},
		{Name: "600180", Start: 11, End: 18, SkillProID: "910000102", TemplateSkillProID: "100", Damage: 4.7},
	}}}}}}
	check := func(source *archive, entries []Item) {
		t.Helper()
		out, err := applyVariants(source, state, entries)
		if err != nil {
			t.Fatal(err)
		}
		info, err := inspect(out, entries)
		if err != nil {
			t.Fatal(err)
		}
		for _, weapon := range info.weapons {
			if weapon.ID != 253450 {
				continue
			}
			for _, stage := range weapon.Stages {
				if stage.State != "2011" {
					continue
				}
				if len(stage.Hits) != 4 {
					t.Fatalf("253450/2011 Hits 应有4条，得到%d：%+v", len(stage.Hits), stage.Hits)
				}
				count := 0
				for _, hit := range stage.Hits {
					if hit.Variant == "406" {
						count++
					}
				}
				if count != 3 {
					t.Fatalf("406分支应有3条命中，得到%d", count)
				}
				return
			}
		}
		t.Fatal("253450/2011 不存在")
	}
	check(a, items)
}

func TestVariantSetValidatesAtomicallyAndMerges(t *testing.T) {
	a, items := variantFixture(t, nil)
	client, folder := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(client, "Data"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath(client), a.data, 0600); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(folder, "settings.json")
	segment := func(id string) []VariantAnm {
		return []VariantAnm{{Name: "600180", Start: 0, End: 5, SkillProID: id, TemplateSkillProID: "100"}}
	}
	call := func(edits map[int][]VariantEdit) error {
		_, err := weaponHandle(Request{Operation: "weapon_variant_set", Weapon: 253450, Variants: edits}, client, items, folder)
		return err
	}
	if err := call(map[int][]VariantEdit{2011: {{Condition: 406, Segments: segment("910000100")}, {Condition: 407, Segments: segment("910000101")}}, 2012: {{Condition: 408, Segments: segment("910000102")}}}); err != nil {
		t.Fatal(err)
	}
	if err := call(map[int][]VariantEdit{2011: {{Condition: 406, Segments: segment("910000103")}}}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	var saved weaponState
	if err := json.Unmarshal(before, &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Variants["253450"][2011]) != 2 || len(saved.Variants["253450"][2012]) != 1 {
		t.Fatal("保存丢了其他状态/条件")
	}
	bad := segment("910000101")
	if err := call(map[int][]VariantEdit{2011: {{Condition: 406, Segments: bad}}}); err == nil {
		t.Fatal("碰撞仍写入状态")
	}
	after, _ := os.ReadFile(settings)
	if string(before) != string(after) {
		t.Fatal("错误请求改了settings")
	}
	bad = segment("910000104")
	bad[0].TemplateSkillProID = "missing"
	if err := call(map[int][]VariantEdit{2011: {{Condition: 406, Segments: bad}}}); err == nil {
		t.Fatal("无效模板仍写入状态")
	}
	after, _ = os.ReadFile(settings)
	if string(before) != string(after) {
		t.Fatal("无效模板改了settings")
	}
	for _, operation := range []string{"weapon_catalog", "weapon_combo_chain"} {
		result, err := weaponHandle(Request{Operation: operation, Weapon: 253450}, client, items, folder)
		if err != nil {
			t.Fatal(err)
		}
		ids := result.(map[string]any)["variant_occupied_ids"].([]string)
		if !includes(ids, "910000103") || !includes(ids, "910000102") {
			t.Fatalf("%s遗漏其他状态占用：%v", operation, ids)
		}
	}
	if err := call(map[int][]VariantEdit{2011: {{Condition: 406, Remove: true}}}); err != nil {
		t.Fatal(err)
	}
	if err := call(map[int][]VariantEdit{}); err != nil {
		t.Fatal(err)
	}
	after, _ = os.ReadFile(settings)
	if err := json.Unmarshal(after, &saved); err != nil {
		t.Fatal(err)
	}
	// 解码复用结构需重置 map，检查原始 JSON 中该武器已清空。
	var cleared weaponState
	if err := json.Unmarshal(after, &cleared); err != nil {
		t.Fatal(err)
	}
	if len(cleared.Variants["253450"]) != 0 {
		t.Fatal("空map没有清空整把")
	}
	actual, _ := os.ReadFile(configPath(client))
	if string(actual) != string(a.data) {
		t.Fatal("保存草稿改了客户端包")
	}
}

func TestVariantTemplateMetadataMatchesIDThenIndex(t *testing.T) {
	segments := []map[string]any{{"anm_id": "b", "skillproid": "910000100"}, {"anm_id": "missing", "skillproid": "910000101"}, {"anm_id": "native", "skillproid": "100"}}
	variantTemplateMetadata(segments, []VariantAnm{{AnmID: "b", TemplateSkillProID: "101"}, {AnmID: "a", SkillProID: "102"}, {AnmID: "native", TemplateSkillProID: "103"}})
	if segments[0]["template_skillproid"] != "101" || segments[1]["template_skillproid"] != "102" || segments[2]["template_skillproid"] != nil {
		t.Fatalf("映射错误：%v", segments)
	}
}

func TestVariantIncrementalMergeAndOccupiedUnion(t *testing.T) {
	a, _ := variantFixture(t, nil)
	saved := map[int][]VariantEdit{2011: {{Condition: 406}, {Condition: 407}}, 2012: {{Condition: 408}}}
	merged, err := mergeVariantEdits(a, "253450", saved, map[int][]VariantEdit{2011: {{Condition: 406, Segments: []VariantAnm{{Name: "new"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(merged[2011]) != 2 || len(merged[2012]) != 1 || saved[2011][0].Segments != nil {
		t.Fatal("合并丢其他分支或修改原值")
	}
	merged, err = mergeVariantEdits(a, "253450", merged, map[int][]VariantEdit{2011: {{Condition: 406, Remove: true}}})
	if err != nil || len(merged[2011]) != 1 {
		t.Fatalf("删除草稿失败：%v %v", merged, err)
	}
	current, _ := variantFixture(t, map[string]string{"animation/9999.xml": `<AnmInfo><Anm skillproid="910500000" /></AnmInfo>`})
	projection, _ := variantFixture(t, map[string]string{"skillproperty.xml": `<SkillProperty><PropertyItem SkillProId="910500001" /></SkillProperty>`})
	state := &weaponState{Variants: map[string]map[int][]VariantEdit{"other": {2011: {{Condition: 406, Segments: []VariantAnm{{SkillProID: "910500002", TemplateSkillProID: "100"}}}}}}}
	ids, err := variantOccupiedIDs(state, a, current, projection)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"100", "910500000", "910500001", "910500002"} {
		if !includes(ids, want) {
			t.Fatalf("完整占用缺号%s：%v", want, ids)
		}
	}
}

func TestMergeVariantEditsRemovesCurrentOnlyBranch(t *testing.T) {
	base, items := variantFixture(t, nil)
	current, _ := variantFixture(t, map[string]string{
		"animation/2001.xml": `<AnmInfo><AnmDesc id="1"><Anm id="1" name="600180" startframe="0" endframe="18" skillproid="100" /></AnmDesc><AnmDesc id="1"><Condition><Ustate id="406" /></Condition><Anm id="1" name="600180" startframe="0" endframe="18" skillproid="100" /></AnmDesc><AnmDesc id="2"><Anm id="1" name="600180" startframe="0" endframe="18" skillproid="100" /></AnmDesc></AnmInfo>`,
	})
	merged, err := mergeVariantEdits(base, "253450", nil, map[int][]VariantEdit{
		2011: {{Condition: 406, Remove: true}},
	}, current)
	if err != nil {
		t.Fatalf("当前包已有、基线没有的分支应允许删除：%v", err)
	}
	if len(merged) != 0 {
		t.Fatalf("删除当前包历史分支后不应留下作者态标记：%v", merged)
	}
	out, err := applyVariants(base, &weaponState{Variants: map[string]map[int][]VariantEdit{
		"253450": merged,
	}}, items, current)
	if err != nil {
		t.Fatal(err)
	}
	text, err := out.text("animation/2001.xml")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, `<Ustate id="406"`) {
		t.Fatalf("候选仍带入当前包历史分支：%s", text)
	}

	merged, err = mergeVariantEdits(base, "253450", nil, map[int][]VariantEdit{
		2011: {{Condition: 407, Remove: true}},
	}, current)
	if err != nil || len(merged) != 0 {
		t.Fatalf("基线、当前包和作者态都不存在时删除应幂等成功：%v %v", merged, err)
	}
}

func TestRewriteSkillDamageInPlace(t *testing.T) {
	selfClosing := `<PropertyItem SkillProId="910000001" SkillDamage="4" />`
	if got := rewriteSkillDamage(selfClosing, 9); !strings.Contains(got, `SkillDamage="9"`) ||
		!strings.Contains(got, `SkillProId="910000001"`) || !strings.HasSuffix(strings.TrimSpace(got), "/>") {
		t.Fatalf("自闭合条目重写异常：%s", got)
	}
	missing := `<PropertyItem SkillProId="910000002" />`
	if got := rewriteSkillDamage(missing, 9); !strings.Contains(got, `SkillDamage="9"`) ||
		!strings.HasSuffix(strings.TrimSpace(got), "/>") {
		t.Fatalf("缺属性时补写异常：%s", got)
	}
	spaced := "<PropertyItem SkillProId = \"910000003\" SkillDamage = \"2\" />"
	if got := rewriteSkillDamage(spaced, 9); !strings.Contains(got, `SkillDamage="9"`) {
		t.Fatalf("带空格写法重写异常：%s", got)
	}
}
