package desktop

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// currentBlock must find a block by its id against the file's *current* text.
// Once an earlier rewrite has replaced the pristine body, matching the text
// captured by inspect() would come up empty.
func TestCurrentBlockMatchesByID(t *testing.T) {
	file := "<AnmInfo>\n" +
		`<AnmDesc id="7"><Node skillproid="100"/></AnmDesc>` + "\n" +
		`<AnmDesc id="8"><Node skillproid="200"/></AnmDesc>` + "\n" +
		"</AnmInfo>"
	rewritten := strings.Replace(file, `skillproid="100"`, `skillproid="999"`, 1)
	block, ok := currentBlock(rewritten, "7")
	if !ok {
		t.Fatal("按 id 应能在改写后的内容里找到块 7")
	}
	if !strings.Contains(block, `skillproid="999"`) {
		t.Fatalf("应返回块 7 的当前内容，得到 %q", block)
	}
	if _, ok := currentBlock(rewritten, "9"); ok {
		t.Fatal("不存在的 id 不应命中")
	}
}

func TestCurrentConditionalBlockNormalizesLeadingZeroID(t *testing.T) {
	file := `<AnmInfo><AnmDesc id="087"><Anm id="1" skillproid="910000105"/></AnmDesc><AnmDesc id="087"><Condition><Ustate id="435"/></Condition><Anm id="1" skillproid="910000105"/></AnmDesc></AnmInfo>`
	block, ok := currentConditionalBlock(file, "87", "435")
	if !ok {
		t.Fatal("动作块 id=087 应能按规范化 id=87 定位条件分支")
	}
	if !strings.Contains(block, `<Ustate id="435"`) {
		t.Fatalf("定位到的不是目标条件块：%q", block)
	}
}

// Two states of one weapon may remap onto the very same action. applyRemaps
// then rewrites that animation block twice in a single pass, and it edits the
// copy cached by the first rewrite — so the pristine block text inspect()
// captured is gone by the second visit. That used to abort the whole catalogue
// with 动作定义无法唯一替换, which surfaced in the GM as the orange banner
// "状态重映射暂不可用" with the editor stuck on the pre-remap structure.
func TestApplyRemapsNilRemapIsIgnored(t *testing.T) {
	root := os.Getenv("OPENKFO_TEST_RUNTIME")
	if root == "" {
		abs, err := filepath.Abs("../../../../runtime-local")
		if err != nil {
			t.Fatal(err)
		}
		root = abs
	}
	source, err := loadArchive(filepath.Join(root, "weapon-config/original.spf2"))
	if err != nil {
		t.Skip("runtime-local 固定装置不可用")
	}
	items, err := catalog(filepath.Join(root, "client"), false, false)
	if err != nil {
		t.Skip("runtime-local/client 不可用: " + err.Error())
	}
	text, err := source.text("itemact.txt")
	if err != nil {
		t.Fatal(err)
	}
	rows := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(rows) < 2 {
		t.Skip("固定装置动作表为空")
	}
	weaponKey := strings.Split(rows[1], "\t")[0]
	remapped, err := applyRemaps(source, &weaponState{Remaps: map[string]map[int]*StageRemap{weaponKey: {2011: nil}}}, items)
	if err != nil {
		t.Fatalf("nil remap 不应导致失败：%v", err)
	}
	if !bytes.Equal(remapped.data, source.data) {
		t.Fatal("nil remap 不应修改归档")
	}
}

func TestApplyRemapsRejectsNativePropertyOverwrite(t *testing.T) {
	root := os.Getenv("OPENKFO_TEST_RUNTIME")
	if root == "" {
		abs, err := filepath.Abs("../../../../runtime-local")
		if err != nil {
			t.Fatal(err)
		}
		root = abs
	}
	source, err := loadArchive(filepath.Join(root, "weapon-config/original.spf2"))
	if err != nil {
		t.Skip("runtime-local 固定装置不可用")
	}
	items, err := catalog(filepath.Join(root, "client"), false, false)
	if err != nil {
		t.Skip("runtime-local/client 不可用: " + err.Error())
	}
	info, err := inspect(source, items)
	if err != nil {
		t.Fatal(err)
	}
	id := ""
	for candidate := range info.properties {
		id = candidate
		break
	}
	if id == "" {
		t.Skip("固定装置没有命中属性")
	}
	_, err = applyRemaps(source, &weaponState{ExtraProperties: map[string]ExtraProperty{id: {Template: id}}}, items)
	if err == nil || !strings.Contains(err.Error(), "覆盖") {
		t.Fatalf("覆盖原生属性必须失败，实际：%v", err)
	}
}

