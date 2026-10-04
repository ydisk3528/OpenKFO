package desktop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// 端到端：分支编辑走完整 prepareClient 管线，必须通过白名单校验、产出合法包，
// 且条件块真的落到动画文件里。
func TestVariantEditPassesPrepareClient(t *testing.T) {
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
	if err = os.WriteFile(filepath.Join(client, "baseline.spf2"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	entry := &clientBaseline{Directory: client, File: "baseline.spf2", SourceHash: digest(raw), AppliedHash: digest(raw)}
	source, err := loadArchive(filepath.Join(client, "baseline.spf2"))
	if err != nil {
		t.Fatal(err)
	}
	if err = source.verify(); err != nil {
		t.Fatal(err)
	}
	itemText, err := source.text("item.txt")
	if err != nil {
		t.Fatal(err)
	}
	items, err := itemsFromText(client, itemText, true, true)
	if err != nil {
		t.Fatal(err)
	}
	info, err := inspect(source, items)
	if err != nil {
		t.Fatal(err)
	}

	// 找一把「动作块被多把武器共用、且 2xxx 有 skillproid」的武器 —— 顺手覆盖
	// 共用块克隆 + 条件块一起复制的路径。
	weaponKey, stageNum, action := "", 0, ""
	for _, w := range info.weapons {
		for _, st := range w.Stages {
			if !stageNeedsSplit(st.State) {
				continue
			}
			if len(info.owners[st.Action]) < 2 {
				continue
			}
			base, ok := pickBlock(actionVariants(info, st.Action))
			if !ok {
				continue
			}
			has := false
			base.node.walk(func(n *xmlNode) {
				if n.tag == "Anm" && strings.TrimSpace(n.get("skillproid")) != "" {
					has = true
				}
			})
			if has {
				weaponKey, action = strconv.Itoa(w.ID), st.Action
				stageNum = mustAtoi(t, strings.TrimSpace(st.State))
				break
			}
		}
		if weaponKey != "" {
			break
		}
	}
	if weaponKey == "" {
		t.Skip("没有共用块样本")
	}

	const condition = 192
	state := &weaponState{
		Baselines: map[string]*clientBaseline{normalizeDir(client): entry},
		Variants:  map[string]map[int][]VariantEdit{weaponKey: {stageNum: {{Condition: condition}}}},
	}
	prepared, err := prepareClient(entry, client, state, map[string][]Rule{}, info)
	if err != nil {
		t.Fatalf("分支编辑没能通过管线：%v", err)
	}
	out, err := parseArchive(prepared.Data)
	if err != nil {
		t.Fatal(err)
	}
	// 新武器动作号（共用块会被隔离换号）。取 action 行看变化。
	actionText, err := out.text("itemact.txt")
	if err != nil {
		t.Fatal(err)
	}
	row := actionRowIndex(actionText)[weaponKey]
	if row == nil {
		t.Fatal("武器动作行丢失")
	}
	header := itemactHeader(actionText)
	newAction := ""
	for j, st := range header {
		if j >= 2 && j < len(row) && strings.TrimSpace(st) == strconv.Itoa(stageNum) {
			newAction = strings.TrimSpace(row[j])
		}
	}
	if newAction == "" {
		t.Fatal("找不到该状态的动作号")
	}
	file := "animation/" + newAction[:4] + ".xml"
	text, err := out.text(file)
	if err != nil {
		t.Fatal(err)
	}
	key := actionKey(newAction)
	id := key[strings.IndexByte(key, '/')+1:]
	if got := countConditionBlocks(text, id, strconv.Itoa(condition)); got != 1 {
		t.Fatalf("条件 %d 的分支块应当正好 1 份，得到 %d（动作 %s）", condition, got, newAction)
	}
	if len(variantTexts(text, id)) < 2 {
		t.Fatalf("隔离后应当同时持有无条件块与条件块，动作 %s", newAction)
	}
	_ = action
}

func TestVariant253450StageHitsFourReal(t *testing.T) {
	installed := os.Getenv("OPENKFO_WEAPON_TEST_CLIENT")
	if installed == "" {
		t.Skip("需要 OPENKFO_WEAPON_TEST_CLIENT 真实资源")
	}
	raw, err := os.ReadFile(configPath(installed))
	if err != nil {
		t.Fatal(err)
	}
	client := t.TempDir()
	if err := os.MkdirAll(filepath.Join(client, "Data"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath(client), raw, 0600); err != nil {
		t.Fatal(err)
	}
	a, err := loadArchive(configPath(client))
	if err != nil {
		t.Fatal(err)
	}
	text, err := a.text("item.txt")
	if err != nil {
		t.Fatal(err)
	}
	items, err := itemsFromText(client, text, true, true)
	if err != nil {
		t.Fatal(err)
	}
	info, err := inspect(a, items)
	if err != nil {
		t.Fatal(err)
	}
	action := ""
	for _, weapon := range info.weapons {
		if weapon.ID == 253450 {
			for _, stage := range weapon.Stages {
				if stage.State == "2011" {
					action = stage.Action
				}
			}
		}
	}
	if action == "" {
		t.Fatal("真实资源缺少253450/2011")
	}
	base, ok := pickBlock(actionVariants(info, action))
	if !ok {
		t.Fatal("缺少默认动作")
	}
	var source VariantAnm
	for _, segment := range segmentsOf(base.node, "") {
		if segment.SkillPro != "" && len(info.properties[segment.SkillPro]) > 0 {
			source = VariantAnm{Name: segment.Name, Start: segment.Start, End: segment.End, SkillProID: segment.SkillPro}
			break
		}
	}
	if source.SkillProID == "" {
		t.Fatal("默认动作缺少命中模板")
	}
	// 样本已有406时只在临时归档删除，再验证从原始动作建立三个独立命中。
	for _, branch := range actionVariants(info, action) {
		if branch.condition == "406" {
			a, err = applyVariants(a, &weaponState{Variants: map[string]map[int][]VariantEdit{"253450": {2011: {{Condition: 406, Remove: true}}}}}, items)
			if err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	alloc, err := newVariantAllocator(a)
	if err != nil {
		t.Fatal(err)
	}
	segments := []VariantAnm{}
	for index := 0; index < 3; index++ {
		id, err := alloc.next()
		if err != nil {
			t.Fatal(err)
		}
		segments = append(segments, VariantAnm{Name: source.Name, Start: source.Start + index, End: source.Start + index + 1, SkillProID: id, TemplateSkillProID: source.SkillProID, Damage: 4.7})
	}
	segments = append(segments, VariantAnm{Name: source.Name, Start: source.Start, End: source.Start, ReplayTimes: 2})
	state := &weaponState{Variants: map[string]map[int][]VariantEdit{"253450": {2011: {{Condition: 406, Segments: segments}}}}}
	out, err := applyVariants(a, state, items)
	if err != nil {
		t.Fatal(err)
	}
	info, err = inspect(out, items)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, weapon := range info.weapons {
		if weapon.ID != 253450 {
			continue
		}
		for _, stage := range weapon.Stages {
			if stage.State != "2011" {
				continue
			}
			found = true
			if len(stage.Hits) != 4 {
				t.Fatalf("真实253450/2011 Hits期望4条，得到%d：%+v", len(stage.Hits), stage.Hits)
			}
		}
	}
	if !found {
		t.Fatal("重建后丢了253450/2011")
	}
	branches := comboVariants(info, "253450", state.Variants["253450"])
	for _, branch := range branches[2011] {
		if branch["condition"] != "406" {
			continue
		}
		encoded, err := json.Marshal(branch["segments"])
		if err != nil {
			t.Fatal(err)
		}
		var returned []VariantAnm
		if err := json.Unmarshal(encoded, &returned); err != nil {
			t.Fatal(err)
		}
		state.Variants["253450"][2011][0].Segments = returned
	}
	rebuilt, err := applyVariants(a, state, items, out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytesEqualArchive(t, out, rebuilt, true) {
		t.Fatal("真实资源回读后从原始基线重建不幂等")
	}
	actual, err := os.ReadFile(configPath(installed))
	if err != nil || string(actual) != string(raw) {
		t.Fatal("真实客户端资源发生变化")
	}
}

func TestVariantSavedVisibleThroughGMRead(t *testing.T) {
	installed := os.Getenv("OPENKFO_WEAPON_TEST_CLIENT")
	if installed == "" {
		t.Skip("set OPENKFO_WEAPON_TEST_CLIENT")
	}
	raw, err := os.ReadFile(configPath(installed))
	if err != nil {
		t.Fatal(err)
	}
	client, folder := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(client, "Data"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath(client), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "original.spf2"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	state := weaponState{
		SourceHash: digest(raw), AppliedHash: digest(raw),
		Drafts:  map[string][]Rule{},
		Applied: map[string][]Rule{},
		Variants: map[string]map[int][]VariantEdit{
			"253047": {2011: {{Condition: 406}}},
		},
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "settings.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	items, err := catalog(installed, false, false)
	if err != nil {
		t.Fatal(err)
	}
	result, err := weaponHandle(Request{Operation: "weapon_combo_chain", Weapon: 253047}, client, items, folder)
	if err != nil {
		t.Fatal(err)
	}
	view, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Variants map[string][]struct {
			Condition string `json:"condition"`
		} `json:"variants"`
	}
	if err := json.Unmarshal(view, &response); err != nil {
		t.Fatal(err)
	}
	for _, branch := range response.Variants["2011"] {
		if branch.Condition == "406" {
			return
		}
	}
	t.Fatalf("GM read omitted saved 406 branch: %s", view)
}
