package desktop

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// 自建武器的 2xxx 段必须整段克隆：动作块 + skillproid 都不与原武器共享。
// 实测背景：253350 完全克隆自 253504，2xxx 段 20 项 skillproid 全部共享。
//
// 需要真实客户端包：
//
//	OPENKFO_WEAPON_TEST_CLIENT=<隔离副本目录> go test ./internal/desktop \
//	  -run TestSplitClonedPropertiesIsolatesStages -v
func TestSplitClonedPropertiesIsolatesStages(t *testing.T) {
	client := os.Getenv("OPENKFO_WEAPON_TEST_CLIENT")
	if client == "" {
		t.Skip("set OPENKFO_WEAPON_TEST_CLIENT")
	}
	source, err := loadArchive(filepath.Join(client, "Data", "config.spf2"))
	if err != nil {
		t.Fatal(err)
	}
	if err = source.verify(); err != nil {
		t.Fatal(err)
	}

	// 拿 253350 当样本 —— 它就是那个「完全克隆但 skillproid 没独立」的武器。
	sample := pickWeaponWithStages(t, source, "253350")
	created := map[string]Blueprint{sample: {ID: mustAtoi(t, sample), Name: "分身测试", Type: "1"}}
	state := &weaponState{Created: created}

	before, err := surveyForWeapons(source, created)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.weaponActions[sample]) == 0 {
		t.Fatal("样本没有 2xxx 段可验证")
	}

	out, err := splitClonedProperties(source, state)
	if err != nil {
		t.Fatal(err)
	}
	if out == source {
		t.Fatal("应当产生分身写入，实际原样返回")
	}
	if err = out.verify(); err != nil {
		t.Fatal(err)
	}

	after, err := surveyForWeapons(out, created)
	if err != nil {
		t.Fatal(err)
	}

	// 段数一致（块可能分身成新 key，所以按「引用的 skillproid 总数」比）。
	beforeRefs := flattenedRefs(before.weaponActions[sample])
	afterRefs := flattenedRefs(after.weaponActions[sample])
	if len(beforeRefs) != len(afterRefs) {
		t.Fatalf("段数不一致：%v -> %v", beforeRefs, afterRefs)
	}
	splits := 0
	for i, oldID := range beforeRefs {
		newID := afterRefs[i]
		if newID == oldID {
			// 公共动作段（空手这类，被大批武器共用）刻意不克隆。
			if oldID != "800012" {
				t.Fatalf("skillproid %s 没有分身", oldID)
			}
			continue
		}
		splits++
		if _, ok := after.properties[newID]; !ok {
			t.Fatalf("新号 %s 缺少 skillproperty 记录", newID)
		}
	}
	t.Logf("共分身 %d 项", splits)
	if splits == 0 {
		t.Fatal("一项都没分身")
	}

	// 隔离验证：新号只被样本引用；供体那条 skillproid 记录必须原样留着。
	for _, ref := range afterRefs {
		if ref == "800012" {
			continue // 公共段
		}
		if len(after.users[ref]) != 1 || !after.users[ref][sample] {
			t.Fatalf("新号 %s 没有被 %s 独占：%v", ref, sample, after.users[ref])
		}
	}
	if len(after.properties) <= len(before.properties) {
		t.Fatal("skillproperty 记录数没有增长")
	}

	// 幂等性：再跑一次不应继续制造新号。
	again, err := splitClonedProperties(out, state)
	if err != nil {
		t.Fatal(err)
	}
	if again != out {
		t.Fatal("第二次运行不应再产生分身写入")
	}
}

