package desktop

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// 分支形态：给某状态加一条带 <Condition><Ustate id="N"/> 的动作块。验证
// 1) 新块带条件、排在无条件块之后；2) 段里的 skillproid 换成新号并注册；
// 3) 原有无条件块与已有条件分支不受影响；4) 删除分支能干净移除。
func TestCloneBlockVariantsRewritesUntouchedConditions(t *testing.T) {
	base := `<AnmDesc id="7"><Anm id="1" skillproid="100" /></AnmDesc>`
	conditional := `<AnmDesc id="7"><Condition><Ustate id="406" /></Condition><Anm id="1" skillproid="100" /></AnmDesc>`
	edited := `<AnmDesc id="7"><Anm id="1" skillproid="900" /></AnmDesc>`
	animation := `<AnmInfo>` + base + conditional + `</AnmInfo>`
	text, err := cloneBlockVariants(
		animation,
		[]block{
			{original: base, node: mustParse(t, base), condition: ""},
			{original: conditional, node: mustParse(t, conditional), condition: "406"},
		},
		"", edited, 88, []splitRef{{oldID: "100", newID: "900"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(text, `id="88"`) != 2 {
		t.Fatalf("应复制两份变体并重命名，文本为：%s", text)
	}
	if strings.Count(text, `skillproid="900"`) != 2 {
		t.Fatalf("未编辑的条件块没有同步改写 skillproid：%s", text)
	}
	if !strings.Contains(text, `<Ustate id="406"`) {
		t.Fatalf("条件分支丢失：%s", text)
	}
}

func TestApplyVariantsCreatesConditionalBranch(t *testing.T) {
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

	// 找一把带 2xxx 状态、动作带 skillproid 的武器。
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

	const condition = 192
	state := &weaponState{Variants: map[string]map[int][]VariantEdit{
		weaponKey: {stageNum: {{Condition: condition}}},
	}}
	out, err := applyVariants(a, state, items)
	if err != nil {
		t.Fatalf("建分支失败：%v", err)
	}
	if out == a {
		t.Fatal("应当产生写入，实际原样返回")
	}
	file := "animation/" + action[:4] + ".xml"
	text, err := out.text(file)
	if err != nil {
		t.Fatal(err)
	}
	key := actionKey(action)
	id := key[strings.IndexByte(key, '/')+1:]
	blocks := variantTexts(text, id)
	if len(blocks) < 2 {
		t.Fatalf("应当有「无条件 + 新分支」至少 2 份块，得到 %d", len(blocks))
	}
	// 无条件块必须在前，条件块在后。
	if conditionUstate(mustParse(t, blocks[0])) != "" {
		t.Fatal("第一份应当是无条件块")
	}
	if got := conditionUstate(mustParse(t, blocks[len(blocks)-1])); got != strconv.Itoa(condition) {
		t.Fatalf("最后一份应当是条件 %d 的分支，得到 %q", condition, got)
	}
	// 分支里的 skillproid 必须是新号，且有 skillproperty 记录。
	newIDs := []string{}
	last := mustParse(t, blocks[len(blocks)-1])
	last.walk(func(n *xmlNode) {
		if n.tag == "Anm" {
			if id := strings.TrimSpace(n.get("skillproid")); id != "" {
				newIDs = append(newIDs, id)
			}
		}
	})
	if len(newIDs) == 0 {
		t.Fatal("分支里没有 skillproid")
	}
	props, err := newActionCloneProperties(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range newIDs {
		number, convErr := strconv.Atoi(id)
		if convErr != nil || number < variantSkillProPrefix {
			t.Fatalf("分支 skillproid %s 不在新号段（%d 起）", id, variantSkillProPrefix)
		}
		if len(props.properties[id]) == 0 {
			t.Fatalf("分支 skillproid %s 没有 skillproperty 记录", id)
		}
	}

	// 删除分支应回到原样（至少条件块消失）。
	state2 := &weaponState{Variants: map[string]map[int][]VariantEdit{
		weaponKey: {stageNum: {{Condition: condition, Remove: true}}},
	}}
	removed, err := applyVariants(out, state2, items)
	if err != nil {
		t.Fatalf("删分支失败：%v", err)
	}
	text2, err := removed.text(file)
	if err != nil {
		t.Fatal(err)
	}
	if got := countConditionBlocks(text2, id, strconv.Itoa(condition)); got != 0 {
		t.Fatalf("分支没删干净，还剩 %d 份", got)
	}
}

// 白名单：分支编辑必须放行对应动画文件。
func TestVariantFilesWhitelisted(t *testing.T) {
	installed := os.Getenv("OPENKFO_WEAPON_TEST_CLIENT")
	if installed == "" {
		t.Skip("set OPENKFO_WEAPON_TEST_CLIENT for installed resource validation")
	}
	a, err := loadArchive(configPath(installed))
	if err != nil {
		t.Fatal(err)
	}
	itemText, err := a.text("item.txt")
	if err != nil {
		t.Fatal(err)
	}
	items, err := itemsFromText(installed, itemText, true, true)
	if err != nil {
		t.Fatal(err)
	}
	info, err := inspect(a, items)
	if err != nil {
		t.Fatal(err)
	}
	var weaponKey string
	var stageNum int
	for _, w := range info.weapons {
		for _, st := range w.Stages {
			if stageNeedsSplit(st.State) && st.Action != "" && st.Action != "0" {
				weaponKey, stageNum = strconv.Itoa(w.ID), mustAtoi(t, strings.TrimSpace(st.State))
				break
			}
		}
		if weaponKey != "" {
			break
		}
	}
	if weaponKey == "" {
		t.Skip("没有样本")
	}
	state := &weaponState{Variants: map[string]map[int][]VariantEdit{
		weaponKey: {stageNum: {{Condition: 192}}},
	}}
	files := variantFiles(a, state)
	if len(files) == 0 {
		t.Fatal("分支编辑没有放行任何动画文件")
	}
}

func mustParse(t *testing.T, text string) *xmlNode {
	t.Helper()
	node, err := parseXML(text)
	if err != nil {
		t.Fatal(err)
	}
	return node
}

func countConditionBlocks(animation, id, condition string) int {
	count := 0
	for _, candidate := range animationPattern.FindAllString(animation, -1) {
		node, err := parseXML(candidate)
		if err != nil {
			continue
		}
		if strings.TrimSpace(node.get("id")) == id && conditionUstate(node) == condition {
			count++
		}
	}
	return count
}
