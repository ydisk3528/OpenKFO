package desktop

import (
	"bytes"
	"os"
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
