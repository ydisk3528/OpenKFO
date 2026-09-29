package desktop

import (
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
