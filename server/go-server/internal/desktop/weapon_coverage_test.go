package desktop

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestWeaponActionCoverage(t *testing.T) {
	client := os.Getenv("OPENKFO_WEAPON_TEST_CLIENT")
	source := filepath.Join(client, "Data/config.spf2")
	if client == "" {
		root := installedRoot(t)
		client = filepath.Join(root, "runtime-local/client")
		source = filepath.Join(root, "runtime-local/weapon-config/original.spf2")
	}
	items, err := catalog(client, false, false)
	if err != nil {
		t.Fatal(err)
	}
	a, err := loadArchive(source)
	if err != nil {
		t.Fatal(err)
	}
	info, err := inspect(a, items)
	if err != nil {
		t.Fatal(err)
	}
	weapons := map[string]Weapon{}
	for _, w := range info.weapons {
		weapons[strconv.Itoa(w.ID)] = w
	}
	text, err := a.text("itemact.txt")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n")), "\n")
	header := strings.Split(lines[0], "\t")
	missing := 0
	checked := 0
	for _, line := range lines[1:] {
		row := strings.Split(line, "\t")
		w, ok := weapons[row[0]]
		if !ok {
			continue
		}
		seen := map[string]bool{}
		for _, stage := range w.Stages {
			seen[stage.State] = true
		}
		for i, action := range row {
			if i >= 2 && action != "0" && action != "" {
				checked++
				if !seen[header[i]] {
					missing++
				}
			}
		}
	}
	if missing != 0 {
		t.Fatalf("missing %d / %d actions", missing, checked)
	}
	t.Logf("weapons=%d action cells=%d", len(weapons), checked)
	for _, w := range info.weapons {
		if w.ID == 253030 {
			count := 0
			editable := 0
			for _, stage := range w.Stages {
				if stage.State == "2031" && (!strings.Contains(stage.Label, "跑动普通攻击") || !strings.Contains(stage.Label, "非按键")) {
					t.Fatalf("missing animation description: %s", stage.Label)
				}
				if strings.Contains(stage.Label, "客户端未标注") {
					t.Fatal(stage.Label)
				}
				state, _ := strconv.Atoi(stage.State)
				if state >= 2000 && state < 3000 {
					count++
					if stage.Supported {
						editable++
					}
				}
			}
			if count != 18 || editable != 17 {
				t.Fatalf("Lingyun actions=%d editable=%d", count, editable)
			}
			t.Logf("Lingyun combat actions=%d editable=%d total=%d", count, editable, len(w.Stages))
		}
	}

}

func TestSharedAndCrossFileWeaponActions(t *testing.T) {
	root := installedRoot(t)
	items, err := catalog(filepath.Join(root, "runtime-local/client"), false, false)
	if err != nil {
		t.Fatal(err)
	}
	source, err := loadArchive(filepath.Join(root, "runtime-local/weapon-config/original.spf2"))
	if err != nil {
		t.Fatal(err)
	}
	before, err := inspect(source, items)
	if err != nil {
		t.Fatal(err)
	}
	var weapon Weapon
	for _, w := range before.weapons {
		if w.ID == 253030 {
			weapon = w
		}
	}
	rules := []Rule{}
	edited := map[string]Stage{}
	for _, state := range []string{"2021", "2051", "2071"} {
		found := false
		for _, s := range weapon.Stages {
			if s.State == state {
				found = true
				if !s.Supported || len(s.Hits) == 0 {
					t.Fatalf("missing editable state %s", state)
				}
				rules = append(rules, Rule{Stage: s.Stage, Buff: 0, Level: 1, Duration: 3000, Properties: map[string]map[string]float64{s.PropertyIDs[0]: {"SkillDamage": 71}}})
				edited[state] = s
			}
		}
		if !found {
			t.Fatal("omitted state", state)
		}
	}
	data, err := render(source, items, map[string][]Rule{"253030": rules}, nil)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := parseArchive(data)
	if err != nil {
		t.Fatal(err)
	}
	if err = changed.verify(); err != nil {
		t.Fatal(err)
	}
	after, err := inspect(changed, items)
	if err != nil {
		t.Fatal(err)
	}
	lookup := map[int]Weapon{}
	for _, w := range after.weapons {
		lookup[w.ID] = w
	}
	for _, w := range before.weapons {
		newStages := map[string]Stage{}
		for _, s := range lookup[w.ID].Stages {
			newStages[s.State] = s
		}
		for _, old := range w.Stages {
			updated := newStages[old.State]
			_, isEdited := edited[old.State]
			if w.ID == 253030 && isEdited {
				if updated.Hits[0].Values["SkillDamage"] != "71" {
					t.Fatal("edit not applied", old.State)
				}
				if len(before.owners[old.Action]) > 1 && updated.Action == old.Action {
					t.Fatal("shared action not isolated")
				}
			} else if !reflect.DeepEqual(old, updated) {
				t.Fatalf("unrelated weapon/action changed: %d %s", w.ID, old.State)
			}
		}
	}
	allowed := map[string]bool{"itemact.txt": true, "skillproperty.xml": true, "animation/2001.xml": true, "animation/2009.xml": true, "animation/2205.xml": true}
	for name := range source.entries {
		if allowed[name] {
			continue
		}
		a, _ := source.raw(name)
		b, _ := changed.raw(name)
		if !bytes.Equal(a, b) {
			t.Fatal("unrelated archive entry changed", name)
		}
	}
	restored, err := render(source, items, map[string][]Rule{}, nil)
	if err != nil || !bytes.Equal(restored, source.data) {
		t.Fatal("restore does not recover original archive")
	}
}

func TestActionDescription(t *testing.T) {
	node, err := parseXML(`<AnmDesc><!-- <Throw skillproid="1"/> --><!-- 70 --><!-- 功夫拳跳跃攻击 50 --><Hit/><!-- 不应读取后续注释 --></AnmDesc>`)
	if err != nil {
		t.Fatal(err)
	}
	if got := actionDescription(node); got != "功夫拳跳跃攻击 50" {
		t.Fatal(got)
	}
	node, _ = parseXML(`<AnmDesc><Hit/><!-- 后续注释 --></AnmDesc>`)
	if got := actionDescription(node); got != "" {
		t.Fatal(got)
	}
}
