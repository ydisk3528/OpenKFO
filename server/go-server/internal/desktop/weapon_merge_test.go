package desktop

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// 合并包导入的 round-trip：从真实客户端提取一把自建武器的合并材料，装进
// zip，导入到一份拷贝的客户端，校验（1）合并后除白名单条目外逐字节不变、
// （2）武器行确实落地、（3）再导入一次字节稳定（幂等）。
func TestWeaponMergeImportRoundTrip(t *testing.T) {
	client := os.Getenv("OPENKFO_WEAPON_TEST_CLIENT")
	if client == "" {
		t.Skip("set OPENKFO_WEAPON_TEST_CLIENT for installed resource validation")
	}
	// 目标客户端 = 源 config.spf2 的拷贝。
	dst := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dst, "Data"), 0700); err != nil {
		t.Fatal(err)
	}
	srcBytes, err := os.ReadFile(configPath(client))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath(dst), srcBytes, 0600); err != nil {
		t.Fatal(err)
	}

	// 从源客户端提取 253300 的合并材料（复用导出侧的提取函数）。
	src, err := loadArchive(configPath(client))
	if err != nil {
		t.Fatal(err)
	}
	items, err := catalog(client, false)
	if err != nil {
		t.Fatal(err)
	}
	info, err := inspect(src, items)
	if err != nil {
		t.Fatal(err)
	}
	const number = "253300"
	weapon, err := mergeWeaponFixture(t, src, info, number)
	if err != nil {
		t.Skip(err.Error())
	}

	zipPath := writeMergeFixture(t, &mergeManifest{
		Format: mergeFormat, Version: 1, Weapons: []mergeWeapon{weapon}, Assets: []string{},
	})

	folder := t.TempDir()
	report, err := weaponMergeImport(Request{SourcePath: zipPath}, dst, folder)
	if err != nil {
		t.Fatalf("导入失败：%v", err)
	}
	rep := report.(mergeImportReport)
	if len(rep.Entries) == 0 {
		t.Fatal("导入报告为空")
	}

	// 合并后：目标 config 除白名单条目外逐字节等于源。
	merged, err := loadArchive(configPath(dst))
	if err != nil {
		t.Fatal(err)
	}
	allowed := mergeAllowedEntries(&mergeManifest{Weapons: []mergeWeapon{weapon}})
	for name := range src.entries {
		before, _ := src.raw(name)
		after, _ := merged.raw(name)
		if !bytes.Equal(before, after) && !allowed[name] {
			t.Fatalf("合并意外改动了 %s", name)
		}
	}
	// 武器行落地。
	gotItem, _ := merged.text("item.txt")
	if _, ok := tabRowOf(gotItem, 1, number); !ok {
		t.Fatal("合并后 item.txt 里没有 253300 行")
	}

	// 幂等：第一次导入后记下各条目内容，再导入一次应当逐条不变。
	firstArchive, err := loadArchive(configPath(dst))
	if err != nil {
		t.Fatal(err)
	}
	firstSnapshot := map[string][]byte{}
	for name := range firstArchive.entries {
		raw, _ := firstArchive.raw(name)
		firstSnapshot[name] = raw
	}
	if _, err = weaponMergeImport(Request{SourcePath: zipPath}, dst, folder); err != nil {
		t.Fatalf("二次导入失败：%v", err)
	}
	secondArchive, err := loadArchive(configPath(dst))
	if err != nil {
		t.Fatal(err)
	}
	for name, before := range firstSnapshot {
		after, _ := secondArchive.raw(name)
		if !bytes.Equal(before, after) {
			t.Fatalf("二次导入改变了条目 %s：len %d -> %d", name, len(before), len(after))
		}
	}
}

