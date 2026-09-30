package desktop

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestValidateBlueprintInfo(t *testing.T) {
	good := Blueprint{Name: "测试刀", Icon: `Picture\ItemIcon\x.png`, Description: "一把测试刀", Note: ""}
	if err := validateBlueprintInfo(good); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name      string
		mutate    func(*Blueprint)
		expectSub string
	}{
		{"empty name", func(b *Blueprint) { b.Name = "" }, "武器名称"},
		{"long name", func(b *Blueprint) { b.Name = strings.Repeat("刀", 25) }, "武器名称"},
		{"tab in name", func(b *Blueprint) { b.Name = "a\tb" }, "武器名称"},
		{"absolute icon", func(b *Blueprint) { b.Icon = "C:/x.png" }, "图标路径"},
		{"escaping icon", func(b *Blueprint) { b.Icon = "../x.png" }, "图标路径"},
		{"long description", func(b *Blueprint) { b.Description = strings.Repeat("简", 201) }, "简介"},
		{"newline description", func(b *Blueprint) { b.Description = "a\nb" }, "简介"},
		{"long note", func(b *Blueprint) { b.Note = strings.Repeat("注", 201) }, "备注"},
	}
	for _, tc := range cases {
		blueprint := good
		tc.mutate(&blueprint)
		err := validateBlueprintInfo(blueprint)
		if err == nil || !strings.Contains(err.Error(), tc.expectSub) {
			t.Fatalf("%s: expected %q failure, got %v", tc.name, tc.expectSub, err)
		}
	}
}

func TestBlueprintDescriptionFallback(t *testing.T) {
	if got := blueprintDescription(Blueprint{Name: "刀", Description: ""}); got != "刀" {
		t.Fatalf("empty description should reuse the name, got %q", got)
	}
	if got := blueprintDescription(Blueprint{Name: "刀", Description: "  "}); got != "刀" {
		t.Fatalf("blank description should reuse the name, got %q", got)
	}
	if got := blueprintDescription(Blueprint{Name: "刀", Description: "简介"}); got != "简介" {
		t.Fatalf("description lost, got %q", got)
	}
}

func TestDropTabRow(t *testing.T) {
	crlf := "Kind\tID\tName\r\n25\t1\t刀\r\n25\t253000\t自建\r\n"
	if got := dropTabRow(crlf, 1, "253000"); got != "Kind\tID\tName\r\n25\t1\t刀\r\n" {
		t.Fatalf("crlf row not dropped cleanly: %q", got)
	}
	lf := strings.ReplaceAll(crlf, "\r\n", "\n")
	if got := dropTabRow(lf, 1, "253000"); got != "Kind\tID\tName\n25\t1\t刀\n" {
		t.Fatalf("lf row not dropped cleanly: %q", got)
	}
	// Column-scoped: dropping on column 1 must not eat a name match elsewhere.
	if got := dropTabRow(lf, 1, "刀"); got != lf {
		t.Fatalf("wrong column matched: %q", got)
	}
}

