package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// currentBlock must find a block by its id against the file's *current* text.
// Once an earlier rewrite has replaced the pristine body, matching the text
// captured by inspect() would come up empty.
func TestCurrentBlockMatchesByID(t *testing.T) {
	file := "<AnmInfo>\n" +
		`<AnmDesc id="7"><Node skillproid="100"/></AnmDesc>` + "\n" +
		`<AnmDesc id="8"><Node skillproid="200"/></AnmDesc>` + "\n" +
		"</AnmInfo>"
	rewritten := strings.Replace(file, `skillproid="100"`, `skillproid="999"`, 1)
	block, ok := currentBlock(rewritten, "7")
	if !ok {
		t.Fatal("按 id 应能在改写后的内容里找到块 7")
	}
	if !strings.Contains(block, `skillproid="999"`) {
		t.Fatalf("应返回块 7 的当前内容，得到 %q", block)
	}
	if _, ok := currentBlock(rewritten, "9"); ok {
		t.Fatal("不存在的 id 不应命中")
	}
}

// Two states of one weapon may remap onto the very same action. applyRemaps
// then rewrites that animation block twice in a single pass, and it edits the
// copy cached by the first rewrite — so the pristine block text inspect()
// captured is gone by the second visit. That used to abort the whole catalogue
// with 动作定义无法唯一替换, which surfaced in the GM as the orange banner
// "状态重映射暂不可用" with the editor stuck on the pre-remap structure.
func TestApplyRemapsRewritesOneBlockTwice(t *testing.T) {
	root := os.Getenv("OPENKFO_TEST_RUNTIME")
	if root == "" {
		abs, err := filepath.Abs("../../../../runtime-local")
		if err != nil {
			t.Fatal(err)
		}
		root = abs
	}
	source, err := loadArchive(filepath.Join(root, "weapon-config/original.spf2"))
	if err != nil {
		t.Skip("runtime-local 固定装置不可用")
	}
	items, err := catalog(filepath.Join(root, "client"), false)
	if err != nil {
		t.Fatal(err)
	}
	info, err := inspect(source, items)
	if err != nil {
		t.Fatal(err)
	}
	// A non-shared action with exactly one block and a live hit property is the
	// branch that rewrites the block in place.
	action, property := "", ""
	for candidate := range info.owners {
		if len(info.owners[candidate]) != 1 {
			continue
		}
		blocks := info.blocks[actionKey(candidate)]
		if len(blocks) != 1 {
			continue
		}
		found := ""
		blocks[0].node.walk(func(node *xmlNode) {
			if found != "" {
				return
			}
			if value := strings.TrimSpace(node.get("skillproid")); value != "" && len(info.properties[value]) == 1 {
				found = value
			}
		})
		if found == "" {
			continue
		}
		action, property = candidate, found
		break
	}
	if action == "" {
		t.Skip("夹具里找不到可安全重映射的动作")
	}
	text, err := source.text("itemact.txt")
	if err != nil {
		t.Fatal(err)
	}
	rows := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	header := strings.Split(rows[0], "\t")
	column := map[string]int{}
	for index, name := range header {
		column[name] = index
	}
	weaponKey := strings.Split(rows[1], "\t")[0]
	row := strings.Split(rows[1], "\t")
	for _, state := range []string{"2011", "2012"} {
		index, ok := column[state]
		if !ok || index >= len(row) {
			t.Skipf("夹具武器行缺少 %s 列", state)
		}
	}
	state := &weaponState{Remaps: map[string]map[int]*StageRemap{
		weaponKey: {
			2011: {Action: action, PropertyID: property},
			2012: {Action: action, PropertyID: property},
		},
	}}
	remapped, err := applyRemaps(source, state, items)
	if err != nil {
		t.Fatalf("两个状态重映射到同一动作时不应报错：%v", err)
	}
	after, err := inspect(remapped, items)
	if err != nil {
		t.Fatal(err)
	}
	blocks := after.blocks[actionKey(action)]
	if len(blocks) != 1 {
		t.Fatalf("重映射后动作 %s 的块数 = %d，期望 1", action, len(blocks))
	}
	got := ""
	blocks[0].node.walk(func(node *xmlNode) {
		if got != "" {
			return
		}
		if value := strings.TrimSpace(node.get("skillproid")); value != "" {
			got = value
		}
	})
	if got != property {
		t.Fatalf("命中属性 = %q，期望 %q", got, property)
	}
}