// 纯函数：动作块合并的幂等与插入。
func TestMergeAnimationBlockIdempotent(t *testing.T) {
	block := `<AnmDesc id = "7" ><Line lineid="1"/></AnmDesc>`
	base := "<?xml version=\"1.0\"?>\n<AnmInfo>\n" + block + "\n</AnmInfo>\n"
	// 同内容：不动。
	out, replaced, inserted, err := mergeAnimationBlock(base, block)
	if err != nil || replaced != 0 || inserted != 0 || out != base {
		t.Fatalf("同内容应当不动：%v %v %v %q", replaced, inserted, err, out)
	}
	// 前导零块 id 也要命中。
	zero := `<AnmDesc id = "007" ><Line lineid="2"/></AnmDesc>`
	withZero := "<?xml version=\"1.0\"?>\n<AnmInfo>\n" + zero + "\n</AnmInfo>\n"
	out, replaced, inserted, err = mergeAnimationBlock(withZero, `<AnmDesc id = "7" ><Line lineid="9"/></AnmDesc>`)
	if err != nil || replaced != 1 || inserted != 0 {
		t.Fatalf("前导零块应按 id 命中：%v %v %v", replaced, inserted, err)
	}
	if out == withZero {
		t.Fatal("块内容不同应当被替换")
	}
	// 新块：插入。
	_, _, inserted, err = mergeAnimationBlock(base, `<AnmDesc id = "99" ><Line lineid="3"/></AnmDesc>`)
	if err != nil || inserted != 1 {
		t.Fatalf("新块应当插入：%v %v", inserted, err)
	}
}

// 纯函数：特效块与连招限制块的幂等。
func TestMergeBlockReplaceIdempotent(t *testing.T) {
	effect := `<WeaponEffect ItemID = "253300">` + "\n\t<EffectFile EffectId = \"1\" File = \"1\" />" + "\n\t</WeaponEffect>"
	text := "<?xml version=\"1.0\"?>\n<ActEffect>\n" + effect + "\n</ActEffect>\n"
	out, err := replaceActEffectBlockText(text, "253300", effect)
	if err != nil || out != text {
		t.Fatalf("特效块同内容应当不动：%v", err)
	}
	out, err = replaceActEffectBlockText(text, "253300", "")
	if err != nil || out == text {
		t.Fatal("空块应当移除登记")
	}

	inner := "\n\t\t<MaxComboForSkill Skill=\"1\" MaxCombo=\"2\"/>\n\t"
	rule := "<?xml version=\"1.0\"?>\n<ComboRuleList>\n\t<ComboRule Weapon=\"253300\">" + inner + "</ComboRule>\n</ComboRuleList>\n"
	out2, err := replaceComboRuleInner(rule, "253300", inner)
	if err != nil || out2 != rule {
		t.Fatalf("连招块同内文应当不动：%v", err)
	}
	out2, err = replaceComboRuleInner(rule, "253300", "")
	if err != nil || out2 == rule {
		t.Fatal("空内文应当删除块")
	}
}

// mergeWeaponFixture 从归档里提取一把武器的全部合并材料（导入/导出两侧共用的
// 提取路径）。物品行缺失时返回 error，让调用方决定 skip 还是 fail。
func mergeWeaponFixture(t *testing.T, a *archive, info *inspection, number string) (mergeWeapon, error) {
	t.Helper()
	id, err := strconv.Atoi(number)
	if err != nil {
		return mergeWeapon{}, err
	}
	itemText, _ := a.text("item.txt")
	actionText, _ := a.text("itemact.txt")
	effectText, _ := a.text("acteffect.xml")
	ruleText, _ := a.text("comborule.xml")
	propertyText, _ := a.text("skillproperty.xml")
	itemRow, ok := tabRowOf(itemText, 1, number)
	if !ok {
		return mergeWeapon{}, fmt.Errorf("归档里没有 %s 物品行", number)
	}
	itemactRow, ok := tabRowOf(actionText, 0, number)
	if !ok {
		return mergeWeapon{}, fmt.Errorf("归档里没有 %s 动作行", number)
	}
	weapon := mergeWeapon{
		ID: id, Name: number,
		ItemRow: itemRow, ItemactRow: itemactRow,
		ActEffectBlock:  weaponEffectBlockOf(effectText, number),
		ComboRuleInner:  comboRuleInnerOf(ruleText, number),
		AnimationBlocks: map[string][]string{},
		SkillProperties: []string{},
	}
	rows, err := comboRowsOfArchive(a, number)
	if err != nil {
		return mergeWeapon{}, err
	}
	for _, row := range rows {
		weapon.DelayRows = append(weapon.DelayRows, mergeDelayRow{Old: row.OldState, New: row.NewState, Key: row.KeyInput, Part: row.StartPart})
	}
	actions, _ := weaponActions(a, number)
	seen := map[string]bool{}
	for _, action := range actions {
		if len(action) < 5 || seen[action] {
			continue
		}
		seen[action] = true
		blocks := info.blocks[actionKey(action)]
		if len(blocks) == 0 {
			continue
		}
		weapon.AnimationBlocks[action[:4]] = append(weapon.AnimationBlocks[action[:4]], blocks[0].original)
		for _, pid := range propertyIDsOfAction(info, action) {
			if node, ok := propertyNodeText(propertyText, pid); ok {
				weapon.SkillProperties = append(weapon.SkillProperties, node)
			}
		}
	}
	return weapon, nil
}