// TestApplyBlueprintsInfoRerender drives applyBlueprints twice against a real
// client archive: register, then change the identity fields and re-render. The
// second pass must rebuild the rows instead of duplicating or leaving stale
// cells behind.
func TestApplyBlueprintsInfoRerender(t *testing.T) {
	client := os.Getenv("OPENKFO_WEAPON_TEST_CLIENT")
	if client == "" {
		t.Skip("client fixture required")
	}
	source, err := loadArchive(filepath.Join(client, "Data", "config.spf2"))
	if err != nil {
		t.Fatal(err)
	}
	if err = source.verify(); err != nil {
		t.Fatal(err)
	}
	itemText, err := source.text("item.txt")
	if err != nil {
		t.Fatal(err)
	}
	ids := weaponItemIDs(source)
	if len(ids) == 0 {
		t.Fatal("no kind-25 rows")
	}
	donor := ids[0]
	blueprint := Blueprint{ID: blueprintMaxID, Name: "重命名刀", Type: "1", Model: "", Donor: donor}
	// Pick the donor's model so validate-style preconditions hold even though
	// applyBlueprints itself does not check the file system.
	donorRow := itemRowIndex(itemText)[strconv.Itoa(donor)]
	blueprint.Model = donorRow[7]
	first, err := applyBlueprints(source, map[string]Blueprint{"253999": blueprint})
	if err != nil {
		t.Fatal(err)
	}
	text, err := first.text("item.txt")
	if err != nil {
		t.Fatal(err)
	}
	rows := itemRowIndex(text)["253999"]
	if rows == nil || rows[3] != "重命名刀" || rows[16] != "重命名刀" {
		t.Fatalf("first render lost identity cells: %v", rows)
	}
	if len(rows) != len(donorRow) || rows[17] != donorRow[17] || rows[53] != donorRow[53] {
		t.Fatalf("borrowed weapon changed the native row layout or inherited fields: %v", rows)
	}
	base, err := buildWeaponBase(source, &weaponState{Created: map[string]Blueprint{"253999": blueprint}})
	if err != nil {
		t.Fatal(err)
	}
	baseText, err := base.text("item.txt")
	if err != nil {
		t.Fatal(err)
	}
	if got := itemRowIndex(baseText)["253999"]; len(got) != len(donorRow) {
		t.Fatalf("base render added columns to client item table: %v", got)
	}
	if got := itemRowIndex(baseText)[strconv.Itoa(donor)]; strings.Join(got, "\t") != strings.Join(donorRow, "\t") {
		t.Fatal("building a weapon changed its donor")
	}
	actionText, err := first.text("itemact.txt")
	if err != nil {
		t.Fatal(err)
	}
	if actionRowIndex(actionText)["253999"][1] != "重命名刀" {
		t.Fatal("first render lost internal name")
	}

	blueprint.Name = "改名刀"
	blueprint.Description = "全新简介"
	blueprint.Icon = `Picture\ItemIcon\renamed.png`
	second, err := applyBlueprints(source, map[string]Blueprint{"253999": blueprint})
	if err != nil {
		t.Fatal(err)
	}
	text, err = second.text("item.txt")
	if err != nil {
		t.Fatal(err)
	}
	rows = itemRowIndex(text)["253999"]
	if rows == nil {
		t.Fatal("second render dropped the row")
	}
	if rows[3] != "改名刀" || rows[16] != "全新简介" || rows[9] != `Picture\ItemIcon\renamed.png` {
		t.Fatalf("second render kept stale identity cells: %v", rows)
	}
	if count := countRowsWithID(text, "253999"); count != 1 {
		t.Fatalf("expected exactly one item row for 253999, got %d", count)
	}
	blueprint.Glow = "#"
	withGlow, err := applyBlueprints(source, map[string]Blueprint{"253999": blueprint})
	if err != nil {
		t.Fatal(err)
	}
	glowText, err := withGlow.text("item.txt")
	if err != nil {
		t.Fatal(err)
	}
	if got := itemRowIndex(glowText)["253999"][53]; got != "#" {
		t.Fatalf("explicit glow override was lost: %q", got)
	}
	actionText, err = second.text("itemact.txt")
	if err != nil {
		t.Fatal(err)
	}
	actionRows := actionRowIndex(actionText)["253999"]
	if actionRows == nil || actionRows[1] != "改名刀" {
		t.Fatalf("second render lost action identity: %v", actionRows)
	}
	if count := countRowsWithID(actionText, "253999"); count != 1 {
		t.Fatalf("expected exactly one action row for 253999, got %d", count)
	}
}

