package desktop

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The GM front-end spawns one backend process per RPC, so two operations really
// do run at the same time (selecting a weapon fires weapon_combo_chain and
// weapon_combo_rule together). Serialise them instead of failing the second one:
// a hard failure is invisible in the UI and looks exactly like "this weapon has
// no rules".
func TestWeaponLockQueuesInsteadOfFailing(t *testing.T) {
	dir := t.TempDir()
	first, err := acquireWeaponLock(dir)
	if err != nil {
		t.Fatalf("第一把锁应该拿到: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		second, err := acquireWeaponLock(dir)
		if err == nil {
			second()
		}
		done <- err
	}()
	time.Sleep(3 * weaponLockRetry) // 让第二个goroutine真的撞上锁
	first()
	if err := <-done; err != nil {
		t.Fatalf("并发取锁应当排队等待，而不是失败: %v", err)
	}
}

// taskkill /F skips the deferred release, so the lock file survives the process.
// An old lock must be taken over, otherwise every later operation fails forever.
func TestWeaponLockTakesOverStaleLock(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, "editing.lock")
	if err := os.WriteFile(lock, []byte("pid 999999\n"), 0600); err != nil {
		t.Fatalf("造陈锁失败: %v", err)
	}
	old := time.Now().Add(-staleLockAge - time.Minute)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatalf("改陈锁时间失败: %v", err)
	}
	release, err := acquireWeaponLock(dir)
	if err != nil {
		t.Fatalf("陈锁应当被接管: %v", err)
	}
	release()
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Fatal("释放后锁文件不该留下")
	}
}

// syntheticRuleFile mirrors the real layout, including the authoring note at the
// bottom that contains a <ComboRule> example inside an XML comment. Treating
// that example as data would both invent a rule for 253108 and corrupt the
// documentation on the next write, so it is the first thing to pin down.
const syntheticRuleFile = `<?xml version="1.0" encoding="gb2312"?>
<ComboRuleList>
	<ComboRule Weapon="253133">
		<!-- 跑C只能打中一下-->
		<MaxComboForSkill Skill="1330310" MaxCombo="1"/>
		<BlackListItem PrevSkill = "1330412" CurSkill="1330411" />
		<WhiteListItem PrevSkill = "1330412" CurSkill="1330410" />
	</ComboRule>
	<ComboRule Weapon="253300"></ComboRule>
</ComboRuleList>
<!--
KK连击限制配置文档
宁超
2011-8-2

<ComboRule Weapon="253108"  >
	武器编号
	<BlackListItem PrevSkill = "1080120" CurSkill="1080130" />
		连招黑名单，2招之间不能连
	<MaxComboForSkill Skill="2001899" MaxCombo="3"/>
</ComboRule>
  -->
`

func TestComboRuleSkipsAuthoringComment(t *testing.T) {
	weapons := comboRuleWeapons(syntheticRuleFile)
	if weapons["253108"] {
		t.Fatal("把文件底部文档注释里的示例当成了真实规则")
	}
	if !weapons["253133"] || !weapons["253300"] {
		t.Fatalf("漏掉了真实规则：%v", weapons)
	}
	if blocks := comboRuleBlocks(syntheticRuleFile); len(blocks) != 2 {
		t.Fatalf("真实块数应为 2，得到 %d", len(blocks))
	}
}

func TestComboRuleParsesEveryKind(t *testing.T) {
	set, found := comboRuleSetOf(syntheticRuleFile, "253133")
	if !found {
		t.Fatal("253133 应当有规则")
	}
	want := ComboRuleSet{
		Max:   []ComboRuleMax{{Skill: "1330310", MaxCombo: "1"}},
		Black: []ComboRuleLink{{Prev: "1330412", Cur: "1330411"}},
		White: []ComboRuleLink{{Prev: "1330412", Cur: "1330410"}},
	}
	if !reflect.DeepEqual(set, want) {
		t.Fatalf("解析结果不符\n得到 %+v\n期望 %+v", set, want)
	}
}