// replaceTableCell 换掉表格行某一列，用来把一把武器的材料改成另一个编号。
func replaceTableCell(t *testing.T, row string, column int, value string) string {
	t.Helper()
	cells := strings.Split(row, "\t")
	if column >= len(cells) {
		t.Fatalf("行没有第 %d 列：%q", column, row)
	}
	cells[column] = value
	return strings.Join(cells, "\t")
}

// 预览把包里的武器分成「新增」与「已存在会被覆盖」；确认后导入按同一判定落盘。
func TestWeaponMergePreviewAndImport(t *testing.T) {
	client := os.Getenv("OPENKFO_WEAPON_TEST_CLIENT")
	if client == "" {
		t.Skip("set OPENKFO_WEAPON_TEST_CLIENT for installed resource validation")
	}
	dst := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dst, "Data"), 0700); err != nil {
		t.Fatal(err)
	}
	srcBytes, err := os.ReadFile(configPath(client))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(configPath(dst), srcBytes, 0600); err != nil {
		t.Fatal(err)
	}

	src, err := loadArchive(configPath(client))
	if err != nil {
		t.Fatal(err)
	}
	items, err := catalog(client, false)
	if err != nil {
		t.Fatal(err)
	}
	info, err := inspect(src, items)
	if err != nil {
		t.Fatal(err)
	}
	// 已存在：目标是从客户端拷来的，所以 253300 算「已存在会被覆盖」。
	existing, err := mergeWeaponFixture(t, src, info, "253300")
	if err != nil {
		t.Skip(err.Error())
	}
	// 新增：把同一把武器的材料换成目标里没有的编号 253999。
	const newNumber = "253999"
	if weaponIDsOf(src)[newNumber] {
		t.Skipf("测试客户端里已经有 %s", newNumber)
	}
	newWeapon := mergeWeapon{
		ID: 253999, Name: "自建新武器",
		ItemRow:         replaceTableCell(t, existing.ItemRow, 1, newNumber),
		ItemactRow:      replaceTableCell(t, existing.ItemactRow, 0, newNumber),
		AnimationBlocks: map[string][]string{},
		SkillProperties: []string{},
	}

	zipPath := writeMergeFixture(t, &mergeManifest{
		Format: mergeFormat, Version: 1,
		Weapons: []mergeWeapon{existing, newWeapon},
	})
	folder := t.TempDir()

	previewAny, err := weaponMergePreview(Request{SourcePath: zipPath}, dst)
	if err != nil {
		t.Fatalf("预览失败：%v", err)
	}
	preview := previewAny.(mergePreview)
	if len(preview.New) != 1 || fmt.Sprint(preview.New[0]["id"]) != newNumber {
		t.Fatalf("新增列表不对：%v", preview.New)
	}
	if len(preview.Modified) != 1 || fmt.Sprint(preview.Modified[0]["id"]) != "253300" {
		t.Fatalf("已存在列表不对：%v", preview.Modified)
	}

	report, err := weaponMergeImport(Request{SourcePath: zipPath}, dst, folder)
	if err != nil {
		t.Fatalf("导入失败：%v", err)
	}
	rep := report.(mergeImportReport)
	if len(rep.New) != 1 || len(rep.Modified) != 1 {
		t.Fatalf("导入报告分类不对：new=%v modified=%v", rep.New, rep.Modified)
	}
	merged, err := loadArchive(configPath(dst))
	if err != nil {
		t.Fatal(err)
	}
	mergedItem, _ := merged.text("item.txt")
	if _, ok := tabRowOf(mergedItem, 1, newNumber); !ok {
		t.Fatal("新增武器 253999 没有合并进来")
	}
	if _, ok := tabRowOf(mergedItem, 1, "253300"); !ok {
		t.Fatal("已存在武器 253300 应当仍在")
	}
	mergedAction, _ := merged.text("itemact.txt")
	if _, ok := tabRowOf(mergedAction, 0, newNumber); !ok {
		t.Fatal("新增武器 253999 的 itemact 行没有合并进来")
	}
}

func writeMergeFixture(t *testing.T, manifest *mergeManifest) string {
	t.Helper()
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "weapon-merge-test.zip")
	handle, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	writer := zip.NewWriter(handle)
	entry, err := writer.Create("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = entry.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
