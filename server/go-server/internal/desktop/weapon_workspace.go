package desktop

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const weaponWorkspaceSchemaVersion = 1

type workspaceVariantEdit struct {
	VariantEdit
}

func (edit *workspaceVariantEdit) UnmarshalJSON(data []byte) error {
	type variantFields VariantEdit
	var row struct {
		variantFields
		Condition json.RawMessage `json:"condition"`
	}
	if err := json.Unmarshal(data, &row); err != nil {
		return err
	}
	text := string(row.Condition)
	if len(text) > 0 && text[0] == '"' {
		if err := json.Unmarshal(row.Condition, &text); err != nil {
			return err
		}
	}
	condition, err := strconv.Atoi(text)
	if err != nil {
		return fmt.Errorf("动作分支 condition 必须为整数：%q", text)
	}
	edit.VariantEdit = VariantEdit(row.variantFields)
	edit.Condition = condition
	return nil
}

func weaponWorkspaceFolder(folder string, profile ...string) string {
	root := filepath.Join(folder, "workspaces")
	if len(profile) > 0 && strings.TrimSpace(profile[0]) != "" {
		return filepath.Join(root, strings.ToLower(strings.TrimSpace(profile[0])))
	}
	return root
}

func weaponWorkspacePath(folder string, weapon int, profile ...string) string {
	return filepath.Join(weaponWorkspaceFolder(folder, profile...), "weapon-"+strconv.Itoa(weapon)+".json")
}

func validateWeaponProfileRequest(request Request, profile, sourceHash string, required bool) error {
	wantProfile := strings.ToLower(strings.TrimSpace(profile))
	wantHash := strings.ToLower(strings.TrimSpace(sourceHash))
	gotProfile := strings.ToLower(strings.TrimSpace(request.ActiveProfile))
	gotHash := strings.ToLower(strings.TrimSpace(request.SourceHash))
	if gotProfile != "" && gotProfile != wantProfile {
		return fmt.Errorf("武器编辑上下文已切换，请刷新当前客户端配置")
	}
	if gotHash != "" && gotHash != wantHash {
		return fmt.Errorf("客户端 SPF2 已切换，请刷新当前武器配置")
	}
	if required && (gotProfile == "" || gotHash == "") {
		return fmt.Errorf("请求缺少客户端配置档身份，请刷新当前武器配置后重试")
	}
	return nil
}

func withWeaponProfile(result map[string]any, profile, sourceHash string) map[string]any {
	if result == nil {
		result = map[string]any{}
	}
	result["active_profile"] = profile
	result["source_hash"] = sourceHash
	return result
}

func validateWorkspacePayload(weapon int, payload map[string]any) error {
	if weapon <= 0 {
		return fmt.Errorf("请选择有效武器")
	}
	if payload == nil {
		return fmt.Errorf("暂存内容为空")
	}
	value, ok := payload["weapon"]
	if !ok {
		return fmt.Errorf("暂存内容缺少 weapon")
	}
	row, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("暂存内容的 weapon 格式无效")
	}
	var id int
	switch rawID := row["id"].(type) {
	case float64:
		id = int(rawID)
	case int:
		id = rawID
	case int64:
		id = int(rawID)
	case json.Number:
		parsed, err := rawID.Int64()
		if err != nil {
			return fmt.Errorf("暂存武器编号无效")
		}
		id = int(parsed)
	default:
		return fmt.Errorf("暂存武器编号无效")
	}
	if id != weapon {
		return fmt.Errorf("暂存武器编号不匹配：期望 %d", weapon)
	}
	return nil
}

func saveWeaponWorkspace(folder string, weapon int, payload map[string]any, profile ...string) (map[string]any, error) {
	if err := validateWorkspacePayload(weapon, payload); err != nil {
		return nil, err
	}
	payload["schema_version"] = weaponWorkspaceSchemaVersion
	payload["saved_at"] = time.Now().Format(time.RFC3339)
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("序列化武器暂存失败：%w", err)
	}
	path := weaponWorkspacePath(folder, weapon, profile...)
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	if err = atomicWrite(path, raw); err != nil {
		return nil, fmt.Errorf("写入武器暂存失败：%w", err)
	}
	return map[string]any{
		"weapon":   weapon,
		"path":     path,
		"saved_at": payload["saved_at"],
		"bytes":    len(raw),
		"message":  "当前武器暂存已保存",
	}, nil
}