func TestCurrentBlueprintsKeepNativeRows(t *testing.T) {
	folder := os.Getenv("OPENKFO_WEAPON_TEST_STATE")
	if folder == "" {
		t.Skip("editing-set fixture required")
	}
	contents, err := os.ReadFile(filepath.Join(folder, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state weaponState
	if err := json.Unmarshal(contents, &state); err != nil {
		t.Fatal(err)
	}
	source, err := loadArchive(filepath.Join(folder, "original.spf2"))
	if err != nil {
		t.Fatal(err)
	}
	if err := source.verify(); err != nil {
		t.Fatal(err)
	}
	original, err := source.text("item.txt")
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := buildWeaponBase(source, &state)
	if err != nil {
		t.Fatal(err)
	}
	result, err := rendered.text("item.txt")
	if err != nil {
		t.Fatal(err)
	}
	before, after := itemRowIndex(original), itemRowIndex(result)
	for _, id := range []string{"253300", "253301", "253350"} {
		blueprint, ok := state.Created[id]
		if !ok {
			t.Fatalf("missing editing-set blueprint %s", id)
		}
		donor := before[strconv.Itoa(blueprint.Donor)]
		row := after[id]
		if len(donor) != 60 || len(row) != len(donor) || row[17] != donor[17] || row[53] != donor[53] {
			t.Fatalf("weapon %s: columns=%d description=%q glow=%q; donor columns=%d description=%q glow=%q", id, len(row), row[17], row[53], len(donor), donor[17], donor[53])
		}
		if got := after[strconv.Itoa(blueprint.Donor)]; strings.Join(got, "\t") != strings.Join(donor, "\t") {
			t.Fatalf("donor %d was changed", blueprint.Donor)
		}
	}
}

func TestCurrentWeaponRenderDryRun(t *testing.T) {
	folder := os.Getenv("OPENKFO_WEAPON_TEST_STATE")
	client := os.Getenv("OPENKFO_WEAPON_TEST_CLIENT")
	if folder == "" || client == "" {
		t.Skip("editing set and client fixtures required")
	}
	contents, err := os.ReadFile(filepath.Join(folder, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state weaponState
	if err := json.Unmarshal(contents, &state); err != nil {
		t.Fatal(err)
	}
	entry := state.baselineFor(client)
	source, err := loadArchive(entry.path(folder))
	if err != nil {
		t.Fatal(err)
	}
	base, err := buildWeaponBase(source, &state)
	if err != nil {
		t.Fatal(err)
	}
	itemText, err := base.text("item.txt")
	if err != nil {
		t.Fatal(err)
	}
	items, err := itemsFromText(client, itemText, true, true)
	if err != nil {
		t.Fatal(err)
	}
	view, err := applyRemaps(base, &state, items)
	if err != nil {
		t.Fatal(err)
	}
	info, err := inspect(view, items)
	if err != nil {
		t.Fatal(err)
	}
	plans := make(map[string][]Rule, len(state.Applied))
	for key, rules := range state.Applied {
		plans[key] = rules
	}
	prepared, err := prepareClient(entry, folder, &state, plans, info)
	if err != nil {
		t.Fatal(err)
	}
	result, err := parseArchive(prepared.Data)
	if err != nil {
		t.Fatal(err)
	}
	current, err := parseArchive(prepared.Current)
	if err != nil {
		t.Fatal(err)
	}
	changed := []string{}
	for name := range current.entries {
		old, err := current.raw(name)
		if err != nil {
			t.Fatal(err)
		}
		updated, err := result.raw(name)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(old, updated) {
			changed = append(changed, name)
		}
	}
	for _, id := range []string{"253300", "253301", "253350"} {
		text, err := result.text("item.txt")
		if err != nil {
			t.Fatal(err)
		}
		row := itemRowIndex(text)[id]
		if len(row) != 60 {
			t.Fatalf("weapon %s has %d columns after full render", id, len(row))
		}
	}
	t.Logf("changed archive entries (%d): %v", len(changed), changed)
}

func TestAppliedWeaponRestoreChangesOnlyItemTable(t *testing.T) {
	beforePath := os.Getenv("OPENKFO_WEAPON_BEFORE")
	client := os.Getenv("OPENKFO_WEAPON_TEST_CLIENT")
	if beforePath == "" || client == "" {
		t.Skip("before and client fixtures required")
	}
	before, err := loadArchive(beforePath)
	if err != nil {
		t.Fatal(err)
	}
	after, err := loadArchive(filepath.Join(client, "Data", "config.spf2"))
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []*archive{before, after} {
		if err := a.verify(); err != nil {
			t.Fatal(err)
		}
	}
	if len(before.entries) != len(after.entries) {
		t.Fatalf("archive entry count changed: %d -> %d", len(before.entries), len(after.entries))
	}
	changed := []string{}
	for name := range before.entries {
		old, err := before.raw(name)
		if err != nil {
			t.Fatal(err)
		}
		updated, err := after.raw(name)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(old, updated) {
			changed = append(changed, name)
		}
	}
	if len(changed) != 1 || changed[0] != "item.txt" {
		t.Fatalf("unexpected archive entry changes: %v", changed)
	}
	beforeText, err := before.text("item.txt")
	if err != nil {
		t.Fatal(err)
	}
	afterText, err := after.text("item.txt")
	if err != nil {
		t.Fatal(err)
	}
	oldRows, newRows := itemRowIndex(beforeText), itemRowIndex(afterText)
	for id, donorID := range map[string]string{"253300": "253013", "253301": "253043", "253350": "253504"} {
		row, donor := newRows[id], oldRows[donorID]
		if len(row) != 60 || len(donor) != 60 || row[17] != donor[17] || row[53] != donor[53] {
			t.Fatalf("weapon %s: columns=%d, skill description=%q, glow=%q; donor glow=%q", id, len(row), row[17], row[53], donor[53])
		}
		if got := newRows[donorID]; strings.Join(got, "\t") != strings.Join(donor, "\t") {
			t.Fatalf("donor %s changed", donorID)
		}
		t.Logf("weapon %s: %d -> %d columns, glow %q -> %q", id, len(oldRows[id]), len(row), oldRows[id][53], row[53])
	}
}

func countRowsWithID(text, id string) int {
	count := 0
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		cells := strings.Split(line, "\t")
		for _, cell := range cells {
			if cell == id {
				count++
				break
			}
		}
	}
	return count
}
