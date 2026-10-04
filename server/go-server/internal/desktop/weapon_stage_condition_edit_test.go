package desktop

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// 同名 <AnmDesc>（无条件 + 条件分支）过去会让 applyBlockElements 直接报「动作不存在
// 或不唯一」，整条编辑链在 253521 这类武器上全死。现在默认编辑无条件那份，条件分支
// 必须原样保留。这里对真实客户端跑一次完整的写入。
func TestConditionedStageStaysEditable(t *testing.T) {
	installed := os.Getenv("OPENKFO_WEAPON_TEST_CLIENT")
	if installed == "" {
		t.Skip("set OPENKFO_WEAPON_TEST_CLIENT for installed resource validation")
	}
	raw, err := os.ReadFile(configPath(installed))
	if err != nil {
		t.Fatal(err)
	}
	// 在临时客户端里跑，绝不写进真实包。
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

	// 找一把「动作带条件分支」的武器 + 状态。
	weaponKey, stageNum, action := "", 0, ""
	found := false
	for _, w := range info.weapons {
		for _, st := range w.Stages {
			variants := actionVariants(info, st.Action)
			if len(variants) < 2 {
				continue
			}
			hasCondition := false
			for _, v := range variants {
				if v.condition != "" {
					hasCondition = true
				}
			}
			if hasCondition {
				weaponKey, stageNum, action = strconv.Itoa(w.ID), st.Stage, st.Action
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		t.Skip("客户端里没有带条件分支的动作")
	}

	// 给这个状态加一条霸体（无条件块）。
	state := &weaponState{BlockElements: map[string]map[int]map[string][]BlockElement{
		weaponKey: {stageNum: {"FakeUnAttack": {element("FakeUnAttack", "startframe", "0", "endframe", "20")}}},
	}}
	out, err := applyBlockElements(a, state, items)
	if err != nil {
		t.Fatalf("条件分支动作应当可编辑，却被拒：%v", err)
	}
	file := "animation/" + action[:4] + ".xml"
	text, err := out.text(file)
	if err != nil {
		t.Fatal(err)
	}
	// 条件分支必须还在（同一 id 至少两份）。
	id := strconv.Itoa(stageBlockID(t, info, action))
	if n := countBlocks(text, id); n < 2 {
		t.Fatalf("%s 的条件分支块被弄丢了：id=%s 只剩 %d 份", file, id, n)
	}
	// 霸体只该落在无条件那份上。
	if !strings.Contains(text, "<FakeUnAttack") {
		t.Fatal("霸体没写进去")
	}
}

// stageBlockID 取一个动作键对应的块号（去掉前缀）。
func stageBlockID(t *testing.T, info *inspection, action string) int {
	t.Helper()
	key := actionKey(action)
	if key == "" {
		t.Fatalf("动作号异常：%s", action)
	}
	slash := strings.IndexByte(key, '/')
	id, err := strconv.Atoi(key[slash+1:])
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// countBlocks 数动画文件里 id 等于 want 的 <AnmDesc> 段数。
func countBlocks(animation, want string) int {
	count := 0
	for _, candidate := range animationPattern.FindAllString(animation, -1) {
		node, err := parseXML(candidate)
		if err != nil {
			continue
		}
		if strings.TrimSpace(node.get("id")) == want {
			count++
		}
	}
	return count
}