func TestExtraPropertyOwners(t *testing.T) {
	for _, test := range []struct {
		name      string
		extra     ExtraProperty
		remaps    map[string]map[int]*StageRemap
		wantError bool
	}{
		{"旧登记同武器", ExtraProperty{Template: "100"}, map[string]map[int]*StageRemap{"253450": {2011: {PropertyID: "800000001"}, 2012: {PropertyID: "800000001"}}}, false},
		{"跨武器", ExtraProperty{Template: "100"}, map[string]map[int]*StageRemap{"253450": {2011: {PropertyID: "800000001"}}, "253451": {2011: {PropertyID: "800000001"}}}, true},
		{"归属不可覆盖", ExtraProperty{Template: "100", OwnerWeapon: "253450"}, map[string]map[int]*StageRemap{"253451": {2011: {PropertyID: "800000001"}}}, true},
		{"未引用", ExtraProperty{Template: "100", OwnerWeapon: "253450"}, nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := &weaponState{ExtraProperties: map[string]ExtraProperty{"800000001": test.extra}, Remaps: test.remaps}
			_, err := extraPropertyOwners(state)
			if (err != nil) != test.wantError {
				t.Fatalf("归属校验错误：%v", err)
			}
		})
	}
}

func TestApplyRemapsExtraPropertyBaselineRoundTrip(t *testing.T) {
	client := os.Getenv("OPENKFO_WEAPON_TEST_CLIENT")
	if client == "" {
		t.Skip("设置 OPENKFO_WEAPON_TEST_CLIENT 后执行真实客户端只读测试")
	}
	original, err := os.ReadFile(configPath(client))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		current, err := os.ReadFile(configPath(client))
		if err != nil || !bytes.Equal(original, current) {
			t.Errorf("真实客户端被改写：%v", err)
		}
	})
	source, err := parseArchive(original)
	if err != nil {
		t.Fatal(err)
	}
	items, err := catalog(client, false, false)
	if err != nil {
		t.Fatal(err)
	}
	info, err := inspect(source, items)
	if err != nil {
		t.Fatal(err)
	}
	template := "1100510"
	if _, ok := info.propertyNode(template); !ok {
		t.Fatal("夹具缺少模板 1100510")
	}
	id := freshPropertyID(info)
	state := &weaponState{ExtraProperties: map[string]ExtraProperty{id: {Template: template, OwnerWeapon: "253450"}}}
	first, err := applyRemaps(source, state, items)
	if err != nil {
		t.Fatal(err)
	}
	second, err := applyRemaps(first, state, items)
	if err != nil {
		t.Fatalf("吸收作者节点后应幂等：%v", err)
	}
	if !bytes.Equal(first.data, second.data) {
		t.Fatal("幂等复用不应追加归档条目")
	}
	after, err := inspect(second, items)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.properties[id]) != 1 {
		t.Fatal("新增属性被重复登记")
	}
	// 历史未使用登记没有 owner，也应只读复用完全相同的定义。
	legacy := &weaponState{ExtraProperties: map[string]ExtraProperty{id: {Template: template}}}
	if _, err := applyRemaps(first, legacy, items); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ name, target, template string }{
		{"原生自身模板", template, template},
		{"同号不同定义", id, "99999"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := applyRemaps(first, &weaponState{ExtraProperties: map[string]ExtraProperty{test.target: {Template: test.template, OwnerWeapon: "253450"}}}, items)
			if err == nil {
				t.Fatal("冲突必须拒绝")
			}
		})
	}
	text, err := first.text("skillproperty.xml")
	if err != nil {
		t.Fatal(err)
	}
	node, _ := after.propertyNode(id)
	encoded, err := node.serialize()
	if err != nil {
		t.Fatal(err)
	}
	sameRaw, err := encodeText(strings.Replace(text, "</SkillProperty>", encoded+"</SkillProperty>", 1))
	if err != nil {
		t.Fatal(err)
	}
	sameData, err := first.replace(map[string][]byte{"skillproperty.xml": sameRaw})
	if err != nil {
		t.Fatal(err)
	}
	same, err := parseArchive(sameData)
	if err != nil {
		t.Fatal(err)
	}
	if reused, err := applyRemaps(same, state, items); err != nil || !bytes.Equal(reused.data, same.data) {
		t.Fatalf("相同历史重复节点应只读复用：%v", err)
	}
	different := node.clone()
	different.set("SkillDamage", "987654")
	encoded, err = different.serialize()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := encodeText(strings.Replace(text, "</SkillProperty>", encoded+"</SkillProperty>", 1))
	if err != nil {
		t.Fatal(err)
	}
	data, err := first.replace(map[string][]byte{"skillproperty.xml": raw})
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := parseArchive(data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := applyRemaps(duplicate, state, items); err == nil {
		t.Fatal("重复定义必须拒绝")
	}
	// 让另一个武器的基线动作引用同号节点；详情过滤不得隐藏此冲突。
	other := ""
	for _, weapon := range info.weapons {
		if weapon.ID != 253450 {
			other = fmt.Sprint(weapon.ID)
			break
		}
	}
	actionTable, err := first.text("itemact.txt")
	if err != nil {
		t.Fatal(err)
	}
	rows := strings.Split(actionTable, "\n")
	for index, row := range rows {
		cells := strings.Split(strings.TrimSuffix(row, "\r"), "\t")
		if len(cells) > 2 && cells[0] == other {
			cells[2] = "2001998"
			rows[index] = strings.Join(cells, "\t")
			break
		}
	}
	animation, err := first.text("animation/2001.xml")
	if err != nil {
		t.Fatal(err)
	}
	animation = strings.Replace(animation, "</AnmInfo>", `<AnmDesc id="998"><Node skillproid="`+id+`"/></AnmDesc></AnmInfo>`, 1)
	actRaw, err := encodeText(strings.Join(rows, "\n"))
	if err != nil {
		t.Fatal(err)
	}
	anmRaw, err := encodeText(animation)
	if err != nil {
		t.Fatal(err)
	}
	crossData, err := first.replace(map[string][]byte{"itemact.txt": actRaw, "animation/2001.xml": anmRaw})
	if err != nil {
		t.Fatal(err)
	}
	cross, err := parseArchive(crossData)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := applyRemaps(cross, state, items, map[int]bool{253450: true}); err == nil || !strings.Contains(err.Error(), "跨武器") {
		t.Fatalf("过滤投影也必须拒绝基线跨武器引用：%v", err)
	}
	t.Logf("只读真实归档：源 SHA256=%s，新号=%s，重复应用字节一致", digest(original), id)
	if existing, ok := info.propertyNode("800000001"); ok {
		templateNode, _ := info.propertyNode(template)
		candidate := templateNode.clone()
		candidate.set("SkillProId", "800000001")
		want, _ := candidate.serialize()
		got, _ := existing.serialize()
		refs := []string{}
		for action, owners := range info.owners {
			if includes(propertyIDsOfAction(info, action), "800000001") {
				for owner := range owners {
					refs = append(refs, owner)
				}
			}
		}
		allEqual := true
		for _, node := range info.properties["800000001"] {
			text, err := node.serialize()
			if err != nil {
				t.Fatal(err)
			}
			allEqual = allEqual && text == want
		}
		t.Logf("实际 800000001：节点数=%d，首条一致=%v，全部一致=%v，武器状态引用=%v", len(info.properties["800000001"]), got == want, allEqual, refs)
		if allEqual && len(refs) == 0 {
			actualState := &weaponState{ExtraProperties: map[string]ExtraProperty{"800000001": {Template: template}}}
			reused, err := applyRemaps(source, actualState, items)
			if err != nil || !bytes.Equal(reused.data, original) {
				t.Fatalf("实际历史节点复用失败：%v", err)
			}
		}
	}
}

