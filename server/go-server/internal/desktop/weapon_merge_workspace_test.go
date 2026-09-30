package desktop

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeWorkspaceAssetsStayInsideData(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"../secret", "Data/../secret", "Data/config.spf2", "Data\\CONFIG.SPF2", "C:/secret", "server.exe"} {
		if _, e := mergeAssetPath(root, rel); e == nil {
			t.Fatal(rel)
		}
	}
	if _, e := mergeAssetPath(root, "Data/Effect/a.rws"); e != nil {
		t.Fatal(e)
	}
}
func TestMergeWorkspaceSelectedStagingAndStaleApply(t *testing.T) {
	installed := os.Getenv("OPENKFO_WEAPON_TEST_CLIENT")
	if installed == "" {
		t.Skip("real client required")
	}
	raw, e := os.ReadFile(configPath(installed))
	if e != nil {
		t.Fatal(e)
	}
	a, e := parseArchive(raw)
	if e != nil {
		t.Fatal(e)
	}
	items, e := catalog(installed, false, false)
	if e != nil {
		t.Fatal(e)
	}
	info, e := inspect(a, items)
	if e != nil {
		t.Fatal(e)
	}
	original, e := mergeWeaponFixture(t, a, info, "253300")
	if e != nil {
		t.Fatal(e)
	}
	makeWeapon := func(id int) mergeWeapon {
		number := fmt.Sprint(id)
		return mergeWeapon{ID: id, Name: "临时测试", ItemRow: replaceTableCell(t, original.ItemRow, 1, number), ItemactRow: replaceTableCell(t, original.ItemactRow, 0, number)}
	}
	ids := weaponIDsOf(a)
	if ids["253999"] || ids["253998"] {
		t.Skip("fixture ids already occupied")
	}
	first, second := makeWeapon(253999), makeWeapon(253998)
	first.ItemRow = replaceTableCell(t, first.ItemRow, 9, "workspace/icon-a.png")
	second.ItemRow = replaceTableCell(t, second.ItemRow, 9, "workspace/icon-b.png")
	manifest := &mergeManifest{Format: mergeFormat, Version: 1, Weapons: []mergeWeapon{first, second}, Assets: []string{"Data/UI/workspace/icon-a.png", "Data/UI/workspace/icon-b.png"}}
	packagePath := filepath.Join(t.TempDir(), "package.zip")
	f, e := os.Create(packagePath)
	if e != nil {
		t.Fatal(e)
	}
	writer := zip.NewWriter(f)
	manifestBytes, _ := json.Marshal(manifest)
	for name, raw := range map[string][]byte{"manifest.json": manifestBytes, "Data/UI/workspace/icon-a.png": []byte("asset-a"), "Data/UI/workspace/icon-b.png": []byte("asset-b")} {
		entry, e := writer.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = entry.Write(raw); e != nil {
			t.Fatal(e)
		}
	}
	if e = writer.Close(); e != nil {
		t.Fatal(e)
	}
	if e = f.Close(); e != nil {
		t.Fatal(e)
	}

	client := t.TempDir()
	if e = os.MkdirAll(filepath.Join(client, "Data"), 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(configPath(client), raw, 0600); e != nil {
		t.Fatal(e)
	}
	originalAsset := filepath.Join(client, "Data", "UI", "workspace", "icon-a.png")
	if e = os.MkdirAll(filepath.Dir(originalAsset), 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(originalAsset, []byte("original-a"), 0600); e != nil {
		t.Fatal(e)
	}
	admin := &Admin{Root: t.TempDir()}
	compare := func(workspace string) string {
		value, e := admin.weaponMergeWorkspace(Request{Operation: "weapon_merge_compare", SourcePath: packagePath, MergeWorkspace: workspace}, client)
		if e != nil {
			t.Fatal(e)
		}
		return value.(map[string]any)["revision"].(string)
	}
	proof := compare("")
	if _, e = admin.weaponMergeWorkspace(Request{Operation: "weapon_merge_stage", SourcePath: packagePath, MergeWeapons: []int{253999}, Revision: "old"}, client); e == nil {
		t.Fatal("stale comparison accepted")
	}
	value, e := admin.weaponMergeWorkspace(Request{Operation: "weapon_merge_stage", SourcePath: packagePath, MergeWeapons: []int{253999}, Revision: proof}, client)
	if e != nil {
		t.Fatal(e)
	}
	stage := value.(map[string]any)
	workspace := stage["workspace"].(string)
	live, _ := os.ReadFile(configPath(client))
	if !bytes.Equal(live, raw) {
		t.Fatal("staging changed client")
	}
	candidate, e := loadArchive(stage["path"].(string))
	if e != nil {
		t.Fatal(e)
	}
	meta, _, e := readMergeWorkspace(filepath.Join(admin.Root, "runtime-local", "weapon-merge-workspaces"), workspace, client)
	if e != nil {
		t.Fatal(e)
	}
	if len(meta.Assets) != 1 || meta.Assets[0] != "Data/UI/workspace/icon-a.png" {
		t.Fatal("unselected resource staged", meta.Assets)
	}
	untouched, _ := os.ReadFile(originalAsset)
	if string(untouched) != "original-a" {
		t.Fatal("staging changed resource")
	}
	stagedIDs := weaponIDsOf(candidate)
	if !stagedIDs["253999"] || stagedIDs["253998"] {
		t.Fatal("selection ignored")
	}
	value, e = admin.weaponMergeWorkspace(Request{Operation: "weapon_merge_stage", SourcePath: packagePath, MergeWorkspace: workspace, MergeWeapons: []int{253998}, Revision: compare(workspace)}, client)
	if e != nil {
		t.Fatal(e)
	}
	stage = value.(map[string]any)
	workspace = stage["workspace"].(string)
	candidate, e = loadArchive(stage["path"].(string))
	if e != nil {
		t.Fatal(e)
	}
	if ids := weaponIDsOf(candidate); !ids["253999"] || !ids["253998"] {
		t.Fatal("second package lost previous candidate")
	}
	for name := range a.entries {
		if !mergeAllowedEntries(&mergeManifest{Weapons: []mergeWeapon{makeWeapon(253999), makeWeapon(253998)}})[name] {
			before, _ := a.raw(name)
			after, _ := candidate.raw(name)
			if !bytes.Equal(before, after) {
				t.Fatal("unrelated entry changed", name)
			}
		}
	}
	snapshot := filepath.Join(t.TempDir(), "snapshot.zip")
	if _, e = admin.weaponMergeWorkspace(Request{Operation: "weapon_merge_save", MergeWorkspace: workspace, Path: snapshot}, client); e != nil {
		t.Fatal(e)
	}
	if _, e = admin.weaponMergeWorkspace(Request{Operation: "weapon_merge_save", MergeWorkspace: workspace, Path: snapshot}, client); e == nil {
		t.Fatal("existing snapshot overwritten")
	}
	if e = os.WriteFile(configPath(client), append(append([]byte{}, raw...), 0), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = admin.weaponMergeWorkspace(Request{Operation: "weapon_merge_apply", MergeWorkspace: workspace}, client); e == nil || !strings.Contains(e.Error(), "已变化") {
		t.Fatal("stale apply accepted", e)
	}
	if e = os.WriteFile(configPath(client), raw, 0600); e != nil {
		t.Fatal(e)
	}
	value, e = admin.weaponMergeWorkspace(Request{Operation: "weapon_merge_apply", MergeWorkspace: workspace}, client)
	if e != nil {
		t.Fatal(e)
	}
	live, e = os.ReadFile(configPath(client))
	if e != nil || !bytes.Equal(live, candidate.data) {
		t.Fatal("apply did not commit", e)
	}
	backup := value.(map[string]any)["backup"].(string)
	asset, _ := os.ReadFile(originalAsset)
	if string(asset) != "asset-a" {
		t.Fatal("selected asset not applied")
	}
	asset, _ = os.ReadFile(filepath.Join(backup, "Data", "UI", "workspace", "icon-a.png"))
	if string(asset) != "original-a" {
		t.Fatal("resource backup incomplete")
	}

	saved, e := os.ReadFile(configPath(backup))
	if e != nil || !bytes.Equal(saved, raw) {
		t.Fatal("backup incomplete", e)
	}
}
