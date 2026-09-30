package desktop

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestWeaponEffectsPreviewApplyPreservesOtherEntries(t *testing.T) {
	client := os.Getenv("OPENKFO_WEAPON_TEST_CLIENT")
	if client == "" {
		t.Skip("set OPENKFO_WEAPON_TEST_CLIENT")
	}
	a, err := loadArchive(filepath.Join(client, "Data/config.spf2"))
	if err != nil {
		t.Fatal(err)
	}
	items, err := catalog(client, false, false)
	if err != nil {
		t.Fatal(err)
	}
	text, err := a.text("acteffect.xml")
	if err != nil {
		t.Fatal(err)
	}
	text = regexp.MustCompile(`(?s)<WeaponEffect\b[^>]*ItemID="253300"[^>]*>.*?</WeaponEffect>`).ReplaceAllString(text, "")
	encoded, _ := encodeText(text)
	data, err := a.replace(map[string][]byte{"acteffect.xml": encoded})
	if err != nil {
		t.Fatal(err)
	}
	fixture, _ := parseArchive(data)
	target := t.TempDir()
	os.MkdirAll(filepath.Join(target, "Data/effect/effect"), 0700)
	os.WriteFile(filepath.Join(target, "Data/config.spf2"), data, 0600)
	// Only resource existence is inspected; no game files are modified by tests.
	entries, err := os.ReadDir(filepath.Join(client, "Data/effect/effect"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			os.WriteFile(filepath.Join(target, "Data/effect/effect", e.Name()), []byte("fixture"), 0600)
		}
	}
	folder := t.TempDir()
	preview, err := weaponHandle(Request{Operation: "weapon_effects_preview", Weapon: 253300}, target, items, folder)
	if err != nil {
		t.Fatal(err)
	}
	p := preview.(map[string]any)
	additions := p["additions"].([]effectRegistration)
	// 这里不写死条数：可补登记的特效数随客户端数据变化（招式引用的 effectid 增删、
	// Data/effect/effect 里资源文件有无都会影响它）。写死就会在数据变化后误报。
	// 真正要守住的是行为：能把缺登记的补上、补上的一定有资源文件、拿不准的走 issues。
	if len(additions) == 0 {
		t.Fatalf("no additions detected: %v", p)
	}
	for _, add := range additions {
		if add.ID == "" || add.File == "" {
			t.Fatalf("addition without id/file: %+v", add)
		}
		// 没有资源文件的引用必须走 issues，绝不能进 additions——否则会写进
		// aeteffect.xml 一条永远加载不出来的空登记。
		resource := filepath.Join(client, "Data", "effect", "effect", filepath.FromSlash(strings.ReplaceAll(add.File, "\\", "/")))
		if stat, statErr := os.Stat(resource); statErr != nil || !stat.Mode().IsRegular() {
			t.Fatalf("addition %+v has no resource file at %s", add, resource)
		}
	}
	if len(p["issues"].([]string)) == 0 {
		t.Fatal("missing unresolved effect diagnostic")
	}
	path := filepath.Join(target, "Data/config.spf2")
	b, _ := os.ReadFile(path)
	if !bytes.Equal(b, data) {
		t.Fatal("preview wrote config")
	}
	if _, err := weaponHandle(Request{Operation: "weapon_effects_apply", Weapon: 253300, Revision: "stale"}, target, items, folder); err == nil {
		t.Fatal("accepted stale preview")
	}
	result, err := weaponHandle(Request{Operation: "weapon_effects_apply", Weapon: 253300, Revision: p["revision"].(string)}, target, items, folder)
	if err != nil {
		t.Fatal(err)
	}
	backup, _ := os.ReadFile(result.(map[string]any)["backup"].(string))
	if !bytes.Equal(backup, data) {
		t.Fatal("backup differs")
	}
	after, err := loadArchive(path)
	if err != nil {
		t.Fatal(err)
	}
	for name := range fixture.entries {
		if name == "acteffect.xml" {
			continue
		}
		x, _ := fixture.raw(name)
		y, _ := after.raw(name)
		if !bytes.Equal(x, y) {
			t.Fatal("unrelated entry changed", name)
		}
	}
	again, err := weaponHandle(Request{Operation: "weapon_effects_preview", Weapon: 253300}, target, items, folder)
	if err != nil || len(again.(map[string]any)["additions"].([]effectRegistration)) != 0 {
		t.Fatal("duplicate registration", err)
	}
}
