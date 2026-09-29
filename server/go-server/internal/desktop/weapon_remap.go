package desktop

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// The weapon editor treats the state machine (itemact.txt's 107 columns) as
// read-only and only tunes the numeric fields of a cloned hit-property node.
// This file opens the *structure*: per state, the author may choose which
// action (animation block) plays there, and which hit-property node (skillproid
// from skillproperty.xml) that action points at — plus author entirely new
// hit-property nodes from a template. States themselves are never renamed:
// delayacttable.xml transitions address them by number.

// StageRemap overrides one state of one weapon.
type StageRemap struct {
	Action     string `json:"action,omitempty"`
	PropertyID string `json:"property_id,omitempty"`
	Label      string `json:"label,omitempty"`
}

// ExtraProperty is a hit-property node owned by the editor: a copy of a
// template node, emitted under a fresh SkillProId whenever the configuration is
// written.
type ExtraProperty struct {
	Template string `json:"template"`
}

var (
	skillPropertyEndPattern = regexp.MustCompile(`</SkillProperty\s*>`)
	anmInfoEndPattern       = regexp.MustCompile(`</AnmInfo\s*>`)
)

// ruleStageOf converts an itemact state column into the stage number used by
// saved rules and the stage list: the standing-attack columns 2011..2016 are
// shown as stages 1..6, every other column keeps its own id. Remaps are keyed
// by the raw column, rules by the stage number — this is the bridge.
func ruleStageOf(state int) int {
	if state >= 2011 && state <= 2016 {
		return state - 2010
	}
	return state
}

