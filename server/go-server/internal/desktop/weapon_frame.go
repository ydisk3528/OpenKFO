package desktop

// 帧级连招编辑：动作块里的 <CustomStateSwitch> 是 delayacttable 之外的第二条连招
// 通道（动作播放到某几帧时按下某键 → 跳到 nextstate）。以前 GM 只能通过"整块换动作"
// 把它从别人那里带过来，这里让它可增可删，且**不改动原武器**：
//
//   * 动作块被多把武器共用时，先克隆一个独占块（同 applyRemaps 的做法：999 向下取
//     空号、改该武器 itemact 行的列、追加到 </AnmInfo> 之前），原块一个字节不动；
//   * 独占块就地改写，且只在块文本里替换 <CustomStateSwitch>，其余内容原样保留。
//
// 属性用有序 key/value 保存，编辑器不认识的属性（ustatecondition / keystate /
// hittype / skillproid / keyintervalminsec）随条目一起往返，不会被抹掉。

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// FrameSwitchAttr is one attribute of a <CustomStateSwitch>, kept ordered so a
// rewrite neither drops nor resorts what the editor does not model.
type FrameSwitchAttr struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// FrameSwitch is one authored frame-level key switch inside an action block.
type FrameSwitch struct {
	Attrs []FrameSwitchAttr `json:"attrs"`
}

var frameSwitchPattern = regexp.MustCompile(
	`(?s)<CustomStateSwitch\b[^>]*?/\s*>|<CustomStateSwitch\b[^>]*?>\s*</CustomStateSwitch\s*>`)

// frameSwitchKeys is the closed set of attributes the editor may write: the
// shipped data only uses these, so anything else is rejected rather than
// forwarded blindly into the archive.
var frameSwitchKeys = map[string]bool{
	"inputstartframe": true, "inputendframe": true, "keycode": true,
	"keyintervalframe": true, "keyintervalminsec": true,
	"switchstartframe": true, "switchendframe": true, "nextstate": true,
	"ustatecondition": true, "keystate": true, "hittype": true, "skillproid": true,
}

// frameSwitchRequired must be present on every authored switch: without a
// window and a target the client never fires it.
var frameSwitchRequired = []string{"switchstartframe", "switchendframe", "nextstate"}

// Attribute values are emitted into the XML by hand, so restrict them to a
// character set that needs no escaping and cannot break out of the tag.
var frameSwitchSafeValue = regexp.MustCompile(`^[A-Za-z0-9_.\-]*$`)

// frameKeyOptions is the底层按键码 table the client reads in CustomStateSwitch.
// It is a different numbering from delayacttable's KeyInput, so the editor must
// offer exactly these ids.
func frameKeyOptions() []map[string]string {
	return []map[string]string{
		{"value": "7", "label": "X"},
		{"value": "8", "label": "C"},
		{"value": "9", "label": "Z"},
		{"value": "5", "label": "跳"},
		{"value": "20", "label": "前"},
		{"value": "21", "label": "后"},
		{"value": "-7", "label": "松开 X"},
		{"value": "-8", "label": "松开 C"},
		{"value": "-9", "label": "松开 Z"},
		{"value": "-5", "label": "松开 跳"},
		{"value": "-20", "label": "松开 前"},
		{"value": "-21", "label": "松开 后"},
	}
}

// frameSwitchText renders one switch back to XML.
func frameSwitchText(sw FrameSwitch) string {
	parts := []string{"<CustomStateSwitch"}
	for _, attr := range sw.Attrs {
		parts = append(parts, attr.Key+`="`+attr.Value+`"`)
	}
	return strings.Join(parts, " ") + " />"
}