func TestComboRuleExceedAttributes(t *testing.T) {
	text := `<ComboRuleList><ComboRule Weapon="253700">
	<MaxComboForSkill Skill="1380410" MaxCombo="2" ExceedState="3005" ExceedSkillProID="3004001" />
	</ComboRule></ComboRuleList>`
	set, _ := comboRuleSetOf(text, "253700")
	if len(set.Max) != 1 {
		t.Fatalf("期望 1 条，得到 %d", len(set.Max))
	}
	entry := set.Max[0]
	if entry.ExceedState != "3005" || entry.ExceedSkillProID != "3004001" {
		t.Fatalf("Exceed 字段没解析出来：%+v", entry)
	}
	// The rendered block has to carry them back.
	rebuilt, err := setComboRules(text, "253700", set)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := comboRuleSetOf(rebuilt, "253700")
	if !reflect.DeepEqual(set, again) {
		t.Fatalf("Exceed 字段往返丢失\n得到 %+v", again)
	}
}

func TestComboRuleRoundTripIsStable(t *testing.T) {
	set, _ := comboRuleSetOf(syntheticRuleFile, "253133")
	once, err := setComboRules(syntheticRuleFile, "253133", set)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := comboRuleSetOf(once, "253133")
	twice, err := setComboRules(once, "253133", parsed)
	if err != nil {
		t.Fatal(err)
	}
	if once != twice {
		t.Fatalf("第二次写入与第一次不一致：\n--- 第一次 ---\n%s\n--- 第二次 ---\n%s", once, twice)
	}
	if strings.Count(once, comboRuleMarker) != 1 {
		t.Fatalf("重复保存时注释标记累积了：%d 个", strings.Count(once, comboRuleMarker))
	}
	// Another weapon's block must be untouched, and so must the authoring note.
	if !strings.Contains(once, `Weapon="253300"`) {
		t.Fatal("改 253133 时动到了 253300 的块")
	}
	if !strings.Contains(once, "宁超") || !strings.Contains(once, "连招黑名单，2招之间不能连") {
		t.Fatal("改规则时破坏了文件底部的策划文档")
	}
}

func TestComboRuleEmptyRemovesTheBlock(t *testing.T) {
	set, _ := comboRuleSetOf(syntheticRuleFile, "253133")
	rebuilt, err := setComboRules(syntheticRuleFile, "253133", set)
	if err != nil {
		t.Fatal(err)
	}
	cleared, err := setComboRules(rebuilt, "253133", ComboRuleSet{})
	if err != nil {
		t.Fatal(err)
	}
	if _, found := comboRuleSetOf(cleared, "253133"); found {
		t.Fatal("清空后仍然存在 253133 的规则")
	}
	if strings.Contains(cleared, comboRuleMarker) {
		t.Fatal("清空后编辑器标记没被一起删掉")
	}
	if !strings.Contains(cleared, `Weapon="253300"`) {
		t.Fatal("清空 253133 时误删了 253300")
	}
	// Saving the same empty set again has to be a no-op.
	again, err := setComboRules(cleared, "253133", ComboRuleSet{})
	if err != nil {
		t.Fatal(err)
	}
	if again != cleared {
		t.Fatal("重复清空不是幂等的")
	}
}

