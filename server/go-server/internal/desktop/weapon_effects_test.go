package desktop

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
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
	if len(p["additions"].([]effectRegistration)) != 19 {
		t.Fatalf("unexpected additions: %v", p)
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
