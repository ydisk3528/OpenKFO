package desktop

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWeaponCatalogAdoptsReplacedClientWithoutReplayingOldPlans(t *testing.T) {
	client := os.Getenv("OPENKFO_WEAPON_TEST_CLIENT")
	if client == "" {
		t.Skip("set OPENKFO_WEAPON_TEST_CLIENT")
	}
	current, err := loadArchive(filepath.Join(client, "Data", "config.spf2"))
	if err != nil {
		t.Fatal(err)
	}
	items, err := catalog(client, false)
	if err != nil {
		t.Fatal(err)
	}
	table, err := current.text("itemact.txt")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(table, "\n")
	found := false
	for i, line := range lines {
		if strings.HasPrefix(line, "253300\t") {
			lines[i] = ""
			found = true
		}
	}
	if !found {
		t.Skip("fixture needs custom weapon 253300")
	}
	encoded, err := encodeText(strings.Join(lines, "\n"))
	if err != nil {
		t.Fatal(err)
	}
	old, err := current.replace(map[string][]byte{"itemact.txt": encoded})
	if err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	os.Mkdir(filepath.Join(target, "Data"), 0700)
	packagePath := filepath.Join(target, "Data", "config.spf2")
	os.WriteFile(packagePath, current.data, 0600)
	folder := filepath.Join(t.TempDir(), "plans")
	os.Mkdir(folder, 0700)
	os.WriteFile(filepath.Join(folder, "original.spf2"), old, 0600)
	previous := weaponState{SourceHash: digest(old), AppliedHash: digest(old), Drafts: map[string][]Rule{"253013": {{Stage: 1, Buff: 1, Level: 1, Duration: 3000}}}, Applied: map[string][]Rule{}}
	saved, _ := json.Marshal(previous)
	os.WriteFile(filepath.Join(folder, "settings.json"), saved, 0600)
	result, err := weaponHandle(Request{Operation: "weapon_catalog"}, target, items, folder)
	if err != nil {
		t.Fatal(err)
	}
	c := result.(map[string]any)
	found = false
	for _, w := range c["weapons"].([]Weapon) {
		if w.ID == 253300 && len(w.Stages) > 0 {
			found = true
		}
	}
	if !found {
		t.Fatal("custom weapon missing")
	}
	if len(c["drafts"].(map[string][]Rule)) != 0 {
		t.Fatal("old plans replayed")
	}
	backups, _ := filepath.Glob(filepath.Join(folder, "previous-*"))
	if len(backups) != 1 {
		t.Fatal("missing backup", backups)
	}
	b, _ := os.ReadFile(filepath.Join(backups[0], "original.spf2"))
	if !bytes.Equal(b, old) {
		t.Fatal("old baseline lost")
	}
	b, _ = os.ReadFile(filepath.Join(backups[0], "settings.json"))
	if !bytes.Equal(b, saved) {
		t.Fatal("old plans lost")
	}
	// Saving a plan uses the current custom weapon, without changing any package entry.
	_, err = weaponHandle(Request{Operation: "weapon_save", Weapon: 253300, Rules: []Rule{}, Revision: c["revision"].(string)}, target, items, folder)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(packagePath)
	if !bytes.Equal(b, current.data) {
		t.Fatal("catalog/save changed client resources")
	}
	_, err = weaponHandle(Request{Operation: "weapon_catalog"}, target, items, folder)
	if err != nil {
		t.Fatal(err)
	}
	backups, _ = filepath.Glob(filepath.Join(folder, "previous-*"))
	if len(backups) != 1 {
		t.Fatal("repeated refresh reset plans")
	}
}