// TestSplitClonedPropertiesToleratesDanglingRefs 确认原生悬空引用不会阻断分身。
//
// 实测背景：animation/2001.xml 的块 435（防御反击1段新）里有一条
// <Anm skillproid="60011780">，但 skillproperty.xml 里根本没有这个号 ——
// 客户端里本来就是断的。早期实现遇到它会直接返回错误，导致整次应用失败。
func TestSplitClonedPropertiesToleratesDanglingRefs(t *testing.T) {
	client := os.Getenv("OPENKFO_WEAPON_TEST_CLIENT")
	if client == "" {
		t.Skip("set OPENKFO_WEAPON_TEST_CLIENT")
	}
	source, err := loadArchive(filepath.Join(client, "Data", "config.spf2"))
	if err != nil {
		t.Fatal(err)
	}

	// 造一个「引用了不存在 skillproid 的块」的样本，看分身是否照常完成。
	//
	// 253039 的 2101 状态挂在 animation/2001.xml 的块 435（防御反击1段新）上，
	// 那个块里的 <Anm skillproid="60011780"> 在 skillproperty.xml 里不存在。
	// 这里不预先假定是 253039：把所有武器都当样本跑一遍 survey，挑出真正带
	// 悬空引用的那个，找不到就跳过（别的客户端可能数据不一样）。
	candidates := allWeaponKeys(t, source)
	created := map[string]Blueprint{}
	for _, key := range candidates {
		created[key] = Blueprint{ID: mustAtoi(t, key), Name: "悬空引用测试", Type: "1"}
	}

	survey, err := surveyForWeapons(source, created)
	if err != nil {
		t.Fatal(err)
	}
	dangling := 0
	danglingWeapon := ""
	for key, actions := range survey.weaponActions {
		for _, refs := range actions {
			for _, ref := range refs {
				if len(survey.properties[ref]) == 0 {
					dangling++
					danglingWeapon = key
				}
			}
		}
	}
	if dangling == 0 {
		t.Skip("本客户端没有悬空 skillproid 引用，跳过")
	}
	t.Logf("样本 %s 含 %d 处悬空 skillproid 引用", danglingWeapon, dangling)

	// 只留带悬空引用的那把武器，缩小写入面。
	created = map[string]Blueprint{danglingWeapon: {ID: mustAtoi(t, danglingWeapon), Name: "悬空引用测试", Type: "1"}}
	state := &weaponState{Created: created}

	out, err := splitClonedProperties(source, state)
	if err != nil {
		t.Fatalf("悬空引用不应阻断分身：%v", err)
	}
	if err = out.verify(); err != nil {
		t.Fatal(err)
	}

	// 悬空号必须原样留着（无处可克隆），且不能出现在新号段里。
	after, err := surveyForWeapons(out, created)
	if err != nil {
		t.Fatal(err)
	}
	for _, refs := range after.weaponActions[danglingWeapon] {
		for _, ref := range refs {
			number, convErr := strconv.Atoi(ref)
			if convErr == nil && number >= propertySplitPrefix && len(after.properties[ref]) == 0 {
				t.Fatalf("新号 %s 缺少 skillproperty 记录", ref)
			}
		}
	}
}

// TestSplitClonedPropertiesLeavesOtherStatesAlone 确认非 2xxx 段一个都没动。
func TestSplitClonedPropertiesLeavesOtherStatesAlone(t *testing.T) {
	client := os.Getenv("OPENKFO_WEAPON_TEST_CLIENT")
	if client == "" {
		t.Skip("set OPENKFO_WEAPON_TEST_CLIENT")
	}
	source, err := loadArchive(filepath.Join(client, "Data", "config.spf2"))
	if err != nil {
		t.Fatal(err)
	}
	sample := pickWeaponWithStages(t, source, "253350")
	created := map[string]Blueprint{sample: {ID: mustAtoi(t, sample), Name: "分身测试", Type: "1"}}

	before := weaponRow(t, source, sample)
	out, err := splitClonedProperties(source, &weaponState{Created: created})
	if err != nil {
		t.Fatal(err)
	}
	after := weaponRow(t, out, sample)
	if len(before) != len(after) {
		t.Fatalf("动作行列数变了：%d -> %d", len(before), len(after))
	}
	header := actionHeader(t, source)
	for i := range before {
		if i < 2 || i >= len(header) {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(header[i]), "2") {
			continue // 2xxx 允许变
		}
		if before[i] != after[i] {
			t.Fatalf("非 2xxx 段 %s 被动过：%s -> %s", header[i], before[i], after[i])
		}
	}
}