// applyRemaps rewires the chosen states: itemact cells for action remaps,
// AnmDesc skillproid for property remaps (cloning a shared block first), and
// extra property nodes cloned from their template. States registered as
// cleared are zeroed out entirely. It runs after blueprints and combo tables
// so the rest of the render pipeline sees the final structure.
func applyRemaps(a *archive, state *weaponState, items []Item) (*archive, error) {
	if len(state.Remaps) == 0 && len(state.ExtraProperties) == 0 && len(state.Cleared) == 0 {
		return a, nil
	}
	info, err := inspect(a, items)
	if err != nil {
		return nil, err
	}
	actionText, err := a.text("itemact.txt")
	if err != nil {
		return nil, err
	}
	actionLines := strings.Split(actionText, "\n")
	header := strings.Split(strings.TrimSuffix(actionLines[0], "\r"), "\t")
	columns := map[string]int{}
	for index, name := range header {
		columns[name] = index
	}
	rowIndex := map[string]int{}
	for index, line := range actionLines[1:] {
		cols := strings.Split(strings.TrimSuffix(line, "\r"), "\t")
		if len(cols) >= 1 {
			rowIndex[cols[0]] = index + 1
		}
	}
	reserved := map[string]bool{}
	for key := range info.blocks {
		reserved[key] = true
	}

	animations := map[string]string{}
	loadAnimation := func(file string) (string, error) {
		if text, ok := animations[file]; ok {
			return text, nil
		}
		text, err := a.text(file)
		if err != nil {
			return "", err
		}
		animations[file] = text
		return text, nil
	}

	propertyClones := []string{}
	tableChanged := false

	// Extra hit-property nodes, emitted first and sorted for determinism.
	extraIDs := make([]string, 0, len(state.ExtraProperties))
	for id := range state.ExtraProperties {
		extraIDs = append(extraIDs, id)
	}
	sort.Strings(extraIDs)
	for _, id := range extraIDs {
		extra := state.ExtraProperties[id]
		nodes := info.properties[extra.Template]
		if len(nodes) != 1 {
			return nil, fmt.Errorf("命中属性模板 %s 不存在或不唯一", extra.Template)
		}
		clone := nodes[0].clone()
		clone.set("SkillProId", id)
		encoded, err := clone.serialize()
		if err != nil {
			return nil, err
		}
		propertyClones = append(propertyClones, encoded)
	}

	// Per-weapon remaps. Cleared states come first: the column is zeroed and
	// any remap registered for it is ignored (the state was deleted).
	weaponKeys := map[string]bool{}
	for key := range state.Remaps {
		weaponKeys[key] = true
	}
	for key := range state.Cleared {
		weaponKeys[key] = true
	}
	sorted := make([]string, 0, len(weaponKeys))
	for key := range weaponKeys {
		sorted = append(sorted, key)
	}
	sort.Strings(sorted)
	for _, weaponKey := range sorted {
		line := rowIndex[weaponKey]
		if line == 0 {
			return nil, fmt.Errorf("武器 %s 不在动作表中", weaponKey)
		}
		ending := ""
		if strings.HasSuffix(actionLines[line], "\r") {
			ending = "\r"
		}
		row := strings.Split(strings.TrimSuffix(actionLines[line], "\r"), "\t")
		for _, column := range sortedIntKeys(state.Cleared[weaponKey]) {
			if index, ok := columns[strconv.Itoa(column)]; ok && index < len(row) {
				row[index] = "0"
				tableChanged = true
			}
		}
		stages := make([]int, 0, len(state.Remaps[weaponKey]))
		for stage := range state.Remaps[weaponKey] {
			if state.Cleared[weaponKey][stage] {
				continue
			}
			stages = append(stages, stage)
		}
		sort.Ints(stages)
		for _, stage := range stages {
			remap := state.Remaps[weaponKey][stage]
			column, ok := columns[strconv.Itoa(stage)]
			if !ok || column >= len(row) {
				return nil, fmt.Errorf("状态 %d 不存在", stage)
			}
			action := row[column]
			if remap.Action != "" {
				action = remap.Action
			}
			if len(info.blocks[actionKey(action)]) == 0 {
				return nil, fmt.Errorf("动作 %s 不存在", action)
			}
			if remap.Action != "" && action != row[column] {
				row[column] = action
				tableChanged = true
			}
			if remap.PropertyID != "" {
				if len(info.properties[remap.PropertyID]) != 1 {
					return nil, fmt.Errorf("命中属性 %s 不存在或不唯一", remap.PropertyID)
				}
				block := info.blocks[actionKey(action)][0]
				file := "animation/" + action[:4] + ".xml"
				animation, err := loadAnimation(file)
				if err != nil {
					return nil, err
				}
				changed := block.node.clone()
				shared := len(info.owners[action]) > 1
				if shared {
					cloneID := 0
					for id := 999; id >= 1; id-- {
						key := action[:4] + "/" + strconv.Itoa(id)
						if !reserved[key] {
							cloneID = id
							reserved[key] = true
							break
						}
					}
					if cloneID == 0 {
						return nil, fmt.Errorf("%s 独立动作编号空间不足", file)
					}
					changed.set("id", strconv.Itoa(cloneID))
					action = action[:4] + fmt.Sprintf("%03d", cloneID)
					row[column] = action
					tableChanged = true
				}
				changed.walk(func(node *xmlNode) {
					node.set("skillproid", remap.PropertyID)
				})
				encoded, err := changed.serialize()
				if err != nil {
					return nil, err
				}
				if shared {
					loc := anmInfoEndPattern.FindAllStringIndex(animation, -1)
					if len(loc) != 1 {
						return nil, fmt.Errorf("动作表结构错误")
					}
					animation = anmInfoEndPattern.ReplaceAllStringFunc(animation, func(string) string {
						return "\n" + encoded + "\n</AnmInfo>"
					})
				} else {
					target := block.original
					if strings.Count(animation, target) != 1 {
						// An earlier stage of this same pass may already have
						// rewritten this very block (two states can remap onto
						// one action), so the pristine text is gone: match by id.
						found, ok := currentBlock(animation, strings.TrimSpace(block.node.get("id")))
						if !ok {
							return nil, fmt.Errorf("动作定义无法唯一替换")
						}
						target = found
					}
					animation = strings.Replace(animation, target, encoded, 1)
				}
				animations[file] = animation
			}
		}
		actionLines[line] = strings.Join(row, "\t") + ending
	}

	if len(propertyClones) == 0 && !tableChanged && len(animations) == 0 {
		return a, nil
	}
	replacements := map[string][]byte{}
	if tableChanged {
		encoded, err := encodeText(strings.Join(actionLines, "\n"))
		if err != nil {
			return nil, err
		}
		replacements["itemact.txt"] = encoded
	}
	for file, animation := range animations {
		if _, err := parseXML(animation); err != nil {
			return nil, fmt.Errorf("%s：%w", file, err)
		}
		encoded, err := encodeText(animation)
		if err != nil {
			return nil, err
		}
		replacements[file] = encoded
	}
	if len(propertyClones) > 0 {
		properties, err := a.text("skillproperty.xml")
		if err != nil {
			return nil, err
		}
		loc := skillPropertyEndPattern.FindAllStringIndex(properties, -1)
		if len(loc) != 1 {
			return nil, fmt.Errorf("技能属性表结构错误")
		}
		properties = skillPropertyEndPattern.ReplaceAllStringFunc(properties, func(string) string {
			return "\n" + strings.Join(propertyClones, "\n") + "\n</SkillProperty>"
		})
		if _, err := parseXML(properties); err != nil {
			return nil, fmt.Errorf("技能属性表结构错误：%w", err)
		}
		encoded, err := encodeText(properties)
		if err != nil {
			return nil, err
		}
		replacements["skillproperty.xml"] = encoded
	}
	data, err := a.replace(replacements)
	if err != nil {
		return nil, err
	}
	return parseArchive(data)
}

