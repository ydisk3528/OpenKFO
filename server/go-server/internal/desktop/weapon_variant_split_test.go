package desktop

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// 从供体复制一把带「条件分支」的武器时，条件分支块必须一起分身成私有块 ——
// 否则新武器只拿到无条件那段，带该状态时的变形态效果直接丢失（条件块留在供体
// 的 id 下成了孤儿）。这里走一遍真实路径：applyBlueprints 抄供体的动作行 →
// splitClonedProperties 做 2xxx 段分身。
func TestSplitClonedPropertiesKeepsConditionVariants(t *testing.T) {
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

	// 找一把「2xxx 动作带条件分支」的供体。
	donor, wantCondition := 0, ""
	for _, w := range info.weapons {
		for _, st := range w.Stages {
			if !stageNeedsSplit(st.State) {
				continue
			}
			for _, v := range actionVariants(info, st.Action) {
				if v.condition != "" {
					donor, wantCondition = w.ID, v.condition
					break
				}
			}
			if donor != 0 {
				break
			}
		}
		if donor != 0 {
			break
		}
	}
	if donor == 0 {
		t.Skip("客户端里没有带条件分支的 2xxx 动作")
	}

	cloneID := 253900
	created := map[string]Blueprint{
		strconv.Itoa(cloneID): {ID: cloneID, Name: "条件分支复制测试", Type: "1", Model: "x.dff", Donor: donor},
	}
	withRow, err := applyBlueprints(a, created)
	if err != nil {
		t.Fatalf("抄供体动作行失败：%v", err)
	}
	out, err := splitClonedProperties(withRow, &weaponState{Created: created})
	if err != nil {
		t.Fatalf("分身失败：%v", err)
	}

	// 新武器的动作号，以及它指向的块里有没有那个条件。
	actionText, err := out.text("itemact.txt")
	if err != nil {
		t.Fatal(err)
	}
	row := actionRowIndex(actionText)[strconv.Itoa(cloneID)]
	if row == nil {
		t.Fatal("新武器没有动作行")
	}
	header := itemactHeader(actionText)
	checked := false
	for j, state := range header {
		if j < 2 || j >= len(row) || !stageNeedsSplit(state) {
			continue
		}
		action := strings.TrimSpace(row[j])
		if action == "" || action == "0" || len(action) <= 4 {
			continue
		}
		file := "animation/" + action[:4] + ".xml"
		text, err := out.text(file)
		if err != nil {
			t.Fatal(err)
		}
		conditions := map[string]bool{}
		for _, v := range variantTexts(text, action[4:]) {
			node, err := parseXML(v)
			if err != nil {
				continue
			}
			conditions[conditionUstate(node)] = true
		}
		if len(conditions) > 1 && conditions[wantCondition] {
			checked = true
		}
	}
	if !checked {
		t.Fatalf("分身后没有找到带条件 %s 的私有块（供体 %d）", wantCondition, donor)
	}
}

// itemactHeader 取动作表的状态列名。
func itemactHeader(text string) []string {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(lines[0], "\r"), "\t")
}
