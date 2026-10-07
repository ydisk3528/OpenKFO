package desktop

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestWeaponProfileSwitchArchivesAndRestoresAuthorState(t *testing.T) {
	folder := t.TempDir()
	statePath := filepath.Join(folder, "settings.json")
	baselines := map[string]*clientBaseline{}
	state := emptyWeaponState(baselines, "old-profile")
	state.SourceHash = "old-source"
	state.AppliedHash = "old-applied"
	state.Created["253011"] = Blueprint{ID: 253011, Name: "old weapon"}
	state.HitProperties["910000105"] = HitProperty{
		ID: "910000105", OwnerWeapon: "253011", TemplateID: "900000525",
	}

	oldCopy := state
	newEntry := &clientBaseline{Directory: t.TempDir(), File: "baseline.spf2", SourceHash: "new-source"}
	if _, err := resetWeaponProfileAfterRebase(folder, statePath, &state, newEntry); err != nil {
		t.Fatal(err)
	}
	if state.ActiveProfile != "new-source" {
		t.Fatalf("active profile = %q, want new-source", state.ActiveProfile)
	}
	if len(state.Created) != 0 || len(state.HitProperties) != 0 {
		t.Fatalf("new SPF2 inherited old author state: created=%v hit=%v", state.Created, state.HitProperties)
	}
	if _, err := os.Stat(weaponProfilePath(folder, "old-profile")); err != nil {
		t.Fatalf("old author profile was not archived: %v", err)
	}
	var archived weaponState
	raw, err := os.ReadFile(weaponProfilePath(folder, "old-profile"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &archived); err != nil {
		t.Fatal(err)
	}
	if archived.Created["253011"].Name != oldCopy.Created["253011"].Name {
		t.Fatalf("archived author state changed: %+v", archived.Created)
	}

	if err = os.WriteFile(statePath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	loaded := emptyWeaponState(baselines, "old-profile")
	if err = json.Unmarshal(raw, &loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.HitProperties["910000105"].TemplateID != "900000525" {
		t.Fatal("switching back did not restore the old branch template")
	}
}

func TestWeaponProfileRPCRejectsWithoutWritingAndReturnsIdentity(t *testing.T) {
	a, items := variantFixture(t, map[string]string{"acteffect.xml": `<ActEffect />`, "comborule.xml": `<ComboRule />`})
	client, folder := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(client, "Data"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath(client), a.data, 0600); err != nil {
		t.Fatal(err)
	}
	state := emptyWeaponState(nil, "")
	entry := state.baselineFor(client)
	if err := ensureBaseline(entry, folder, false); err != nil {
		t.Fatal(err)
	}
	state.ActiveProfile = entry.SourceHash
	state.SourceHash = entry.SourceHash
	path := filepath.Join(folder, "settings.json")
	if err := persistWeaponState(path, &state); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	payload := map[string]any{"weapon": map[string]any{"id": 253450}}
	for _, request := range []Request{
		{Operation: "weapon_workspace_save", Weapon: 253450, Workspace: payload},
		{Operation: "weapon_workspace_save", Weapon: 253450, Workspace: payload, ActiveProfile: "stale", SourceHash: "stale"},
		{Operation: "weapon_buff_save", ActiveProfile: "stale", SourceHash: "stale"},
		{Operation: "weapon_effects_apply", ActiveProfile: "stale", SourceHash: "stale"},
	} {
		if _, err := weaponHandle(request, client, items, folder); err == nil {
			t.Fatalf("accepted stale/missing context: %s", request.Operation)
		}
		after, _ := os.ReadFile(path)
		if !bytes.Equal(before, after) {
			t.Fatalf("rejected %s wrote settings", request.Operation)
		}
	}
	for _, operation := range []string{"weapon_workspace_status", "weapon_detail", "weapon_combo_chain", "weapon_combo_rule", "weapon_effect_view"} {
		result, err := weaponHandle(Request{Operation: operation, Weapon: 253450}, client, items, folder)
		if err != nil {
			t.Fatalf("%s: %v", operation, err)
		}
		response := result.(map[string]any)
		if response["active_profile"] != entry.SourceHash || response["source_hash"] != entry.SourceHash {
			t.Fatalf("%s missing identity: %v", operation, response)
		}
		after, _ := os.ReadFile(path)
		if !bytes.Equal(before, after) {
			t.Fatalf("read %s wrote settings", operation)
		}
	}
	request := Request{Operation: "weapon_workspace_save", Weapon: 253450, Workspace: payload, ActiveProfile: entry.SourceHash, SourceHash: entry.SourceHash}
	if _, err := weaponHandle(request, client, items, folder); err != nil {
		t.Fatal(err)
	}
	request.Operation = "weapon_workspace_load"
	result, err := weaponHandle(request, client, items, folder)
	if err != nil || result.(map[string]any)["exists"] != true {
		t.Fatalf("workspace roundtrip: %v", err)
	}
}

// 快照包含目录、内容和修改时间，既检查新增文件，也检查已有文件被重写。
type weaponProfileFileSnapshot struct {
	Directory bool
	Data      string
	Modified  int64
}

func snapshotWeaponProfileFiles(t *testing.T, root string) map[string]weaponProfileFileSnapshot {
	t.Helper()
	files := map[string]weaponProfileFileSnapshot{}
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		entry := weaponProfileFileSnapshot{Directory: info.IsDir()}
		if !info.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			entry.Data = string(data)
			entry.Modified = info.ModTime().UnixNano()
		}
		files[relative] = entry
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return files
}

func assertWeaponProfileFilesUnchanged(t *testing.T, root string, before map[string]weaponProfileFileSnapshot) {
	t.Helper()
	if after := snapshotWeaponProfileFiles(t, root); !reflect.DeepEqual(before, after) {
		t.Fatalf("业务文件被改写：原路径 %v，现路径 %v", reflect.ValueOf(before).MapKeys(), reflect.ValueOf(after).MapKeys())
	}
}

func writeWeaponProfileClient(t *testing.T, client string, source *archive) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(client, "Data"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath(client), source.data, 0600); err != nil {
		t.Fatal(err)
	}
}

func readWeaponProfileState(t *testing.T, path string) weaponState {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state weaponState
	if err = json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestWeaponProfileRPCMissingBaselineRejectsWithoutBusinessWrites(t *testing.T) {
	source, items := variantFixture(t, nil)
	for _, initial := range []string{"empty", "legacy", "other-profile", "missing-snapshot"} {
		t.Run(initial, func(t *testing.T) {
			root := t.TempDir()
			client, folder := filepath.Join(root, "client"), filepath.Join(root, "weapon-config")
			writeWeaponProfileClient(t, client, source)
			if err := os.MkdirAll(folder, 0700); err != nil {
				t.Fatal(err)
			}
			if initial != "empty" {
				state := emptyWeaponState(nil, "")
				state.SourceHash = "obsolete-source"
				state.AppliedHash = "obsolete-applied"
				state.UStates["400"] = UStateEdit{Action: "delete"}
				if initial == "other-profile" || initial == "missing-snapshot" {
					state.ActiveProfile = "obsolete-source"
				}
				if initial == "missing-snapshot" {
					state.baselineFor(client)
				}
				if err := persistWeaponState(filepath.Join(folder, "settings.json"), &state); err != nil {
					t.Fatal(err)
				}
				if _, err := saveWeaponWorkspace(folder, 253450, map[string]any{"weapon": map[string]any{"id": 253450}}); err != nil {
					t.Fatal(err)
				}
				archived := emptyWeaponState(nil, digest(source.data))
				if err := archiveWeaponProfile(folder, archived.ActiveProfile, &archived); err != nil {
					t.Fatal(err)
				}
			}
			before := snapshotWeaponProfileFiles(t, root)
			payload := map[string]any{"weapon": map[string]any{"id": 253450}}
			requests := []Request{
				{Operation: "weapon_workspace_save", Weapon: 253450, Workspace: payload},
				{Operation: "weapon_workspace_delete", Weapon: 253450},
				{Operation: "weapon_apply", Weapon: 253450, Workspace: payload},
				{Operation: "weapon_workspace_save", Weapon: 253450, Workspace: payload, ActiveProfile: digest(source.data)},
				{Operation: "weapon_workspace_save", Weapon: 253450, Workspace: payload, SourceHash: digest(source.data)},
				{Operation: "weapon_workspace_save", Weapon: 253450, Workspace: payload, ActiveProfile: "obsolete-source", SourceHash: "obsolete-source"},
				{Operation: "weapon_buff_save", ActiveProfile: "obsolete-source", SourceHash: "obsolete-source"},
				{Operation: "weapon_effects_apply", ActiveProfile: "obsolete-source", SourceHash: "obsolete-source"},
			}
			for _, request := range requests {
				if _, err := weaponHandle(request, client, items, folder); err == nil || !(strings.Contains(err.Error(), "身份") || strings.Contains(err.Error(), "切换")) {
					t.Fatalf("%s 未因身份拒绝：%v", request.Operation, err)
				}
				assertWeaponProfileFilesUnchanged(t, root, before)
			}
			// 缺失快照中的旧 hash 不能阻止使用真实 live hash 的合法采集。
			result, err := weaponHandle(Request{Operation: "weapon_workspace_save", Weapon: 253450, Workspace: payload, ActiveProfile: digest(source.data), SourceHash: digest(source.data)}, client, items, folder)
			if err != nil {
				t.Fatal(err)
			}
			if result.(map[string]any)["source_hash"] != digest(source.data) {
				t.Fatalf("采集身份错误：%v", result)
			}
			state := readWeaponProfileState(t, filepath.Join(folder, "settings.json"))
			entry := state.Baselines[normalizeDir(client)]
			captured, err := os.ReadFile(entry.path(folder))
			if err != nil || !bytes.Equal(captured, source.data) {
				t.Fatalf("通过身份后未采集真实基线：%v", err)
			}
		})
	}
}

func TestWeaponProfileRPCFirstReadDoesNotPersist(t *testing.T) {
	source, items := variantFixture(t, map[string]string{"acteffect.xml": `<ActEffect />`, "comborule.xml": `<ComboRule />`})
	root := t.TempDir()
	client, folder := filepath.Join(root, "client"), filepath.Join(root, "weapon-config")
	writeWeaponProfileClient(t, client, source)
	if err := os.MkdirAll(folder, 0700); err != nil {
		t.Fatal(err)
	}
	packagePath := filepath.Join(root, "merge-preview.zip")
	if _, _, err := writeMergeZip(packagePath, client, &mergeManifest{Format: mergeFormat, Version: 1, Weapons: []mergeWeapon{{ID: 253451, Name: "预览武器"}}}, nil); err != nil {
		t.Fatal(err)
	}
	before := snapshotWeaponProfileFiles(t, root)
	for _, operation := range []string{"weapon_workspace_status", "weapon_workspace_load", "weapon_list", "weapon_detail", "weapon_combo_chain", "weapon_combo_rule", "weapon_effect_view", "weapon_buff_catalog", "weapon_merge_preview", "weapon_merge_packages"} {
		t.Run(operation, func(t *testing.T) {
			if !readOnlyWeaponOperation(operation) {
				t.Fatal("查询没有纳入只读")
			}
			result, err := weaponHandle(Request{Operation: operation, Weapon: 253450, SourcePath: packagePath}, client, items, folder)
			if err != nil {
				t.Fatal(err)
			}
			if response, ok := result.(map[string]any); ok {
				if response["active_profile"] != digest(source.data) || response["source_hash"] != digest(source.data) {
					t.Fatalf("初次读取没有返回真实身份：%v", response)
				}
			} else if preview, ok := result.(mergePreview); !ok || len(preview.New) != 1 {
				t.Fatalf("合并预览未成功：%v", result)
			}
			assertWeaponProfileFilesUnchanged(t, root, before)
		})
	}
}

func TestWeaponProfileRPCExistingBaselineHashGuard(t *testing.T) {
	source, items := variantFixture(t, nil)
	other, _ := variantFixture(t, map[string]string{"ustate.xml": `<UState><!-- 不同基线 --></UState>`})
	root := t.TempDir()
	client, folder := filepath.Join(root, "client"), filepath.Join(root, "weapon-config")
	writeWeaponProfileClient(t, client, source)
	if err := os.MkdirAll(folder, 0700); err != nil {
		t.Fatal(err)
	}
	state := emptyWeaponState(nil, "")
	entry, _, err := activateWeaponProfile(folder, filepath.Join(folder, "settings.json"), &state, client, true)
	if err != nil {
		t.Fatal(err)
	}
	writeWeaponProfileClient(t, client, other)
	beforeLiveRead := snapshotWeaponProfileFiles(t, root)
	result, err := weaponHandle(Request{Operation: "weapon_detail", Weapon: 253450}, client, items, folder)
	if err != nil || result.(map[string]any)["source_hash"] != digest(source.data) {
		t.Fatalf("现有基线身份错误地改用了 live hash：%v %v", result, err)
	}
	assertWeaponProfileFilesUnchanged(t, root, beforeLiveRead)
	if err = os.WriteFile(entry.path(folder), other.data, 0600); err != nil {
		t.Fatal(err)
	}
	before := snapshotWeaponProfileFiles(t, root)
	if _, err = weaponHandle(Request{Operation: "weapon_detail", Weapon: 253450}, client, items, folder); err == nil || !strings.Contains(err.Error(), "基线备份已变化") {
		t.Fatalf("现有基线 hash 校验失效：%v", err)
	}
	assertWeaponProfileFilesUnchanged(t, root, before)
}

func TestWeaponProfileRPCSwitchRestoresAuthorStateAndSeparateWorkspaces(t *testing.T) {
	a, items := variantFixture(t, map[string]string{"ustate.xml": `<UState><Data type="1"></Data></UState>`})
	b, _ := variantFixture(t, map[string]string{"ustate.xml": `<UState><!-- 配置 B --><Data type="1"></Data></UState>`})
	root := t.TempDir()
	clientA, clientB, folder := filepath.Join(root, "client-a"), filepath.Join(root, "client-b"), filepath.Join(root, "weapon-config")
	writeWeaponProfileClient(t, clientA, a)
	writeWeaponProfileClient(t, clientB, b)
	statePath := filepath.Join(folder, "settings.json")
	for index, target := range []struct {
		client string
		source *archive
		label  string
	}{{clientA, a, "A"}, {clientB, b, "B"}, {clientA, a, "A"}, {clientB, b, "B"}} {
		hash := digest(target.source.data)
		request := Request{Operation: "weapon_workspace_save", Weapon: 253450, Workspace: map[string]any{"weapon": map[string]any{"id": 253450}, "marker": target.label}, ActiveProfile: hash, SourceHash: hash}
		if index >= 2 {
			before := snapshotWeaponProfileFiles(t, root)
			result, err := weaponHandle(Request{Operation: "weapon_workspace_load", Weapon: 253450, ActiveProfile: hash, SourceHash: hash}, target.client, items, folder)
			if err != nil {
				t.Fatal(err)
			}
			response := result.(map[string]any)
			if response["exists"] != true || response["payload"].(map[string]any)["marker"] != target.label {
				t.Fatalf("%s 工作区串档：%v", target.label, result)
			}
			catalog, err := weaponHandle(Request{Operation: "weapon_buff_catalog"}, target.client, items, folder)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, row := range catalog.(map[string]any)["buffs"].([]buffRow) {
				if row.Type == "400" && row.Name == target.label {
					found = true
				}
			}
			if !found {
				t.Fatalf("%s 只读回切未恢复作者状态：%v", target.label, catalog)
			}
			assertWeaponProfileFilesUnchanged(t, root, before)
		}
		if _, err := weaponHandle(request, target.client, items, folder); err != nil {
			t.Fatal(err)
		}
		state := readWeaponProfileState(t, statePath)
		if state.ActiveProfile != hash {
			t.Fatalf("%s 激活身份错误：%s", target.label, state.ActiveProfile)
		}
		if index >= 2 {
			if state.UStates["400"].Name != target.label {
				t.Fatalf("%s 持久化回切丢失作者状态：%v", target.label, state.UStates)
			}
		} else {
			if len(state.UStates) != 0 {
				t.Fatalf("新 profile %s 继承了其它配置：%v", target.label, state.UStates)
			}
			// 前端 Buff 还没有身份字段，继续验证无身份保存兼容。
			if _, err := weaponHandle(Request{Operation: "weapon_buff_save", Key: "400", UState: &UStateEdit{Name: target.label, Text: `<Data type="400"></Data>`}}, target.client, items, folder); err != nil {
				t.Fatalf("无身份 Buff 保存失败：%v", err)
			}
		}
	}
	for _, source := range []struct {
		client string
		data   []byte
	}{{clientA, a.data}, {clientB, b.data}} {
		live, err := os.ReadFile(configPath(source.client))
		if err != nil || !bytes.Equal(live, source.data) {
			t.Fatalf("profile 切换改写了客户端：%v", err)
		}
	}
}

func TestWeaponProfileRPCSameHashRebaseClearsAuthorState(t *testing.T) {
	source, items := variantFixture(t, map[string]string{"ustate.xml": `<UState><Data type="1"></Data></UState>`})
	root := t.TempDir()
	client, folder := filepath.Join(root, "client"), filepath.Join(root, "weapon-config")
	writeWeaponProfileClient(t, client, source)
	if _, err := weaponHandle(Request{Operation: "weapon_buff_save", Key: "400", UState: &UStateEdit{Name: "旧编辑", Text: `<Data type="400"></Data>`}}, client, items, folder); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(folder, "settings.json")
	before := readWeaponProfileState(t, statePath)
	result, err := weaponHandle(Request{Operation: "weapon_client_rebase", ActiveProfile: digest(source.data), SourceHash: digest(source.data)}, client, items, folder)
	if err != nil {
		t.Fatal(err)
	}
	after := readWeaponProfileState(t, statePath)
	if after.ActiveProfile != before.ActiveProfile || after.SourceHash != digest(source.data) || after.AppliedHash != "" || hasWeaponAuthorState(&after) {
		t.Fatalf("同 hash 显式 rebase 没有清空编辑集：%+v", after)
	}
	if result.(map[string]any)["active_profile"] != digest(source.data) || result.(map[string]any)["source_hash"] != digest(source.data) {
		t.Fatalf("rebase 返回身份错误：%v", result)
	}
	archived := readWeaponProfileState(t, weaponProfilePath(folder, before.ActiveProfile))
	if !reflect.DeepEqual(archived.UStates, before.UStates) {
		t.Fatal("同 hash rebase 没有归档旧编辑集")
	}
	baseline := after.Baselines[normalizeDir(client)].path(folder)
	backups, err := filepath.Glob(baseline + ".*.bak")
	if err != nil || len(backups) != 1 {
		t.Fatalf("旧基线没有备份：%v %v", backups, err)
	}
	live, err := os.ReadFile(configPath(client))
	if err != nil || !bytes.Equal(live, source.data) {
		t.Fatalf("rebase 改写了 live config：%v", err)
	}
	beforeRead := snapshotWeaponProfileFiles(t, root)
	if _, err = weaponHandle(Request{Operation: "weapon_buff_catalog"}, client, items, folder); err != nil {
		t.Fatal(err)
	}
	assertWeaponProfileFilesUnchanged(t, root, beforeRead)
	if state := readWeaponProfileState(t, statePath); hasWeaponAuthorState(&state) {
		t.Fatal("同 hash rebase 后读取复活了归档编辑集")
	}
}

func TestWeaponWorkspacePathIncludesProfile(t *testing.T) {
	oldPath := weaponWorkspacePath("C:/plans", 253011, "old-source")
	newPath := weaponWorkspacePath("C:/plans", 253011, "new-source")
	if oldPath == newPath || filepath.Base(oldPath) != filepath.Base(newPath) {
		t.Fatalf("workspace paths are not profile isolated: %q %q", oldPath, newPath)
	}
	if filepath.Base(filepath.Dir(oldPath)) != "old-source" || filepath.Base(filepath.Dir(newPath)) != "new-source" {
		t.Fatalf("workspace profile directories are wrong: %q %q", oldPath, newPath)
	}
}

func TestWeaponProfileRequestRejectsStaleWriteContext(t *testing.T) {
	request := Request{ActiveProfile: "old-profile", SourceHash: "old-source"}
	if err := validateWeaponProfileRequest(request, "new-profile", "new-source", true); err == nil {
		t.Fatal("stale profile write was accepted")
	}
	if err := validateWeaponProfileRequest(Request{}, "new-profile", "new-source", true); err == nil {
		t.Fatal("profile-less write was accepted")
	}
	if err := validateWeaponProfileRequest(Request{}, "new-profile", "new-source", false); err != nil {
		t.Fatalf("legacy read request was rejected: %v", err)
	}
}