// insideComment reports whether the match starting at offset falls inside a
// <!-- --> region. The shipped data leaves disabled switches commented out as
// planner notes (e.g. 253300 state 2016 carries a commented "接C → 2091"), and
// rewriting one of those would put our edit back inside the comment: it looks
// saved but the client never reads it, and the read side stays empty.
func insideComment(text string, offset int) bool {
	open := strings.LastIndex(text[:offset], "<!--")
	if open < 0 {
		return false
	}
	return strings.LastIndex(text[:offset], "-->") < open
}

// rewriteFrameSwitches replaces every live <CustomStateSwitch> of a block with
// the given list, keeping the position of the first one: sibling order decides
// which of several overlapping windows the client honours, so it must not move.
// Commented-out switches are left exactly as they are.
func rewriteFrameSwitches(block string, fresh []FrameSwitch) (string, bool) {
	matches := frameSwitchPattern.FindAllStringIndex(block, -1)
	locs := make([][]int, 0, len(matches))
	for _, loc := range matches {
		if !insideComment(block, loc[0]) {
			locs = append(locs, loc)
		}
	}
	if len(locs) == 0 && len(fresh) == 0 {
		return block, false
	}
	pieces := make([]string, 0, len(fresh))
	for _, sw := range fresh {
		pieces = append(pieces, frameSwitchText(sw))
	}
	joined := strings.Join(pieces, "\n\t\t")
	if len(locs) == 0 {
		end := strings.LastIndex(block, "</AnmDesc")
		if end < 0 {
			return "", false
		}
		return block[:end] + joined + "\n\t" + block[end:], true
	}
	var out strings.Builder
	last := 0
	for index, loc := range locs {
		out.WriteString(block[last:loc[0]])
		if index == 0 {
			out.WriteString(joined)
		}
		last = loc[1]
	}
	out.WriteString(block[last:])
	return out.String(), true
}

// retitleBlock rewrites the id of an <AnmDesc> open tag, used when a shared
// block is cloned into a private one.
func retitleBlock(block string, id int) string {
	end := strings.Index(block, ">")
	if end < 0 {
		return block
	}
	head := block[:end+1]
	if !anmIDPattern.MatchString(head) {
		return block
	}
	return anmIDPattern.ReplaceAllString(head, `id="`+strconv.Itoa(id)+`"`) + block[end+1:]
}

// frameSwitchStageEdit is one stage's declared list. An empty list is
// meaningful: it means "this stage has no frame switches at all".
type frameSwitchStageEdit = []FrameSwitch

// validateFrameSwitches checks an edit set against the weapon's own action row:
// the state must exist, the target must be a state the client knows, and every
// attribute must be one the shipped data actually uses.
func validateFrameSwitches(a *archive, weaponKey string, edits map[int]frameSwitchStageEdit) error {
	actionText, err := a.text("itemact.txt")
	if err != nil {
		return err
	}
	lines := strings.Split(actionText, "\n")
	// 前两列是武器编号与内部名，不是状态列。
	states := strings.Split(strings.TrimSuffix(lines[0], "\r"), "\t")
	if len(states) < 3 {
		return fmt.Errorf("动作表结构错误")
	}
	header := map[string]bool{}
	for _, state := range states[2:] {
		header[strings.TrimSpace(state)] = true
	}
	row := []string{}
	for _, line := range lines[1:] {
		cols := strings.Split(strings.TrimSuffix(line, "\r"), "\t")
		if len(cols) >= 1 && cols[0] == weaponKey {
			row = cols
			break
		}
	}
	if row == nil {
		return fmt.Errorf("武器 %s 不在本客户端的动作表中", weaponKey)
	}
	stages := make([]int, 0, len(edits))
	for stage := range edits {
		stages = append(stages, stage)
	}
	sort.Ints(stages)
	for _, stage := range stages {
		if !header[strconv.Itoa(stage)] {
			return fmt.Errorf("状态 %d 不在本客户端的动作表里", stage)
		}
		switches := edits[stage]
		for index, sw := range switches {
			position := fmt.Sprintf("状态 %d 第 %d 条", stage, index+1)
			seen := map[string]bool{}
			for _, attr := range sw.Attrs {
				if !frameSwitchKeys[attr.Key] {
					return fmt.Errorf("%s：不支持的属性 %s", position, attr.Key)
				}
				if seen[attr.Key] {
					return fmt.Errorf("%s：属性 %s 重复", position, attr.Key)
				}
				seen[attr.Key] = true
				if !frameSwitchSafeValue.MatchString(attr.Value) {
					return fmt.Errorf("%s：属性 %s 的值 %q 含非法字符", position, attr.Key, attr.Value)
				}
			}
			for _, key := range frameSwitchRequired {
				if !seen[key] {
					return fmt.Errorf("%s：缺少 %s", position, key)
				}
			}
			if err := validateSwitchFrames(position, sw, "inputstartframe", "inputendframe"); err != nil {
				return err
			}
			if err := validateSwitchFrames(position, sw, "switchstartframe", "switchendframe"); err != nil {
				return err
			}
			if keycode := attrValue(sw, "keycode"); keycode != "" {
				for _, part := range strings.Split(keycode, ",") {
					if _, err := strconv.Atoi(strings.TrimPrefix(part, "-")); err != nil {
						return fmt.Errorf("%s：按键码 %q 无效", position, keycode)
					}
				}
			}
			next := attrValue(sw, "nextstate")
			if !header[next] {
				return fmt.Errorf("%s：目标状态 %s 不在动作表里", position, next)
			}
		}
	}
	return nil
}

