package desktop

import (
	"fmt"
	"sort"
	"strings"
)

// HitProperty is the canonical in-memory representation of one skillproperty
// node. Legacy fields (rules.properties, variants and extra_properties) remain
// accepted at the boundary, but new code should resolve the node through this
// map so an ID and its references cannot drift apart.
type HitProperty struct {
	ID           string             `json:"id"`
	TemplateID   string             `json:"template_id,omitempty"`
	OwnerWeapon  string             `json:"owner_weapon,omitempty"`
	State        int                `json:"state,omitempty"`
	Action       string             `json:"action,omitempty"`
	UState       int                `json:"ustate,omitempty"`
	Buff         int                `json:"buff,omitempty"`
	Level        float64            `json:"level,omitempty"`
	Duration     int                `json:"duration,omitempty"`
	Condition    int                `json:"condition,omitempty"`
	SegmentID    string             `json:"segment_id,omitempty"`
	Source       string             `json:"source,omitempty"`
	Values       map[string]float64 `json:"values,omitempty"`
	References   []HitPropertyRef   `json:"references,omitempty"`
	Preallocated bool               `json:"preallocated,omitempty"`
	Removed      bool               `json:"removed,omitempty"`
}

type HitPropertyRef struct {
	Weapon    string `json:"weapon"`
	State     int    `json:"state,omitempty"`
	Action    string `json:"action,omitempty"`
	Condition int    `json:"condition,omitempty"`
	Segment   string `json:"segment,omitempty"`
	Kind      string `json:"kind,omitempty"`
}

func (p *HitProperty) normalize(id string) {
	if p.ID == "" {
		p.ID = strings.TrimSpace(id)
	}
	p.ID = strings.TrimSpace(p.ID)
	p.TemplateID = strings.TrimSpace(p.TemplateID)
	p.OwnerWeapon = strings.TrimSpace(p.OwnerWeapon)
	p.Action = strings.TrimSpace(p.Action)
	p.SegmentID = strings.TrimSpace(p.SegmentID)
	p.Source = strings.TrimSpace(p.Source)
	if p.Values == nil {
		p.Values = map[string]float64{}
	} else {
		allowed := map[string]bool{}
		for _, field := range propertyFields {
			allowed[field.Key] = true
		}
		clean := make(map[string]float64, len(p.Values))
		for key, value := range p.Values {
			if allowed[key] {
				clean[key] = value
			}
		}
		p.Values = clean
	}
}

func (p HitProperty) validate(key string) error {
	p.normalize(key)
	if p.ID == "" {
		return fmt.Errorf("统一命中属性缺少 id")
	}
	if p.OwnerWeapon != "" && key != "" && p.OwnerWeapon != key {
		return fmt.Errorf("命中属性 %s 属于武器 %s，不能挂到武器 %s", p.ID, p.OwnerWeapon, key)
	}
	for _, ref := range p.References {
		if strings.TrimSpace(ref.Weapon) == "" {
			return fmt.Errorf("命中属性 %s 存在空引用武器", p.ID)
		}
	}
	return nil
}

