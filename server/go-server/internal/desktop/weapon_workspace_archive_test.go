package desktop

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const workspaceTestPython = "C:/Users/njk/.workbuddy/binaries/python/versions/3.13.12/python.exe"

func TestWeaponWorkspace253521ArchiveRoundTrip(t *testing.T) {
	installed := os.Getenv("OPENKFO_WEAPON_TEST_CLIENT")
	if installed == "" {
		t.Skip("设置 OPENKFO_WEAPON_TEST_CLIENT 后执行真实归档集成测试")
	}
	installed, err := filepath.Abs(installed)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(configPath(installed))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		current, err := os.ReadFile(configPath(installed))
		if err != nil || !bytes.Equal(current, original) {
			t.Errorf("真实客户端归档发生变化：%v", err)
		}
	})
	source, err := parseArchive(original)
	if err != nil {
		t.Fatal(err)
	}
	if err = source.verify(); err != nil {
		t.Fatal(err)
	}
	items, err := catalog(installed, false, false)
	if err != nil {
		t.Fatal(err)
	}
	info, err := inspect(source, items)
	if err != nil {
		t.Fatal(err)
	}
	donor := findWeapon(info, 253521)
	if donor == nil {
		t.Fatal("真实归档缺少 253521")
	}
	cloneID := freeWorkspaceCloneID(source, 253999)
	cloneKey := strconv.Itoa(cloneID)

	client := t.TempDir()
	if err = os.MkdirAll(filepath.Join(client, "Data"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(configPath(client), original, 0600); err != nil {
		t.Fatal(err)
	}
	folder := t.TempDir()
	if err = os.WriteFile(filepath.Join(folder, "baseline.spf2"), original, 0600); err != nil {
		t.Fatal(err)
	}
	entry := &clientBaseline{Directory: client, File: "baseline.spf2", SourceHash: digest(original), AppliedHash: digest(original)}

	blueprint := Blueprint{ID: cloneID, Name: "253521 workspace clone", Type: donor.Type, Model: donor.Model, Donor: donor.ID}
	base, err := buildWeaponBase(source, &weaponState{Created: map[string]Blueprint{cloneKey: blueprint}})
	if err != nil {
		t.Fatal(err)
	}
	cloneItems, err := itemsFromText(client, mustArchiveText(t, base, "item.txt"), true, true)
	if err != nil {
		t.Fatal(err)
	}
	cloneInfo, err := inspect(base, cloneItems)
	if err != nil {
		t.Fatal(err)
	}
	clone := findWeapon(cloneInfo, cloneID)
	if clone == nil {
		t.Fatal("253521 克隆未生成")
	}
	stage, action, segmentID, stageEffects := pickWorkspaceEffectStage(t, base, clone)
	baseBlocks := actionVariantsFromText(t, mustArchiveText(t, base, "animation/"+action[:4]+".xml"), action)
	propertyID := xmlAttrOnID(baseBlocks[0], "Anm", "id", segmentID, "skillproid")
	segmentName := xmlAttrOnID(baseBlocks[0], "Anm", "id", segmentID, "name")
	if propertyID == "" || segmentName == "" {
		t.Fatal("克隆模板缺少命中属性或动画名")
	}
	propertyKey, propertyValue := "SkillDamage", 12.5
	stageKey := stage.State
	effectRows := realEffectRowsForStage(t, base, stageEffects)

	frameAttrs := []FrameSwitchAttr{{Key: "switchstartframe", Value: "1"}, {Key: "switchendframe", Value: "2"}, {Key: "nextstate", Value: stage.State}}
	counterAttrs := []FrameSwitchAttr{{Key: "startframe", Value: "1"}, {Key: "endframe", Value: "2"}, {Key: "nextstate", Value: stage.State}, {Key: "angleoffset", Value: "0"}, {Key: "anglehalfrange", Value: "180"}}
	counterBox := []FrameSwitchAttr{{Key: "length", Value: "1"}, {Key: "width", Value: "1"}, {Key: "heigth", Value: "1"}}
	scopeAttrs := []FrameSwitchAttr{{Key: "centerx", Value: "0"}, {Key: "centery", Value: "10"}, {Key: "centerz", Value: "10"}, {Key: "length", Value: "20"}, {Key: "width", Value: "20"}, {Key: "heigth", Value: "20"}}
	guardAttrs := []FrameSwitchAttr{{Key: "startframe", Value: "1"}, {Key: "endframe", Value: "2"}}
	variant := VariantEdit{Condition: 406, Segments: []VariantAnm{
		{Name: segmentName, AnmID: "71", Start: 1, End: 2, SkillProID: propertyID, Damage: 17.25},
		{Name: segmentName, AnmID: "72", Start: 2, End: 3, SkillProID: propertyID, Damage: 23.5, ReplayTimes: 3},
	}}
	chain := ComboTransition{OldState: stage.State, NewState: stage.State, KeyInput: "1", StartPart: "1"}
	combo := ComboRuleSet{
		Max:   []ComboRuleMax{{Skill: propertyID, MaxCombo: "2", ExceedState: stage.State, ExceedSkillProID: propertyID}},
		Black: []ComboRuleLink{{Prev: propertyID, Cur: propertyID}},
		White: []ComboRuleLink{{Prev: propertyID, Cur: propertyID}},
	}
	payload := map[string]any{
		"weapon":               map[string]any{"id": cloneID, "name": blueprint.Name},
		"rules":                []Rule{{Stage: stage.Stage, Buff: 0, Level: 1, Duration: 3000, Properties: map[string]map[string]float64{propertyID: {propertyKey: propertyValue}}}},
		"block_elements_saved": map[string]map[string][]BlockElement{stageKey: {"FakeUnAttack": {{Tag: "FakeUnAttack", Attrs: guardAttrs}}}},
		"variants":             map[string][]VariantEdit{stageKey: {variant}},
		"frame_saved":          map[string][]FrameSwitch{stageKey: {{Attrs: frameAttrs}}},
		"counter_saved":        map[string]*CounterEdit{stageKey: {Attrs: counterAttrs, Box: counterBox}},
		"scope_saved":          map[string]map[string][]FrameSwitchAttr{stageKey: {segmentID: scopeAttrs}},
		"combo_chain":          []ComboTransition{chain},
		"combo_rule_info":      map[string]any{"rules": combo},
		"effect_rows":          effectRows,
		"stage_effects":        map[string][]StageEffect{stageKey: stageEffects},
		"empty_effect_rows":    []EffectRow{},
		"empty_stage_effects":  map[string][]StageEffect{stageKey: {}},
	}

	workspaceFolder := t.TempDir()
	if _, err = saveWeaponWorkspace(workspaceFolder, cloneID, payload); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadWeaponWorkspace(workspaceFolder, cloneID)
	if err != nil {
		t.Fatal(err)
	}
	loadedPayload, ok := loaded["payload"].(map[string]any)
	if !ok {
		t.Fatal("workspace load 未返回 payload")
	}
	if loadedPayload["schema_version"] != float64(weaponWorkspaceSchemaVersion) || loadedPayload["saved_at"] == nil {
		t.Fatalf("workspace 元数据不完整：%v", loadedPayload)
	}
	assertWorkspaceJSONExact(t, payload, loadedPayload)

	state := &weaponState{Drafts: map[string][]Rule{}, Applied: map[string][]Rule{}, Created: map[string]Blueprint{cloneKey: blueprint}}
	if err = mergeWorkspaceIntoState(loadedPayload, cloneKey, state); err != nil {
		t.Fatal(err)
	}
	assertWorkspaceFields(t, state, cloneKey, stage, stageKey, segmentID, propertyID, propertyKey, propertyValue, frameAttrs, counterAttrs, counterBox, scopeAttrs, guardAttrs, variant, chain, combo)
	assertWorkspaceEffectFields(t, loadedPayload, stage.State, stageEffects, effectRows)
	state.Applied[cloneKey] = append([]Rule(nil), state.Drafts[cloneKey]...)

	prepared, err := prepareClient(entry, folder, state, state.Applied, cloneInfo)
	if err != nil {
		t.Fatalf("253521 克隆 workspace prepare 失败：%v", err)
	}
	if !bytes.Equal(original, prepared.Current) {
		t.Fatal("prepare 阶段修改了客户端副本")
	}
	current, err := os.ReadFile(configPath(client))
	if err != nil || !bytes.Equal(original, current) {
		t.Fatalf("prepare 阶段实际客户端副本被修改：%v", err)
	}
	out, err := parseArchive(prepared.Data)
	if err != nil {
		t.Fatal(err)
	}
	if err = out.verify(); err != nil {
		t.Fatal(err)
	}
	changed := changedArchiveEntries(t, source, out)
	strictChangedXML(t, source, out, changed)
	assertFinalCloneValues(t, out, cloneKey, stage, action, segmentID, propertyID, propertyKey, propertyValue, frameAttrs, counterAttrs, counterBox, scopeAttrs, guardAttrs, variant)
	assertFinalEffectValues(t, out, cloneKey, stage.State, stageEffects, effectRows)
	emptyStageState := &weaponState{StageEffects: map[string]map[int][]StageEffect{cloneKey: {mustStateColumn(t, stage.State): {}}}}
	withoutStageEffects, err := applyStageEffects(out, emptyStageState)
	if err != nil {
		t.Fatalf("空 stage_effects 最终归档渲染失败：%v", err)
	}
	assertFinalEffectValues(t, withoutStageEffects, cloneKey, stage.State, nil, effectRows)
	emptyRowsState := &weaponState{Created: map[string]Blueprint{cloneKey: blueprint}, EffectRows: map[string][]EffectRow{cloneKey: {}}}
	withoutEffectRows, err := syncWeaponEffects(withoutStageEffects, emptyRowsState.Created, emptyRowsState.EffectRows)
	if err != nil {
		t.Fatalf("空 effect_rows 最终归档渲染失败：%v", err)
	}
	if strings.Contains(mustArchiveText(t, withoutEffectRows, "acteffect.xml"), `ItemID = "`+cloneKey+`"`) {
		t.Fatalf("空 effect_rows 未删除最终 acteffect 登记块 %s", cloneKey)
	}
	assertWorkspaceFinalRegistrations(t, out, cloneKey, stage, segmentID, propertyID, chain, variant)
	finalItems, err := itemsFromText(client, mustArchiveText(t, out, "item.txt"), false, false)
	if err != nil {
		t.Fatal(err)
	}
	nameFound := false
	for _, item := range finalItems {
		if int(item.ID) == cloneID {
			nameFound = item.Name == blueprint.Name
		}
	}
	if !nameFound {
		t.Fatal("最终克隆武器名称与 workspace 不一致")
	}
	assertDonorOutputUnchanged(t, source, out, donor, stage.State)
	reportOriginalInvalidXML(t, source)

	if err = commitClient(prepared, folder); err != nil {
		t.Fatal(err)
	}
	committed, err := os.ReadFile(configPath(client))
	if err != nil || !bytes.Equal(committed, prepared.Data) {
		t.Fatalf("commit 最终归档不匹配：%v", err)
	}

	failedState := &weaponState{
		Drafts:     map[string][]Rule{},
		Applied:    map[string][]Rule{cloneKey: state.Applied[cloneKey]},
		Created:    map[string]Blueprint{cloneKey: blueprint},
		ComboRules: map[string]ComboRuleSet{cloneKey: {Black: []ComboRuleLink{{Prev: "not-a-real-number", Cur: "not-a-real-number"}}}},
		EffectRows: map[string][]EffectRow{cloneKey: {{EffectID: "not-valid", File: "../../outside"}}},
	}
	failedBefore, err := os.ReadFile(configPath(client))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = prepareClient(entry, folder, failedState, failedState.Applied, cloneInfo); err == nil {
		t.Fatal("无效 effect_rows/comboRule 未被 prepare 拒绝")
	}
	t.Logf("无效 comboRule 被拒绝：%v", err)
	failedAfter, err := os.ReadFile(configPath(client))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(failedBefore, failedAfter) {
		t.Fatal("prepare 失败后客户端仍被写入")
	}
}

func TestWeaponWorkspace253451State2012MultiPropertyRemapIsolation(t *testing.T) {
	installed := os.Getenv("OPENKFO_WEAPON_TEST_CLIENT")
	if installed == "" {
		t.Skip("设置 OPENKFO_WEAPON_TEST_CLIENT 后执行真实归档集成测试")
	}
	installed, err := filepath.Abs(installed)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(configPath(installed))
	if err != nil {
		t.Fatal(err)
	}
	source, err := parseArchive(original)
	if err != nil {
		t.Fatal(err)
	}
	if err = source.verify(); err != nil {
		t.Fatal(err)
	}
	items, err := catalog(installed, false, false)
	if err != nil {
		t.Fatal(err)
	}
	info, err := inspect(source, items)
	if err != nil {
		t.Fatal(err)
	}
	donor := findWeapon(info, 253451)
	if donor == nil {
		t.Fatal("真实归档缺少 253451")
	}
	var donorStage *Stage
	for index := range donor.Stages {
		if donor.Stages[index].State == "2012" {
			donorStage = &donor.Stages[index]
			break
		}
	}
	if donorStage == nil || donorStage.Action == "" || donorStage.Action == "0" {
		t.Fatal("253451 状态 2012 缺少可用动作")
	}
	if len(donorStage.PropertyIDs) < 2 {
		t.Fatalf("253451 状态 2012 命中属性数量=%d，无法验证多属性映射", len(donorStage.PropertyIDs))
	}
	t.Logf("253451 状态2012 donor action=%s property_ids=%v hits=%+v", donorStage.Action, donorStage.PropertyIDs, donorStage.Hits)

	// 选取 253451 当前动作表中的空状态，模拟「新增状态」后映射到真实 2012。
	actionText := mustArchiveText(t, source, "itemact.txt")
	header := itemactHeader(actionText)
	row := actionRowIndex(actionText)["253451"]
	if row == nil {
		t.Fatal("itemact.txt 缺少 253451")
	}
	targetState := ""
	for index, stateName := range header {
		if index < 2 || stateName == "2012" || index >= len(row) {
			continue
		}
		if _, parseErr := strconv.Atoi(stateName); parseErr != nil {
			continue
		}
		if strings.TrimSpace(row[index]) == "" || strings.TrimSpace(row[index]) == "0" {
			targetState = stateName
			break
		}
	}
	if targetState == "" {
		t.Skip("253451 当前动作表没有空状态列，无法模拟新增状态")
	}

	// 每个供体 skillproid 都获得目标武器独立的 800000xxx 属性号。
	propertyMap := map[string]string{}
	extraProperties := map[string]ExtraProperty{}
	usedIDs := map[string]bool{}
	for _, oldID := range donorStage.PropertyIDs {
		newID := freshPropertyID(info)
		for usedIDs[newID] {
			delete(info.properties, newID)
			newID = freshPropertyID(info)
		}
		if newID == "" {
			t.Fatal("无法分配 253451 多属性映射编号")
		}
		usedIDs[newID] = true
		info.properties[newID] = []*xmlNode{{}}
		propertyMap[oldID] = newID
		extraProperties[newID] = ExtraProperty{Template: oldID, OwnerWeapon: "253451"}
	}
	// 上面的临时占位会阻止 freshPropertyID 重复；真实 merge/apply 仍使用原始归档。
	mainPropertyID := propertyMap[donorStage.PropertyIDs[0]]
	properties := map[string]map[string]float64{}
	for _, oldID := range donorStage.PropertyIDs {
		properties[propertyMap[oldID]] = map[string]float64{"SkillDamage": 1}
	}
	stageNumber, err := strconv.Atoi(targetState)
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{
		"weapon": map[string]any{"id": 253451, "name": donor.Name},
		"remaps": map[string]map[int]*StageRemap{"253451": {
			stageNumber: {Action: donorStage.Action, PropertyID: mainPropertyID, PropertyMap: propertyMap},
		}},
		"extra_properties": extraProperties,
		"rules":            []Rule{{Stage: ruleStageOf(stageNumber), Buff: 0, Level: 1, Duration: 3000, Properties: properties}},
	}
	workspaceFolder := t.TempDir()
	if _, err = saveWeaponWorkspace(workspaceFolder, 253451, payload); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadWeaponWorkspace(workspaceFolder, 253451)
	if err != nil {
		t.Fatal(err)
	}
	loadedPayload, ok := loaded["payload"].(map[string]any)
	if !ok {
		t.Fatal("253451 workspace load 未返回 payload")
	}
	state := &weaponState{
		Drafts:          map[string][]Rule{},
		Applied:         map[string][]Rule{},
		Remaps:          map[string]map[int]*StageRemap{},
		ExtraProperties: map[string]ExtraProperty{},
	}
	if err = mergeWorkspaceIntoState(loadedPayload, "253451", state); err != nil {
		t.Fatal(err)
	}
	if state.Remaps["253451"][stageNumber] == nil || len(state.Remaps["253451"][stageNumber].PropertyMap) != len(propertyMap) {
		t.Fatalf("253451 property_map 合并错误：%+v", state.Remaps)
	}
	state.Applied["253451"] = append([]Rule(nil), state.Drafts["253451"]...)

	// 基线与客户端副本均为临时文件；正式 local-client 只读校验字节不变。
	client := t.TempDir()
	if err = os.MkdirAll(filepath.Join(client, "Data"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(configPath(client), original, 0600); err != nil {
		t.Fatal(err)
	}
	folder := t.TempDir()
	if err = os.WriteFile(filepath.Join(folder, "baseline.spf2"), original, 0600); err != nil {
		t.Fatal(err)
	}
	entry := &clientBaseline{Directory: client, File: "baseline.spf2", SourceHash: digest(original), AppliedHash: digest(original)}
	beforeClient, err := os.ReadFile(configPath(client))
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := prepareClient(entry, folder, state, state.Applied, info)
	if err != nil {
		t.Fatalf("253451 状态 %s 多属性重映射 prepare 失败：%v", targetState, err)
	}
	if !bytes.Equal(beforeClient, original) {
		t.Fatal("prepare 阶段修改了临时客户端")
	}
	out, err := parseArchive(prepared.Data)
	if err != nil {
		t.Fatal(err)
	}
	if err = out.verify(); err != nil {
		t.Fatal(err)
	}

	finalAction := workspaceActionForState(mustArchiveText(t, out, "itemact.txt"), "253451", targetState)
	if finalAction == "" || finalAction == "0" {
		t.Fatal("新增目标状态最终没有动作号")
	}
	if finalAction == donorStage.Action {
		t.Fatal("共享动作未被隔离为目标武器独立动作块")
	}
	finalBlocks := actionVariantsFromText(t, mustArchiveText(t, out, "animation/"+finalAction[:4]+".xml"), finalAction)
	if len(finalBlocks) == 0 {
		t.Fatalf("最终动画缺少动作块：%s", finalAction)
	}
	plainBlocks := 0
	conditionedBlocks := 0
	seenFinal := map[string]bool{}
	finalVariantCounts := map[string]int{}
	for _, block := range finalBlocks {
		condition := conditionUstate(block)
		finalVariantCounts[condition]++
		if condition == "" {
			plainBlocks++
		} else {
			conditionedBlocks++
		}
		block.walk(func(node *xmlNode) {
			if node.tag != "Anm" {
				return
			}
			id := strings.TrimSpace(node.get("skillproid"))
			if id == "" || id == "0" {
				return
			}
			seenFinal[id] = true
		})
	}
	if plainBlocks != 1 || conditionedBlocks == 0 {
		t.Fatalf("最终动作块条件副本不完整：plain=%d conditioned=%d action=%s", plainBlocks, conditionedBlocks, finalAction)
	}
	donorBlocks := actionVariantsFromText(t, mustArchiveText(t, source, "animation/"+donorStage.Action[:4]+".xml"), donorStage.Action)
	donorVariantCounts := map[string]int{}
	for _, block := range donorBlocks {
		donorVariantCounts[conditionUstate(block)]++
	}
	if !reflect.DeepEqual(finalVariantCounts, donorVariantCounts) {
		t.Fatalf("最终动作条件副本数量与供体不一致：got=%v want=%v", finalVariantCounts, donorVariantCounts)
	}
	propertyText := mustArchiveText(t, out, "skillproperty.xml")
	t.Logf("253451 target=%s donorAction=%s finalAction=%s propertyMap=%v finalBlocks=%v", targetState, donorStage.Action, finalAction, propertyMap, seenFinal)
	for oldID := range propertyMap {
		if seenFinal[oldID] {
			t.Fatalf("最终动作仍引用供体命中属性 %s：%v", oldID, seenFinal)
		}
	}
	if len(seenFinal) != len(propertyMap) {
		t.Fatalf("最终动作命中属性数量异常：实际独立编号=%d，供体映射=%d", len(seenFinal), len(propertyMap))
	}
	for finalID := range seenFinal {
		number, parseErr := strconv.Atoi(finalID)
		if parseErr != nil || number < propertySplitPrefix {
			t.Fatalf("最终动作命中属性未生成客户端独立编号：%s", finalID)
		}
		if count := strings.Count(propertyText, `SkillProId="`+finalID+`"`); count != 1 {
			t.Fatalf("最终命中属性 %s 登记数量=%d，期望1", finalID, count)
		}
		if got, ok := propertyAttrExact(propertyText, finalID, "SkillDamage"); !ok || got != "1" {
			t.Fatalf("最终命中属性 %s 未保留 workspace 规则值 SkillDamage=1，实际=%q", finalID, got)
		}
	}
	finalItems, err := itemsFromText(client, mustArchiveText(t, out, "item.txt"), true, true)
	if err != nil {
		t.Fatal(err)
	}
	finalInfo, err := inspect(out, finalItems)
	if err != nil {
		t.Fatal(err)
	}
	finalWeapon := findWeapon(finalInfo, 253451)
	if finalWeapon == nil {
		t.Fatal("最终 inspection 缺少 253451")
	}
	var finalStage *Stage
	for index := range finalWeapon.Stages {
		if finalWeapon.Stages[index].State == targetState {
			finalStage = &finalWeapon.Stages[index]
			break
		}
	}
	if finalStage == nil || finalStage.Action != finalAction || len(finalStage.PropertyIDs) != len(propertyMap) {
		t.Fatalf("新增状态最终阶段不完整：%+v", finalStage)
	}
	if finalStage.RawFrames <= 0 {
		t.Fatalf("新增状态最终动画没有帧信息：%+v", finalStage)
	}
	strictChangedXML(t, source, out, changedArchiveEntries(t, source, out))
	if err = commitClient(prepared, folder); err != nil {
		t.Fatal(err)
	}
	committed, err := os.ReadFile(configPath(client))
	if err != nil || !bytes.Equal(committed, prepared.Data) {
		t.Fatalf("253451 临时客户端 commit 不匹配：%v", err)
	}
	if err = commitClient(prepared, folder); err != nil {
		t.Fatalf("重复 commit 应保持幂等：%v", err)
	}
	committedAgain, err := os.ReadFile(configPath(client))
	if err != nil || !bytes.Equal(committedAgain, prepared.Data) {
		t.Fatalf("重复 commit 改变了临时客户端：%v", err)
	}
	finalInstalled, err := os.ReadFile(configPath(installed))
	if err != nil || !bytes.Equal(finalInstalled, original) {
		t.Fatalf("正式客户端归档被测试修改：%v", err)
	}
}

func pickBlockFromNodes(nodes []*xmlNode) (*xmlNode, bool) {
	for _, node := range nodes {
		if conditionUstate(node) == "" {
			return node, true
		}
	}
	if len(nodes) == 0 {
		return nil, false
	}
	return nodes[0], true
}

func TestWeaponWorkspace253521RemapExtraPropertyIsolation(t *testing.T) {
	installed := os.Getenv("OPENKFO_WEAPON_TEST_CLIENT")
	if installed == "" {
		t.Skip("设置 OPENKFO_WEAPON_TEST_CLIENT 后执行真实归档集成测试")
	}
	installed, err := filepath.Abs(installed)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(configPath(installed))
	if err != nil {
		t.Fatal(err)
	}
	source, err := parseArchive(original)
	if err != nil {
		t.Fatal(err)
	}
	items, err := catalog(installed, false, false)
	if err != nil {
		t.Fatal(err)
	}
	info, err := inspect(source, items)
	if err != nil {
		t.Fatal(err)
	}
	donor := findWeapon(info, 253521)
	if donor == nil {
		t.Fatal("真实归档缺少 253521")
	}
	cloneID := freeWorkspaceCloneID(source, 254200)
	cloneKey := strconv.Itoa(cloneID)
	blueprint := Blueprint{ID: cloneID, Name: "253521 remap isolation", Type: donor.Type, Model: donor.Model, Donor: donor.ID}
	base, err := buildWeaponBase(source, &weaponState{Created: map[string]Blueprint{cloneKey: blueprint}})
	if err != nil {
		t.Fatal(err)
	}
	cloneItems, err := itemsFromText(t.TempDir(), mustArchiveText(t, base, "item.txt"), true, true)
	if err != nil {
		t.Fatal(err)
	}
	cloneInfo, err := inspect(base, cloneItems)
	if err != nil {
		t.Fatal(err)
	}
	clone := findWeapon(cloneInfo, cloneID)
	if clone == nil || len(clone.Stages) < 2 {
		t.Skip("253521 克隆可用状态不足两个")
	}
	first := clone.Stages[0]
	second := clone.Stages[1]
	firstState, err := strconv.Atoi(first.State)
	if err != nil {
		t.Fatal(err)
	}
	secondState, err := strconv.Atoi(second.State)
	if err != nil {
		t.Fatal(err)
	}
	templateID := ""
	for candidate := range cloneInfo.properties {
		templateID = strings.TrimSpace(candidate)
		if templateID != "" {
			break
		}
	}
	if templateID == "" {
		t.Skip("253521 克隆归档没有可用命中属性模板")
	}
	extraID := freshPropertyID(cloneInfo)
	if extraID == "" {
		t.Fatal("无法分配新增命中属性编号")
	}
	payload := map[string]any{
		"weapon":           map[string]any{"id": cloneID, "name": blueprint.Name},
		"remaps":           map[string]map[int]*StageRemap{cloneKey: {firstState: {Action: first.Action, PropertyID: extraID}}},
		"cleared":          map[string]map[int]bool{cloneKey: {secondState: true, firstState + 100000: false}},
		"extra_properties": map[string]ExtraProperty{extraID: {Template: templateID}},
	}
	folder := t.TempDir()
	if _, err = saveWeaponWorkspace(folder, cloneID, payload); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadWeaponWorkspace(folder, cloneID)
	if err != nil {
		t.Fatal(err)
	}
	state := &weaponState{Remaps: map[string]map[int]*StageRemap{}, Cleared: map[string]map[int]bool{}, ExtraProperties: map[string]ExtraProperty{}, Created: map[string]Blueprint{cloneKey: blueprint}}
	if err = mergeWorkspaceIntoState(loaded["payload"].(map[string]any), cloneKey, state); err != nil {
		t.Fatal(err)
	}
	if state.Remaps[cloneKey][firstState] == nil || state.ExtraProperties[extraID].Template != templateID || !state.Cleared[cloneKey][secondState] || state.Cleared[cloneKey][firstState+100000] {
		t.Fatalf("workspace remap/clear merge 错误：%+v %+v %+v", state.Remaps, state.Cleared, state.ExtraProperties)
	}
	// 使用真实临时客户端，确保 prepare/commit 全程与正式客户端隔离。
	client := t.TempDir()
	if err = os.MkdirAll(filepath.Join(client, "Data"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(configPath(client), original, 0600); err != nil {
		t.Fatal(err)
	}
	entry := &clientBaseline{Directory: client, File: "baseline.spf2", SourceHash: digest(original), AppliedHash: digest(original)}
	prepared, err := prepareClient(entry, folder, state, nil, cloneInfo)
	if err != nil {
		t.Fatalf("新增 property + remap 同轮 prepare 失败：%v", err)
	}
	before, err := os.ReadFile(configPath(client))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, original) {
		t.Fatal("prepare 阶段修改了临时客户端")
	}
	if _, err = prepareClient(entry, folder, state, nil, cloneInfo); err != nil {
		t.Fatalf("重复 prepare 不应失败：%v", err)
	}
	out, err := parseArchive(prepared.Data)
	if err != nil {
		t.Fatal(err)
	}
	if err = out.verify(); err != nil {
		t.Fatal(err)
	}
	propertyText := mustArchiveText(t, out, "skillproperty.xml")
	if count := strings.Count(propertyText, `SkillProId="`+extraID+`"`); count != 1 {
		t.Fatalf("新增 property 登记数量=%d，期望1", count)
	}
	row := actionRowIndex(mustArchiveText(t, out, "itemact.txt"))[cloneKey]
	firstColumn := itemactHeader(mustArchiveText(t, out, "itemact.txt"))
	firstAction := ""
	secondAction := ""
	for i, name := range firstColumn {
		if i >= len(row) {
			continue
		}
		if name == first.State {
			firstAction = row[i]
		}
		if name == second.State {
			secondAction = row[i]
		}
	}
	if firstAction == "" || firstAction == "0" || secondAction != "0" {
		t.Fatalf("cleared/remap 输出错误：first=%q second=%q", firstAction, secondAction)
	}
	if !strings.Contains(mustArchiveText(t, out, "animation/"+firstAction[:4]+".xml"), `skillproid="`+extraID+`"`) {
		t.Fatal("同轮 remap 未引用新增 property")
	}
	changed := changedArchiveEntries(t, source, out)
	strictChangedXML(t, source, out, changed)
	assertDonorOutputUnchanged(t, source, out, donor, first.State)
	if err = commitClient(prepared, folder); err != nil {
		t.Fatal(err)
	}
	committed, err := os.ReadFile(configPath(client))
	if err != nil || !bytes.Equal(committed, prepared.Data) {
		t.Fatalf("临时客户端 commit 不匹配：%v", err)
	}
	failedBefore, err := os.ReadFile(configPath(client))
	if err != nil {
		t.Fatal(err)
	}
	state.Remaps[cloneKey][firstState].PropertyID = "does-not-exist"
	if _, err = prepareClient(entry, folder, state, nil, cloneInfo); err == nil {
		t.Fatal("冲突 remap 未失败")
	}
	failedAfter, err := os.ReadFile(configPath(client))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(failedBefore, failedAfter) {
		t.Fatal("冲突失败后仍写入临时客户端")
	}
}

func assertWorkspaceJSONExact(t *testing.T, original, loaded map[string]any) {
	t.Helper()
	want := cloneJSONMap(t, original)
	got := cloneJSONMap(t, loaded)
	delete(want, "schema_version")
	delete(want, "saved_at")
	delete(got, "schema_version")
	delete(got, "saved_at")
	if !bytes.Equal(mustJSON(t, want), mustJSON(t, got)) {
		t.Fatalf("workspace JSON 保存加载不一致\nwant=%s\ngot=%s", mustJSON(t, want), mustJSON(t, got))
	}
}

func TestWeaponWorkspaceEffectEmptyListsAreExplicit(t *testing.T) {
	key := "253999"
	state := &weaponState{
		StageEffects: map[string]map[int][]StageEffect{key: {2011: {{Kind: "effect", EffectID: "100011", Start: 1, End: 1}}}},
		EffectRows:   map[string][]EffectRow{key: {{EffectID: "100011", File: "100011"}}},
	}
	payload := map[string]any{
		"weapon":        map[string]any{"id": 253999},
		"stage_effects": map[string][]StageEffect{"2012": {}},
		"effect_rows":   []EffectRow{},
	}
	if err := mergeWorkspaceIntoState(payload, key, state); err != nil {
		t.Fatal(err)
	}
	if got, ok := state.EffectRows[key]; !ok || got == nil || len(got) != 0 {
		t.Fatalf("空 effect_rows 必须保留权威空 slice：%#v", state.EffectRows[key])
	}
	if _, ok := state.StageEffects[key][2011]; !ok {
		t.Fatal("缺失 stage 未继承既有状态")
	}
	if got, ok := state.StageEffects[key][2012]; !ok || got == nil || len(got) != 0 {
		t.Fatalf("空 stage_effects 必须清除目标状态并保留空 slice：%#v", state.StageEffects[key][2012])
	}
}

func assertWorkspaceEffectFields(t *testing.T, payload map[string]any, stage string, want []StageEffect, rows []EffectRow) {
	t.Helper()
	var gotStage map[string][]StageEffect
	encoded, err := json.Marshal(payload["stage_effects"])
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(encoded, &gotStage); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotStage[stage], want) {
		t.Fatalf("stage_effects 保存加载值不一致：got=%+v want=%+v", gotStage[stage], want)
	}
	var gotRows []EffectRow
	encoded, err = json.Marshal(payload["effect_rows"])
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(encoded, &gotRows); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotRows, rows) {
		t.Fatalf("effect_rows 保存加载值不一致：got=%+v want=%+v", gotRows, rows)
	}
}

func assertFinalEffectValues(t *testing.T, a *archive, key, stage string, want []StageEffect, rows []EffectRow) {
	t.Helper()
	action := workspaceActionForState(mustArchiveText(t, a, "itemact.txt"), key, stage)
	if action == "" {
		t.Fatalf("最终克隆缺少特效状态 %s", stage)
	}
	blocks := actionVariantsFromText(t, mustArchiveText(t, a, "animation/"+action[:4]+".xml"), action)
	plain := 0
	conditioned := 0
	for _, block := range blocks {
		if conditionUstate(block) == "" {
			plain++
		} else {
			conditioned++
		}
	}
	if plain != 1 || conditioned == 0 {
		t.Fatalf("特效目标动作 %s 的无条件/条件块数量异常：plain=%d conditioned=%d", action, plain, conditioned)
	}
	var target *xmlNode
	for _, block := range blocks {
		if conditionUstate(block) == "" {
			target = block
			break
		}
	}
	for _, effect := range want {
		tag := map[string]string{"effect": "Effect", "hit": "HitEffect"}[effect.Kind]
		found := false
		target.walk(func(node *xmlNode) {
			if found || node.tag != tag || node.get("effectid") != effect.EffectID {
				return
			}
			if effect.Kind == "effect" && node.get("frame") != strconv.Itoa(effect.Start) {
				return
			}
			if effect.Kind == "hit" && (node.get("startframe") != strconv.Itoa(effect.Start) || node.get("endframe") != strconv.Itoa(effect.End)) {
				return
			}
			for attrKey, attrValue := range map[string]string{"bindtype": effect.BindType, "bindindex": effect.BindIndex, "break": effect.Break} {
				if attrValue != "" && node.get(attrKey) != attrValue {
					return
				}
			}
			found = true
		})
		if !found {
			t.Fatalf("最终动作缺少 %s 特效：%+v", effect.Kind, effect)
		}
	}
	block := regexp.MustCompile(`(?s)<WeaponEffect\b[^>]*ItemID\s*=\s*"` + regexp.QuoteMeta(key) + `"[^>]*>.*?</WeaponEffect\s*>`).FindString(mustArchiveText(t, a, "acteffect.xml"))
	if block == "" {
		t.Fatalf("最终 acteffect 缺少 %s", key)
	}
	for _, row := range rows {
		if !strings.Contains(block, `EffectId = "`+row.EffectID+`"`) || !strings.Contains(block, `File = "`+row.File+`"`) {
			t.Fatalf("最终 acteffect 缺少精确登记 %+v：%s", row, block)
		}
	}
}

func (n *xmlNode) serializeMust(t *testing.T) string {
	t.Helper()
	text, err := n.serialize()
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func assertWorkspaceFields(t *testing.T, state *weaponState, key string, stage *Stage, stageKey, segmentID, propertyID, propertyKey string, propertyValue float64, frameAttrs, counterAttrs, counterBox, scopeAttrs, guardAttrs []FrameSwitchAttr, variant VariantEdit, chain ComboTransition, combo ComboRuleSet) {
	t.Helper()
	rules := state.Drafts[key]
	wantRule := Rule{Stage: stage.Stage, Buff: 0, Level: 1, Duration: 3000, Properties: map[string]map[string]float64{propertyID: {propertyKey: propertyValue}}}
	if !reflect.DeepEqual(rules, []Rule{wantRule}) {
		t.Fatalf("rules/properties merge 错误：%+v", rules)
	}
	stateNumber := mustStateColumn(t, stageKey)
	if got := state.Variants[key][stateNumber]; len(got) != 1 || !reflect.DeepEqual(got[0], variant) {
		t.Fatalf("variants merge 错误：%+v", got)
	}
	if got := state.FrameSwitches[key][stateNumber]; !sameFrameSwitches(got, []FrameSwitch{{Attrs: frameAttrs}}) {
		t.Fatalf("frame merge 错误：%+v", got)
	}
	if got := state.Counters[key][stateNumber]; got == nil || !sameAttrs(got.Attrs, counterAttrs) || !sameAttrs(got.Box, counterBox) {
		t.Fatalf("counter merge 错误：%+v", got)
	}
	if got := state.Scopes[key][stateNumber][segmentID]; !sameAttrs(got, scopeAttrs) {
		t.Fatalf("scope merge 错误：%+v", got)
	}
	if got := state.BlockElements[key][stateNumber]["FakeUnAttack"]; len(got) != 1 || got[0].Tag != "FakeUnAttack" || !sameAttrs(got[0].Attrs, guardAttrs) {
		t.Fatalf("防护 merge 错误：%+v", got)
	}
	if len(state.Chains[key]) != 1 || state.Chains[key][0] != chain {
		t.Fatalf("chain merge 错误：%+v", state.Chains[key])
	}
	if got := state.ComboRules[key]; len(got.Max) != 1 || len(got.Black) != 1 || len(got.White) != 1 || got.Max[0] != combo.Max[0] || got.Black[0] != combo.Black[0] || got.White[0] != combo.White[0] {
		t.Fatalf("comboRule merge 错误：%+v", got)
	}
}

func assertFinalCloneValues(t *testing.T, a *archive, key string, sourceStage *Stage, action, segmentID, propertyID, propertyKey string, propertyValue float64, frameAttrs, counterAttrs, counterBox, scopeAttrs, guardAttrs []FrameSwitchAttr, variant VariantEdit) {
	t.Helper()
	itemText := mustArchiveText(t, a, "item.txt")
	if _, ok := tabRowOf(itemText, 1, key); !ok {
		t.Fatalf("最终 item.txt 缺少克隆武器 %s", key)
	}
	actionText := mustArchiveText(t, a, "itemact.txt")
	row := actionRowIndex(actionText)[key]
	if row == nil {
		t.Fatalf("最终 itemact.txt 缺少克隆武器 %s", key)
	}
	outputAction := workspaceActionForState(actionText, key, sourceStage.State)
	if outputAction == "" {
		t.Fatal("最终克隆动作号为空")
	}
	blockText := mustArchiveText(t, a, "animation/"+outputAction[:4]+".xml")
	blocks := actionVariantsFromText(t, blockText, outputAction)
	if len(blocks) == 0 {
		t.Fatalf("最终动作文件没有动作块 %s", outputAction)
	}
	baseBlock := blocks[0]
	assertXMLAttrs(t, baseBlock, "CustomStateSwitch", frameAttrs)
	assertXMLAttrs(t, baseBlock, "Counter", counterAttrs)
	assertXMLChildAttrs(t, baseBlock, "Counter", "TriggerBox", counterBox)
	assertXMLAttrs(t, baseBlock, "FakeUnAttack", guardAttrs)
	assertXMLChildAttrs(t, baseBlock, "Anm", "AttackScope", scopeAttrs)
	assertXMLAttrOnID(t, baseBlock, "Anm", "id", segmentID, "AttackScope", scopeAttrs)
	variantFound := false
	for _, block := range blocks {
		if hasCondition(block, variant.Condition) {
			variantFound = true
			break
		}
	}
	if !variantFound {
		t.Fatalf("最终动作块缺少 variant condition=%d", variant.Condition)
	}
	clonePropertyID := xmlAttrOnID(baseBlock, "Anm", "id", segmentID, "skillproid")
	if clonePropertyID == "" {
		t.Fatalf("克隆动作 Anm %s 缺少具体 skillproid", segmentID)
	}
	propertyText := mustArchiveText(t, a, "skillproperty.xml")
	gotProperty, ok := propertyAttrExact(propertyText, clonePropertyID, propertyKey)
	if !ok {
		t.Fatalf("最终 skillproperty.xml 缺少克隆 property %s/%s", clonePropertyID, propertyKey)
	}
	if gotProperty != formatNumber(propertyValue) {
		t.Fatalf("最终克隆 property %s 未保留 %s=%v，实际=%s", clonePropertyID, propertyKey, propertyValue, gotProperty)
	}
}

func assertWorkspaceFinalRegistrations(t *testing.T, a *archive, key string, stage *Stage, segmentID, oldID string, chain ComboTransition, variant VariantEdit) {
	t.Helper()
	action := workspaceActionForState(mustArchiveText(t, a, "itemact.txt"), key, stage.State)
	blocks := actionVariantsFromText(t, mustArchiveText(t, a, "animation/"+action[:4]+".xml"), action)
	newID := xmlAttrOnID(blocks[0], "Anm", "id", segmentID, "skillproid")
	if newID == "" || newID == oldID {
		t.Fatalf("完整协调路径未产生重编号：%s -> %s", oldID, newID)
	}
	root, err := parseXML(mustArchiveText(t, a, "comborule.xml"))
	if err != nil {
		t.Fatal(err)
	}
	var rules []*xmlNode
	root.walk(func(n *xmlNode) {
		if n.tag == "ComboRule" && n.get("Weapon") == key {
			rules = append(rules, n)
		}
	})
	if len(rules) != 1 {
		t.Fatalf("克隆 ComboRule 数量=%d，期望1", len(rules))
	}
	wantRules := map[string][]FrameSwitchAttr{
		"MaxComboForSkill": {{Key: "Skill", Value: newID}, {Key: "MaxCombo", Value: "2"}, {Key: "ExceedState", Value: stage.State}, {Key: "ExceedSkillProID", Value: newID}},
		"BlackListItem":    {{Key: "PrevSkill", Value: newID}, {Key: "CurSkill", Value: newID}},
		"WhiteListItem":    {{Key: "PrevSkill", Value: newID}, {Key: "CurSkill", Value: newID}},
	}
	count := 0
	for _, child := range rules[0].children {
		if child.comment {
			continue
		}
		want, ok := wantRules[child.tag]
		if !ok || !attrsEqual(child, want) {
			t.Fatalf("克隆 ComboRule %s 属性不匹配：%+v", child.tag, child.attrs)
		}
		delete(wantRules, child.tag)
		count++
	}
	if count != 3 || len(wantRules) != 0 {
		t.Fatal("克隆 ComboRule 子项缺失或重复")
	}
	root, err = parseXML(mustArchiveText(t, a, "delayacttable.xml"))
	if err != nil {
		t.Fatal(err)
	}
	count = 0
	root.walk(func(n *xmlNode) {
		if n.tag == "Item" && n.get("WeaponTypeId") == key {
			count++
			if !attrsEqual(n, []FrameSwitchAttr{{Key: "WeaponTypeId", Value: key}, {Key: "OldState", Value: chain.OldState}, {Key: "NewState", Value: chain.NewState}, {Key: "KeyInput", Value: chain.KeyInput}, {Key: "StartPart", Value: chain.StartPart}}) {
				t.Fatalf("克隆 delayacttable 字段不匹配：%+v", n.attrs)
			}
		}
	})
	if count != 1 {
		t.Fatalf("克隆连招行数量=%d，期望1", count)
	}
	var branch *xmlNode
	for _, block := range blocks {
		if hasCondition(block, variant.Condition) {
			if branch != nil {
				t.Fatal("自定义 variant 条件块重复")
			}
			branch = block
		}
	}
	if branch == nil {
		t.Fatal("自定义 variant 条件块缺失")
	}
	var segments []*xmlNode
	for _, child := range branch.children {
		if child.tag == "Anm" {
			segments = append(segments, child)
		}
	}
	if len(segments) != len(variant.Segments) {
		t.Fatalf("自定义 variant 段数量=%d", len(segments))
	}
	seen := map[string]bool{newID: true}
	properties := mustArchiveText(t, a, "skillproperty.xml")
	propertyRoot, err := parseXML(properties)
	if err != nil {
		t.Fatal(err)
	}
	propertyCounts := map[string]int{}
	propertyRoot.walk(func(n *xmlNode) {
		if n.tag == "PropertyItem" {
			propertyCounts[n.get("SkillProId")]++
		}
	})
	for i, want := range variant.Segments {
		n := segments[i]
		for field, value := range map[string]string{"id": want.AnmID, "name": want.Name, "startframe": strconv.Itoa(want.Start), "endframe": strconv.Itoa(want.End)} {
			if n.get(field) != value {
				t.Fatalf("variant段%d %s=%q，期望%q", i, field, n.get(field), value)
			}
		}
		replay := n.get("replaytimes")
		if (want.ReplayTimes > 0 && replay != strconv.Itoa(want.ReplayTimes)) || (want.ReplayTimes == 0 && replay != "" && replay != "0") {
			t.Fatalf("variant段%d replaytimes=%q", i, replay)
		}
		id := n.get("skillproid")
		number, err := strconv.Atoi(id)
		if err != nil || number < variantSkillProPrefix || number >= variantSkillProLimit || seen[id] || id == oldID {
			t.Fatalf("variant段%d 命中编号不独立或不合法：%s", i, id)
		}
		seen[id] = true
		if propertyCounts[id] != 1 {
			t.Fatalf("variant段%d 命中属性%s 注册数量=%d，期望1", i, id, propertyCounts[id])
		}
		value, ok := propertyAttrExact(properties, id, "SkillDamage")
		if !ok || value != formatNumber(want.Damage) {
			t.Fatalf("variant段%d 引用property %s 的SkillDamage=%q，期望%v", i, id, value, want.Damage)
		}
	}
	t.Logf("完整协调及最终登记通过：命中ID %s -> %s；自定义variant命中段和卡帧段均已核对", oldID, newID)
}

func assertDonorOutputUnchanged(t *testing.T, before, out *archive, donor *Weapon, state string) {
	t.Helper()
	for _, name := range []string{"item.txt", "itemact.txt", "skillproperty.xml"} {
		beforeRaw, err := before.raw(name)
		if err != nil {
			t.Fatal(err)
		}
		afterRaw, err := out.raw(name)
		if err != nil {
			t.Fatal(err)
		}
		if name == "skillproperty.xml" {
			checked := map[string]bool{}
			for _, donorStage := range donor.Stages {
				for _, propertyID := range donorStage.PropertyIDs {
					beforeNode, beforeOK := propertyNodeText(string(beforeRaw), propertyID)
					afterNode, afterOK := propertyNodeText(string(afterRaw), propertyID)
					if !beforeOK || !afterOK || beforeNode != afterNode {
						t.Fatalf("供体 property %s 缺失或被污染", propertyID)
					}
					checked[propertyID] = true
				}
			}
			if len(checked) == 0 {
				t.Fatal("没有供体 property 可用于隔离断言")
			}
			continue
		}
		if name == "itemact.txt" {
			beforeRow := actionRowIndex(string(beforeRaw))[strconv.Itoa(donor.ID)]
			afterRow := actionRowIndex(string(afterRaw))[strconv.Itoa(donor.ID)]
			if strings.Join(beforeRow, "\t") != strings.Join(afterRow, "\t") {
				t.Fatalf("供体 itemact 行被污染")
			}
			continue
		}
		if name == "item.txt" {
			beforeRow, beforeOK := tabRowOf(string(beforeRaw), 1, strconv.Itoa(donor.ID))
			afterRow, afterOK := tabRowOf(string(afterRaw), 1, strconv.Itoa(donor.ID))
			if !beforeOK || !afterOK || beforeRow != afterRow {
				t.Fatalf("供体 item.txt 行被污染")
			}
			continue
		}
	}
	donorAction := ""
	for _, donorStage := range donor.Stages {
		if donorStage.State == state {
			donorAction = donorStage.Action
			break
		}
	}
	if donorAction != "" && len(donorAction) >= 4 {
		beforeBlocks := before.groupBlockTexts(donorAction[:4])
		afterBlocks := out.groupBlockTexts(donorAction[:4])
		for _, want := range beforeBlocks {
			found := false
			for _, got := range afterBlocks {
				if got == want {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("供体动作块 %s 在输出归档中被污染", donorAction)
			}
		}
	}
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func pickWorkspaceEffectStage(t *testing.T, a *archive, w *Weapon) (*Stage, string, string, []StageEffect) {
	t.Helper()
	registered := []string{}
	blockIndex, _, err := stageEffectIndex(a)
	if err != nil {
		t.Fatal(err)
	}
	for _, match := range regexp.MustCompile(`<EffectFile\b[^>]*EffectId\s*=\s*"([^"]+)"`).FindAllStringSubmatch(mustArchiveText(t, a, "acteffect.xml"), -1) {
		registered = append(registered, match[1])
	}
	if len(registered) < 2 {
		t.Fatal("真实 acteffect.xml 可用登记少于两条")
	}
	for i := range w.Stages {
		stage := &w.Stages[i]
		if !stage.Supported || stage.Action == "" || stage.Action == "0" {
			continue
		}
		blocks := actionVariantsFromText(t, mustArchiveText(t, a, "animation/"+stage.Action[:4]+".xml"), stage.Action)
		for _, block := range blocks {
			if conditionUstate(block) != "" {
				continue
			}
			var effects []StageEffect
			var hasEffect, hasHit bool
			block.walk(func(node *xmlNode) {
				switch node.tag {
				case "Effect":
					effects = append(effects, StageEffect{Kind: "effect", EffectID: node.get("effectid"), Start: mustAttrInt(t, node, "frame"), End: mustAttrInt(t, node, "frame"), BindType: node.get("bindtype"), BindIndex: node.get("bindindex"), Break: node.get("break")})
					hasEffect = true
				case "HitEffect":
					effects = append(effects, StageEffect{Kind: "hit", EffectID: node.get("effectid"), Start: mustAttrInt(t, node, "startframe"), End: mustAttrInt(t, node, "endframe"), BindType: node.get("bindtype"), BindIndex: node.get("bindindex"), Break: node.get("break")})
					hasHit = true
				}
			})
			if hasEffect && hasHit && len(effects) > 0 && len(blockIndex[actionKey(stage.Action)]) == 1 {
				t.Logf("选中特效动作 stage=%s action=%s blocks=%d", stage.State, stage.Action, len(blockIndex[actionKey(stage.Action)]))
				for i := range effects {
					if i < len(registered) {
						effects[i].EffectID = registered[i]
					}
				}
				return stage, stage.Action, firstSegmentWithProperty(t, block), effects
			}
		}
	}
	t.Fatal("真实 253521 没有无条件且同时含 Effect/HitEffect 的动作块")
	return nil, "", "", nil
}

func firstSegmentWithProperty(t *testing.T, block *xmlNode) string {
	t.Helper()
	var id string
	block.walk(func(node *xmlNode) {
		if id == "" && node.tag == "Anm" && node.get("skillproid") != "" && len(childrenByTag(node, "AttackScope")) > 0 {
			id = node.get("id")
		}
	})
	if id == "" {
		t.Fatal("动作块没有同时含命中属性和 AttackScope 的片段")
	}
	return id
}

func childrenByTag(node *xmlNode, tag string) []*xmlNode {
	result := []*xmlNode{}
	for _, child := range node.children {
		if child.tag == tag {
			result = append(result, child)
		}
	}
	return result
}

func mustAttrInt(t *testing.T, node *xmlNode, key string) int {
	t.Helper()
	value, err := strconv.Atoi(strings.TrimSpace(node.get(key)))
	if err != nil {
		t.Fatalf("特效字段 %s 无效：%q", key, node.get(key))
	}
	return value
}

func realEffectRowsForStage(t *testing.T, a *archive, effects []StageEffect) []EffectRow {
	t.Helper()
	text := mustArchiveText(t, a, "acteffect.xml")
	files := map[string]string{}
	for _, match := range regexp.MustCompile(`<EffectFile\b[^>]*EffectId\s*=\s*"([^"]+)"[^>]*File\s*=\s*"([^"]+)"`).FindAllStringSubmatch(text, -1) {
		files[match[1]] = match[2]
	}
	rows := make([]EffectRow, 0, len(effects))
	seen := map[string]bool{}
	for _, effect := range effects {
		if seen[effect.EffectID] {
			continue
		}
		file := files[effect.EffectID]
		if file == "" {
			t.Fatalf("真实 acteffect.xml 缺少动作特效 %s 的资源登记", effect.EffectID)
		}
		rows = append(rows, EffectRow{EffectID: effect.EffectID, File: file})
		seen[effect.EffectID] = true
	}
	return rows
}

func pickWorkspaceStage(t *testing.T, a *archive, w *Weapon) (*Stage, string, string) {
	t.Helper()
	for i := range w.Stages {
		stage := &w.Stages[i]
		if !stage.Supported || stage.Action == "" || stage.Action == "0" {
			continue
		}
		blocks := a.groupBlockTexts(stage.Action[:4])
		if len(blocks) == 0 {
			continue
		}
		node, err := parseXML(blocks[0])
		if err != nil {
			continue
		}
		segments := segmentsOf(node, "")
		for _, segment := range segments {
			if segment.SkillPro != "" && len(segment.Scope) > 0 {
				return stage, stage.Action, segment.ID
			}
		}
	}

	t.Fatal("253521 克隆没有可用动作块")
	return nil, "", ""
}

func workspaceActionForState(text, weapon, state string) string {
	header := itemactHeader(text)
	row := actionRowIndex(text)[weapon]
	for i, name := range header {
		if strings.TrimSpace(name) == strings.TrimSpace(state) && i < len(row) {
			return strings.TrimSpace(row[i])
		}
	}
	return ""
}

func actionVariantsFromText(t *testing.T, text, action string) []*xmlNode {
	t.Helper()
	key := actionKey(action)
	id := key[strings.IndexByte(key, '/')+1:]
	root, err := parseXML(text)
	if err != nil {
		t.Fatal(err)
	}
	result := []*xmlNode{}
	root.walk(func(n *xmlNode) {
		if n.tag == "AnmDesc" && strings.TrimSpace(n.get("id")) == id {
			result = append(result, n)
		}
	})
	return result
}

func hasCondition(node *xmlNode, condition int) bool {
	want := strconv.Itoa(condition)
	for _, child := range node.children {
		if child.tag != "Condition" {
			continue
		}
		for _, state := range child.children {
			if state.tag == "Ustate" && strings.TrimSpace(state.get("id")) == want {
				return true
			}
		}
	}
	return false
}

func assertXMLAttrs(t *testing.T, node *xmlNode, tag string, attrs []FrameSwitchAttr) {
	t.Helper()
	found := false
	node.walk(func(n *xmlNode) {
		if n.tag == tag && !found && attrsEqual(n, attrs) {
			found = true
		}
	})
	if !found {
		t.Fatalf("动作块缺少 %s 的精确属性 %+v", tag, attrs)
	}
}

func assertXMLChildAttrs(t *testing.T, node *xmlNode, parentTag, childTag string, attrs []FrameSwitchAttr) {
	t.Helper()
	found := false
	node.walk(func(n *xmlNode) {
		if n.tag != parentTag || found {
			return
		}
		for _, child := range n.children {
			if child.tag == childTag && attrsEqual(child, attrs) {
				found = true
			}
		}
	})
	if !found {
		t.Fatalf("动作块缺少 %s/%s 的精确属性 %+v", parentTag, childTag, attrs)
	}
}

func xmlAttrOnID(node *xmlNode, tag, idKey, id, attr string) string {
	var value string
	node.walk(func(n *xmlNode) {
		if value == "" && n.tag == tag && strings.TrimSpace(n.get(idKey)) == id {
			value = strings.TrimSpace(n.get(attr))
		}
	})
	return value
}

func assertXMLAttrOnID(t *testing.T, node *xmlNode, tag, idKey, id, childTag string, attrs []FrameSwitchAttr) {
	t.Helper()
	found := false
	node.walk(func(n *xmlNode) {
		if n.tag != tag || strings.TrimSpace(n.get(idKey)) != id || found {
			return
		}
		for _, child := range n.children {
			if child.tag == childTag && attrsEqual(child, attrs) {
				found = true
			}
		}
	})
	if !found {
		t.Fatalf("Anm %s 缺少 %s 的精确属性 %+v", id, childTag, attrs)
	}
}

func attrsEqual(node *xmlNode, want []FrameSwitchAttr) bool {
	if len(node.attrs) != len(want) {
		return false
	}
	for _, attr := range want {
		if strings.TrimSpace(node.get(attr.Key)) != attr.Value {
			return false
		}
	}
	return true
}

func sameAttrs(a, b []FrameSwitchAttr) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameFrameSwitches(a, b []FrameSwitch) bool {
	if len(a) != len(b) || len(a) == 0 {
		return len(a) == len(b)
	}
	return sameAttrs(a[0].Attrs, b[0].Attrs)
}

func changedArchiveEntries(t *testing.T, before, after *archive) map[string]bool {
	t.Helper()
	changed := map[string]bool{}
	for name := range before.entries {
		oldRaw, err := before.raw(name)
		if err != nil {
			t.Fatal(err)
		}
		newRaw, err := after.raw(name)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(oldRaw, newRaw) {
			changed[name] = true
		}
	}
	for name := range after.entries {
		if _, exists := before.entries[name]; !exists {
			changed[name] = true
		}
	}
	if len(changed) == 0 {
		t.Fatal("prepare 没有产生任何归档改动")
	}
	return changed
}

func strictChangedXML(t *testing.T, before, after *archive, changed map[string]bool) {
	t.Helper()
	python, err := filepath.Abs(workspaceTestPython)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(python); err != nil {
		t.Fatalf("指定 Python 不存在：%s：%v", python, err)
	}
	root := t.TempDir()
	paths := []string{}
	for name := range changed {
		if !strings.HasSuffix(strings.ToLower(name), ".xml") {
			continue
		}
		raw, err := after.raw(name)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, fmt.Sprintf("%03d.xml", len(paths)))
		if err = os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	if len(paths) == 0 {
		t.Fatal("没有新增或改变的 XML 条目可校验")
	}
	script := "import sys, xml.etree.ElementTree as ET\nfor p in sys.argv[1:]:\n    raw = open(p, 'rb').read()\n    text = raw.decode('gb18030')\n    ET.fromstring(text)\n"
	cmd := exec.Command(python, append([]string{"-c", script}, paths...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("新增/改变 XML 的 ElementTree 严格校验失败：%v\n%s", err, output)
	}
	t.Logf("ElementTree 严格校验通过：%d 个新增/改变 XML 条目", len(paths))
}

func reportOriginalInvalidXML(t *testing.T, a *archive) {
	t.Helper()
	python, err := filepath.Abs(workspaceTestPython)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(python); err != nil {
		t.Fatalf("指定 Python 不存在：%s：%v", python, err)
	}
	root := t.TempDir()
	paths := []string{}
	names := []string{}
	for name := range a.entries {
		if !strings.HasSuffix(strings.ToLower(name), ".xml") {
			continue
		}
		raw, err := a.raw(name)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, fmt.Sprintf("%03d.xml", len(paths)))
		if err = os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
		names = append(names, name)
	}
	script := "import sys, json, xml.etree.ElementTree as ET\nfor p in json.load(sys.stdin):\n    try:\n        ET.fromstring(open(p, 'rb').read().decode('gb18030'))\n    except Exception as e:\n        print(p + ': ' + type(e).__name__ + ': ' + str(e))\n"
	cmd := exec.Command(python, "-c", script)
	cmd.Stdin = bytes.NewReader(mustJSON(t, paths))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("原始 XML 诊断进程失败：%v\n%s", err, output)
	}
	if len(output) == 0 {
		t.Logf("原归档 %d 个 XML 条目通过 ElementTree 严格校验", len(paths))
		return
	}
	diagnostic := string(output)
	for i, path := range paths {
		diagnostic = strings.ReplaceAll(diagnostic, path, names[i])
	}
	t.Logf("供体归档原有 XML 严格校验问题（仅报告，不归咎本次改动）：%s", strings.TrimSpace(diagnostic))
}

func archiveSnapshot(t *testing.T, a *archive) map[string][]byte {
	t.Helper()
	result := map[string][]byte{}
	for name := range a.entries {
		raw, err := a.raw(name)
		if err != nil {
			t.Fatal(err)
		}
		result[name] = raw
	}
	return result
}

func cloneJSONMap(t *testing.T, value map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err = json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func formatNumber(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func propertyAttrExact(text, propertyID, key string) (string, bool) {
	node, ok := propertyNodeText(text, propertyID)
	if !ok {
		return "", false
	}
	parsed, err := parseXML(node)
	if err != nil {
		return "", false
	}
	return parsed.get(key), parsed.get(key) != ""
}

func findWeapon(info *inspection, id int) *Weapon {
	for i := range info.weapons {
		if info.weapons[i].ID == id {
			return &info.weapons[i]
		}
	}
	return nil
}

func freeWorkspaceCloneID(a *archive, start int) int {
	ids := weaponIDsOf(a)
	for id := start; ; id++ {
		if !ids[strconv.Itoa(id)] {
			return id
		}
	}
}

func mustStateColumn(t *testing.T, state string) int {
	t.Helper()
	value, err := strconv.Atoi(state)
	if err != nil {
		t.Fatalf("状态 %q 不是数字列：%v", state, err)
	}
	return value
}

func firstValidRuleProperty(stage Stage, allowed map[string][]int) (string, string, float64) {
	_ = allowed
	if len(stage.PropertyIDs) == 0 {
		return "", "SkillDamage", 12.5
	}
	return stage.PropertyIDs[0], "SkillDamage", 12.5
}

func mustArchiveText(t *testing.T, a *archive, name string) string {
	t.Helper()
	text, err := a.text(name)
	if err != nil {
		t.Fatal(err)
	}
	return text
}
