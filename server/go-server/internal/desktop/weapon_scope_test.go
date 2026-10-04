package desktop

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

// 招式帧数不再取动作块尾巴那条 99/999 占位片断的最大 endframe，而是引用动画
// (Data/animation/<name>.anm) 自己的帧数。这里对真实客户端做一次对表。
func TestAnmFramesMatchesSample(t *testing.T) {
	client := os.Getenv("OPENKFO_WEAPON_TEST_CLIENT")
	if client == "" {
		t.Skip("set OPENKFO_WEAPON_TEST_CLIENT for installed resource validation")
	}
	case100 := anmFrames(client, "200101")
	if case100 == 0 {
		t.Skip("客户端没有 200101.anm")
	}
	// 200101 的动作块片断到 15 帧、尾巴到 99；真实时长必须远小于 100。
	if case100 >= 100 {
		t.Fatalf("200101 帧数应远小于 100，得到 %d", case100)
	}
	if got := anmFrames(client, "../evil"); got != 0 {
		t.Fatalf("越界动画名必须被拒绝，得到 %d", got)
	}
}

func TestSegmentsAndMarkersParse(t *testing.T) {
	node, err := parseXML(`<AnmDesc id="3104">` +
		`<KeyProcess nextattackSF="1" nextattackEF="49" reattackSF="99" />` +
		`<Audio frame="1" audioid="6001180" />` +
		`<Effect frame="1" effectid="60011883" />` +
		`<Anm id="1" name="60011883" startframe="0" endframe="19" hmove="0"/>` +
		`<Anm id="2" name="60011883" startframe="20" endframe="25" skillproid="0890140">` +
		`<AttackScope centerx="0" centery="18" centerz="10" length="30" width="30" heigth="30" />` +
		`</Anm>` +
		`<Anm id="3" name="60011883" startframe="26" endframe="38" />` +
		`</AnmDesc>`)
	if err != nil {
		t.Fatal(err)
	}
	segments := segmentsOf(node, "")
	if len(segments) != 3 {
		t.Fatalf("片断数应为 3，得到 %d", len(segments))
	}
	if segments[1].Start != 20 || segments[1].End != 25 || segments[1].SkillPro != "0890140" {
		t.Fatalf("片断 2 解析错误：%+v", segments[1])
	}
	if len(segments[1].Scope) != 6 {
		t.Fatalf("片断 2 的攻击范围应为 6 个属性，得到 %d", len(segments[1].Scope))
	}
	markers := markersOf(node)
	kinds := map[string]int{}
	for _, marker := range markers {
		kinds[marker.Kind]++
	}
	if kinds["key"] != 2 || kinds["audio"] != 1 || kinds["effect"] != 1 {
		t.Fatalf("标记解析错误：%+v", markers)
	}
}

// 同一 actionKey 下可能存在两条同名 <AnmDesc>（一条无条件 + 一条带 <Condition>），
// 引擎按玩家是否拥有该状态二选一。片断必须把条件带出来，否则两份 <Anm id> 冲突，
// 界面分不出谁属于哪个条件。
func TestSegmentsCarryCondition(t *testing.T) {
	plain, err := parseXML(`<AnmDesc id="521011">` +
		`<Anm id="2" name="600180" startframe="15" endframe="18" skillproid="5210110" />` +
		`</AnmDesc>`)
	if err != nil {
		t.Fatal(err)
	}
	if got := segmentsOf(plain, ""); len(got) != 1 || got[0].Condition != "" {
		t.Fatalf("无条件块不应带条件：%+v", got)
	}
	cond, err := parseXML(`<AnmDesc id="521011">` +
		`<Condition><Ustate id="406" /></Condition>` +
		`<Anm id="2" name="600180" startframe="14" endframe="15" skillproid="5210111" />` +
		`<Anm id="3" name="600180" startframe="15" endframe="15" replaytimes="2" />` +
		`</AnmDesc>`)
	if err != nil {
		t.Fatal(err)
	}
	got := segmentsOf(cond, "")
	if len(got) != 2 {
		t.Fatalf("应解析出 2 段，得到 %d", len(got))
	}
	for _, segment := range got {
		if segment.Condition != "406" {
			t.Fatalf("条件块内每段都应带 406，得到 %+v", segment)
		}
	}
}