func attrValue(sw FrameSwitch, key string) string {
	for _, attr := range sw.Attrs {
		if attr.Key == key {
			return attr.Value
		}
	}
	return ""
}

// validateSwitchFrames checks a window pair. The input window is optional in the
// shipped data (it is simply omitted when the whole action accepts the key), so
// a pair that is missing entirely is fine while a half-written one is not.
func validateSwitchFrames(position string, sw FrameSwitch, startKey, endKey string) error {
	startText := attrValue(sw, startKey)
	endText := attrValue(sw, endKey)
	if startText == "" && endText == "" {
		return nil
	}
	if startText == "" || endText == "" {
		return fmt.Errorf("%s：%s 与 %s 必须成对出现", position, startKey, endKey)
	}
	start, err := strconv.Atoi(startText)
	if err != nil || start < 0 || start > 9999 {
		return fmt.Errorf("%s：%s 需要 0..9999 的整数", position, startKey)
	}
	end, err := strconv.Atoi(endText)
	if err != nil || end < 0 || end > 9999 {
		return fmt.Errorf("%s：%s 需要 0..9999 的整数", position, endKey)
	}
	if start > end {
		return fmt.Errorf("%s：%s(%d) 不能大于 %s(%d)", position, startKey, start, endKey, end)
	}
	return nil
}

