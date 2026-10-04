package desktop

import (
	"bytes"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestActionCloneKeepsNativeEntryProperty(t *testing.T) {
	a, err := parseArchive(synthArchive(t, map[string][]byte{"skill": []byte(`<SkillProperty><PropertyItem SkillProId="2201504" NeedMP="20" SkillDamage="125"/><PropertyItem SkillProId="2201999" NeedMP="99"/></SkillProperty>`)}))
	if err != nil {
		t.Fatal(err)
	}
	// The compact synthetic archive uses eight-byte tree labels.
	a.entries["skillproperty.xml"] = a.entries["skill"]
	p, err := newActionCloneProperties(a)
	if err != nil {
		t.Fatal(err)
	}
	reserved := map[string]bool{"2201/997": true}
	if got := p.allocate("2201504", reserved); got != 998 {
		t.Fatalf("must skip occupied property: %d", got)
	}
	if got := p.allocate("2201504", reserved); got != 996 {
		t.Fatalf("must skip prior clone and animation: %d", got)
	}
	// Ordinary actions without an entry property must not acquire a fabricated one.
	p.allocate("2001123", reserved)
	data, err := p.replace(a, map[string][]byte{"skillproperty.xml": []byte(strings.Replace(mustText(t, a, "skillproperty.xml"), "</SkillProperty>", `<PropertyItem SkillProId="900000138" SkillDamage="24"/></SkillProperty>`, 1))})
	if err != nil {
		t.Fatal(err)
	}
	b, err := parseArchive(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.verify(); err != nil {
		t.Fatal(err)
	}
	b.entries["skillproperty.xml"] = b.entries["skill"]
	text := mustText(t, b, "skillproperty.xml")
	for _, id := range []string{"2201504", "2201999", "2201998", "2201996", "900000138"} {
		if _, ok := propertyNodeText(text, id); !ok {
			t.Fatalf("missing property %s", id)
		}
	}
	if strings.Contains(text, `SkillProId="2001999"`) {
		t.Fatal("fabricated ordinary entry")
	}
	node, _ := propertyNodeText(text, "2201998")
	if !strings.Contains(node, `NeedMP="20"`) {
		t.Fatal("entry cost changed")
	}
}

func TestActionCloneExtendedNumberSpace(t *testing.T) {
	p := &actionCloneProperties{
		properties: map[string][]*xmlNode{"2001123": {{tag: "PropertyItem"}}, "20011000": {{tag: "PropertyItem"}}},
		clones:     map[string]string{},
	}
	reserved := map[string]bool{}
	for id := 1; id <= 999; id++ {
		reserved["2001/"+strconv.Itoa(id)] = true
	}
	reserved["2001/1001"] = true
	for _, want := range []int{1002, 1003} {
		got := p.allocate("2001123", reserved)
		if got != want {
			t.Fatalf("three-digit space exhausted: got %d, want %d", got, want)
		}
		if !reserved["2001/"+strconv.Itoa(got)] || p.clones["2001"+strconv.Itoa(got)] != "2001123" {
			t.Fatal("extended action was not reserved or its entry property was lost")
		}
	}
	for id := 1000; id <= 99999; id++ {
		reserved["2001/"+strconv.Itoa(id)] = true
	}
	if got := p.allocate("2001123", reserved); got != 0 {
		t.Fatalf("exhausted safe range must fail: %d", got)
	}
	if got := p.allocate("bad", reserved); got != 0 {
		t.Fatalf("invalid action must fail: %d", got)
	}
}

func TestActionCloneExtendedRemapIsolation(t *testing.T) {
	var animation strings.Builder
	animation.WriteString(`<AnmInfo>`)
	for id := 1; id <= 999; id++ {
		fmt.Fprintf(&animation, `<AnmDesc id="%d"><Anm id="1" name="600180" skillproid="100"/></AnmDesc>`, id)
	}
	animation.WriteString(`<AnmDesc id="1"><Condition><Ustate id="406"/></Condition><Anm id="1" name="600180" skillproid="100"/></AnmDesc></AnmInfo>`)
	a, _ := variantFixture(t, map[string]string{
		"itemact.txt":        "ID\tName\t2011\n253450\tdonor\t2001001\n253451\ttarget\t2001001\n",
		"animation/2001.xml": animation.String(),
		"skillproperty.xml":  `<SkillProperty><PropertyItem SkillProId="100" SkillDamage="7"/><PropertyItem SkillProId="2001001" NeedMP="20"/><PropertyItem SkillProId="20011000" NeedMP="99"/></SkillProperty>`,
	})
	items := []Item{{ID: 253450, Kind: 25}, {ID: 253451, Kind: 25}}
	state := &weaponState{
		Remaps:          map[string]map[int]*StageRemap{"253451": {2011: {Action: "2001001", PropertyID: "800000001", PropertyMap: map[string]string{"100": "800000001"}}}},
		ExtraProperties: map[string]ExtraProperty{"800000001": {Template: "100", OwnerWeapon: "253451"}},
	}
	out, err := applyRemaps(a, state, items)
	if err != nil {
		t.Fatal(err)
	}
	if err := out.verify(); err != nil {
		t.Fatal(err)
	}
	table := mustText(t, out, "itemact.txt")
	if !strings.Contains(table, "253450\tdonor\t2001001") || !strings.Contains(table, "253451\ttarget\t20011001") {
		t.Fatalf("donor or extended target action wrong: %s", table)
	}
	text := mustText(t, out, "animation/2001.xml")
	before := variantTexts(animation.String(), "1")
	after := variantTexts(text, "1")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("donor default or conditional block changed")
	}
	cloned := variantTexts(text, "1001")
	if len(cloned) != 2 {
		t.Fatalf("conditional copies lost: %d", len(cloned))
	}
	for _, block := range cloned {
		if !strings.Contains(block, `skillproid="800000001"`) {
			t.Fatal("cloned condition hit mapping lost")
		}
	}
	if !strings.Contains(cloned[1], `<Ustate id="406"`) {
		t.Fatal("clone condition lost")
	}
	entry, ok := propertyNodeText(mustText(t, out, "skillproperty.xml"), "20011001")
	if !ok || !strings.Contains(entry, `NeedMP="20"`) {
		t.Fatal("extended native entry missing")
	}
	for name := range a.entries {
		if name == "itemact.txt" || name == "animation/2001.xml" || name == "skillproperty.xml" {
			continue
		}
		x, _ := a.raw(name)
		y, _ := out.raw(name)
		if !bytes.Equal(x, y) {
			t.Fatalf("unrelated archive entry changed: %s", name)
		}
	}
	again, err := applyRemaps(a, state, items)
	if err != nil || !bytes.Equal(out.data, again.data) {
		t.Fatalf("extended remap is not deterministic: %v", err)
	}
}

func TestWeapon253350StandingUltimateEntry(t *testing.T) {
	path := os.Getenv("OPENKFO_WEAPON_253350_BASELINE")
	if path == "" {
		t.Skip("set baseline config path for real resource regression")
	}
	a, err := loadArchive(path)
	if err != nil {
		t.Fatal(err)
	}
	items, err := itemsFromText("", mustText(t, a, "item.txt"), true, false)
	if err != nil {
		t.Fatal(err)
	}
	clones := map[string]map[string]string{"253350": {"2071|2201504": "900000138", "2071|5040710": "900000139"}}
	plans := map[string][]Rule{"253350": {{Stage: 2071, Level: 1, Duration: 3000, Properties: map[string]map[string]float64{"2201504": {"SkillDamage": 24}}}}}
	data, err := render(a, items, plans, clones)
	if err != nil {
		t.Fatal(err)
	}
	b, err := parseArchive(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.verify(); err != nil {
		t.Fatal(err)
	}
	info, err := inspect(b, items)
	if err != nil {
		t.Fatal(err)
	}
	action := ""
	for _, w := range info.weapons {
		if w.ID == 253350 {
			for _, s := range w.Stages {
				if s.Stage == 2071 {
					action = s.Action
				}
			}
		}
	}
	if action == "" || action == "2201504" {
		t.Fatalf("action was not isolated: %s", action)
	}
	if len(info.properties[action]) != 1 {
		t.Fatalf("native 9EECC0 entry lookup fails: %s", action)
	}
	if got := info.properties["900000138"][0].get("SkillDamage"); got != "24" {
		t.Fatalf("damage=%s", got)
	}
	if clones["253350"]["2071|2201504"] != "900000138" {
		t.Fatal("combo property ID moved")
	}
	for name := range a.entries {
		if name == "itemact.txt" || name == "skillproperty.xml" || name == "animation/2201.xml" {
			continue
		}
		x, _ := a.raw(name)
		y, _ := b.raw(name)
		if !bytes.Equal(x, y) {
			t.Fatalf("unrelated entry changed: %s", name)
		}
	}
	before := strings.Split(mustText(t, a, "itemact.txt"), "\n")
	after := strings.Split(mustText(t, b, "itemact.txt"), "\n")
	for i, line := range before {
		if !strings.HasPrefix(line, strconv.Itoa(253350)+"\t") && line != after[i] {
			t.Fatalf("other action row changed: %d", i)
		}
	}
}
