package desktop

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestBlockElements253450EmptyStatePipeline(t *testing.T) {
	for _, cleared := range []bool{false, true} {
		t.Run(strconv.FormatBool(cleared), func(t *testing.T) {
			action := "0"
			if cleared {
				action = "2001002"
			}
			a, items := variantFixture(t, map[string]string{
				"item.txt":    "25\t253450\t6\t测试" + strings.Repeat("\t#", 13) + "\n",
				"itemact.txt": "ID\tName\t2011\t2123\n253450\t测试\t2001001\t" + action + "\n",
			})
			state := weaponState{Drafts: map[string][]Rule{}, Applied: map[string][]Rule{}, BlockElements: map[string]map[int]map[string][]BlockElement{"253450": {2123: {"FakeUnAttack": nil, "AddBuff": {}}}}}
			if cleared {
				state.Cleared = map[string]map[int]bool{"253450": {2123: true}}
				state.BlockElements["253450"][2123]["BodyGraze"] = []BlockElement{element("BodyGraze", "startframe", "1", "endframe", "2")}
				state.Remaps = map[string]map[int]*StageRemap{"253450": {2123: {Action: "2001002"}}}
				state.Drafts = map[string][]Rule{"253450": {{Stage: 2123}, {Stage: 1}}}
				state.Applied = map[string][]Rule{"253450": {{Stage: 2123}}}
				state.Chains = map[string][]ComboTransition{"253450": {{OldState: "2011", NewState: "2123"}}}
				state.PropertyClones = map[string]map[string]string{"253450": {"2123|100": "900000001", "1|100": "900000002"}}
				state.Variants = map[string]map[int][]VariantEdit{"253450": {2123: {{Condition: 406}}}}
				state.StageEffects = map[string]map[int][]StageEffect{"253450": {2123: {{Kind: "effect", EffectID: "100", Start: 1}}}}
			}
			if !cleared {
				out, err := applyBlockElements(a, &state, items)
				if err != nil || out != a {
					t.Fatalf("无动作空清除应为归档no-op：%v", err)
				}
			}
			client, folder := t.TempDir(), t.TempDir()
			if err := os.MkdirAll(filepath.Join(client, "Data"), 0700); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{configPath(client), filepath.Join(folder, "baseline.spf2")} {
				if err := os.WriteFile(path, a.data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			entry := &clientBaseline{Directory: client, File: "baseline.spf2", SourceHash: digest(a.data)}
			state.Baselines = map[string]*clientBaseline{normalizeDir(client): entry}
			raw, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			settings := filepath.Join(folder, "settings.json")
			if err := os.WriteFile(settings, raw, 0600); err != nil {
				t.Fatal(err)
			}
			result, err := weaponHandle(Request{Operation: "weapon_detail", Weapon: 253450}, client, items, folder)
			if err != nil {
				t.Fatal(err)
			}
			if failure, ok := result.(map[string]any)["remap_error"]; ok {
				t.Fatalf("detail仍退回未映射结构：%v", failure)
			}
			info, err := inspect(a, items)
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := prepareClient(entry, folder, &state, nil, info)
			if err != nil {
				t.Fatalf("prepare被空清除或Cleared残留阻断：%v", err)
			}
			out, err := parseArchive(prepared.Data)
			if err != nil {
				t.Fatal(err)
			}
			table, err := out.text("itemact.txt")
			if err != nil {
				t.Fatal(err)
			}
			header, row := itemactHeaderAndRow(table, "253450")
			found := false
			for index, name := range header {
				if name == "2123" && index < len(row) {
					found = row[index] == "0"
				}
			}
			if !found {
				t.Fatal("清除状态未保持0")
			}
			if cleared && (len(state.BlockElements["253450"]) != 0 || len(state.Variants["253450"]) != 0 || len(state.StageEffects["253450"]) != 0) {
				t.Fatal("Cleared残留引用未一致清理")
			}
			if cleared {
				if len(state.Remaps["253450"]) != 0 || len(state.Applied["253450"]) != 0 || len(state.Chains["253450"]) != 0 || len(state.Drafts["253450"]) != 1 || state.Drafts["253450"][0].Stage != 1 {
					t.Fatal("清除状态仍有重映射、规则或连招引用，或误清其他状态")
				}
				if _, ok := state.PropertyClones["253450"]["2123|100"]; ok || state.PropertyClones["253450"]["1|100"] != "900000002" {
					t.Fatal("克隆属性引用清理错误")
				}
			}
			for _, path := range []string{configPath(client), settings} {
				got, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				want := raw
				if path == configPath(client) {
					want = a.data
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("只读投影或prepare写入了%s", path)
				}
			}
		})
	}
}

func TestClearedStageReferencesProjectionIsolated(t *testing.T) {
	state := weaponState{
		Cleared: map[string]map[int]bool{"253450": {2123: true, 2011: false}, "253451": {2123: true}},
		BlockElements: map[string]map[int]map[string][]BlockElement{
			"253450": {2123: {"BodyGraze": {element("BodyGraze", "startframe", "1", "endframe", "2")}}, 2011: {"BodyGraze": nil}},
			"253451": {2123: {"BodyGraze": nil}},
		},
	}
	projected, err := detailProjectionState(&state, 253450)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := projected.BlockElements["253450"][2123]; ok {
		t.Fatal("投影仍保留已清除状态")
	}
	if _, ok := projected.BlockElements["253450"][2011]; !ok {
		t.Fatal("false清除标记误删有效编辑")
	}
	if len(state.BlockElements["253450"]) != 2 || len(state.BlockElements["253451"]) != 1 {
		t.Fatal("投影修改了原状态或其他武器")
	}
}

func TestBlockElementsNoActionRejectsNonEmptyAndUnknown(t *testing.T) {
	a, items := variantFixture(t, map[string]string{"itemact.txt": "ID\tName\t2011\t2123\n253450\t测试\t2001001\t0\n"})
	for label, edits := range map[string]map[string][]BlockElement{
		"非空":    {"BodyGraze": {element("BodyGraze", "startframe", "1", "endframe", "2")}},
		"空未知标签": {"Unknown": nil},
		"混合":    {"FakeUnAttack": nil, "AddBuff": {element("AddBuff", "frame", "1", "UnNormalState", "192")}},
	} {
		t.Run(label, func(t *testing.T) {
			state := weaponState{BlockElements: map[string]map[int]map[string][]BlockElement{"253450": {2123: edits}}}
			if _, err := applyBlockElements(a, &state, items); err == nil {
				t.Fatal("无动作的非空编辑或未知标签被静默丢弃")
			}
		})
	}
}

func element(tag string, pairs ...string) BlockElement {
	attrs := []FrameSwitchAttr{}
	for i := 0; i+1 < len(pairs); i += 2 {
		attrs = append(attrs, FrameSwitchAttr{Key: pairs[i], Value: pairs[i+1]})
	}
	return BlockElement{Tag: tag, Attrs: attrs}
}

// 改写规则：整组替换但保留第一处的位置；空列表 = 删除；本来没有就插到 </AnmDesc> 前；
// 注释里的示例不算数。**没有实例 + 空列表 = 一点都不动**（不写空配置）。
func TestRewriteBlockElementsReplaceDeleteInsert(t *testing.T) {
	addBuff := func(state string) string {
		return blockElementText(element("AddBuff", "frame", "1", "Scope", "1", "UnNormalState", state,
			"UStateLevel", "1", "UStateLastCycle", "1500", "Param1", ""))
	}
	block := `<AnmDesc id="119">` + "\n\t" +
		`<FakeUnAttack startframe="0" endframe="20" />` + "\n\t" +
		addBuff("192") + "\n\t" +
		addBuff("406") + "\n\t" +
		`<Audio frame="1" audioid="1" />` + "\n</AnmDesc>"

	// 霸体：整条替换（单实例），位置不变。
	out, changed := rewriteBlockElements(block, "FakeUnAttack",
		[]BlockElement{element("FakeUnAttack", "startframe", "5", "endframe", "30")})
	if !changed || strings.Count(out, "<FakeUnAttack") != 1 {
		t.Fatalf("霸体应当替换成一条：%s", out)
	}
	if !strings.Contains(out, `endframe="30"`) || !strings.Contains(out, "<Audio") {
		t.Fatalf("新值没写进去或兄弟节点被动了：%s", out)
	}
	if strings.Index(out, "<FakeUnAttack") > strings.Index(out, "<AddBuff") {
		t.Fatalf("应当保留原位置：%s", out)
	}

	// 自身状态（AddBuff 位于 AnmDesc 直属层）：整组替换（两条 → 一条），保留第一处位置。
	out2, changed := rewriteBlockElements(out, "AddBuff",
		[]BlockElement{element("AddBuff", "frame", "3", "Scope", "1", "UnNormalState", "354",
			"UStateLevel", "2", "UStateLastCycle", "3000", "Param1", "")})
	if !changed || strings.Count(out2, "<AddBuff") != 1 {
		t.Fatalf("AddBuff 应当只剩一条：%s", out2)
	}
	if strings.Contains(out2, `UnNormalState="406"`) {
		t.Fatalf("旧的那条应当被替换掉：%s", out2)
	}

	// 空列表 = 删除，且不留空行。
	out3, changed := rewriteBlockElements(out2, "AddBuff", nil)
	if !changed || strings.Contains(out3, "<AddBuff") || strings.Contains(out3, "\n\n") {
		t.Fatalf("空列表应当删干净且不留空行：%q", out3)
	}

	// 本来没有 → 插到 </AnmDesc> 前。
	plain := `<AnmDesc id="1"><Audio frame="1" /></AnmDesc>`
	out4, changed := rewriteBlockElements(plain, "BodyGraze",
		[]BlockElement{element("BodyGraze", "startframe", "1", "endframe", "10")})
	if !changed || strings.Count(out4, "<BodyGraze") != 1 ||
		strings.Index(out4, "<BodyGraze") > strings.Index(out4, "</AnmDesc") {
		t.Fatalf("应当插到 AnmDesc 之内：%s", out4)
	}

	// 没有实例 + 空列表 ⇒ 一点都不动（这正是「不预填空配置」的保证）。
	if _, changed := rewriteBlockElements(plain, "FakeUnAttack", nil); changed {
		t.Fatal("没有实例又传空列表时不该报告改动")
	}
	// 注释里的示例不算真数据。
	commented := `<AnmDesc><!-- <BodyGraze startframe="1" endframe="2" /> --><Audio/></AnmDesc>`
	if _, changed := rewriteBlockElements(commented, "BodyGraze", nil); changed {
		t.Fatal("注释里的元素不该被当成真数据")
	}
}

// UState 只住在 <LockedAttackHit> 里（命中给目标）、Ustate 只住在 <Condition> 里（触发条件）。
// 这条约束是本次修复的核心：以前两者会被当成「自身状态」采集和改写，语义全错。
func TestBlockElementsArchiveRoundTripSeparatesConditionHitAndSelfBuff(t *testing.T) {
	a, items := variantFixture(t, map[string]string{
		"animation/2001.xml": `<AnmInfo>` +
			`<AnmDesc id="1"><Condition><Ustate id="354" /></Condition>` +
			`<LockedAttackHit frame="1" damage="3"><UState id="192" level="1" duration="1500" /></LockedAttackHit>` +
			`<Audio frame="1" /></AnmDesc>` +
			`<AnmDesc id="2"><Audio frame="1" /></AnmDesc>` +
			`</AnmInfo>`,
	})
	state := &weaponState{BlockElements: map[string]map[int]map[string][]BlockElement{
		"253450": {
			2011: {
				"Ustate": {element("Ustate", "id", "406")},
				"UState": {element("UState", "id", "420", "level", "2", "duration", "800")},
			},
			2012: {
				"AddBuff": {element("AddBuff", "frame", "10", "Scope", "1", "UnNormalState", "435", "UStateLevel", "1", "UStateLastCycle", "3000")},
			},
		},
	}}
	out, err := applyBlockElements(a, state, items)
	if err != nil {
		t.Fatalf("三类状态元素归档写回失败：%v", err)
	}
	text, err := out.text("animation/2001.xml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, `<Condition><Ustate id="406" /></Condition>`) {
		t.Fatalf("动作分支条件没有写回 Condition：%s", text)
	}
	if !strings.Contains(text, `<LockedAttackHit frame="1" damage="3"><UState id="420" level="2" duration="800" /></LockedAttackHit>`) {
		t.Fatalf("命中目标状态没有写回 LockedAttackHit：%s", text)
	}
	if !strings.Contains(text, `<AnmDesc id="2"><Audio frame="1" /><AddBuff frame="10" Scope="1" UnNormalState="435" UStateLevel="1" UStateLastCycle="3000" />`) {
		t.Fatalf("2012 自身状态没有写回直属 AddBuff：%s", text)
	}
	if strings.Contains(text, `<AnmDesc id="2"><Condition>`) || strings.Contains(text, `<AnmDesc id="2"><LockedAttackHit>`) {
		t.Fatalf("2012 写 AddBuff 时不应凭空创建条件或命中容器：%s", text)
	}
	if err := out.verify(); err != nil {
		t.Fatalf("写回后的归档校验失败：%v", err)
	}
}

func TestBlockElementPlacementScopedToContainer(t *testing.T) {
	block := `<AnmDesc id="7">` +
		`<Condition><Ustate id="354" /></Condition>` +
		`<LockedAttackHit frame="1" damage="3"><UState id="192" level="1" duration="1500"/></LockedAttackHit>` +
		`<Audio frame="1" /></AnmDesc>`

	// 换命中点里的 UState：只动 <LockedAttackHit> 内那一条。
	out, changed := rewriteBlockElements(block, "UState",
		[]BlockElement{element("UState", "id", "406", "level", "2", "duration", "800")})
	if !changed || !strings.Contains(out, `id="406"`) || strings.Count(out, "<UState") != 1 {
		t.Fatalf("命中点的 UState 应当被整组替换：%s", out)
	}
	if !strings.Contains(out, `<Ustate id="354" />`) || !strings.Contains(out, "<Condition>") {
		t.Fatalf("Condition 里的 Ustate 不该被动：%s", out)
	}

	// 换条件里的 Ustate：只动 <Condition> 内那一条。
	out2, changed := rewriteBlockElements(block, "Ustate",
		[]BlockElement{element("Ustate", "id", "420")})
	if !changed || !strings.Contains(out2, `id="420"`) {
		t.Fatalf("条件的 Ustate 应当被替换：%s", out2)
	}
	if !strings.Contains(out2, `<UState id="192"`) {
		t.Fatalf("命中点的 UState 不该被动：%s", out2)
	}

	// 块里没有对应容器 → 不写也不插（由调用方给出可读错误）。
	noContainers := `<AnmDesc id="7"><Audio frame="1" /></AnmDesc>`
	if _, changed := rewriteBlockElements(noContainers, "UState",
		[]BlockElement{element("UState", "id", "192", "level", "1", "duration", "1500")}); changed {
		t.Fatal("没有 <LockedAttackHit> 时不该写 UState")
	}
	if _, changed := rewriteBlockElements(noContainers, "Ustate",
		[]BlockElement{element("Ustate", "id", "354")}); changed {
		t.Fatal("没有 <Condition> 时不该写 Ustate")
	}
	if blockElementWritable(noContainers, blockElementSpecOf(t, "UState")) {
		t.Fatal("没有 <LockedAttackHit> 时应当报告不可写，交给调用方报错")
	}
	if blockElementWritable(noContainers, blockElementSpecOf(t, "Ustate")) {
		t.Fatal("没有 <Condition> 时应当报告不可写，交给调用方报错")
	}

	// 自身状态使用直属 AddBuff，不依赖 Condition；这正是没有条件块的
	// 普通动作（例如 253451 的 2012）应走的写回路径。
	selfBuff := element("AddBuff", "frame", "10", "Scope", "1",
		"UnNormalState", "435", "UStateLevel", "1", "UStateLastCycle", "3000")
	selfOut, changed := rewriteBlockElements(noContainers, "AddBuff", []BlockElement{selfBuff})
	if !changed || !strings.Contains(selfOut, `<AddBuff frame="10"`) ||
		!strings.Contains(selfOut, `UnNormalState="435"`) {
		t.Fatalf("无 Condition 的动作块应能写入直属 AddBuff：%s", selfOut)
	}
	if strings.Contains(selfOut, "<Condition>") {
		t.Fatalf("AddBuff 写回不应凭空创建 Condition：%s", selfOut)
	}

	// 有容器但里面空着 → 插进容器内，而不是 </AnmDesc> 前。
	emptyHit := `<AnmDesc id="7"><LockedAttackHit frame="1" damage="3"></LockedAttackHit>` +
		`<Audio frame="1" /></AnmDesc>`
	out3, changed := rewriteBlockElements(emptyHit, "UState",
		[]BlockElement{element("UState", "id", "192", "level", "1", "duration", "1500")})
	if !changed || strings.Index(out3, "<UState") > strings.Index(out3, "</LockedAttackHit") {
		t.Fatalf("UState 必须落在 <LockedAttackHit> 之内：%s", out3)
	}
}

// blockElementSpecOf 取规格，取不到直接失败（测试用）。
func blockElementSpecOf(t *testing.T, tag string) blockElementSpec {
	t.Helper()
	spec, ok := blockElementSpecFor(tag)
	if !ok {
		t.Fatalf("缺少规格：%s", tag)
	}
	return spec
}

// 校验：只检查编辑集里出现过的状态/标签；单实例标签不许重复；必填/范围/帧序都要过。
func TestValidateBlockElementsScopedToEditedTags(t *testing.T) {
	client := os.Getenv("OPENKFO_WEAPON_TEST_CLIENT")
	if client == "" {
		t.Skip("set OPENKFO_WEAPON_TEST_CLIENT for installed resource validation")
	}
	a, err := loadArchive(configPath(client))
	if err != nil {
		t.Fatalf("读取客户端失败：%v", err)
	}
	// 253119 的 2011：给霸体、无敌各一条自身状态，合法。
	good := map[int]map[string][]BlockElement{2011: {
		"FakeUnAttack": {element("FakeUnAttack", "startframe", "0", "endframe", "20")},
		"DirectionalInvc": {element("DirectionalInvc", "startframe", "0", "endframe", "35",
			"angleoffset", "0", "anglehalfrange", "55")},
		"UState": {element("UState", "id", "192", "level", "1", "duration", "1500")},
	}}
	if err := validateBlockElements(a, "253119", good); err != nil {
		t.Fatalf("合法编辑被拒：%v", err)
	}
	// 只写一个标签（其余不碰）也必须合法。
	if err := validateBlockElements(a, "253119",
		map[int]map[string][]BlockElement{2011: {"BodyGraze": nil}}); err != nil {
		t.Fatalf("空列表（=删除该标签）应该合法：%v", err)
	}

	for label, bad := range map[string]map[int]map[string][]BlockElement{
		"未知标签": {2011: {"Nope": {element("Nope", "startframe", "0")}}},
		"未知属性": {2011: {"FakeUnAttack": {element("FakeUnAttack", "startframe", "0", "endframe", "9", "bogus", "1")}}},
		"缺必填":  {2011: {"FakeUnAttack": {element("FakeUnAttack", "startframe", "0")}}},
		"帧倒置":  {2011: {"BodyGraze": {element("BodyGraze", "startframe", "9", "endframe", "1")}}},
		"帧超范围": {2011: {"FakeUnAttack": {element("FakeUnAttack", "startframe", "0", "endframe", "10000")}}},
		"角度越界": {2011: {"DirectionalInvc": {element("DirectionalInvc", "startframe", "0", "endframe", "9", "anglehalfrange", "999")}}},
		"单实例两条": {2011: {"FakeUnAttack": {
			element("FakeUnAttack", "startframe", "0", "endframe", "5"),
			element("FakeUnAttack", "startframe", "6", "endframe", "9")}}},
		"状态不存在": {1: {"FakeUnAttack": {element("FakeUnAttack", "startframe", "0", "endframe", "5")}}},
	} {
		if err := validateBlockElements(a, "253119", bad); err == nil {
			t.Fatalf("%s 应当被拒", label)
		}
	}
}

// 状态目录：来自 ustate.xml，id 唯一且递增，带策划中文名。
func TestUstateCatalogHasNames(t *testing.T) {
	client := os.Getenv("OPENKFO_WEAPON_TEST_CLIENT")
	if client == "" {
		t.Skip("set OPENKFO_WEAPON_TEST_CLIENT for installed resource validation")
	}
	a, err := loadArchive(configPath(client))
	if err != nil {
		t.Fatalf("读取客户端失败：%v", err)
	}
	catalog := ustateCatalog(a)
	if len(catalog) < 100 {
		t.Fatalf("状态目录条目太少：%d", len(catalog))
	}
	seen := map[string]bool{}
	previous := 0
	named := 0
	names := map[string]string{}
	for _, item := range catalog {
		id := item["id"]
		if seen[id] {
			t.Fatalf("状态 id 重复：%s", id)
		}
		seen[id] = true
		number, err := strconv.Atoi(id)
		if err != nil || number <= previous {
			t.Fatalf("状态 id 应当递增：%s 在 %d 之后", id, previous)
		}
		previous = number
		if item["name"] != "" {
			named++
			names[id] = item["name"]
		}
	}
	if named < len(catalog)/2 {
		t.Fatalf("带中文名的条目太少：%d/%d", named, len(catalog))
	}
	// 数据里的注释把 10 号写得很清楚，用它当锚点。
	if !strings.Contains(names["10"], "无敌") {
		t.Fatalf("10 号应当是「无敌」，实际 %q", names["10"])
	}
}

// ForceField 带子元素 <ScopeBox>：改写要输出嵌套结构，校验要求六个盒尺寸齐全。
func TestForceFieldChildElement(t *testing.T) {
	block := `<AnmDesc id="1"><Audio frame="1" /></AnmDesc>`
	ff := BlockElement{
		Tag:   "ForceField",
		Attrs: []FrameSwitchAttr{{"startframe", "0"}, {"endframe", "999"}, {"pushspeed", "5"}, {"friendlyfire", "1"}},
		Box: []FrameSwitchAttr{{"centerx", "0"}, {"centery", "70"}, {"centerz", "10"},
			{"length", "80"}, {"width", "80"}, {"heigth", "160"}},
	}
	out, changed := rewriteBlockElements(block, "ForceField", []BlockElement{ff})
	if !changed || !strings.Contains(out, "<ScopeBox") ||
		!strings.Contains(out, "</ForceField>") {
		t.Fatalf("应当输出带子元素的 ForceField：%s", out)
	}
	if strings.Contains(out, "<ForceField") && !strings.Contains(out, `heigth="160"`) {
		t.Fatalf("子元素属性没写进去：%s", out)
	}

	// 校验：缺子元素、子元素属性不全、子元素未知属性都要拒。
	client := os.Getenv("OPENKFO_WEAPON_TEST_CLIENT")
	if client == "" {
		return // 没有真实客户端就只测改写
	}
	a, err := loadArchive(configPath(client))
	if err != nil {
		t.Fatalf("读取客户端失败：%v", err)
	}
	noBox := ff
	noBox.Box = nil
	if err := validateBlockElements(a, "253119",
		map[int]map[string][]BlockElement{2011: {"ForceField": {noBox}}}); err == nil {
		t.Fatal("力场缺作用盒子元素应当被拒")
	}
	short := ff
	short.Box = ff.Box[:3]
	if err := validateBlockElements(a, "253119",
		map[int]map[string][]BlockElement{2011: {"ForceField": {short}}}); err == nil {
		t.Fatal("作用盒六个尺寸不齐应当被拒")
	}
	if err := validateBlockElements(a, "253119",
		map[int]map[string][]BlockElement{2011: {"ForceField": {ff}}}); err != nil {
		t.Fatalf("合法力场被拒：%v", err)
	}
}