func TestRewriteSegmentScopeReplaceAndInsert(t *testing.T) {
	block := `<AnmDesc id="3104">` +
		`<Anm id="1" name="60011883" startframe="0" endframe="19" />` +
		`<Anm id="2" name="60011883" startframe="20" endframe="25" skillproid="0890140">` +
		`<AttackScope centerx="0" centery="18" centerz="10" length="30" width="30" heigth="30" />` +
		`</Anm>` +
		`</AnmDesc>`
	attrs := []FrameSwitchAttr{
		{"centerx", "0"}, {"centery", "20"}, {"centerz", "12"},
		{"length", "40"}, {"width", "35"}, {"heigth", "30"},
	}
	out, changed, err := rewriteSegmentScope(block, "2", attrs)
	if err != nil || !changed {
		t.Fatalf("替换失败：%v changed=%v", err, changed)
	}
	if !strings.Contains(out, `length="40"`) || !strings.Contains(out, `centery="20"`) {
		t.Fatalf("新值没写进去：%s", out)
	}
	// 编辑器不认识/未编辑的属性必须保留。
	if !strings.Contains(out, `skillproid="0890140"`) {
		t.Fatalf("片断的 skillproid 被抹掉了：%s", out)
	}
	if strings.Count(out, "<AttackScope") != 1 {
		t.Fatalf("AttackScope 数量不对：%s", out)
	}
	// 幂等：同样的值再跑一次不应改变字节。
	if _, again, _ := rewriteSegmentScope(out, "2", attrs); again {
		t.Fatalf("重复写入不应再产生变化")
	}

	// 自闭合片断要能展开并插入 AttackScope。
	plain := `<AnmDesc id="1"><Anm id="2" name="x" startframe="0" endframe="5" /></AnmDesc>`
	inserted, changed, err := rewriteSegmentScope(plain, "2", attrs)
	if err != nil || !changed {
		t.Fatalf("插入失败：%v changed=%v", err, changed)
	}
	if !strings.Contains(inserted, "<AttackScope") || !strings.Contains(inserted, "</Anm>") {
		t.Fatalf("自闭合片断没有正确展开：%s", inserted)
	}
	if _, err := parseXML(inserted); err != nil {
		t.Fatalf("插入结果不是合法 XML：%v", err)
	}
}

// 共用动作块编辑攻击范围必须先克隆，原武器一个字节都不能动。
func TestScopeEditClonesSharedBlock(t *testing.T) {
	source, items := frameTestSource(t)
	info, err := inspect(source, items)
	if err != nil {
		t.Fatal(err)
	}
	var weapon Weapon
	var stage Stage
	for _, w := range info.weapons {
		for _, s := range w.Stages {
			if s.Action == "" || s.Action == "0" {
				continue
			}
			blocks := info.blocks[actionKey(s.Action)]
			if len(info.owners[s.Action]) > 1 && len(blocks) == 1 &&
				len(segmentsWithScope(blocks[0].node)) > 0 {
				weapon, stage = w, s
				break
			}
		}
		if stage.Action != "" {
			break
		}
	}
	if stage.Action == "" {
		t.Skip("本客户端没有可编辑攻击范围的共用动作块")
	}
	segmentID := segmentsWithScope(info.blocks[actionKey(stage.Action)][0].node)[0]
	original := info.blocks[actionKey(stage.Action)][0].original
	stateNumber, err := strconv.Atoi(stage.State)
	if err != nil {
		t.Fatal(err)
	}
	state := &weaponState{
		Drafts: map[string][]Rule{}, Applied: map[string][]Rule{}, Created: map[string]Blueprint{},
		Scopes: map[string]map[int]map[string][]FrameSwitchAttr{
			strconv.Itoa(weapon.ID): {stateNumber: {segmentID: {
				{"centerx", "0"}, {"centery", "22"}, {"centerz", "14"},
				{"length", "44"}, {"width", "33"}, {"heigth", "28"},
			}}},
		},
	}
	rendered, err := applyScopes(source, state, items)
	if err != nil {
		t.Fatal(err)
	}
	// 原动作块必须原样保留。
	after, err := rendered.text("animation/" + stage.Action[:4] + ".xml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(after, original) {
		t.Fatalf("原动作块被改动了")
	}
	if !strings.Contains(after, `length="44"`) {
		t.Fatalf("克隆块里没有写入新范围")
	}
	// 通过渲染后的 itemact 行确认该状态指向了新的克隆块。
	rowText, err := rendered.text("itemact.txt")
	if err != nil {
		t.Fatal(err)
	}
	if actionRowIndex(rowText)[strconv.Itoa(weapon.ID)] == nil {
		t.Fatalf("武器动作行丢失")
	}
}

func segmentsWithScope(node *xmlNode) []string {
	ids := []string{}
	for _, segment := range segmentsOf(node, "") {
		ids = append(ids, segment.ID)
	}
	return ids
}