// normalizeHitProperties builds the canonical map from the current legacy
// state. It is deliberately deterministic and idempotent, so it can be called
// after workspace merge, before validation, and before rendering.
func normalizeHitProperties(state *weaponState) error {
	if state == nil {
		return nil
	}
	if state.HitProperties == nil {
		state.HitProperties = map[string]HitProperty{}
	}
	explicit := make(map[string]HitProperty, len(state.HitProperties))
	for id, property := range state.HitProperties {
		explicit[id] = property
	}
	for id, property := range state.HitProperties {
		property.normalize(id)
		state.HitProperties[id] = property
	}
	for id, legacy := range state.ExtraProperties {
		property := HitProperty{
			ID:          id,
			TemplateID:  legacy.Template,
			OwnerWeapon: legacy.OwnerWeapon,
			Source:      "extra_property",
		}
		property.normalize(id)
		if err := property.validate(property.OwnerWeapon); err != nil {
			return err
		}
		mergeHitProperty(state.HitProperties, property)
	}
	for weapon, stages := range state.Remaps {
		for stage, remap := range stages {
			if remap == nil || strings.TrimSpace(remap.PropertyID) == "" {
				continue
			}
			id := strings.TrimSpace(remap.PropertyID)
			property := state.HitProperties[id]
			property.ID = id
			if property.OwnerWeapon == "" {
				property.OwnerWeapon = weapon
			}
			if property.State == 0 {
				property.State = stage
			}
			if property.Action == "" {
				property.Action = strings.TrimSpace(remap.Action)
			}
			if property.Source == "" {
				property.Source = "remap"
			}
			property.normalize(id)
			property.References = appendUniqueHitRef(property.References, HitPropertyRef{
				Weapon: weapon, State: stage, Action: property.Action, Kind: "remap",
			})
			state.HitProperties[id] = property
		}
	}
	for weapon, stages := range state.Variants {
		for stage, edits := range stages {
			for _, edit := range edits {
				if edit.Remove {
					continue
				}
				for index, segment := range edit.Segments {
					id := strings.TrimSpace(segment.SkillProID)
					if id == "" {
						continue
					}
					property := state.HitProperties[id]
					property.ID = id
					if property.OwnerWeapon == "" {
						property.OwnerWeapon = weapon
					}
					if property.State == 0 {
						property.State = stage
					}
					if property.Condition == 0 {
						property.Condition = edit.Condition
					}
					if property.Action == "" {
						property.Action = strings.TrimSpace(segment.Name)
					}
					if property.SegmentID == "" {
						property.SegmentID = strings.TrimSpace(segment.AnmID)
					}
					if property.TemplateID == "" {
						property.TemplateID = strings.TrimSpace(segment.TemplateSkillProID)
					}
					if property.Source == "" {
						property.Source = "variant"
					}
					property.Preallocated = property.Preallocated || strings.HasPrefix(id, "910")
					property.normalize(id)
					property.References = appendUniqueHitRef(property.References, HitPropertyRef{
						Weapon: weapon, State: stage, Action: property.Action,
						Condition: edit.Condition, Segment: property.SegmentID,
						Kind: fmt.Sprintf("variant:%d", index),
					})
					state.HitProperties[id] = property
				}
			}
		}
	}
	for weapon, rules := range state.Drafts {
		for _, rule := range rules {
			for id, values := range rule.Properties {
				property := state.HitProperties[id]
				property.ID = id
				if property.OwnerWeapon == "" {
					property.OwnerWeapon = weapon
				}
				if property.State == 0 {
					property.State = rule.Stage
				}
				if len(property.Values) == 0 {
					property.Values = copyFloatMap(values)
				}
				if property.Source == "" {
					property.Source = "rule"
				}
				property.normalize(id)
				property.References = appendUniqueHitRef(property.References, HitPropertyRef{
					Weapon: weapon, State: rule.Stage, Kind: "rule",
				})
				state.HitProperties[id] = property
			}
		}
	}
	for id, property := range explicit {
		property.normalize(id)
		property.References = appendUniqueHitRefs(property.References, state.HitProperties[id].References...)
		state.HitProperties[id] = property
	}
	for id, property := range state.HitProperties {
		property.normalize(id)
		if err := property.validate(property.OwnerWeapon); err != nil {
			return err
		}
		state.HitProperties[id] = property
	}
	return nil
}

// projectHitPropertiesToLegacy keeps the existing XML writers as a compatibility
// backend while making HitProperties the authoritative edit-value source.
func projectHitPropertiesToLegacy(state *weaponState) {
	if state == nil {
		return
	}
	for _, ruleSet := range []map[string][]Rule{state.Drafts, state.Applied} {
		for weapon, rules := range ruleSet {
			for index := range rules {
				for id := range rules[index].Properties {
					property, exists := state.HitProperties[id]
					if !exists || property.Removed || !hitPropertyReferencesRule(property, weapon, rules[index].Stage) {
						delete(rules[index].Properties, id)
					}
				}
				for id, property := range state.HitProperties {
					if property.Removed || !hitPropertyReferencesRule(property, weapon, rules[index].Stage) {
						continue
					}
					if len(property.Values) > 0 {
						if rules[index].Properties == nil {
							rules[index].Properties = map[string]map[string]float64{}
						}
						rules[index].Properties[id] = copyFloatMap(property.Values)
					}
					if property.Buff != 0 || property.Level != 0 || property.Duration != 0 {
						rules[index].Buff = property.Buff
						rules[index].Level = property.Level
						rules[index].Duration = property.Duration
					}
				}
			}
			ruleSet[weapon] = rules
		}
	}
}

func hitPropertyReferencesWeapon(property HitProperty, weapon string) bool {
	if property.OwnerWeapon == weapon {
		return true
	}
	for _, ref := range property.References {
		if ref.Weapon == weapon {
			return true
		}
	}
	return false
}

func hitPropertyReferencesRule(property HitProperty, weapon string, stage int) bool {
	for _, ref := range property.References {
		if ref.Weapon == weapon && ref.State == stage {
			return true
		}
	}
	return property.OwnerWeapon == weapon && property.State == stage
}