// freshPropertyID returns an unused SkillProId in a range that never collides
// with the render pipeline's own clones (900000000+) or with shipped ids.
func freshPropertyID(info *inspection) string {
	used := map[string]bool{}
	for id := range info.properties {
		used[id] = true
	}
	for id := 800000001; id <= 899999999; id++ {
		key := strconv.Itoa(id)
		if !used[key] {
			return key
		}
	}
	return ""
}

// actionCatalog lists every action the client can play, each with a human
// label, so the author can pick an animation for a state.
func actionCatalog(info *inspection) []map[string]string {
	keys := make([]string, 0, len(info.blocks))
	for key := range info.blocks {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := []map[string]string{}
	for _, key := range keys {
		blocks := info.blocks[key]
		prefix := key[:strings.IndexByte(key, '/')]
		id := key[strings.IndexByte(key, '/')+1:]
		action := prefix + id
		label := action
		if len(blocks) == 1 {
			if description := actionDescription(blocks[0].node); description != "" {
				label = description
			}
		}
		result = append(result, map[string]string{"id": action, "label": label})
	}
	return result
}

// propertyCatalog lists every hit-property node with a compact summary so the
// author can pick (or template) a 招式 node.
func propertyCatalog(info *inspection) []map[string]string {
	ids := make([]string, 0, len(info.properties))
	for id := range info.properties {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := []map[string]string{}
	for _, id := range ids {
		nodes := info.properties[id]
		if len(nodes) != 1 {
			continue
		}
		node := nodes[0]
		summary := strings.Join([]string{
			"伤害 " + orZero(node.get("SkillDamage")),
			"BUFF " + orZero(node.get("UnNormalState")),
			"目标 " + targetLabel(node.get("TargetEnemy"), node.get("TargetSelf")),
		}, " · ")
		result = append(result, map[string]string{"id": id, "summary": summary})
	}
	return result
}

func orZero(value string) string {
	if value == "" {
		return "0"
	}
	return value
}

func targetLabel(enemy, self string) string {
	if enemy == "1" && self == "0" {
		return "敌方"
	}
	if enemy == "0" && self == "1" {
		return "自身"
	}
	if enemy == "1" && self == "1" {
		return "敌我"
	}
	return "其他"
}

// actionForState returns the action id a weapon plays in a given state.
func actionForState(a *archive, weaponID string, stage int) (string, error) {
	text, err := a.text("itemact.txt")
	if err != nil {
		return "", err
	}
	lines := strings.Split(text, "\n")
	header := strings.Split(strings.TrimSuffix(lines[0], "\r"), "\t")
	column := -1
	for index, name := range header {
		if name == strconv.Itoa(stage) {
			column = index
			break
		}
	}
	if column < 0 {
		return "", fmt.Errorf("状态 %d 不存在", stage)
	}
	for _, line := range lines[1:] {
		cols := strings.Split(strings.TrimSuffix(line, "\r"), "\t")
		if len(cols) > column && cols[0] == weaponID {
			return cols[column], nil
		}
	}
	return "", fmt.Errorf("武器 %s 不在动作表中", weaponID)
}

// propertyIDsOfAction lists the hit-property nodes an action references.
func propertyIDsOfAction(info *inspection, action string) []string {
	refs := map[string]bool{}
	for _, block := range info.blocks[actionKey(action)] {
		block.node.walk(func(node *xmlNode) {
			if ref := node.get("skillproid"); ref != "" && ref != "0" {
				refs[ref] = true
			}
		})
	}
	ids := make([]string, 0, len(refs))
	for id := range refs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// validateRemap checks that the target state exists and the chosen action and
// hit-property id resolve to exactly one block/node.
func validateRemap(a *archive, info *inspection, weaponKey string, stage int, action, propertyID string) error {
	if _, err := actionForState(a, weaponKey, stage); err != nil {
		return err
	}
	if action != "" && len(info.blocks[actionKey(action)]) == 0 {
		return fmt.Errorf("动作 %s 不存在", action)
	}
	if propertyID != "" && len(info.properties[propertyID]) != 1 {
		return fmt.Errorf("命中属性 %s 不存在或不唯一", propertyID)
	}
	return nil
}

// resolveTemplate returns the action and (single) hit property a donor weapon
// plays in a donor state, for reuse by another weapon's state.
func resolveTemplate(a *archive, info *inspection, donorWeapon string, donorStage int) (string, string, error) {
	action, err := actionForState(a, donorWeapon, donorStage)
	if err != nil {
		return "", "", err
	}
	if action == "" || action == "0" {
		return "", "", fmt.Errorf("模板状态 %d 没有配置动作", donorStage)
	}
	propertyID := ""
	if ids := propertyIDsOfAction(info, action); len(ids) == 1 {
		propertyID = ids[0]
	}
	return action, propertyID, nil
}

// itemactStates lists the fixed state-machine columns (e.g. 2011..5005), so the
// editor can also surface empty states a new weapon has not filled yet. The
// first two columns (weapon id and internal name) are not states.
func itemactStates(a *archive) []string {
	text, err := a.text("itemact.txt")
	if err != nil {
		return nil
	}
	lines := strings.Split(text, "\n")
	if len(lines) == 0 {
		return nil
	}
	header := strings.Split(strings.TrimSuffix(lines[0], "\r"), "\t")
	if len(header) < 2 {
		return nil
	}
	return header[2:]
}

// clearStageRule drops a state's saved edits after a remap changed its action
// or hit property: those edits referenced the old nodes and would otherwise
// fail validateRules against the remapped structure. When the action itself
// changed the buff goes too, because the new action may have no hit property
// to attach it to. The stage is an itemact column; rules use ruleStageOf.
func clearStageRule(state *weaponState, weaponKey string, stage int, resetBuff bool) {
	ruleStage := ruleStageOf(stage)
	for _, rules := range []map[string][]Rule{state.Drafts, state.Applied} {
		list := rules[weaponKey]
		for i := range list {
			if list[i].Stage == ruleStage {
				list[i].Properties = nil
				if resetBuff {
					list[i].Buff = 0
					list[i].Level = 1
					list[i].Duration = 3000
				}
			}
		}
	}
}

// sortedIntKeys lists a bool-map's keys in order (cleared states).
func sortedIntKeys(source map[int]bool) []int {
	keys := make([]int, 0, len(source))
	for key := range source {
		keys = append(keys, key)
	}
	sort.Ints(keys)
	return keys
}

// pruneStaleRules drops saved edits that cannot survive the current structure:
// rules for stages the weapon no longer plays, hit-property edits for nodes
// that no longer belong to their stage after a remap, and rules whose stage
// is not editable at all. Without this, one stale draft blocks every apply.
func pruneStaleRules(state *weaponState, info *inspection) {
	weapons := map[string]*Weapon{}
	for index := range info.weapons {
		weapons[strconv.Itoa(info.weapons[index].ID)] = &info.weapons[index]
	}
	for _, rules := range []map[string][]Rule{state.Drafts, state.Applied} {
		for key, list := range rules {
			weapon, ok := weapons[key]
			if !ok {
				delete(rules, key)
				continue
			}
			stages := map[int]Stage{}
			for _, stage := range weapon.Stages {
				stages[stage.Stage] = stage
			}
			kept := make([]Rule, 0, len(list))
			for _, rule := range list {
				stage, exists := stages[rule.Stage]
				if !exists || !stage.Supported {
					continue
				}
				for ref := range rule.Properties {
					if !includes(stage.PropertyIDs, ref) {
						delete(rule.Properties, ref)
					}
				}
				if !includes(weapon.BuffIDs, rule.Buff) {
					rule.Buff = 0
				}
				if rule.Buff == 0 && len(rule.Properties) == 0 {
					continue
				}
				kept = append(kept, rule)
			}
			if len(kept) == 0 {
				delete(rules, key)
			} else {
				rules[key] = kept
			}
		}
	}
}

// overlayRemapLabels lets an author name each state of a self-made weapon:
// when a remap carries a label it wins over whatever the combo tips say.
func overlayRemapLabels(state *weaponState, info *inspection) {
	for index := range info.weapons {
		per := state.Remaps[strconv.Itoa(info.weapons[index].ID)]
		if per == nil {
			continue
		}
		for s := range info.weapons[index].Stages {
			stage := &info.weapons[index].Stages[s]
			column, err := strconv.Atoi(stage.State)
			if err != nil {
				continue
			}
			if remap := per[column]; remap != nil && strings.TrimSpace(remap.Label) != "" {
				stage.Label = strings.TrimSpace(remap.Label)
			}
		}
	}
}

// iconSize is the shipped item-icon dimension: every standard PNG in
// Picture/ItemIcon is 80x80 RGBA. The client's UI texture loader predates
// rescale-on-load, so a larger upload fails to render exactly like a broken
// filename does.
const iconSize = 80

// normalizeIcon decodes a PNG and box-filters it into an 80x80 RGBA icon,
// matching the shipped convention. Images already at the target size are
// re-encoded as-is. Box filtering is the ideal downscale for large factors:
// every target pixel is the plain average of its source footprint.
func normalizeIcon(data []byte, size int) ([]byte, error) {
	src, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("解析 PNG 失败：%w", err)
	}
	bounds := src.Bounds()
	if bounds.Dx() == size && bounds.Dy() == size {
		flat := image.NewRGBA(bounds)
		draw.Draw(flat, flat.Bounds(), src, bounds.Min, draw.Src)
		return encodeIconRGBA(flat)
	}
	flat := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(flat, flat.Bounds(), src, bounds.Min, draw.Src)
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		sy0, sy1 := y*bounds.Dy()/size, (y+1)*bounds.Dy()/size
		if sy1 <= sy0 {
			sy1 = sy0 + 1
		}
		for x := 0; x < size; x++ {
			sx0, sx1 := x*bounds.Dx()/size, (x+1)*bounds.Dx()/size
			if sx1 <= sx0 {
				sx1 = sx0 + 1
			}
			// Alpha-weighted average: un-premultiply per sample so semi
			// transparent edges do not darken against a phantom black.
			var r, g, bl, a float64
			for sy := sy0; sy < sy1; sy++ {
				row := flat.Pix[sy*flat.Stride:]
				for sx := sx0; sx < sx1; sx++ {
					off := sx * 4
					w := float64(row[off+3]) / 255
					r += float64(row[off]) * w
					g += float64(row[off+1]) * w
					bl += float64(row[off+2]) * w
					a += w
				}
			}
			n := float64((sy1 - sy0) * (sx1 - sx0))
			off := dst.PixOffset(x, y)
			if a > 0 {
				dst.Pix[off] = clampByte(r / a)
				dst.Pix[off+1] = clampByte(g / a)
				dst.Pix[off+2] = clampByte(bl / a)
			}
			dst.Pix[off+3] = clampByte(a / n * 255)
		}
	}
	return encodeIconRGBA(dst)
}

// encodeIconRGBA writes the icon as colortype 6 (truecolor + alpha), the
// format every shipped icon uses. Go's encoder silently drops the alpha
// channel for fully opaque images, so nudge one pixel's alpha to keep the
// client loader on its expected code path.
func encodeIconRGBA(img *image.RGBA) ([]byte, error) {
	if img.Opaque() {
		img.Pix[img.PixOffset(0, 0)+3] = 254
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func clampByte(v float64) byte {
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return byte(v + 0.5)
}

// uploadWeaponIcon copies a local PNG into the client's item-icon directory so
// a self-made weapon can carry a custom picture. The returned path is relative
// to Data/UI and goes straight into item.txt's icon column.
func uploadWeaponIcon(client, sourcePath string) (map[string]any, error) {
	source := strings.TrimSpace(sourcePath)
	if source == "" {
		return nil, fmt.Errorf("请选择本地图片")
	}
	info, err := os.Stat(source)
	if err != nil || info.IsDir() {
		return nil, fmt.Errorf("本地图片不存在")
	}
	if !strings.EqualFold(filepath.Ext(source), ".png") {
		return nil, fmt.Errorf("仅支持 PNG 图片")
	}
	raw, err := os.ReadFile(source)
	if err != nil {
		return nil, fmt.Errorf("读取图片失败：%w", err)
	}
	// Bring the picture to the shipped icon format first: the hash (and thus
	// the file name) is computed over the normalized bytes, so re-uploading
	// the same picture stays idempotent even if the source resolution differs.
	data, err := normalizeIcon(raw, iconSize)
	if err != nil {
		return nil, err
	}
	name := filepath.Base(source)
	// The client resolves item.txt's icon column with a conservative loader:
	// every shipped name is [A-Za-z0-9_-], and a name carrying spaces,
	// parentheses or CJK (everything a browser download appends) fails to
	// load in game, which then falls back to the donor's picture. Keep a
	// name that already follows the shipped convention; otherwise rename by
	// content hash.
	if !safeIconName(name) {
		sum := sha256.Sum256(data)
		name = "custom_" + hex.EncodeToString(sum[:5]) + ".png"
	}
	dir := filepath.Join(client, "Data", "UI", "Picture", "ItemIcon")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	dest := filepath.Join(dir, name)
	if err = os.WriteFile(dest, data, 0600); err != nil {
		return nil, fmt.Errorf("复制图片失败：%w", err)
	}
	return map[string]any{"icon": "Picture\\ItemIcon\\" + name, "message": "已上传图标 " + name}, nil
}

// safeIconName mirrors the shipped icon naming: plain ASCII letters, digits,
// underscore and dash in the stem, any-case .png extension.
func safeIconName(name string) bool {
	if !strings.EqualFold(filepath.Ext(name), ".png") {
		return false
	}
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	if stem == "" {
		return false
	}
	for _, r := range stem {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}