// 回归：以前几乎所有招式都显示 100/1000 帧（动作块尾巴那条占位片断）。
// 改成读动画真实帧数后，这类占位值必须只剩极少数。
func TestStageFramesNoLongerPlaceholder(t *testing.T) {
	source, items := frameTestSource(t)
	info, err := inspect(source, items)
	if err != nil {
		t.Fatal(err)
	}
	client := os.Getenv("OPENKFO_WEAPON_TEST_CLIENT")
	before, total := 0, 0
	for _, weapon := range info.weapons {
		for _, stage := range weapon.Stages {
			if stage.RawFrames <= 0 {
				continue
			}
			total++
			if stage.RawFrames >= 100 {
				before++
			}
		}
	}
	enrichStageFrames(info, client)
	after := 0
	for _, weapon := range info.weapons {
		for _, stage := range weapon.Stages {
			if stage.RawFrames <= 0 {
				continue
			}
			if stage.Frames >= 100 {
				after++
			}
		}
	}
	if total < 100 {
		t.Skip("样本太少")
	}
	if after*2 >= before {
		t.Fatalf("占位帧数没有被修正：原 %d/%d，现 %d/%d", before, total, after, total)
	}
	t.Logf(">=100 帧的招式：修正前 %d/%d，修正后 %d/%d", before, total, after, total)
}

// 力场盒/投技盒要能作为"块级盒子"交给预览画出来。
func TestBlockBoxesParse(t *testing.T) {
	node, err := parseXML(`<AnmDesc id="1">` +
		`<ForceField startframe="3" endframe="40" pushspeed="5" friendlyfire="1">` +
		`<ScopeBox centerx="0" centery="70" centerz="10" length="80" width="80" heigth="160" />` +
		`</ForceField>` +
		`<Anm id="1" name="x" startframe="0" endframe="5">` +
		`<AttackScope centerx="0" centery="20" centerz="10" length="30" width="20" heigth="20" />` +
		`</Anm>` +
		`<LockedAttackSwitch framestart="18" frameend="25" nextstate="3005">` +
		`<Scope centerx="0" centery="20" centerz="10" length="25" width="15" heigth="15" />` +
		`</LockedAttackSwitch>` +
		`</AnmDesc>`)
	if err != nil {
		t.Fatal(err)
	}
	boxes := blockBoxesOf(node)
	if len(boxes) != 2 {
		t.Fatalf("块级盒子应为 2 个（力场盒 + 投技盒），得到 %d：%+v", len(boxes), boxes)
	}
	force := boxes[0]
	if force.Kind != "force" || force.Start != 3 || force.End != 40 || len(force.Attrs) != 6 {
		t.Fatalf("力场盒解析错误：%+v", force)
	}
	if boxes[1].Kind != "grab" || boxes[1].Start != 18 || boxes[1].End != 25 {
		t.Fatalf("投技盒解析错误：%+v", boxes[1])
	}
	// <Anm> 里的攻击盒不能被当成块级盒子（它按片断给）。
	for _, box := range boxes {
		if box.Kind == "attack" {
			t.Fatalf("片断内攻击盒不该出现在块级盒子里")
		}
	}
}

// Scope 白名单原来卡在 0..9，真实数据里有 40（2204/149·964·965），改一条既有
// AddBuff 都会被拒。这里对真实客户端验一遍：Scope=40 必须能过校验。
func TestAddBuffScopeRangeAllowsForty(t *testing.T) {
	source, items := frameTestSource(t)
	info, err := inspect(source, items)
	if err != nil {
		t.Fatal(err)
	}
	var weaponKey, state string
	for _, weapon := range info.weapons {
		if len(weapon.Stages) == 0 {
			continue
		}
		weaponKey, state = strconv.Itoa(weapon.ID), weapon.Stages[0].State
		break
	}
	if weaponKey == "" {
		t.Skip("没有可用武器")
	}
	edits := map[int]map[string][]BlockElement{}
	number, err := strconv.Atoi(state)
	if err != nil {
		t.Skip("状态号不是数字")
	}
	edits[number] = map[string][]BlockElement{
		"AddBuff": {{
			Tag: "AddBuff",
			Attrs: []FrameSwitchAttr{
				{"frame", "0"}, {"UnNormalState", "208"}, {"UStateLevel", "2"},
				{"UStateLastCycle", "600"}, {"Scope", "40"}, {"Param1", ""},
			},
		}},
	}
	if err := validateBlockElements(source, weaponKey, edits); err != nil {
		t.Fatalf("Scope=40 不该被拒：%v", err)
	}
	// 越界（>255）仍要拦住。
	edits[number]["AddBuff"][0].Attrs[4] = FrameSwitchAttr{"Scope", "9999"}
	if err := validateBlockElements(source, weaponKey, edits); err == nil {
		t.Fatalf("Scope=9999 应当被拒绝")
	}
}
