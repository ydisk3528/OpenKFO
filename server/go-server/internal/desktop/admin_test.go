package desktop

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"kungfu.local/server/internal/persistence"
)

func installedRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../../..")
	if configured := os.Getenv("OPENKFO_TEST_RUNTIME"); configured != "" {
		root = configured
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, "runtime-local/client/Data/config.spf2")); err != nil {
		t.Skip("installed client fixture unavailable")
	}
	return root
}
func TestInstalledCatalogAndWeaponRoundTrip(t *testing.T) {
	root := installedRoot(t)
	client := filepath.Join(root, "runtime-local/client")
	items, err := Catalog(client)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3503 {
		t.Fatalf("catalog count %d", len(items))
	}
	source, err := loadArchive(filepath.Join(root, "runtime-local/weapon-config/original.spf2"))
	if err != nil {
		t.Fatal(err)
	}
	if err = source.verify(); err != nil {
		t.Fatal(err)
	}
	info, err := inspect(source, items)
	if err != nil {
		t.Fatal(err)
	}
	var weapon Weapon
	for _, candidate := range info.weapons {
		if candidate.ID == 253013 {
			weapon = candidate
		}
	}
	if len(weapon.Stages) == 0 {
		t.Fatal("missing weapon")
	}
	var first Stage
	for _, stage := range weapon.Stages {
		if stage.Stage == 1 {
			first = stage
		}
	}
	if !first.Supported || len(first.PropertyIDs) == 0 {
		t.Fatal("C1 unsupported")
	}
	rules := []Rule{{Stage: 1, Buff: 1, Level: 1, Duration: 3000, Properties: map[string]map[string]float64{first.PropertyIDs[0]: {"SkillDamage": 30}}}}
	rendered, err := render(source, items, map[string][]Rule{"253013": rules}, nil)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := parseArchive(rendered)
	if err != nil {
		t.Fatal(err)
	}
	if err = changed.verify(); err != nil {
		t.Fatal(err)
	}
	for name := range source.entries {
		after, err := changed.raw(name)
		if err != nil {
			t.Fatal(err)
		}
		before, err := source.raw(name)
		if err != nil {
			t.Fatal(err)
		}
		if name != "animation/2001.xml" && name != "skillproperty.xml" && !bytes.Equal(before, after) {
			t.Fatalf("unrelated changed: %s", name)
		}
	}
	props, err := changed.xml("skillproperty.xml")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, node := range props.children {
		if node.get("SkillProId") == "900000000" {
			found = node.get("SkillDamage") == "30" && node.get("UnNormalState") == "1"
		}
	}
	if !found {
		t.Fatal("configured property missing")
	}
	restored, err := render(source, items, map[string][]Rule{}, nil)
	if err != nil || !bytes.Equal(restored, source.data) {
		t.Fatal("empty plan does not restore baseline")
	}
	corrupted := append([]byte(nil), rendered...)
	corrupted[56] ^= 1
	bad, err := parseArchive(corrupted)
	if err != nil || bad.verify() == nil {
		t.Fatal("CRC corruption accepted")
	}
	rules[0].Properties[first.PropertyIDs[0]]["StandHurtFly"] = 254
	if _, err = validateRules(rules, weapon); err == nil {
		t.Fatal("non-native reaction accepted")
	}
	tempClient := filepath.Join(t.TempDir(), "client")
	if err = os.MkdirAll(filepath.Join(tempClient, "Data"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(tempClient, "Data/config.spf2"), source.data, 0600); err != nil {
		t.Fatal(err)
	}
	emptyRules := []Rule{}
	catalog, err := weaponHandle(Request{Operation: "weapon_catalog"}, tempClient, items, "")
	if err != nil {
		t.Fatal(err)
	}
	revision := catalog.(map[string]any)["revision"].(string)
	request := Request{Operation: "weapon_save", Weapon: 253013, Rules: emptyRules, Revision: revision}
	if _, err = weaponHandle(request, tempClient, items, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = weaponHandle(request, tempClient, items, ""); err == nil {
		t.Fatal("stale revision accepted")
	}
	unchanged, _ := os.ReadFile(filepath.Join(tempClient, "Data/config.spf2"))
	if !bytes.Equal(unchanged, source.data) {
		t.Fatal("save modified package")
	}
}
func TestDesktopRequestsUseRemoteOnly(t *testing.T) {
	root := installedRoot(t)
	var requests []persistence.AdminRequest
	admin := New(root)
	admin.Remote = func(request persistence.AdminRequest) (json.RawMessage, error) {
		requests = append(requests, request)
		return json.RawMessage(`{"message":"ok"}`), nil
	}
	enabled := true
	for _, request := range []Request{{Operation: "accounts"}, {Operation: "grant", ID: "retry-1", UID: 1003, Keys: []string{"25:253013"}, Quantity: 1, Days: 365}, {Operation: "shop_batch", ID: "batch-1", Keys: []string{"25:253013"}, Enabled: &enabled}} {
		if _, err := admin.Call(request); err != nil {
			t.Fatal(err)
		}
	}
	grant := requests[1].Records[0]
	if little.Uint32(grant[13:]) != 8760 || little.Uint16(grant[23:]) != 0 {
		t.Fatal("equipment duration/count mixed")
	}
	batch := requests[2]
	if !batch.Preserve || batch.All || len(batch.Offers) != 1 || little.Uint32(batch.Offers[0].Record[38:]) != 100 {
		t.Fatal("batch defaults/preserve invalid")
	}
	duplicate := Request{Operation: "grant", ID: "invalid", Keys: []string{"25:253013", "25:253013"}, Quantity: 1, Days: 365}
	if _, err := admin.Call(duplicate); err == nil {
		t.Fatal("duplicate accepted")
	}
	if len(requests) != 3 {
		t.Fatal("invalid input reached remote")
	}
	potion := Item{Kind: 64, ID: 640001, Stackable: true}
	record := template(potion, 10, 365)
	if little.Uint16(record[23:]) != 10 || little.Uint32(record[13:]) != 0 {
		t.Fatal("consumable treated as equipment")
	}
}
