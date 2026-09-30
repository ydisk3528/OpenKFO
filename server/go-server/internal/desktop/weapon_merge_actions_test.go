package desktop

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeActionsKeepsEntryAndIsolatesConflict(t *testing.T) {
	const oldBlock = `<AnmDesc id="999"><Anm id="1" name="old" skillproid="2201999"/></AnmDesc>`
	const newBlock = `<AnmDesc id="999"><Anm id="1" name="new" skillproid="2201999"/></AnmDesc>`
	const property = `<PropertyItem SkillProId="2201999" SkillDamage="24"/>`
	a, err := parseArchive(synthArchive(t, map[string][]byte{
		"props": []byte(`<SkillProperty>` + property + `</SkillProperty>`),
		"acts":  []byte("weapon\tname\t2071\n253350\tw\t2201999\n253504\tother\t2201999\n"),
		"anim":  []byte(`<AnmInfo>` + oldBlock + `</AnmInfo>`),
	}))
	if err != nil {
		t.Fatal(err)
	}
	a.entries["skillproperty.xml"] = a.entries["props"]
	a.entries["itemact.txt"] = a.entries["acts"]
	a.entries["animation/2201.xml"] = a.entries["anim"]
	fresh := func() *mergeManifest {
		return &mergeManifest{Weapons: []mergeWeapon{{ID: 253350, ItemactRow: "253350\tw\t2201999", AnimationBlocks: map[string][]string{"2201": {newBlock}}, SkillProperties: []string{property}, ComboRuleInner: `<MaxComboForSkill Skill="2201999"/>`}}}
	}
	m := fresh()
	r, err := planMergePropertyIDs(mustText(t, a, "skillproperty.xml"), m)
	if err != nil {
		t.Fatal(err)
	}
	if len(r) != 0 {
		t.Fatalf("entry must not be independently renumbered: %v", r)
	}
	if err = planMergeActions(a, m, r); err != nil {
		t.Fatal(err)
	}
	if r["2201999"] != "2201998" || !strings.HasSuffix(m.Weapons[0].ItemactRow, "2201998") {
		t.Fatalf("inconsistent action/entry: %v %+v", r, m.Weapons[0])
	}
	block := rewritePropertyRefs(m.Weapons[0].AnimationBlocks["2201"][0], r)
	animation, _, _, err := mergeAnimationBlock(mustText(t, a, "animation/2201.xml"), block)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(animation, oldBlock) || !strings.Contains(block, `skillproid="2201998"`) {
		t.Fatal("original changed or hit reference lost")
	}
	props, err := insertPropertyNode(mustText(t, a, "skillproperty.xml"), rewritePropertyRefs(property, r))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := a.replace(map[string][]byte{"props": []byte(props), "anim": []byte(animation)})
	if err != nil {
		t.Fatal(err)
	}
	b, err := parseArchive(raw)
	if err != nil {
		t.Fatal(err)
	}
	b.entries["skillproperty.xml"] = b.entries["props"]
	b.entries["itemact.txt"] = b.entries["acts"]
	b.entries["animation/2201.xml"] = b.entries["anim"]
	m = fresh()
	r, err = planMergePropertyIDs(props, m)
	if err != nil {
		t.Fatal(err)
	}
	if err = planMergeActions(b, m, r); err != nil {
		t.Fatal(err)
	}
	if r["2201999"] != "2201998" {
		t.Fatalf("repeat import did not reuse: %v", r)
	}
	out, _, _, err := mergeAnimationBlock(animation, rewritePropertyRefs(m.Weapons[0].AnimationBlocks["2201"][0], r))
	if err != nil || out != animation {
		t.Fatalf("repeat import changed animation: %v", err)
	}
}

func TestWeapon253350MergeRoundTrip(t *testing.T) {
	source, items := frameTestSource(t)
	info, err := inspect(source, items)
	if err != nil {
		t.Fatal(err)
	}
	w, err := mergeWeaponFixture(t, source, info, "253350")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := propertyNodeText(strings.Join(w.SkillProperties, "\n"), "2201999"); !ok {
		t.Fatal("export lost native entry property")
	}
	// Change only the imported copy of the standing ultimate. The destination's
	// existing block must survive even if other weapons still reference it.
	found := false
	for i, b := range w.AnimationBlocks["2201"] {
		n, _ := parseXML(b)
		if n.get("id") == "999" {
			w.AnimationBlocks["2201"][i] = strings.Replace(b, `name="6001246"`, `name="6001247"`, 1)
			found = true
		}
	}
	if !found {
		t.Fatal("expected repaired standing ultimate")
	}
	zipPath := writeMergeFixture(t, &mergeManifest{Format: mergeFormat, Version: 1, Weapons: []mergeWeapon{w}})
	dst := t.TempDir()
	os.MkdirAll(filepath.Join(dst, "Data"), 0700)
	if err = os.WriteFile(configPath(dst), source.data, 0600); err != nil {
		t.Fatal(err)
	}
	folder := t.TempDir()
	if _, err = weaponMergeImport(Request{SourcePath: zipPath}, dst, folder); err != nil {
		t.Fatal(err)
	}
	first, err := loadArchive(configPath(dst))
	if err != nil {
		t.Fatal(err)
	}
	action, lookupErr := actionForState(first, "253350", 2071)
	if lookupErr != nil || action == "2201999" {
		t.Fatalf("conflict not isolated: %s", action)
	}
	if _, ok := propertyNodeText(mustText(t, first, "skillproperty.xml"), action); !ok {
		t.Fatal("entry lost during import")
	}
	old, _ := currentBlock(mustText(t, source, "animation/2201.xml"), "999")
	if !strings.Contains(mustText(t, first, "animation/2201.xml"), old) {
		t.Fatal("original shared block overwritten")
	}
	beforeRows := strings.Split(mustText(t, source, "itemact.txt"), "\n")
	afterText := mustText(t, first, "itemact.txt")
	for _, row := range beforeRows {
		if strings.TrimSpace(row) != "" && !strings.HasPrefix(row, "253350\t") && !strings.Contains(afterText, row) {
			t.Fatalf("other weapon row changed: %s", row)
		}
	}
	if _, err = weaponMergeImport(Request{SourcePath: zipPath}, dst, folder); err != nil {
		t.Fatal(err)
	}
	second, err := loadArchive(configPath(dst))
	if err != nil {
		t.Fatal(err)
	}
	for name := range first.entries {
		x, _ := first.raw(name)
		y, _ := second.raw(name)
		if !bytes.Equal(x, y) {
			t.Fatalf("repeat import changed %s", name)
		}
	}
}