// applyFrameSwitches renders the authored frame switches onto the archive. It
// runs right after applyRemaps, so a stage that was remapped is already pointing
// at its private block; anything still shared gets cloned here instead of being
// edited in place.
func applyFrameSwitches(a *archive, state *weaponState, items []Item) (*archive, error) {
	if len(state.FrameSwitches) == 0 {
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
	columns := map[string]int{}
	for index, name := range strings.Split(strings.TrimSuffix(actionLines[0], "\r"), "\t") {
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

	tableChanged := false
	weaponKeys := make([]string, 0, len(state.FrameSwitches))
	for key := range state.FrameSwitches {
		weaponKeys = append(weaponKeys, key)
	}
	sort.Strings(weaponKeys)
	for _, weaponKey := range weaponKeys {
		line := rowIndex[weaponKey]
		if line == 0 {
			return nil, fmt.Errorf("武器 %s 不在动作表中", weaponKey)
		}
		ending := ""
		if strings.HasSuffix(actionLines[line], "\r") {
			ending = "\r"
		}
		row := strings.Split(strings.TrimSuffix(actionLines[line], "\r"), "\t")
		sorted := sortedStageKeys(state.FrameSwitches[weaponKey])
		for _, stage := range sorted {
			switches := state.FrameSwitches[weaponKey][stage]
			column, ok := columns[strconv.Itoa(stage)]
			if !ok || column >= len(row) {
				return nil, fmt.Errorf("状态 %d 不属于武器 %s", stage, weaponKey)
			}
			action := row[column]
			if action == "" || action == "0" {
				return nil, fmt.Errorf("状态 %d 没有动作，无法编辑帧级连招", stage)
			}
			blocks := info.blocks[actionKey(action)]
			if len(blocks) != 1 {
				return nil, fmt.Errorf("动作 %s 不存在或不唯一", action)
			}
			file := "animation/" + action[:4] + ".xml"
			animation, err := loadAnimation(file)
			if err != nil {
				return nil, err
			}
			target := blocks[0].original
			if strings.Count(animation, target) != 1 {
				found, ok := currentBlock(animation, strings.TrimSpace(blocks[0].node.get("id")))
				if !ok {
					return nil, fmt.Errorf("动作定义无法唯一替换")
				}
				target = found
			}
			rewritten, changed := rewriteFrameSwitches(target, switches)
			if !changed {
				continue
			}
			if len(info.owners[action]) > 1 {
				// 这一块被别的武器共用：克隆一个私有块再改，原武器不受影响。
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
				clone := retitleBlock(rewritten, cloneID)
				if !anmInfoEndPattern.MatchString(animation) {
					return nil, fmt.Errorf("动作表结构错误")
				}
				animation = anmInfoEndPattern.ReplaceAllStringFunc(animation, func(string) string {
					return "\n" + clone + "\n</AnmInfo>"
				})
				row[column] = action[:4] + fmt.Sprintf("%03d", cloneID)
				tableChanged = true
			} else {
				animation = strings.Replace(animation, target, rewritten, 1)
			}
			animations[file] = animation
		}
		actionLines[line] = strings.Join(row, "\t") + ending
	}

	if !tableChanged && len(animations) == 0 {
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
	data, err := a.replace(replacements)
	if err != nil {
		return nil, err
	}
	return parseArchive(data)
}

// sortedStageKeys lists the stages of one weapon's frame-switch edit in a
// deterministic order.
func sortedStageKeys(source map[int]frameSwitchStageEdit) []int {
	stages := make([]int, 0, len(source))
	for stage := range source {
		stages = append(stages, stage)
	}
	sort.Ints(stages)
	return stages
}

// frameSwitchFiles reports which animation entries an edit set can touch, so the
// write guard lets exactly those through and nothing else.
func frameSwitchFiles(source *archive, state *weaponState) map[string]bool {
	files := map[string]bool{}
	if len(state.FrameSwitches) == 0 {
		return files
	}
	actionText, err := source.text("itemact.txt")
	if err != nil {
		return files
	}
	lines := strings.Split(actionText, "\n")
	columns := map[string]int{}
	for index, name := range strings.Split(strings.TrimSuffix(lines[0], "\r"), "\t") {
		columns[name] = index
	}
	rows := map[string][]string{}
	for _, line := range lines[1:] {
		cols := strings.Split(strings.TrimSuffix(line, "\r"), "\t")
		if len(cols) >= 1 {
			rows[cols[0]] = cols
		}
	}
	for weaponKey, perStage := range state.FrameSwitches {
		row := rows[weaponKey]
		if row == nil {
			continue
		}
		for stage := range perStage {
			column, ok := columns[strconv.Itoa(stage)]
			if !ok || column >= len(row) {
				continue
			}
			action := row[column]
			if remap := state.Remaps[weaponKey][stage]; remap != nil && remap.Action != "" {
				action = remap.Action
			}
			if len(action) >= 5 && action != "0" {
				files["animation/"+action[:4]+".xml"] = true
			}
		}
	}
	return files
}