func TestWeapon253451ProductionDetailReadOnly(t *testing.T) {
	client := os.Getenv("OPENKFO_WEAPON_TEST_CLIENT")
	folder := os.Getenv("OPENKFO_REMAP_TEST_FOLDER")
	if client == "" || folder == "" {
		t.Skip("set client and author-state folder for real 253451 regression")
	}
	settingsPath := filepath.Join(folder, "settings.json")
	settings, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var state weaponState
	if err = json.Unmarshal(settings, &state); err != nil {
		t.Fatal(err)
	}
	entry := state.Baselines[normalizeDir(client)]
	if entry == nil {
		t.Fatal("client baseline missing")
	}
	baseline, err := os.ReadFile(entry.path(folder))
	if err != nil {
		t.Fatal(err)
	}
	current, err := os.ReadFile(configPath(client))
	if err != nil {
		t.Fatal(err)
	}
	workspacePath := weaponWorkspacePath(folder, 253451)
	workspace, err := os.ReadFile(workspacePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for path, before := range map[string][]byte{settingsPath: settings, entry.path(folder): baseline, configPath(client): current, workspacePath: workspace} {
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Errorf("production file changed: %s: %v", path, err)
			}
		}
	})
	for _, withWorkspace := range []bool{false, true} {
		t.Run(fmt.Sprintf("workspace=%t", withWorkspace), func(t *testing.T) {
			var snapshot weaponState
			if err := json.Unmarshal(settings, &snapshot); err != nil {
				t.Fatal(err)
			}
			if withWorkspace {
				var payload map[string]any
				if err := json.Unmarshal(workspace, &payload); err != nil {
					t.Fatal(err)
				}
				if err := mergeWorkspaceIntoState(payload, "253451", &snapshot); err != nil {
					t.Fatal(err)
				}
			}
			isolatedClient, isolatedFolder := t.TempDir(), t.TempDir()
			if err := os.MkdirAll(filepath.Join(isolatedClient, "Data"), 0700); err != nil {
				t.Fatal(err)
			}
			for path, data := range map[string][]byte{configPath(isolatedClient): current, filepath.Join(isolatedFolder, "baseline.spf2"): baseline} {
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			snapshot.Baselines = map[string]*clientBaseline{normalizeDir(isolatedClient): {Directory: isolatedClient, File: "baseline.spf2", SourceHash: digest(baseline), AppliedHash: digest(current)}}
			raw, err := json.Marshal(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			isolatedSettings := filepath.Join(isolatedFolder, "settings.json")
			if err := os.WriteFile(isolatedSettings, raw, 0600); err != nil {
				t.Fatal(err)
			}
			a, err := parseArchive(baseline)
			if err != nil {
				t.Fatal(err)
			}
			base, err := buildWeaponBase(a, &snapshot)
			if err != nil {
				t.Fatal(err)
			}
			items, err := itemsFromText(isolatedClient, mustText(t, base, "item.txt"), true, true)
			if err != nil {
				t.Fatal(err)
			}
			var first []byte
			var revision string
			for repeat := 0; repeat < 2; repeat++ {
				value, err := weaponHandle(Request{Operation: "weapon_detail", Weapon: 253451}, isolatedClient, items, isolatedFolder)
				if err != nil {
					t.Fatal(err)
				}
				result := value.(map[string]any)
				revision = result["revision"].(string)
				if failure, ok := result["remap_error"]; ok {
					t.Fatalf("253451 detail fell back to unmapped structure: %v", failure)
				}
				encoded, err := json.Marshal(result["weapon"])
				if err != nil {
					t.Fatal(err)
				}
				if repeat == 0 {
					first = encoded
				} else if !bytes.Equal(first, encoded) {
					t.Fatal("detail action IDs changed across reads")
				}
			}
			for path, before := range map[string][]byte{isolatedSettings: raw, configPath(isolatedClient): current} {
				after, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(before, after) {
					t.Fatalf("detail wrote %s: %v", path, err)
				}
			}
			if withWorkspace {
				var payload map[string]any
				if err := json.Unmarshal(workspace, &payload); err != nil {
					t.Fatal(err)
				}
				if _, err := weaponHandle(Request{Operation: "weapon_apply", Weapon: 253451, Workspace: payload, Revision: revision}, isolatedClient, items, isolatedFolder); err != nil {
					t.Fatalf("real 253451 workspace apply to isolated client failed: %v", err)
				}
				out, err := loadArchive(configPath(isolatedClient))
				if err != nil {
					t.Fatal(err)
				}
				if err := out.verify(); err != nil {
					t.Fatal(err)
				}
				strictChangedXML(t, a, out, changedArchiveEntries(t, a, out))
				committed, err := os.ReadFile(isolatedSettings)
				if err != nil {
					t.Fatal(err)
				}
				var saved weaponState
				if err := json.Unmarshal(committed, &saved); err != nil {
					t.Fatal(err)
				}
				finalInfo, err := inspect(out, items)
				if err != nil {
					t.Fatal(err)
				}
				if err := checkAllowedWrites(a, out, &saved, finalInfo); err != nil {
					t.Fatal(err)
				}
				payload["variants"] = saved.Variants["253451"]
				payload["rules"] = saved.Drafts["253451"]
				canonical := map[string]HitProperty{}
				for id, property := range saved.HitProperties {
					if hitPropertyReferencesWeapon(property, "253451") {
						canonical[id] = property
					}
				}
				payload["hit_properties"] = canonical
				read, err := weaponHandle(Request{Operation: "weapon_detail", Weapon: 253451}, isolatedClient, items, isolatedFolder)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := weaponHandle(Request{Operation: "weapon_apply", Weapon: 253451, Workspace: payload, Revision: read.(map[string]any)["revision"].(string)}, isolatedClient, items, isolatedFolder); err != nil {
					t.Fatalf("repeat 253451 workspace apply failed: %v", err)
				}
				repeated, err := os.ReadFile(configPath(isolatedClient))
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(out.data, repeated) {
					after, err := parseArchive(repeated)
					if err != nil {
						t.Fatal(err)
					}
					bytesEqualArchive(t, out, after, true)
					t.Fatal("repeat 253451 apply changed final archive")
				}
				t.Logf("real 253451 workspace weapon_apply and repeat succeeded: entries=%d, sha256=%s", len(out.entries), digest(out.data))
			}
		})
	}
}

func TestApplyRemapsProductionBaselineReadOnly(t *testing.T) {
	client := os.Getenv("OPENKFO_WEAPON_TEST_CLIENT")
	folder := os.Getenv("OPENKFO_REMAP_TEST_FOLDER")
	if client == "" || folder == "" {
		t.Skip("设置 OPENKFO_WEAPON_TEST_CLIENT 和 OPENKFO_REMAP_TEST_FOLDER 后执行生产基线只读测试")
	}
	settingsPath := filepath.Join(folder, "settings.json")
	settings, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var state weaponState
	if err := json.Unmarshal(settings, &state); err != nil {
		t.Fatal(err)
	}
	entry := state.Baselines[normalizeDir(client)]
	if entry == nil {
		t.Fatal("客户端没有独立基线")
	}
	baseline, err := os.ReadFile(entry.path(folder))
	if err != nil {
		t.Fatal(err)
	}
	current, err := os.ReadFile(configPath(client))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for path, before := range map[string][]byte{settingsPath: settings, entry.path(folder): baseline, configPath(client): current} {
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Errorf("生产文件被修改 %s：%v", path, err)
			}
		}
	})
	source, err := parseArchive(baseline)
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
	info, err := inspect(base, items)
	if err != nil {
		t.Fatal(err)
	}
	remapped, err := applyRemaps(base, &state, items)
	if err != nil {
		t.Fatalf("实际状态重映射失败：%v", err)
	}
	after, err := inspect(remapped, items)
	if err != nil {
		t.Fatal(err)
	}
	for id := range state.ExtraProperties {
		t.Logf("生产属性 %s：模板=%s，owner=%s，基线定义=%d，重映射后定义=%d", id, state.ExtraProperties[id].Template, state.ExtraProperties[id].OwnerWeapon, len(info.properties[id]), len(after.properties[id]))
	}
	prepared, err := prepareClient(entry, folder, &state, state.Applied, after)
	if err != nil {
		t.Fatalf("生产完整只读渲染失败：%v", err)
	}
	t.Logf("生产完整只读渲染通过：基线 SHA256=%s，当前 SHA256=%s，输出 SHA256=%s，未调用 commitClient", digest(baseline), digest(current), digest(prepared.Data))
}

