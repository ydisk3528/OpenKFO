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

func writeMergeProfileFixture(t *testing.T) (string, string, []byte, []byte, string, string) {
	t.Helper()
	folder := t.TempDir()
	client := t.TempDir()
	if err := os.MkdirAll(filepath.Join(client, "Data"), 0700); err != nil {
		t.Fatal(err)
	}
	oldConfig := synthArchive(t, map[string][]byte{"item.txt": []byte("old")})
	newConfig := synthArchive(t, map[string][]byte{"item.txt": []byte("imported")})
	if err := os.WriteFile(configPath(client), newConfig, 0600); err != nil {
		t.Fatal(err)
	}
	oldHash := digest(oldConfig)
	newHash := digest(newConfig)
	entry := &clientBaseline{Directory: client, File: "baseline.spf2", SourceHash: oldHash, AppliedHash: oldHash}
	if err := os.WriteFile(entry.path(folder), oldConfig, 0600); err != nil {
		t.Fatal(err)
	}
	baselines := map[string]*clientBaseline{normalizeDir(client): entry}
	targetState := emptyWeaponState(baselines, oldHash)
	if err := archiveWeaponProfile(folder, oldHash, &targetState); err != nil {
		t.Fatal(err)
	}
	return folder, client, oldConfig, newConfig, oldHash, newHash
}

func TestSyncBaselineAfterImportDoesNotUseOtherActiveProfile(t *testing.T) {
	folder, client, _, newConfig, oldHash, newHash := writeMergeProfileFixture(t)
	baselines := map[string]*clientBaseline{normalizeDir(client): {
		Directory: client, File: "baseline.spf2", SourceHash: oldHash, AppliedHash: oldHash,
	}}
	state := emptyWeaponState(baselines, "global-profile")
	state.Created["253011"] = Blueprint{ID: 253011, Name: "other profile weapon"}
	state.SourceHash = "global-source"
	state.AppliedHash = "global-applied"
	globalWorkspace := weaponWorkspacePath(folder, 253011, "global-profile")
	if err := os.MkdirAll(filepath.Dir(globalWorkspace), 0700); err != nil {
		t.Fatal(err)
	}
	globalWorkspaceData := []byte(`{"profile":"global"}`)
	if err := os.WriteFile(globalWorkspace, globalWorkspaceData, 0600); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(folder, "settings.json")
	if err := persistWeaponState(settingsPath, &state); err != nil {
		t.Fatal(err)
	}
	if err := syncBaselineAfterImport(folder, client, ""); err != nil {
		t.Fatal(err)
	}
	var got weaponState
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Created) != 0 {
		t.Fatalf("导入目标继承了其他 active profile 的作者态：%v", got.Created)
	}
	if got.ActiveProfile != newHash {
		t.Fatalf("目标 profile = %q，want %q", got.ActiveProfile, newHash)
	}
	if _, err = os.Stat(globalWorkspace); err != nil {
		t.Fatalf("其他 profile workspace 被错误移走：%v", err)
	}
	if _, err = os.Stat(configPath(client)); err != nil {
		t.Fatal(err)
	}
	if got.Baselines[normalizeDir(client)].SourceHash != newHash {
		t.Fatalf("目标 baseline SourceHash = %q，want %q", got.Baselines[normalizeDir(client)].SourceHash, newHash)
	}
	if !bytes.Equal(newConfig, func() []byte { data, _ := os.ReadFile(configPath(client)); return data }()) {
		t.Fatal("导入后的客户端配置被错误重渲染")
	}
}