func loadWeaponWorkspace(folder string, weapon int, profile ...string) (map[string]any, error) {
	if weapon <= 0 {
		return nil, fmt.Errorf("请选择有效武器")
	}
	path := weaponWorkspacePath(folder, weapon, profile...)
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{
			"weapon": weapon,
			"exists": false,
		}, nil
	}
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err = json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("读取武器暂存失败：%w", err)
	}
	if err = validateWorkspacePayload(weapon, payload); err != nil {
		return nil, err
	}
	return map[string]any{
		"weapon":  weapon,
		"exists":  true,
		"path":    path,
		"payload": payload,
	}, nil
}

func mergeWorkspaceIntoState(payload map[string]any, key string, state *weaponState) error {
	if payload == nil || state == nil {
		return nil
	}
	hitPropertiesSnapshot := false
	if raw, ok := payload["remaps"]; ok && raw != nil {
		encoded, err := json.Marshal(raw)
		if err != nil {
			return fmt.Errorf("工作区重映射序列化失败：%w", err)
		}
		var grouped map[string]map[int]*StageRemap
		if err = json.Unmarshal(encoded, &grouped); err != nil {
			return fmt.Errorf("工作区重映射格式无效：%w", err)
		}
		if state.Remaps == nil {
			state.Remaps = map[string]map[int]*StageRemap{}
		}
		if perStage, exists := grouped[key]; exists {
			if len(perStage) == 0 {
				delete(state.Remaps, key)
			} else {
				state.Remaps[key] = perStage
			}
		}
	}
	if raw, ok := payload["cleared"]; ok && raw != nil {
		encoded, err := json.Marshal(raw)
		if err != nil {
			return fmt.Errorf("工作区状态清除序列化失败：%w", err)
		}
		var grouped map[string]map[int]bool
		if err = json.Unmarshal(encoded, &grouped); err != nil {
			return fmt.Errorf("工作区状态清除格式无效：%w", err)
		}
		if state.Cleared == nil {
			state.Cleared = map[string]map[int]bool{}
		}
		if perStage, exists := grouped[key]; exists {
			if len(perStage) == 0 {
				delete(state.Cleared, key)
			} else {
				state.Cleared[key] = perStage
			}
		}
	}
	if raw, ok := payload["extra_properties"]; ok && raw != nil {
		encoded, err := json.Marshal(raw)
		if err != nil {
			return fmt.Errorf("工作区新增命中属性序列化失败：%w", err)
		}
		var properties map[string]ExtraProperty
		if err = json.Unmarshal(encoded, &properties); err != nil {
			return fmt.Errorf("工作区新增命中属性格式无效：%w", err)
		}
		if state.ExtraProperties == nil {
			state.ExtraProperties = map[string]ExtraProperty{}
		}
		for id, property := range properties {
			if strings.TrimSpace(id) == "" || strings.TrimSpace(property.Template) == "" {
				return fmt.Errorf("工作区新增命中属性 %q 无效", id)
			}
			if previous, exists := state.ExtraProperties[id]; exists {
				if strings.TrimSpace(previous.Template) != strings.TrimSpace(property.Template) ||
					(previous.OwnerWeapon != "" && property.OwnerWeapon != "" && previous.OwnerWeapon != property.OwnerWeapon) {
					return fmt.Errorf("工作区新增命中属性 %s 已被其他定义或武器占用", id)
				}
				if property.OwnerWeapon == "" {
					property.OwnerWeapon = previous.OwnerWeapon
				}
			}
			state.ExtraProperties[id] = property
		}
	}
	var effectRows []EffectRow
	var stageEffects map[int][]StageEffect
	if raw, ok := payload["effect_rows"]; ok && raw != nil {
		encoded, err := json.Marshal(raw)
		if err != nil {
			return fmt.Errorf("工作区特效登记序列化失败：%w", err)
		}
		if err = json.Unmarshal(encoded, &effectRows); err != nil {
			return fmt.Errorf("工作区特效登记格式无效：%w", err)
		}
		if err = validateEffectRows("", effectRows); err != nil {
			return err
		}
	}
	if raw, ok := payload["stage_effects"]; ok && raw != nil {
		encoded, err := json.Marshal(raw)
		if err != nil {
			return fmt.Errorf("工作区招式特效序列化失败：%w", err)
		}
		var grouped map[string][]StageEffect
		if err = json.Unmarshal(encoded, &grouped); err != nil {
			return fmt.Errorf("工作区招式特效格式无效：%w", err)
		}
		stageEffects = map[int][]StageEffect{}
		seen := map[int]bool{}
		for text, effects := range grouped {
			stage, err := strconv.Atoi(text)
			if err != nil || stage <= 0 {
				return fmt.Errorf("工作区特效状态无效：%s", text)
			}
			if seen[stage] {
				return fmt.Errorf("工作区特效状态重复：%s", text)
			}
			seen[stage] = true
			if effects == nil {
				continue
			}
			if err = validateStageEffectValues(effects); err != nil {
				return err
			}
			stageEffects[stage] = effects
		}
	}
	if raw, ok := payload["rules"]; ok {
		encoded, err := json.Marshal(raw)
		if err != nil {
			return fmt.Errorf("工作区规则序列化失败：%w", err)
		}
		var rules []Rule
		if err = json.Unmarshal(encoded, &rules); err != nil {
			return fmt.Errorf("工作区规则格式无效：%w", err)
		}
		// Older workspace snapshots serialized complete HitProperty objects into
		// rules.properties. Keep only numeric client fields at this legacy writer
		// boundary; canonical hit_properties remains the metadata source.
		allowed := map[string]bool{}
		for _, field := range propertyFields {
			allowed[field.Key] = true
		}
		for ruleIndex := range rules {
			for propertyID, values := range rules[ruleIndex].Properties {
				clean := map[string]float64{}
				for name, value := range values {
					if allowed[name] {
						clean[name] = value
					}
				}
				if len(clean) == 0 {
					delete(rules[ruleIndex].Properties, propertyID)
				} else {
					rules[ruleIndex].Properties[propertyID] = clean
				}
			}
		}
		if state.Drafts == nil {
			state.Drafts = map[string][]Rule{}
		}
		state.Drafts[key] = rules
	}
	if raw, ok := payload["block_elements_saved"]; ok {
		encoded, err := json.Marshal(raw)
		if err != nil {
			return fmt.Errorf("工作区防护元素序列化失败：%w", err)
		}
		var grouped map[string]map[string][]BlockElement
		if err = json.Unmarshal(encoded, &grouped); err != nil {
			return fmt.Errorf("工作区防护元素格式无效：%w", err)
		}
		perStage := map[int]map[string][]BlockElement{}
		for stageText, tags := range grouped {
			stage, err := strconv.Atoi(stageText)
			if err != nil {
				return fmt.Errorf("工作区防护状态无效：%s", stageText)
			}
			perStage[stage] = tags
		}
		if state.BlockElements == nil {
			state.BlockElements = map[string]map[int]map[string][]BlockElement{}
		}
		if len(perStage) == 0 {
			delete(state.BlockElements, key)
		} else {
			state.BlockElements[key] = perStage
		}
	}
	if raw, ok := payload["variants"]; ok {
		encoded, err := json.Marshal(raw)
		if err != nil {
			return fmt.Errorf("工作区动作分支序列化失败：%w", err)
		}
		var legacy map[string][]workspaceVariantEdit
		if err = json.Unmarshal(encoded, &legacy); err != nil {
			return fmt.Errorf("工作区动作分支格式无效：%w", err)
		}
		perStage := map[int][]VariantEdit{}
		for stageText, edits := range legacy {
			stage, err := strconv.Atoi(stageText)
			if err != nil {
				return fmt.Errorf("工作区动作状态无效：%s", stageText)
			}
			for _, edit := range edits {
				perStage[stage] = append(perStage[stage], edit.VariantEdit)
			}
		}
		if state.Variants == nil {
			state.Variants = map[string]map[int][]VariantEdit{}
		}
		if len(perStage) == 0 {
			delete(state.Variants, key)
		} else {
			state.Variants[key] = perStage
		}
	}
	if raw, ok := payload["frame_saved"]; ok {
		encoded, err := json.Marshal(raw)
		if err != nil {
			return fmt.Errorf("工作区帧切换序列化失败：%w", err)
		}
		var grouped map[string][]FrameSwitch
		if err = json.Unmarshal(encoded, &grouped); err != nil {
			return fmt.Errorf("工作区帧切换格式无效：%w", err)
		}
		perStage := map[int]frameSwitchStageEdit{}
		for stageText, edits := range grouped {
			stage, err := strconv.Atoi(stageText)
			if err != nil {
				return fmt.Errorf("工作区帧切换状态无效：%s", stageText)
			}
			perStage[stage] = edits
		}
		if state.FrameSwitches == nil {
			state.FrameSwitches = map[string]map[int]frameSwitchStageEdit{}
		}
		if len(perStage) == 0 {
			delete(state.FrameSwitches, key)
		} else {
			state.FrameSwitches[key] = perStage
		}
	}
	if raw, ok := payload["counter_saved"]; ok {
		encoded, err := json.Marshal(raw)
		if err != nil {
			return fmt.Errorf("工作区招架序列化失败：%w", err)
		}
		var grouped map[string]*CounterEdit
		if err = json.Unmarshal(encoded, &grouped); err != nil {
			return fmt.Errorf("工作区招架格式无效：%w", err)
		}
		perStage := map[int]*CounterEdit{}
		for stageText, edit := range grouped {
			stage, err := strconv.Atoi(stageText)
			if err != nil {
				return fmt.Errorf("工作区招架状态无效：%s", stageText)
			}
			perStage[stage] = edit
		}
		if state.Counters == nil {
			state.Counters = map[string]map[int]*CounterEdit{}
		}
		if len(perStage) == 0 {
			delete(state.Counters, key)
		} else {
			state.Counters[key] = perStage
		}
	}
	if raw, ok := payload["scope_saved"]; ok {
		encoded, err := json.Marshal(raw)
		if err != nil {
			return fmt.Errorf("工作区攻击范围序列化失败：%w", err)
		}
		var grouped map[string]map[string][]FrameSwitchAttr
		if err = json.Unmarshal(encoded, &grouped); err != nil {
			return fmt.Errorf("工作区攻击范围格式无效：%w", err)
		}
		perStage := map[int]map[string][]FrameSwitchAttr{}
		for stageText, segments := range grouped {
			stage, err := strconv.Atoi(stageText)
			if err != nil {
				return fmt.Errorf("工作区攻击范围状态无效：%s", stageText)
			}
			perStage[stage] = segments
		}
		if state.Scopes == nil {
			state.Scopes = map[string]map[int]map[string][]FrameSwitchAttr{}
		}
		if len(perStage) == 0 {
			delete(state.Scopes, key)
		} else {
			state.Scopes[key] = perStage
		}
	}
	if raw, ok := payload["combo_chain"]; ok {
		encoded, err := json.Marshal(raw)
		if err != nil {
			return fmt.Errorf("工作区连招链序列化失败：%w", err)
		}
		var transitions []ComboTransition
		if err = json.Unmarshal(encoded, &transitions); err != nil {
			return fmt.Errorf("工作区连招链格式无效：%w", err)
		}
		if state.Chains == nil {
			state.Chains = map[string][]ComboTransition{}
		}
		if len(transitions) == 0 {
			delete(state.Chains, key)
		} else {
			state.Chains[key] = transitions
		}
	}
	if raw, ok := payload["combo_rule_info"]; ok {
		encoded, err := json.Marshal(raw)
		if err != nil {
			return fmt.Errorf("工作区连招规则序列化失败：%w", err)
		}
		var info struct {
			Rules ComboRuleSet `json:"rules"`
		}
		if err = json.Unmarshal(encoded, &info); err != nil {
			return fmt.Errorf("工作区连招规则格式无效：%w", err)
		}
		// Editor drafts are persisted for restoration, not committed for application.
		if state.ComboRules == nil {
			state.ComboRules = map[string]ComboRuleSet{}
		}
		if info.Rules.empty() {
			delete(state.ComboRules, key)
		} else {
			state.ComboRules[key] = info.Rules
		}
	}
	if raw, ok := payload["hit_properties"]; ok && raw != nil {
		encoded, err := json.Marshal(raw)
		if err != nil {
			return fmt.Errorf("工作区统一命中属性序列化失败：%w", err)
		}
		var properties map[string]HitProperty
		if err = json.Unmarshal(encoded, &properties); err != nil {
			return fmt.Errorf("工作区统一命中属性格式无效：%w", err)
		}
		if err = replaceWorkspaceHitProperties(state, key, properties); err != nil {
			return err
		}
		hitPropertiesSnapshot = true
	}
	if err := normalizeHitProperties(state); err != nil {
		return fmt.Errorf("工作区统一命中属性归一化失败：%w", err)
	}
	if hitPropertiesSnapshot {
		removeWorkspaceHitPropertyRevivals(state, key, payload["hit_properties"])
	}
	if effectRows != nil {
		if state.EffectRows == nil {
			state.EffectRows = map[string][]EffectRow{}
		}
		state.EffectRows[key] = effectRows
	}
	if len(stageEffects) > 0 {
		if state.StageEffects == nil {
			state.StageEffects = map[string]map[int][]StageEffect{}
		}
		if state.StageEffects[key] == nil {
			state.StageEffects[key] = map[int][]StageEffect{}
		}
		for stage, effects := range stageEffects {
			state.StageEffects[key][stage] = effects
		}
	}
	return nil
}