// TestRepairOrphanedEntryProperties 确认「块已分身但出招属性丢失」能被补回。
//
// 实测背景：253400 克隆自 253043（缈音），早期版本分块时漏掉了出招属性，导致
// 2071（2201993）/2081（2204994）按了键没反应。这里先用 splitClonedProperties
// 正常分身，再人为删掉出招属性模拟脏数据，验证修复函数能按供体补回。
func TestRepairOrphanedEntryProperties(t *testing.T) {
	client := os.Getenv("OPENKFO_WEAPON_TEST_CLIENT")
	if client == "" {
		t.Skip("set OPENKFO_WEAPON_TEST_CLIENT")
	}
	source, err := loadArchive(filepath.Join(client, "Data", "config.spf2"))
	if err != nil {
		t.Fatal(err)
	}

	created := map[string]Blueprint{
		"253999": {ID: 253999, Name: "修复测试", Type: "7", Donor: 253043},
	}
	state := &weaponState{Created: created}

	// 先建武器行（applyBlueprints 复制供体动作行），再正常分身，
	// 得到「块 + 出招属性」都齐全的结果。
	blueprinted, err := applyBlueprints(source, created)
	if err != nil {
		t.Fatal(err)
	}
	split, err := splitClonedProperties(blueprinted, state)
	if err != nil {
		t.Fatal(err)
	}

	// 取分身后 253999 的 2071 动作号，它应当已独立于供体 2201033。
	splitRow := weaponRow(t, split, "253999")
	header := actionHeader(t, split)
	action := ""
	for i, st := range header {
		if strings.TrimSpace(st) == "2071" && i < len(splitRow) {
			action = strings.TrimSpace(splitRow[i])
		}
	}
	if action == "" || action == "2201033" {
		t.Skip("分身后 2071 动作号未独立，跳过")
	}

	// 模拟早期脏数据：把这条出招属性从 skillproperty.xml 里删掉。
	broken := stripEntryProperty(t, split, action)

	// 修复。
	fixed, err := repairOrphanedEntryProperties(broken, state)
	if err != nil {
		t.Fatal(err)
	}
	props, err := newActionCloneProperties(fixed)
	if err != nil {
		t.Fatal(err)
	}
	if len(props.properties[action]) != 1 {
		t.Fatalf("修复后 %s 出招属性未补回", action)
	}
	if props.properties[action][0].get("SkillProId") != action {
		t.Fatalf("修复后的出招属性 SkillProId 不对：%s", props.properties[action][0].get("SkillProId"))
	}

	// 幂等：再修一次不应再变。
	again, err := repairOrphanedEntryProperties(fixed, state)
	if err != nil {
		t.Fatal(err)
	}
	if again != fixed {
		t.Fatal("修复应为幂等")
	}
}

// stripEntryProperty 从 skillproperty.xml 里删除 SkillProId == action 的整条
// PropertyItem，返回新归档。只删一条（出招属性本就唯一）。
func stripEntryProperty(t *testing.T, a *archive, action string) *archive {
	t.Helper()
	text, err := a.text("skillproperty.xml")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?s)\n?[ \t]*<PropertyItem\b[^>]*SkillProId\s*=\s*"` + regexp.QuoteMeta(action) + `"[^>]*?(/>|>.*?</PropertyItem\s*>)`)
	if !re.MatchString(text) {
		t.Fatalf("找不到出招属性 %s 可删除", action)
	}
	text = re.ReplaceAllString(text, "")
	if _, err := parseXML(text); err != nil {
		t.Fatalf("删除后 skillproperty.xml 不合法：%v", err)
	}
	replacements := map[string][]byte{}
	if replacements["skillproperty.xml"], err = encodeText(text); err != nil {
		t.Fatal(err)
	}
	data, err := a.replace(replacements)
	if err != nil {
		t.Fatal(err)
	}
	out, err := parseArchive(data)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// ---- 测试辅助 ----

// flattenedRefs 把 {动作key: [skillproid...]} 拍平成有序列表，便于跨块分身比对。
func flattenedRefs(actions map[string][]string) []string {
	keys := make([]string, 0, len(actions))
	for key := range actions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := []string{}
	for _, key := range keys {
		out = append(out, actions[key]...)
	}
	return out
}

func pickWeaponWithStages(t *testing.T, a *archive, preferred string) string {
	t.Helper()
	keys := allWeaponKeys(t, a)
	if contains(keys, preferred) {
		return preferred
	}
	if len(keys) == 0 {
		t.Fatal("动作表里没有武器")
	}
	sort.Strings(keys)
	return keys[0]
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

func mustAtoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("不是数字：%s", s)
	}
	return n
}

// allWeaponKeys 列出 itemact.txt 里的所有武器 id（跳过表头）。
func allWeaponKeys(t *testing.T, a *archive) []string {
	t.Helper()
	text, err := a.text("itemact.txt")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(text, "\n")
	keys := []string{}
	for _, line := range lines[1:] {
		cols := strings.Split(strings.TrimSuffix(line, "\r"), "\t")
		if len(cols) < 2 || cols[0] == "" {
			continue
		}
		keys = append(keys, cols[0])
	}
	return keys
}

func weaponRow(t *testing.T, a *archive, weapon string) []string {
	t.Helper()
	text, err := a.text("itemact.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(text, "\n")[1:] {
		cols := strings.Split(strings.TrimSuffix(line, "\r"), "\t")
		if len(cols) > 0 && cols[0] == weapon {
			return cols
		}
	}
	t.Fatalf("找不到武器 %s 的动作行", weapon)
	return nil
}

func actionHeader(t *testing.T, a *archive) []string {
	t.Helper()
	text, err := a.text("itemact.txt")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(text, "\n")
	return strings.Split(strings.TrimSuffix(lines[0], "\r"), "\t")
}