// TestComboRuleInsertAndMerge covers the two shapes the shipped file does not
// exercise on its own: a weapon the file never mentions (new block inserted
// before the closing tag, and still only one after a second save) and a weapon
// declared twice (253043 ships that way, so the duplicate has to be merged).
func TestComboRuleInsertAndMerge(t *testing.T) {
	base := "<?xml version=\"1.0\"?>\n<ComboRuleList>\n\t<ComboRule Weapon=\"253133\"></ComboRule>\n</ComboRuleList>\n"
	set := ComboRuleSet{Max: []ComboRuleMax{{Skill: "3001001", MaxCombo: "3"}}}
	once, err := setComboRules(base, "253300", set)
	if err != nil {
		t.Fatal(err)
	}
	if !comboRuleWeapons(once)["253300"] {
		t.Fatal("新增的块没写进去")
	}
	if !strings.Contains(once, `Weapon="253133"`) {
		t.Fatal("新增块时动到了别的武器")
	}
	twice, err := setComboRules(once, "253300", set)
	if err != nil {
		t.Fatal(err)
	}
	if once != twice {
		t.Fatalf("新增块重复保存不稳定：\n--- 第一次 ---\n%s\n--- 第二次 ---\n%s", once, twice)
	}
	if strings.Count(twice, `Weapon="253300"`) != 1 {
		t.Fatal("重复保存把新块写了两遍")
	}
	if strings.Count(twice, comboRuleMarker) != 1 {
		t.Fatalf("标记注释累积了：%d 个", strings.Count(twice, comboRuleMarker))
	}

	dup := "<ComboRuleList>\n" +
		"\t<ComboRule Weapon=\"253043\">\n\t\t<BlackListItem PrevSkill=\"1001001\" CurSkill=\"1001002\" />\n\t</ComboRule>\n" +
		"\t<ComboRule Weapon=\"253043\">\n\t\t<MaxComboForSkill Skill=\"3001001\" MaxCombo=\"2\" />\n\t</ComboRule>\n" +
		"</ComboRuleList>\n"
	merged, err := setComboRules(dup, "253043", ComboRuleSet{
		Black: []ComboRuleLink{{Prev: "1001001", Cur: "1001002"}},
		Max:   []ComboRuleMax{{Skill: "3001001", MaxCombo: "2"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if blocks := comboRuleBlocks(merged); len(blocks) != 1 {
		t.Fatalf("同一个武器的重复块没合并成一个：%d", len(blocks))
	}
	parsed, _ := comboRuleSetOf(merged, "253043")
	if len(parsed.Black) != 1 || len(parsed.Max) != 1 {
		t.Fatalf("合并后规则丢了：%+v", parsed)
	}
	if stable, _ := setComboRules(merged, "253043", parsed); stable != merged {
		t.Fatal("合并后的块重复保存不稳定")
	}
}

// TestComboRuleSaveOrderIsIrrelevant pins the property the batch apply relies on:
// because an existing block is rewritten where it stands, saving weapons in any
// order yields the same bytes and never shuffles them in the file.
func TestComboRuleSaveOrderIsIrrelevant(t *testing.T) {
	base := "<ComboRuleList>\n" +
		"\t<ComboRule Weapon=\"253001\">\n\t\t<MaxComboForSkill Skill=\"1001001\" MaxCombo=\"1\" />\n\t</ComboRule>\n" +
		"\t<ComboRule Weapon=\"253002\">\n\t\t<MaxComboForSkill Skill=\"1002001\" MaxCombo=\"1\" />\n\t</ComboRule>\n" +
		"</ComboRuleList>\n"
	save := func(order ...string) string {
		text := base
		for _, weapon := range order {
			set, _ := comboRuleSetOf(text, weapon)
			next, err := setComboRules(text, weapon, set)
			if err != nil {
				t.Fatalf("保存 %s 失败：%v", weapon, err)
			}
			text = next
		}
		return text
	}
	forward := save("253001", "253002")
	backward := save("253002", "253001")
	if forward != backward {
		t.Fatalf("保存顺序影响了结果：\n--- 正序 ---\n%s\n--- 逆序 ---\n%s", forward, backward)
	}
	if strings.Index(forward, `Weapon="253001"`) > strings.Index(forward, `Weapon="253002"`) {
		t.Fatal("重写改变了武器在文件里的先后顺序")
	}
}

// TestComboRuleApplyWiring pins the two places the apply path has to know about
// comborule.xml: buildWeaponBase has to layer the editor's rule sets in, and
// checkAllowedWrites has to permit that one entry — and only while the editor
// actually owns a rule set, since an unconditional pass would let a future bug
// rewrite shipped rules without anyone noticing.
func TestComboRuleApplyWiring(t *testing.T) {
	path := os.Getenv("OPENKFO_COMBORULE_TEST_ARCHIVE")
	if path == "" {
		t.Skip("set OPENKFO_COMBORULE_TEST_ARCHIVE to a config.spf2")
	}
	source, err := loadArchive(path)
	if err != nil {
		t.Fatal(err)
	}
	state := weaponState{ComboRules: map[string]ComboRuleSet{
		"253300": {
			Max:   []ComboRuleMax{{Skill: "1930210", MaxCombo: "3"}},
			Black: []ComboRuleLink{{Prev: "1930210", Cur: "1000110"}},
		},
	}}
	base, err := buildWeaponBase(source, &state)
	if err != nil {
		t.Fatal(err)
	}
	text, err := base.text("comborule.xml")
	if err != nil {
		t.Fatal(err)
	}
	set, found := comboRuleSetOf(text, "253300")
	if !found || len(set.Max) != 1 || len(set.Black) != 1 {
		t.Fatalf("apply 后没看到 253300 的规则：%+v", set)
	}
	if !comboRuleWeapons(text)["253133"] {
		t.Fatal("写入时丢掉了别的武器已有的规则")
	}
	if !strings.Contains(text, "宁超") {
		t.Fatal("写入时破坏了文件底部的策划文档")
	}
	if err = checkAllowedWrites(source, base, &state, nil); err != nil {
		t.Fatalf("comborule.xml 未被列入允许写入：%v", err)
	}
	// The pass must be tied to state.ComboRules.
	if err = checkAllowedWrites(source, base, &weaponState{}, nil); err == nil {
		t.Fatal("没有定制规则时仍允许改写 comborule.xml")
	}
}

func TestComboRuleValidation(t *testing.T) {
	bad := []ComboRuleSet{
		{Max: []ComboRuleMax{{Skill: "1330310", MaxCombo: "0"}}},
		{Max: []ComboRuleMax{{Skill: "abc", MaxCombo: "1"}}},
		{Max: []ComboRuleMax{{Skill: "1330310", MaxCombo: "1", ExceedState: "30"}}},
		{White: []ComboRuleLink{{Prev: "", Cur: "1330310"}}},
		{Black: []ComboRuleLink{{Prev: "", Cur: "1330310"}}},
		// 黑名单两端都必须有编号（官方没有空值用例）。
		{Black: []ComboRuleLink{{Prev: "1330310", Cur: ""}}},
	}
	for i, set := range bad {
		if err := validateComboRuleSet(set); err == nil {
			t.Fatalf("第 %d 组非法规则没有被拒绝：%+v", i, set)
		}
	}
	good := ComboRuleSet{
		Max:   []ComboRuleMax{{Skill: "1330310", MaxCombo: "2", ExceedState: "3005", ExceedSkillProID: "3004001"}},
		Black: []ComboRuleLink{{Prev: "1330412", Cur: "1330411"}},
	}
	if err := validateComboRuleSet(good); err != nil {
		t.Fatalf("合法规则被拒绝：%v", err)
	}
	// Prev == Cur 是"同一个技能不能连续放"，官方数据里大量存在，必须能存。
	selfLoop := ComboRuleSet{
		Black: []ComboRuleLink{{Prev: "80819", Cur: "80819"}},
		White: []ComboRuleLink{{Prev: "80818", Cur: "80818"}},
	}
	if err := validateComboRuleSet(selfLoop); err != nil {
		t.Fatalf("自环（同一技能不能连续释放）被拒：%v", err)
	}
	// 白名单 CurSkill 为空 = 这个前招之后什么都不许接（官方 253147 的写法）。
	if err := validateComboRuleSet(ComboRuleSet{
		White: []ComboRuleLink{{Prev: "1471210", Cur: ""}},
	}); err != nil {
		t.Fatalf("白名单空 CurSkill 被拒：%v", err)
	}
}

// 官方 comborule.xml 里的每一条规则都必须通过校验。以前这里把 Prev==Cur 当
// 非法（黑名单里 85 条自环），于是官方本来就有的数据既读得出来、又写不回去。
func TestComboRuleAcceptsShippedRules(t *testing.T) {
	root := os.Getenv("OPENKFO_TEST_RUNTIME")
	if root == "" {
		abs, err := filepath.Abs("../../../../runtime-local")
		if err != nil {
			t.Fatal(err)
		}
		root = abs
	}
	a, err := loadArchive(filepath.Join(root, "weapon-config/original.spf2"))
	if err != nil {
		t.Skip("runtime-local 固定装置不可用")
	}
	text, err := a.text("comborule.xml")
	if err != nil {
		t.Fatal(err)
	}
	loops, blocks := 0, 0
	for weapon := range comboRuleWeapons(text) {
		set, found := comboRuleSetOf(text, weapon)
		if !found {
			continue
		}
		blocks++
		if err := validateComboRuleSet(set); err != nil {
			t.Fatalf("官方数据被判非法（武器 %s）：%v", weapon, err)
		}
		for _, link := range append(append([]ComboRuleLink{}, set.Black...), set.White...) {
			if link.Prev == link.Cur {
				loops++
			}
		}
	}
	if blocks == 0 {
		t.Fatal("官方文件里没解析到任何规则")
	}
	if loops == 0 {
		t.Fatal("官方数据里没有自环条目，这条测试失去意义")
	}
	t.Logf("官方规则 %d 个武器块，其中自环条目 %d 条", blocks, loops)
}

// TestComboRuleAgainstShippedArchive re-runs the round-trip against the real
// client file: every weapon that ships with rules must survive a parse/set
// cycle unchanged in meaning, and the authoring note must stay intact.
func TestComboRuleAgainstShippedArchive(t *testing.T) {
	path := os.Getenv("OPENKFO_COMBORULE_TEST_ARCHIVE")
	if path == "" {
		t.Skip("set OPENKFO_COMBORULE_TEST_ARCHIVE to a config.spf2")
	}
	a, err := loadArchive(path)
	if err != nil {
		t.Fatal(err)
	}
	text, err := a.text("comborule.xml")
	if err != nil {
		t.Fatal(err)
	}
	if weapons := comboRuleWeapons(text); weapons["253108"] {
		t.Fatal("真实文件里 253108 只有文档示例，不该被当成规则")
	}
	blocks := comboRuleBlocks(text)
	if len(blocks) == 0 {
		t.Fatal("真实文件里没有解析到任何规则")
	}
	seen := map[string]bool{}
	for _, block := range blocks {
		seen[block.Weapon] = true
	}
	if len(seen) < 100 {
		t.Fatalf("解析到的武器数偏少：%d", len(seen))
	}
	current := text
	for weapon := range seen {
		set, _ := comboRuleSetOf(current, weapon)
		current, err = setComboRules(current, weapon, set)
		if err != nil {
			t.Fatalf("重写 %s 失败：%v", weapon, err)
		}
	}
	for weapon := range seen {
		before, _ := comboRuleSetOf(text, weapon)
		after, found := comboRuleSetOf(current, weapon)
		if !found || !reflect.DeepEqual(before, after) {
			t.Fatalf("%s 的规则往返后变了\n前 %+v\n后 %+v", weapon, before, after)
		}
	}
	if !strings.Contains(current, "宁超") || !strings.Contains(current, "连招白名单") {
		t.Fatal("批量重写破坏了文件底部的策划文档")
	}
	// A second full pass must be byte-stable, i.e. no note accumulation.
	second := current
	for weapon := range seen {
		set, _ := comboRuleSetOf(second, weapon)
		second, err = setComboRules(second, weapon, set)
		if err != nil {
			t.Fatal(err)
		}
	}
	if second != current {
		t.Fatal("第二轮重写不是幂等的")
	}
	// And the archive must still be writable: parse the re-encoded entry back.
	encoded, err := encodeText(current)
	if err != nil {
		t.Fatal(err)
	}
	data, err := a.replace(map[string][]byte{"comborule.xml": encoded})
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := parseArchive(data)
	if err != nil {
		t.Fatal(err)
	}
	back, err := reloaded.text("comborule.xml")
	if err != nil {
		t.Fatal(err)
	}
	if back != current {
		t.Fatal("归档往返后 comborule.xml 不一致")
	}
}