func replaceWorkspaceHitProperties(state *weaponState, key string, incoming map[string]HitProperty) error {
	if state.HitProperties == nil {
		state.HitProperties = map[string]HitProperty{}
	}
	for id, property := range incoming {
		property.normalize(id)
		if property.OwnerWeapon != "" && property.OwnerWeapon != key {
			return fmt.Errorf("工作区统一命中属性 %s 属于武器 %s，不能挂到武器 %s", id, property.OwnerWeapon, key)
		}
		if err := property.validate(key); err != nil {
			return fmt.Errorf("工作区统一命中属性 %s 无效：%w", id, err)
		}
	}
	for id, existing := range state.HitProperties {
		if existing.OwnerWeapon != key && !hitPropertyReferencesWeapon(existing, key) {
			continue
		}
		if _, ok := incoming[id]; !ok {
			deleteHitPropertyForWeapon(state, key, id)
		}
	}
	for id, property := range incoming {
		property.OwnerWeapon = key
		property.normalize(id)
		state.HitProperties[id] = property
	}
	return nil
}

func deleteHitPropertyForWeapon(state *weaponState, weapon, id string) {
	property, ok := state.HitProperties[id]
	if !ok {
		return
	}
	refs := property.References[:0]
	for _, ref := range property.References {
		if ref.Weapon != weapon {
			refs = append(refs, ref)
		}
	}
	property.References = refs
	if property.OwnerWeapon == weapon {
		if len(refs) == 0 {
			delete(state.HitProperties, id)
			delete(state.ExtraProperties, id)
		} else {
			property.OwnerWeapon = refs[0].Weapon
			state.HitProperties[id] = property
		}
	}
	for _, ruleSets := range []map[string][]Rule{state.Drafts, state.Applied} {
		for weaponKey, rules := range ruleSets {
			if weaponKey != weapon {
				continue
			}
			for index := range rules {
				delete(rules[index].Properties, id)
			}
			ruleSets[weaponKey] = rules
		}
	}
	if stages := state.Remaps[weapon]; stages != nil {
		for _, remap := range stages {
			if remap != nil && remap.PropertyID == id {
				remap.PropertyID = ""
			}
		}
	}
	for weaponKey, stages := range state.Variants {
		if weaponKey != weapon {
			continue
		}
		for _, edits := range stages {
			for editIndex := range edits {
				for segmentIndex := range edits[editIndex].Segments {
					if edits[editIndex].Segments[segmentIndex].SkillProID == id {
						edits[editIndex].Segments[segmentIndex].SkillProID = ""
						edits[editIndex].Segments[segmentIndex].TemplateSkillProID = ""
						edits[editIndex].Segments[segmentIndex].Damage = 0
					}
				}
			}
		}
	}
}

func removeWorkspaceHitPropertyRevivals(state *weaponState, key string, raw any) {
	encoded, err := json.Marshal(raw)
	if err != nil {
		return
	}
	var incoming map[string]HitProperty
	if json.Unmarshal(encoded, &incoming) != nil {
		return
	}
	for id, property := range state.HitProperties {
		if property.OwnerWeapon == key || hitPropertyReferencesWeapon(property, key) {
			if _, ok := incoming[id]; !ok {
				deleteHitPropertyForWeapon(state, key, id)
			}
		}
	}
	for id, property := range incoming {
		property.OwnerWeapon = key
		property.normalize(id)
		state.HitProperties[id] = property
	}
}

func deleteWeaponWorkspace(folder string, weapon int, profile ...string) (map[string]any, error) {
	if weapon <= 0 {
		return nil, fmt.Errorf("请选择有效武器")
	}
	path := weaponWorkspacePath(folder, weapon, profile...)
	err := os.Remove(path)
	if os.IsNotExist(err) {
		err = nil
	}
	if err != nil {
		return nil, fmt.Errorf("删除武器暂存失败：%w", err)
	}
	return map[string]any{
		"weapon":  weapon,
		"exists":  false,
		"message": "当前武器暂存已删除",
	}, nil
}
