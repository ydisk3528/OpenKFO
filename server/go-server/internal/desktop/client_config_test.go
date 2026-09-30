package desktop

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClientConfigRecordIsolation(t *testing.T) {
	text := `<MapInfo><MapConfig MapId="805" Name="双梯"/><MapConfig MapId="804" Name="原版"/></MapInfo>`
	units, _, err := configUnits("mapmgr.xml", text)
	if err != nil || len(units) != 2 {
		t.Fatal(err)
	}
	changed, err := configReplace("mapmgr.xml", text, units[0].Key, `<MapConfig MapId="805" Name="双梯新版"/>`)
	if err != nil {
		t.Fatal(err)
	}
	after, _, err := configUnits("mapmgr.xml", changed)
	if err != nil || after[1].Content != units[1].Content {
		t.Fatal("unrelated map changed", err)
	}
	if _, err = configReplace("mapmgr.xml", text, units[0].Key, `<MapConfig MapId="806"/>`); err == nil {
		t.Fatal("id replacement accepted")
	}
	if _, _, err = configUnits("mapmgr.xml", `<R><M Id="1"/><M Id="1"/></R>`); err == nil {
		t.Fatal("duplicate ID accepted")
	}
}
func TestClientConfigNativeDuplicateScope(t *testing.T) {
	text := `<R><PropertyItem SkillProId="1" SkillDamage="9"/><PropertyItem SkillProId="1" SkillDamage="10"/></R>`
	units, _, err := configUnits("skillproperty.xml", text)
	if err != nil || len(units) != 1 {
		t.Fatalf("native duplicate: %v, %d", err, len(units))
	}
	changed, err := configReplace("skillproperty.xml", text, units[0].Key, `<PropertyItem SkillProId="1" SkillDamage="11"/>`)
	if err != nil || !strings.Contains(changed, `SkillDamage="10"`) {
		t.Fatalf("duplicate sibling lost: %v, %s", err, changed)
	}
	if _, _, err := configUnits("mapmgr.xml", text); err == nil {
		t.Fatal("duplicate allowed outside skillproperty")
	}
}
func TestClientConfigResourceBoundary(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.spf2"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "Data"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../secret", "Data/../../secret", "C:/secret", "Data/../config.spf2"} {
		if _, err := configSafeFile(root, path); err == nil {
			t.Fatal(path)
		}
	}
}
func TestClientConfigRealPlans(t *testing.T) {
	path := os.Getenv("OPENKFO_TEST_CONFIG")
	if path == "" {
		t.Skip("real client config required")
	}
	a, err := loadArchive(path)
	if err != nil {
		t.Fatal(err)
	}
	admin := &Admin{Root: t.TempDir()}
	r := clientConfigRequest{Base: path, Category: "items", File: "item.txt", Revision: digest(a.data), Name: "测试披风方案"}
	result, err := admin.clientConfig("client_config_records", r)
	if err != nil {
		t.Fatal(err)
	}
	records := result.(map[string]any)["records"].([]configUnit)
	var unit configUnit
	for _, u := range records {
		if u.Key == "21:213002" {
			unit = u
		}
	}
	if unit.Key == "" {
		t.Fatal("cape missing")
	}
	r.Record = unit.Key
	r.Values = map[string]string{"说明": unit.Values["说明"] + "（测试）"}
	saved, err := admin.clientConfig("client_config_save", r)
	if err != nil {
		t.Fatal(err)
	}
	plan := saved.(map[string]any)["plan"].(configPlan)
	r.Selected = []string{plan.ID}
	r.Version = "test-config-1"
	first, err := admin.clientConfig("client_config_preview", r)
	if err != nil {
		t.Fatal(err)
	}
	second, err := admin.clientConfig("client_config_preview", r)
	if err != nil {
		t.Fatal(err)
	}
	if first.(map[string]any)["preview"] != second.(map[string]any)["preview"] {
		t.Fatal("non-deterministic preview")
	}
	current, _ := os.ReadFile(path)
	if !bytes.Equal(current, a.data) {
		t.Fatal("source modified")
	}
	folder := filepath.Join(admin.Root, "runtime-local/client-config-plans")
	plans, _ := configPlans(folder)
	duplicate := plans[0]
	duplicate.ID = "conflict"
	if err = saveConfigPlan(folder, duplicate); err != nil {
		t.Fatal(err)
	}
	r.Selected = append(r.Selected, duplicate.ID)
	if _, err = admin.clientConfig("client_config_preview", r); err == nil || !strings.Contains(err.Error(), "冲突") {
		t.Fatal("conflict not rejected", err)
	}
	r.Selected = []string{plan.ID}
	r.Resources = []string{"Data/Map/no-such-resource.rws"}
	if _, err = admin.clientConfig("client_config_preview", r); err == nil {
		t.Fatal("missing resource accepted")
	}
	r.Resources = nil
	r.Preview = "stale"
	if _, err = admin.clientConfig("client_config_build", r); err == nil {
		t.Fatal("stale preview accepted")
	}
}

func TestClientConfigBuiltArchiveIsolation(t *testing.T) {
	path := os.Getenv("OPENKFO_TEST_BUILT_CONFIG")
	if path == "" {
		t.Skip("acceptance output required")
	}
	base, e := loadArchive(filepath.Join(filepath.Dir(path), "base.spf2"))
	if e != nil {
		t.Fatal(e)
	}
	output, e := loadArchive(path)
	if e != nil {
		t.Fatal(e)
	}
	if e = output.verify(); e != nil {
		t.Fatal(e)
	}
	if len(base.entries) != len(output.entries) {
		t.Fatal("archive entries changed")
	}
	for name := range base.entries {
		if name == "mapmgr.xml" || name == "animationmd5.xml" {
			continue
		}
		a, e := base.raw(name)
		if e != nil {
			t.Fatal(e)
		}
		b, e := output.raw(name)
		if e != nil || !bytes.Equal(a, b) {
			t.Fatalf("unselected entry changed: %s: %v", name, e)
		}
	}
}

func TestClientConfigUsesSharedDirectory(t *testing.T) {
	root := t.TempDir()
	settings := filepath.Join(root, "gm-settings.json")
	directory := filepath.Join(root, "current-client")
	raw, _ := json.Marshal(map[string]string{"client_directory": directory})
	if err := os.WriteFile(settings, raw, 0600); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(root, "runtime-local", "client-config-plans")
	if err := os.MkdirAll(folder, 0700); err != nil {
		t.Fatal(err)
	}
	stale, _ := json.Marshal(clientConfigRequest{Base: "old.spf2", ResourceRoot: "old-client"})
	if err := os.WriteFile(filepath.Join(folder, "project.json"), stale, 0600); err != nil {
		t.Fatal(err)
	}
	admin := Admin{Root: root, GMSettings: settings}
	result, err := admin.clientConfig("client_config_plans", clientConfigRequest{})
	if err != nil {
		t.Fatal(err)
	}
	got := result.(map[string]any)
	if got["base"] != filepath.Join(directory, "Data", "config.spf2") || got["resource_root"] != directory {
		t.Fatalf("wrong source: %v", got)
	}
	for _, request := range []clientConfigRequest{{Base: "old.spf2"}, {ResourceRoot: "old-client"}} {
		if _, err := admin.clientConfig("client_config_save", request); err == nil || !strings.Contains(err.Error(), "目录已变化") {
			t.Fatalf("stale edit accepted: %v", err)
		}
	}
}