func TestSyncBaselineAfterImportAlignsProfileHashAndWorkspace(t *testing.T) {
	folder, client, _, _, oldHash, newHash := writeMergeProfileFixture(t)
	baselines := map[string]*clientBaseline{normalizeDir(client): {
		Directory: client, File: "baseline.spf2", SourceHash: oldHash, AppliedHash: oldHash,
	}}
	state := emptyWeaponState(baselines, oldHash)
	workspace := weaponWorkspacePath(folder, 253011, oldHash)
	if err := os.MkdirAll(filepath.Dir(workspace), 0700); err != nil {
		t.Fatal(err)
	}
	workspaceData := []byte(`{"profile":"target"}`)
	if err := os.WriteFile(workspace, workspaceData, 0600); err != nil {
		t.Fatal(err)
	}
	if err := persistWeaponState(filepath.Join(folder, "settings.json"), &state); err != nil {
		t.Fatal(err)
	}
	if err := syncBaselineAfterImport(folder, client, ""); err != nil {
		t.Fatal(err)
	}
	var got weaponState
	raw, err := os.ReadFile(filepath.Join(folder, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.ActiveProfile != newHash || got.SourceHash != newHash {
		t.Fatalf("导入后 profile/hash 未对齐：profile=%q source=%q want=%q", got.ActiveProfile, got.SourceHash, newHash)
	}
	if got.AppliedHash != "" {
		t.Fatalf("无目标作者编辑时 applied hash 应为空，实际为 %q", got.AppliedHash)
	}
	newWorkspace := weaponWorkspacePath(folder, 253011, newHash)
	migrated, err := os.ReadFile(newWorkspace)
	if err != nil {
		t.Fatalf("目标 workspace 未迁移到新 profile：%v", err)
	}
	if !bytes.Equal(migrated, workspaceData) {
		t.Fatal("迁移后的 workspace 内容变化")
	}
	archivedRaw, err := os.ReadFile(weaponProfilePath(folder, oldHash))
	if err != nil {
		t.Fatalf("旧 profile archive 不存在：%v", err)
	}
	var archived weaponState
	if err = json.Unmarshal(archivedRaw, &archived); err != nil {
		t.Fatal(err)
	}
	if got := archived.Baselines[normalizeDir(client)].SourceHash; got != oldHash {
		t.Fatalf("旧 profile archive SourceHash = %q，want %q", got, oldHash)
	}
	if _, err = os.Stat(workspace); err != nil {
		t.Fatalf("旧 profile workspace 未保留：%v", err)
	}
}

func writeMergeWorkspaceFixture(t *testing.T, folder, client, id string, workspace mergeWorkspace, config []byte) {
	t.Helper()
	directory := filepath.Join(folder, id)
	if err := os.MkdirAll(filepath.Join(directory, "Data"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath(directory), config, 0600); err != nil {
		t.Fatal(err)
	}
	workspace.Client = client
	workspace.Hash = digest(config)
	encoded, err := json.Marshal(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(directory, "workspace.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
}

func mergeWorkspaceStageFixture(t *testing.T, withSettings bool) (*Admin, string, string, []byte) {
	t.Helper()
	admin := &Admin{Root: t.TempDir()}
	client := t.TempDir()
	base, _ := variantFixture(t, map[string]string{
		"item.txt":      "",
		"acteffect.xml": `<ActEffect />`,
		"comborule.xml": `<ComboRuleList />`,
	})
	if err := os.MkdirAll(filepath.Join(client, "Data", "UI"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath(client), base.data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(client, "Data", "UI", "fixture.png"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if withSettings {
		writeMergeWorkspaceBaseline(t, admin.Root, client, base.data)
	}
	fields := make([]string, 17)
	for i := range fields {
		fields[i] = "#"
	}
	fields[0], fields[1], fields[2], fields[3], fields[9] = "25", "253999", "6", "fixture", "fixture.png"
	manifest := mergeManifest{Format: mergeFormat, Version: 1, Weapons: []mergeWeapon{{
		ID: 253999, Name: "fixture", ItemRow: strings.Join(fields, "\t"), ItemactRow: "253999\tfixture\t2001001\t2001002",
	}}, Assets: []string{"Data/UI/fixture.png"}}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, data := range map[string][]byte{"manifest.json": encoded, "Data/UI/fixture.png": []byte("incoming")} {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = entry.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "merge.zip")
	if err := os.WriteFile(path, buffer.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return admin, client, path, base.data
}

func writeMergeWorkspaceBaseline(t *testing.T, root, client string, config []byte) []byte {
	t.Helper()
	folder := filepath.Join(root, "runtime-local", "weapon-config")
	if err := os.MkdirAll(folder, 0700); err != nil {
		t.Fatal(err)
	}
	hash := digest(config)
	entry := &clientBaseline{Directory: client, File: "baseline.spf2", SourceHash: hash, AppliedHash: hash}
	if err := os.WriteFile(entry.path(folder), config, 0600); err != nil {
		t.Fatal(err)
	}
	// 全局作者态属于其他客户端；目标身份必须从 Baselines 解析。
	state := emptyWeaponState(map[string]*clientBaseline{normalizeDir(client): entry}, "other-profile")
	state.SourceHash = "other-source"
	path := filepath.Join(folder, "settings.json")
	if err := persistWeaponState(path, &state); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func stageMergeWorkspaceFixture(t *testing.T, admin *Admin, client, path, workspace string, clientConfig *clientConfigRequest) map[string]any {
	t.Helper()
	request := Request{Operation: "weapon_merge_compare", SourcePath: path, MergeWorkspace: workspace, ClientConfig: clientConfig}
	value, err := admin.weaponMergeWorkspace(request, client)
	if err != nil {
		t.Fatal(err)
	}
	preview := value.(map[string]any)
	request.Operation = "weapon_merge_stage"
	request.Revision = preview["revision"].(string)
	request.ActiveProfile = preview["active_profile"].(string)
	request.SourceHash = preview["source_hash"].(string)
	request.MergeWeapons = []int{253999}
	value, err = admin.weaponMergeWorkspace(request, client)
	if err != nil {
		t.Fatal(err)
	}
	return value.(map[string]any)
}

func TestMergeWorkspaceRejectsOldProfileBeforeWriting(t *testing.T) {
	admin, client, path, config := mergeWorkspaceStageFixture(t, true)
	settingsPath := filepath.Join(admin.Root, "runtime-local", "weapon-config", "settings.json")
	settingsBefore, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	stage := stageMergeWorkspaceFixture(t, admin, client, path, "", nil)
	workspaceID := stage["workspace"].(string)
	folder := filepath.Join(admin.Root, "runtime-local", "weapon-merge-workspaces")
	meta, _, err := readMergeWorkspace(folder, workspaceID, client)
	if err != nil {
		t.Fatal(err)
	}
	if meta.ActiveProfile != digest(config) || meta.SourceHash != digest(config) || stage["active_profile"] != meta.ActiveProfile || stage["source_hash"] != meta.SourceHash || len(meta.Assets) != 1 {
		t.Fatalf("真实 stage 未绑定目标 baseline 身份和资源：%+v", meta)
	}
	if got, err := os.ReadFile(settingsPath); err != nil || !bytes.Equal(got, settingsBefore) {
		t.Fatalf("stage 提前修改 settings：%v", err)
	}
	// 保持客户端字节相同，仅切换目标 baseline：BaseHash 守卫无法代替身份守卫。
	newBaseline, err := parseArchive(config)
	if err != nil {
		t.Fatal(err)
	}
	newConfig, err := newBaseline.replace(map[string][]byte{"item.txt": []byte("\n")})
	if err != nil {
		t.Fatal(err)
	}
	settingsBefore = writeMergeWorkspaceBaseline(t, admin.Root, client, newConfig)
	for _, operation := range []string{"weapon_merge_stage", "weapon_merge_apply"} {
		if _, err = admin.weaponMergeWorkspace(Request{Operation: operation, MergeWorkspace: workspaceID, SourcePath: path, Revision: "old", MergeWeapons: []int{253999}, ActiveProfile: meta.ActiveProfile, SourceHash: meta.SourceHash}, client); err == nil || !strings.Contains(err.Error(), "配置档") {
			t.Fatalf("旧 profile %s 未拒绝：%v", operation, err)
		}
	}
	if _, err = admin.weaponMergeWorkspace(Request{Operation: "weapon_merge_save", MergeWorkspace: workspaceID, ActiveProfile: meta.ActiveProfile, SourceHash: meta.SourceHash, Path: filepath.Join(t.TempDir(), "snapshot.zip")}, client); err != nil {
		t.Fatalf("旧 profile 候选无法保存：%v", err)
	}
	clientAfter, err := os.ReadFile(configPath(client))
	if err != nil || !bytes.Equal(clientAfter, config) {
		t.Fatalf("拒绝旧 profile 后客户端被修改：%v", err)
	}
	settingsAfter, err := os.ReadFile(settingsPath)
	if err != nil || !bytes.Equal(settingsAfter, settingsBefore) {
		t.Fatalf("拒绝旧 profile 后 settings 被修改：%v", err)
	}
	if got, err := os.ReadFile(filepath.Join(client, "Data", "UI", "fixture.png")); err != nil || string(got) != "original" {
		t.Fatalf("拒绝旧 profile 后资源被修改：%v", err)
	}
	backups, err := filepath.Glob(filepath.Join(folder, "before-apply-*"))
	if err != nil || len(backups) != 0 {
		t.Fatalf("身份拒绝前创建了备份：%v %v", backups, err)
	}
}

func TestMergeWorkspaceLegacyIdentityUsesClientAndBaseHash(t *testing.T) {
	for _, withSettings := range []bool{false, true} {
		t.Run(fmt.Sprintf("settings-%t", withSettings), func(t *testing.T) {
			admin, client, path, original := mergeWorkspaceStageFixture(t, withSettings)
			folder := filepath.Join(admin.Root, "runtime-local", "weapon-merge-workspaces")
			workspaceID := "legacy-workspace"
			writeMergeWorkspaceFixture(t, folder, client, workspaceID, mergeWorkspace{BaseHash: digest(original)}, original)
			stage := stageMergeWorkspaceFixture(t, admin, client, path, workspaceID, nil)
			workspaceID = stage["workspace"].(string)
			meta, directory, err := readMergeWorkspace(folder, workspaceID, client)
			if err != nil {
				t.Fatal(err)
			}
			if meta.ActiveProfile != "" || meta.SourceHash != "" || stage["active_profile"] != "" || stage["source_hash"] != "" {
				t.Fatalf("旧 workspace 继续 stage 被补入当前身份：%+v", meta)
			}
			if _, err = admin.weaponMergeWorkspace(Request{Operation: "weapon_merge_apply", MergeWorkspace: workspaceID}, t.TempDir()); err == nil || !strings.Contains(err.Error(), "其他客户端") {
				t.Fatalf("旧 workspace Client 守卫失效：%v", err)
			}
			if err = os.WriteFile(configPath(client), append(append([]byte{}, original...), 0), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err = admin.weaponMergeWorkspace(Request{Operation: "weapon_merge_apply", MergeWorkspace: workspaceID}, client); err == nil || !strings.Contains(err.Error(), "已变化") {
				t.Fatalf("旧 workspace BaseHash 守卫失效：%v", err)
			}
			if err = os.WriteFile(configPath(client), original, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err = admin.weaponMergeWorkspace(Request{Operation: "weapon_merge_apply", MergeWorkspace: workspaceID}, client); err != nil {
				t.Fatalf("旧 workspace 未按 Client/BaseHash 兼容应用：%v", err)
			}
			want, err := os.ReadFile(configPath(directory))
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(configPath(client))
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("旧 workspace 应用结果错误：%v", err)
			}
		})
	}
}

func TestLegacyDirectMergeUsesStagedIdentityAndSync(t *testing.T) {
	admin, client, path, original := mergeWorkspaceStageFixture(t, true)
	previewAny, err := admin.weaponMergeWorkspace(Request{Operation: "weapon_merge_compare", SourcePath: path}, client)
	if err != nil {
		t.Fatal(err)
	}
	preview := previewAny.(map[string]any)
	request := Request{
		Operation:     "weapon_merge_import",
		SourcePath:    path,
		MergeWeapons:  []int{253999},
		Revision:      preview["revision"].(string),
		ActiveProfile: preview["active_profile"].(string),
		SourceHash:    preview["source_hash"].(string),
	}
	value, err := weaponHandle(request, client, nil, filepath.Join(admin.Root, "runtime-local", "weapon-config"))
	if err != nil {
		t.Fatal(err)
	}
	result := value.(map[string]any)
	if result["active_profile"] == preview["active_profile"] || result["source_hash"] == preview["source_hash"] {
		t.Fatalf("直接导入没有返回新的 profile 身份：%v", result)
	}
	got, err := os.ReadFile(configPath(client))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(got, original) {
		t.Fatal("直接导入没有修改隔离客户端")
	}
	settingsPath := filepath.Join(admin.Root, "runtime-local", "weapon-config", "settings.json")
	settings, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var state weaponState
	if err = json.Unmarshal(settings, &state); err != nil {
		t.Fatal(err)
	}
	if state.ActiveProfile != result["active_profile"] || state.SourceHash != result["source_hash"] {
		t.Fatalf("直接导入后作者态身份未同步：profile=%q source=%q applied=%q result=%v", state.ActiveProfile, state.SourceHash, state.AppliedHash, result)
	}
	// A fresh baseline deliberately has no applied_hash; the live bytes then
	// match source_hash. Exercise the actual guard rather than guessing hashes.
	if _, err = prepareBuffOnlyClient(state.baselineFor(client), filepath.Dir(settingsPath), &state); err != nil {
		t.Fatalf("直接导入后仍有基线死锁：%v", err)
	}
	if _, err = weaponHandle(Request{
		Operation:     "weapon_merge_import",
		SourcePath:    path,
		MergeWeapons:  []int{253999},
		Revision:      preview["revision"].(string),
		ActiveProfile: preview["active_profile"].(string),
		SourceHash:    preview["source_hash"].(string),
	}, client, nil, filepath.Join(admin.Root, "runtime-local", "weapon-config")); err == nil {
		t.Fatal("旧 profile 直接导入未拒绝")
	}
}

func TestLegacyDirectMergeRejectsStaleIdentityAndRevisionWithoutWrites(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Request)
		want   string
	}{
		{
			name: "profile",
			mutate: func(request *Request) {
				request.ActiveProfile = "stale-profile"
			},
			// validateWeaponProfileRequest: 配置档不匹配 → 「武器编辑上下文已切换」
			want: "武器编辑上下文已切换",
		},
		{
			name: "source-hash",
			mutate: func(request *Request) {
				request.SourceHash = "stale-source-hash"
			},
			want: "SPF2 已切换",
		},
		{
			name: "revision",
			mutate: func(request *Request) {
				request.Revision = "stale-revision"
			},
			want: "已变化",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			admin, client, path, _ := mergeWorkspaceStageFixture(t, true)
			previewAny, err := admin.weaponMergeWorkspace(Request{Operation: "weapon_merge_compare", SourcePath: path}, client)
			if err != nil {
				t.Fatal(err)
			}
			preview := previewAny.(map[string]any)
			request := Request{
				Operation:     "weapon_merge_import",
				SourcePath:    path,
				MergeWeapons:  []int{253999},
				Revision:      preview["revision"].(string),
				ActiveProfile: preview["active_profile"].(string),
				SourceHash:    preview["source_hash"].(string),
			}
			beforeAdmin := snapshotWeaponProfileFiles(t, admin.Root)
			beforeClient := snapshotWeaponProfileFiles(t, client)
			test.mutate(&request)
			if _, err = weaponHandle(request, client, nil, filepath.Join(admin.Root, "runtime-local", "weapon-config")); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("stale %s 未按预期拒绝：%v", test.name, err)
			}
			assertWeaponProfileFilesUnchanged(t, admin.Root, beforeAdmin)
			assertWeaponProfileFilesUnchanged(t, client, beforeClient)
			// 取 revision 的前置 compare 已经建过这个目录，所以这里断言的是
			//「拒绝时没有新增 workspace 条目」，而不是「目录不存在」。
			entries, err := os.ReadDir(filepath.Join(admin.Root, "runtime-local", "weapon-merge-workspaces"))
			if err != nil && !os.IsNotExist(err) {
				t.Fatalf("stale %s 读取 workspace 目录失败：%v", test.name, err)
			}
			if len(entries) > 0 {
				names := make([]string, 0, len(entries))
				for _, entry := range entries {
					names = append(names, entry.Name())
				}
				t.Fatalf("stale %s 拒绝时仍创建了 workspace：%v", test.name, names)
			}
			backups, err := filepath.Glob(filepath.Join(client, "before-apply-*"))
			if err != nil || len(backups) != 0 {
				t.Fatalf("stale %s 拒绝前创建了客户端备份：%v %v", test.name, backups, err)
			}
		})
	}
}

func TestMergeWorkspaceApplyReturnsNewIdentity(t *testing.T) {
	for _, withSettings := range []bool{false, true} {
		t.Run(fmt.Sprintf("settings-%t", withSettings), func(t *testing.T) {
			admin, client, path, original := mergeWorkspaceStageFixture(t, withSettings)
			stage := stageMergeWorkspaceFixture(t, admin, client, path, "", nil)
			if stage["active_profile"] != digest(original) || stage["source_hash"] != digest(original) {
				t.Fatalf("新 workspace 未绑定身份：%v", stage)
			}
			request := Request{Operation: "weapon_merge_apply", MergeWorkspace: stage["workspace"].(string)}
			for _, identity := range []Request{{ActiveProfile: "wrong"}, {SourceHash: "wrong"}} {
				request.ActiveProfile, request.SourceHash = identity.ActiveProfile, identity.SourceHash
				if _, err := admin.weaponMergeWorkspace(request, client); err == nil {
					t.Fatalf("错误请求身份未拒绝：%+v", identity)
				}
			}
			folder := filepath.Join(admin.Root, "runtime-local", "weapon-merge-workspaces")
			backups, err := filepath.Glob(filepath.Join(folder, "before-apply-*"))
			if err != nil || len(backups) != 0 {
				t.Fatalf("错误请求身份创建了备份：%v %v", backups, err)
			}
			request.ActiveProfile, request.SourceHash = digest(original), digest(original)
			value, err := admin.weaponMergeWorkspace(request, client)
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(configPath(client))
			if err != nil {
				t.Fatal(err)
			}
			result := value.(map[string]any)
			if result["active_profile"] != digest(got) || result["source_hash"] != digest(got) || digest(got) == digest(original) {
				t.Fatalf("apply 未返回新身份：%v", result)
			}
			if !withSettings {
				if _, err := os.Stat(filepath.Join(admin.Root, "runtime-local", "weapon-config", "settings.json")); !os.IsNotExist(err) {
					t.Fatalf("无 settings 的流程提前创建了作者态：%v", err)
				}
			}
		})
	}
}

func TestMergeWorkspaceExternalBaseAndResourceRoot(t *testing.T) {
	admin, client, path, original := mergeWorkspaceStageFixture(t, true)
	settingsPath := filepath.Join(admin.Root, "runtime-local", "weapon-config", "settings.json")
	settingsBefore, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(t.TempDir(), "external.spf2")
	if err = os.WriteFile(base, original, 0600); err != nil {
		t.Fatal(err)
	}
	resourceRoot := t.TempDir()
	clientConfig := &clientConfigRequest{Base: base, ResourceRoot: resourceRoot}
	stage := stageMergeWorkspaceFixture(t, admin, client, path, "", clientConfig)
	if stage["active_profile"] != "" || stage["source_hash"] != "" {
		t.Fatalf("没有目标客户端基线的外部资源目录被伪造身份：%v", stage)
	}
	if _, err = admin.weaponMergeWorkspace(Request{Operation: "weapon_merge_save", MergeWorkspace: stage["workspace"].(string), ClientConfig: clientConfig, Path: filepath.Join(t.TempDir(), "snapshot.zip")}, client); err != nil {
		t.Fatalf("外部参照候选不能保存：%v", err)
	}
	if got, err := os.ReadFile(settingsPath); err != nil || !bytes.Equal(got, settingsBefore) {
		t.Fatalf("外部参照流程修改 settings：%v", err)
	}
	if got, err := os.ReadFile(configPath(client)); err != nil || !bytes.Equal(got, original) {
		t.Fatalf("外部参照流程修改客户端：%v", err)
	}
	if _, err := os.Stat(configPath(resourceRoot)); !os.IsNotExist(err) {
		t.Fatalf("外部资源目录写入了配置：%v", err)
	}
}

func TestMigrateMergeImportWorkspacesRejectsConflictAndKeepsSource(t *testing.T) {
	folder := t.TempDir()
	oldProfile, newProfile := "old-profile", "new-profile"
	samePath := weaponWorkspacePath(folder, 253010, oldProfile)
	if err := os.MkdirAll(filepath.Dir(samePath), 0700); err != nil {
		t.Fatal(err)
	}
	sameContent := []byte(`{"profile":"same"}`)
	if err := os.WriteFile(samePath, sameContent, 0600); err != nil {
		t.Fatal(err)
	}
	if err := migrateMergeImportWorkspaces(folder, oldProfile, oldProfile); err != nil {
		t.Fatalf("同 hash 不应执行 workspace 迁移：%v", err)
	}
	if got, err := os.ReadFile(samePath); err != nil || !bytes.Equal(got, sameContent) {
		t.Fatalf("同 hash workspace 被错误改写：%v", err)
	}
	oldPath := weaponWorkspacePath(folder, 253011, oldProfile)
	if err := os.MkdirAll(filepath.Dir(oldPath), 0700); err != nil {
		t.Fatal(err)
	}
	content := []byte(`{"profile":"old"}`)
	if err := os.WriteFile(oldPath, content, 0600); err != nil {
		t.Fatal(err)
	}
	if err := migrateMergeImportWorkspaces(folder, oldProfile, newProfile); err != nil {
		t.Fatal(err)
	}
	migratedPath := weaponWorkspacePath(folder, 253011, newProfile)
	migrated, err := os.ReadFile(migratedPath)
	if err != nil || !bytes.Equal(migrated, content) {
		t.Fatalf("workspace 未复制到新 profile：%v", err)
	}
	if source, err := os.ReadFile(oldPath); err != nil || !bytes.Equal(source, content) {
		t.Fatalf("旧 profile workspace 未保留：%v", err)
	}
	if err := migrateMergeImportWorkspaces(folder, oldProfile, newProfile); err != nil {
		t.Fatalf("相同内容 workspace 重复迁移失败：%v", err)
	}
	if err := os.WriteFile(migratedPath, []byte(`{"profile":"different"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := migrateMergeImportWorkspaces(folder, oldProfile, newProfile); err == nil {
		t.Fatal("新 profile 已有不同 workspace 时仍允许覆盖")
	}
	if source, err := os.ReadFile(oldPath); err != nil || !bytes.Equal(source, content) {
		t.Fatalf("冲突后旧 profile workspace 被修改：%v", err)
	}
}
