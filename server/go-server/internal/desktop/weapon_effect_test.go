package desktop

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const effectTestArchive = `<?xml version="1.0" encoding="gb2312"?>
<AnmInfo>
	<AnmDesc id = "1" >
		<Effect frame="1" effectid = "100011" bindtype="3" bindindex="0"/>
		<HitEffect startframe="0" endframe="99" effectid="100302" bindtype="2"/>
		<Anm id="1" name="x" startframe="0" endframe="14"></Anm>
	</AnmDesc>
	<AnmDesc id = "2" >
		<Effect frame="1" effectid = "193012" bindtype="3" bindindex="0"/>
	</AnmDesc>
	<AnmDesc id = "3" >
		<Effect frame="1" effectid = "100302" bindtype="3" bindindex="0"/>
	</AnmDesc>
</AnmInfo>`

const actEffectTestTable = `<?xml version="1.0" encoding="gb2312"?><ActEffect>
	<!-- 所有主武器都需要加载的特效 -->
	<WeaponEffect ItemID = "0"><!--法宝特效-->
	<EffectFile EffectId = "100302" File = "100302" />
	</WeaponEffect>
	<WeaponEffect ItemID = "253100">
	<EffectFile EffectId = "100011" File = "100011_skill" />
	</WeaponEffect>
</ActEffect>`

const itemactEffectTest = "ID\tName\t2011\t2012\n253300\t测试\t2001001\t2001002\n"

// effectFixture builds an archive from the real client config with the
// animation, itemact and acteffect tables swapped for the test fixtures.
func effectFixture(t *testing.T) *archive {
	t.Helper()
	client := os.Getenv("OPENKFO_WEAPON_TEST_CLIENT")
	if client == "" {
		t.Skip("set OPENKFO_WEAPON_TEST_CLIENT for installed resource validation")
	}
	parts := map[string]string{
		"animation/2001.xml": effectTestArchive,
		"itemact.txt":        itemactEffectTest,
		"acteffect.xml":      actEffectTestTable,
	}
	replacements := map[string][]byte{}
	for name, text := range parts {
		encoded, err := encodeText(text)
		if err != nil {
			t.Fatal(err)
		}
		replacements[name] = encoded
	}
	raw, err := os.ReadFile(filepath.Join(client, "Data", "config.spf2"))
	if err != nil {
		t.Fatal(err)
	}
	a, err := parseArchive(raw)
	if err != nil {
		t.Fatal(err)
	}
	data, err := a.replace(replacements)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseArchive(data)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestSyncWeaponEffectsInsertsBlock(t *testing.T) {
	a := effectFixture(t)
	created := map[string]Blueprint{"253300": {ID: 253300, Name: "测试"}}
	synced, err := syncWeaponEffects(a, created)
	if err != nil {
		t.Fatal(err)
	}
	text, err := synced.text("acteffect.xml")
	if err != nil {
		t.Fatal(err)
	}
	// 100302 lives in the common block and must not repeat; 100011 keeps the
	// *_skill file alias registered by its original owner; 193012 falls back
	// to the id itself.
	for _, want := range []string{
		`<WeaponEffect ItemID = "253300">`,
		`<EffectFile EffectId = "100011" File = "100011_skill" />`,
		`<EffectFile EffectId = "193012" File = "193012" />`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("缺少 %s\n%s", want, text)
		}
	}
	if strings.Contains(text, `EffectId = "100302"`) && strings.Contains(text, `ItemID = "253300"`) {
		if strings.Count(text, `EffectId = "100302"`) != 1 {
			t.Fatalf("公共块特效被重复登记进自建武器块：\n%s", text)
		}
		block := regexp.MustCompile(`(?s)<WeaponEffect ItemID = "253300">.*?</WeaponEffect>`).FindString(text)
		if strings.Contains(block, "100302") {
			t.Fatalf("公共块特效混入自建武器块：\n%s", block)
		}
	}
	// Idempotent: syncing again must not change a byte.
	again, err := syncWeaponEffects(synced, created)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := synced.text("acteffect.xml")
	second, _ := again.text("acteffect.xml")
	if first != second {
		t.Fatalf("重复同步结果不稳定：\n%s\n---\n%s", first, second)
	}
	// 顺序：块插在 </ActEffect> 前。
	if !strings.Contains(text, "</WeaponEffect>\n</ActEffect>") && !regexp.MustCompile(`(?s)253300.*?</WeaponEffect>\s*</ActEffect>`).MatchString(text) {
		t.Fatalf("块未插在 ActEffect 结束标签前：\n%s", text)
	}
}

func TestSyncWeaponEffectsReplacesAndRemoves(t *testing.T) {
	a := effectFixture(t)
	created := map[string]Blueprint{"253300": {ID: 253300, Name: "测试"}}
	synced, err := syncWeaponEffects(a, created)
	if err != nil {
		t.Fatal(err)
	}
	// 动作改为只用 2001003（特效 100302 全在公共块）→ 块应被移除。
	replacements := map[string][]byte{}
	encoded, _ := encodeText("ID\tName\t2011\n253300\t测试\t2001003\n")
	replacements["itemact.txt"] = encoded
	data, err := synced.replace(replacements)
	if err != nil {
		t.Fatal(err)
	}
	synced, err = parseArchive(data)
	if err != nil {
		t.Fatal(err)
	}
	cleared, err := syncWeaponEffects(synced, created)
	if err != nil {
		t.Fatal(err)
	}
	text, _ := cleared.text("acteffect.xml")
	if strings.Contains(text, "253300") {
		t.Fatalf("无特效时块应被移除：\n%s", text)
	}
}

func TestSyncWeaponEffectsNoCreatedNoop(t *testing.T) {
	a := effectFixture(t)
	same, err := syncWeaponEffects(a, nil)
	if err != nil {
		t.Fatal(err)
	}
	if same != a {
		t.Fatal("无自建武器时应原样返回")
	}
}

// 真实归档回归：253300 的块必须覆盖它全部动作引用、且不与公共块重复。
func TestSyncWeaponEffectsRealArchive(t *testing.T) {
	client := os.Getenv("OPENKFO_WEAPON_TEST_CLIENT")
	if client == "" {
		t.Skip("set OPENKFO_WEAPON_TEST_CLIENT for installed resource validation")
	}
	raw, err := os.ReadFile(filepath.Join(client, "Data", "config.spf2"))
	if err != nil {
		t.Fatal(err)
	}
	a, err := parseArchive(raw)
	if err != nil {
		t.Fatal(err)
	}
	created := map[string]Blueprint{"253300": {ID: 253300, Name: "王八拳", Donor: 253013}}
	synced, err := syncWeaponEffects(a, created)
	if err != nil {
		t.Fatal(err)
	}
	text, _ := synced.text("acteffect.xml")
	block := regexp.MustCompile(`(?s)<WeaponEffect ItemID = "253300">.*?</WeaponEffect>`).FindString(text)
	if block == "" {
		t.Fatal("253300 未生成特效块")
	}
	ids := regexp.MustCompile(`EffectId = "(\w+)"`).FindAllStringSubmatch(block, -1)
	if len(ids) == 0 {
		t.Fatal("特效块为空")
	}
	commonBlock := regexp.MustCompile(`(?s)<WeaponEffect ItemID = "0">.*?</WeaponEffect>`).FindString(text)
	for _, m := range ids {
		if strings.Contains(commonBlock, `EffectId = "`+m[1]+`"`) {
			t.Fatalf("公共块特效 %s 混入 253300 块", m[1])
		}
	}
	t.Logf("253300 块登记 %d 条特效", len(ids))
}