func TestApplyRemapsRewritesOneBlockTwice(t *testing.T) {
	root := os.Getenv("OPENKFO_TEST_RUNTIME")
	if root == "" {
		abs, err := filepath.Abs("../../../../runtime-local")
		if err != nil {
			t.Fatal(err)
		}
		root = abs
	}
	source, err := loadArchive(filepath.Join(root, "weapon-config/original.spf2"))
	if err != nil {
		t.Skip("runtime-local 固定装置不可用")
	}
	items, err := catalog(filepath.Join(root, "client"), false, false)
	if err != nil {
		// 同 original.spf2：固定装置依赖工作区里的 runtime-local/client，
		// 在只有源码的仓库（如上游侧）里不存在，跳过而不是报错。
		t.Skip("runtime-local/client 不可用: " + err.Error())
	}
	info, err := inspect(source, items)
	if err != nil {
		t.Fatal(err)
	}
	// A non-shared action with exactly one block and a live hit property is the
	// branch that rewrites the block in place.
	action, property := "", ""
	for candidate := range info.owners {
		if len(info.owners[candidate]) != 1 {
			continue
		}
		blocks := info.blocks[actionKey(candidate)]
		if len(blocks) != 1 {
			continue
		}
		found := ""
		blocks[0].node.walk(func(node *xmlNode) {
			if found != "" {
				return
			}
			if value := strings.TrimSpace(node.get("skillproid")); value != "" && len(info.properties[value]) == 1 {
				found = value
			}
		})
		if found == "" {
			continue
		}
		action, property = candidate, found
		break
	}
	if action == "" {
		t.Skip("夹具里找不到可安全重映射的动作")
	}
	text, err := source.text("itemact.txt")
	if err != nil {
		t.Fatal(err)
	}
	rows := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	header := strings.Split(rows[0], "\t")
	column := map[string]int{}
	for index, name := range header {
		column[name] = index
	}
	weaponKey := strings.Split(rows[1], "\t")[0]
	row := strings.Split(rows[1], "\t")
	for _, state := range []string{"2011", "2012"} {
		index, ok := column[state]
		if !ok || index >= len(row) {
			t.Skipf("夹具武器行缺少 %s 列", state)
		}
	}
	state := &weaponState{Remaps: map[string]map[int]*StageRemap{
		weaponKey: {
			2011: {Action: action, PropertyID: property},
			2012: {Action: action, PropertyID: property},
		},
	}}
	remapped, err := applyRemaps(source, state, items)
	if err != nil {
		t.Fatalf("两个状态重映射到同一动作时不应报错：%v", err)
	}
	after, err := inspect(remapped, items)
	if err != nil {
		t.Fatal(err)
	}
	blocks := after.blocks[actionKey(action)]
	if len(blocks) != 1 {
		t.Fatalf("重映射后动作 %s 的块数 = %d，期望 1", action, len(blocks))
	}
	got := ""
	blocks[0].node.walk(func(node *xmlNode) {
		if got != "" {
			return
		}
		if value := strings.TrimSpace(node.get("skillproid")); value != "" {
			got = value
		}
	})
	if got != property {
		t.Fatalf("命中属性 = %q，期望 %q", got, property)
	}
}