// deleteHitProperty is the single removal operation for an editable property.
// It updates the canonical map and every legacy projection still consumed by
// the current renderers. Removing an ID therefore cannot leave a stale XML
// reference behind on the next apply.
func renameHitProperty(state *weaponState, oldID, newID string) error {
	if state == nil || strings.TrimSpace(oldID) == "" || strings.TrimSpace(newID) == "" || oldID == newID {
		return nil
	}
	oldID = strings.TrimSpace(oldID)
	newID = strings.TrimSpace(newID)
	property, exists := state.HitProperties[oldID]
	// Retain rendered clone IDs when their source property is renamed.
	for weapon, clones := range state.PropertyClones {
		if exists && property.OwnerWeapon != "" && !hitPropertyReferencesWeapon(property, weapon) {
			continue
		}
		for reference, id := range clones {
			stage, source, ok := strings.Cut(reference, "|")
			if !ok || source != oldID {
				continue
			}
			if pinned := clones[stage+"|"+newID]; pinned != "" && pinned != id {
				return fmt.Errorf("命中属性克隆映射冲突：武器 %s，%s -> %s", weapon, oldID, newID)
			}
		}
	}
	if exists {
		if _, occupied := state.HitProperties[newID]; occupied {
			return fmt.Errorf("统一命中属性 ID 冲突：%s -> %s", oldID, newID)
		}
		property.ID = newID
		state.HitProperties[newID] = property
		delete(state.HitProperties, oldID)
		if extra, ok := state.ExtraProperties[oldID]; ok {
			state.ExtraProperties[newID] = extra
			delete(state.ExtraProperties, oldID)
		}
	}
	for weapon, clones := range state.PropertyClones {
		if exists && property.OwnerWeapon != "" && !hitPropertyReferencesWeapon(property, weapon) {
			continue
		}
		for reference, id := range clones {
			stage, source, ok := strings.Cut(reference, "|")
			if ok && source == oldID {
				clones[stage+"|"+newID] = id
				delete(clones, reference)
			}
		}
	}
	for _, stages := range state.Remaps {
		for _, remap := range stages {
			if remap != nil && strings.TrimSpace(remap.PropertyID) == oldID {
				remap.PropertyID = newID
			}
		}
	}
	for _, ruleSets := range []map[string][]Rule{state.Drafts, state.Applied} {
		for weapon, rules := range ruleSets {
			for index := range rules {
				if values, ok := rules[index].Properties[oldID]; ok {
					delete(rules[index].Properties, oldID)
					if rules[index].Properties == nil {
						rules[index].Properties = map[string]map[string]float64{}
					}
					rules[index].Properties[newID] = values
				}
			}
			ruleSets[weapon] = rules
		}
	}
	for _, stages := range state.Variants {
		for _, edits := range stages {
			for editIndex := range edits {
				for segmentIndex := range edits[editIndex].Segments {
					if strings.TrimSpace(edits[editIndex].Segments[segmentIndex].SkillProID) == oldID {
						edits[editIndex].Segments[segmentIndex].SkillProID = newID
					}
				}
			}
		}
	}
	return nil
}

func deleteHitProperty(state *weaponState, id string) {
	if state == nil || strings.TrimSpace(id) == "" {
		return
	}
	id = strings.TrimSpace(id)
	delete(state.HitProperties, id)
	delete(state.ExtraProperties, id)
	for key, stages := range state.Remaps {
		for stage, remap := range stages {
			if remap != nil && strings.TrimSpace(remap.PropertyID) == id {
				remap.PropertyID = ""
				stages[stage] = remap
			}
		}
		state.Remaps[key] = stages
	}
	for _, ruleSets := range []map[string][]Rule{state.Drafts, state.Applied} {
		for weapon, rules := range ruleSets {
			for index := range rules {
				delete(rules[index].Properties, id)
			}
			ruleSets[weapon] = rules
		}
	}
	for weapon, stages := range state.Variants {
		for stage, edits := range stages {
			for editIndex := range edits {
				for segmentIndex := range edits[editIndex].Segments {
					if strings.TrimSpace(edits[editIndex].Segments[segmentIndex].SkillProID) == id {
						// 删除命中属性不应删除动画段；保留纯动作段。
						edits[editIndex].Segments[segmentIndex].SkillProID = ""
						edits[editIndex].Segments[segmentIndex].TemplateSkillProID = ""
						edits[editIndex].Segments[segmentIndex].Damage = 0
					}
				}
			}
			stages[stage] = edits
		}
		state.Variants[weapon] = stages
	}
}

func mergeHitProperty(dst map[string]HitProperty, property HitProperty) {
	previous, exists := dst[property.ID]
	if !exists {
		dst[property.ID] = property
		return
	}
	if previous.TemplateID == "" {
		previous.TemplateID = property.TemplateID
	}
	if previous.OwnerWeapon == "" {
		previous.OwnerWeapon = property.OwnerWeapon
	}
	if previous.Source == "" {
		previous.Source = property.Source
	}
	if len(previous.Values) == 0 && len(property.Values) > 0 {
		previous.Values = copyFloatMap(property.Values)
	}
	previous.References = appendUniqueHitRefs(previous.References, property.References...)
	previous.Preallocated = previous.Preallocated || property.Preallocated
	previous.Removed = previous.Removed && property.Removed
	dst[property.ID] = previous
}

func appendUniqueHitRef(refs []HitPropertyRef, ref HitPropertyRef) []HitPropertyRef {
	for _, existing := range refs {
		if existing == ref {
			return refs
		}
	}
	return append(refs, ref)
}

func appendUniqueHitRefs(refs []HitPropertyRef, additions ...HitPropertyRef) []HitPropertyRef {
	for _, ref := range additions {
		refs = appendUniqueHitRef(refs, ref)
	}
	return refs
}

func copyFloatMap(source map[string]float64) map[string]float64 {
	if source == nil {
		return nil
	}
	result := make(map[string]float64, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func hitPropertyIDs(state *weaponState) []string {
	if state == nil || len(state.HitProperties) == 0 {
		return nil
	}
	ids := make([]string, 0, len(state.HitProperties))
	for id, property := range state.HitProperties {
		if !property.Removed {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}
