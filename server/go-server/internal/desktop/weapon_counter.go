package desktop

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// 连招的**第三条通道**：动作块里的 `<Counter>`。
//
// 它不是按键触发，而是**被对手攻击触发** —— 在 startframe–endframe 这段窗口内，
// 对手的攻击落进它内部的 `<TriggerBox>`、来向又在 angleoffset ± anglehalfrange
// 之内，客户端就切到 nextstate；同一段窗口通常还配 `<DirectionalInvc>` 做方向性无敌。
// 例：253119 的 2011 `<Counter 3–25 → 2012>`、2031 `<Counter 0–25 → 2044>`。
//
// GM 的连招链（delayacttable）与帧连招（CustomStateSwitch）都看不到这条通道，
// 所以这里把它读出来挂在 stage 上只读展示，并在写盘前校验目标状态是真的存在。

// CounterWindow 是一个动作块声明的招架窗口。
type CounterWindow struct {
	StartFrame     int    `json:"start_frame"`
	EndFrame       int    `json:"end_frame"`
	NextState      string `json:"next_state"`
	AngleOffset    int    `json:"angle_offset"`
	AngleHalfRange int    `json:"angle_half_range"`
	// TriggerBox 是判定盒的长×宽×高，只用于展示（编辑器不提供修改）。
	TriggerBox string `json:"trigger_box,omitempty"`
	// NextUnreachable 表示这次招架会落到空处：目标状态在该武器自己的动作行里是
	// 空列，客户端切过去等于什么也没发生（表现为“架住了却没反应”）。写盘会被拒。
	NextUnreachable bool `json:"next_unreachable,omitempty"`
}

// counterWindowOf 读一组同名动作块里声明的招架窗口。没有 nextstate（或为 0）的
// `<Counter>` 不构成状态转移，按“没有”处理。
func counterWindowOf(candidates []block) *CounterWindow {
	for _, candidate := range candidates {
		if found := counterInNode(candidate.node); found != nil {
			return found
		}
	}
	return nil
}

func counterInNode(node *xmlNode) *CounterWindow {
	if node == nil || node.comment {
		return nil
	}
	if node.tag == "Counter" {
		if next := strings.TrimSpace(node.get("nextstate")); next != "" && next != "0" {
			window := &CounterWindow{
				StartFrame:     counterInt(node, "startframe"),
				EndFrame:       counterInt(node, "endframe"),
				NextState:      next,
				AngleOffset:    counterInt(node, "angleoffset"),
				AngleHalfRange: counterInt(node, "anglehalfrange"),
			}
			for _, child := range node.children {
				if child.tag != "TriggerBox" {
					continue
				}
				size := []string{}
				for _, key := range []string{"length", "width", "heigth"} {
					if value := strings.TrimSpace(child.get(key)); value != "" {
						size = append(size, value)
					}
				}
				if len(size) == 3 {
					window.TriggerBox = strings.Join(size, "×")
				}
			}
			return window
		}
	}
	for _, child := range node.children {
		if found := counterInNode(child); found != nil {
			return found
		}
	}
	return nil
}

func counterInt(node *xmlNode, key string) int {
	number, err := strconv.Atoi(strings.TrimSpace(node.get(key)))
	if err != nil {
		return 0
	}
	return number
}

// counterWarnings 列出“招架会切到空状态”的地方，供界面提示。
//
// **只提示、不拦写盘**（2026-09-28 用户明确要求「做展示即可」）：白架只影响那一招
// —— 架住了却切不到任何动作，武器的其它功能照旧。为它拒掉整个「应用到游戏/发版」
// 代价不对，也会让复用供体动作块的自建武器直接提交不了。界面上标出来，由作者决定
// 要不要修。
func counterWarnings(info *inspection) []string {
	if info == nil {
		return nil
	}
	warnings := []string{}
	for _, weapon := range info.weapons {
		for _, stage := range weapon.Stages {
			counter := stage.Counter
			if counter == nil || !counter.NextUnreachable {
				continue
			}
			warnings = append(warnings, fmt.Sprintf("%s（%d）状态 %s 招架到 %s，但该武器没有这个状态 —— 架住后不会切到任何动作",
				weapon.Name, weapon.ID, stage.State, counter.NextState))
		}
	}
	sort.Strings(warnings)
	return warnings
}

// ---------------------------------------------------------------------------
// 招架可编辑。照帧级连招（weapon_frame.go）的同一套做法：
//
//   * 每个动作块**至多一个** `<Counter>`（全库 26 处全是如此），所以编辑模型是
//     「每状态 0 或 1 条」而不是列表；
//   * 动作块被多把武器共用时先克隆独占块（999 向下取空号、改 itemact 列、
//     追加到 </AnmInfo> 之前），原块一个字节不动；
//   * 属性有序 key/value 往返，编辑器不认识的属性不会被抹掉；
//   * `<DirectionalInvc>`（与 Counter 同窗口的方向性无敌）不归这个编辑器管，
//     改写时原样保留。
// ---------------------------------------------------------------------------

// CounterEdit is one authored parry window. Attrs models the `<Counter>`
// attributes themselves, Box its `<TriggerBox>` child.
type CounterEdit struct {
	Attrs []FrameSwitchAttr `json:"attrs"`
	Box   []FrameSwitchAttr `json:"box"`
}

// counterElementPattern matches a whole <Counter>…</Counter> element; the
// shipped data always carries a TriggerBox child, so the element is never
// self-closing.
var counterElementPattern = regexp.MustCompile(`(?s)<Counter\b[^>]*>.*?</Counter\s*>`)

// The closed attribute sets the editor may write.
var counterEditKeys = map[string]bool{
	"startframe": true, "endframe": true, "nextstate": true,
	"angleoffset": true, "anglehalfrange": true,
}
var counterBoxKeys = map[string]bool{
	"centerx": true, "centery": true, "centerz": true,
	"length": true, "width": true, "heigth": true,
}
var counterEditRequired = []string{"startframe", "endframe", "nextstate"}

var counterSafeValue = regexp.MustCompile(`^-?[A-Za-z0-9_.]*$`)

// counterEditText renders one parry window back to XML.
func counterEditText(edit CounterEdit) string {
	parts := []string{"<Counter"}
	for _, attr := range edit.Attrs {
		parts = append(parts, attr.Key+`="`+attr.Value+`"`)
	}
	head := strings.Join(parts, " ") + " >"
	if len(edit.Box) == 0 {
		return head + "</Counter>"
	}
	boxParts := []string{"<TriggerBox"}
	for _, attr := range edit.Box {
		boxParts = append(boxParts, attr.Key+`="`+attr.Value+`"`)
	}
	return head + strings.Join(boxParts, " ") + " /></Counter>"
}

// rewriteCounters replaces the single live <Counter> of a block. An empty edit
// deletes it; the replacement keeps the position of the shipped one so sibling
// order (which the client honours) never moves. Commented-out counters stay.
func rewriteCounters(block string, fresh *CounterEdit) (string, bool) {
	matches := counterElementPattern.FindAllStringIndex(block, -1)
	locs := make([][]int, 0, len(matches))
	for _, loc := range matches {
		if !insideComment(block, loc[0]) {
			locs = append(locs, loc)
		}
	}
	if len(locs) == 0 && fresh == nil {
		return block, false
	}
	var replacement string
	if fresh != nil {
		replacement = counterEditText(*fresh)
	}
	if len(locs) == 0 {
		end := strings.LastIndex(block, "</AnmDesc")
		if end < 0 || fresh == nil {
			return block, false
		}
		return block[:end] + replacement + "\n\t" + block[end:], true
	}
	if fresh == nil {
		// 删除：连同紧贴的缩进换行一起拿掉，避免留下空行。
		start := locs[0][0]
		for start > 0 && (block[start-1] == '\t' || block[start-1] == ' ') {
			start--
		}
		if start > 0 && block[start-1] == '\n' {
			start--
		}
		return block[:start] + block[locs[0][1]:], true
	}
	return block[:locs[0][0]] + replacement + block[locs[0][1]:], true
}

// validateCounters checks an edit set against the weapon's own action row.
// nextstate 只要求出现在表头里（任意武器可见的状态号都行）——按 2026-09-28 的口径，
// 「目标状态在本武器行里是空列」只提示不拦截。
func validateCounters(a *archive, weaponKey string, edits map[int]*CounterEdit) error {
	actionText, err := a.text("itemact.txt")
	if err != nil {
		return err
	}
	lines := strings.Split(actionText, "\n")
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
		edit := edits[stage]
		if edit == nil {
			continue // 删除这条状态上的招架
		}
		position := fmt.Sprintf("状态 %d 的招架", stage)
		seen := map[string]bool{}
		for _, attr := range edit.Attrs {
			if !counterEditKeys[attr.Key] {
				return fmt.Errorf("%s：不支持的属性 %s", position, attr.Key)
			}
			if seen[attr.Key] {
				return fmt.Errorf("%s：属性 %s 重复", position, attr.Key)
			}
			seen[attr.Key] = true
			if !counterSafeValue.MatchString(attr.Value) {
				return fmt.Errorf("%s：属性 %s 的值 %q 含非法字符", position, attr.Key, attr.Value)
			}
		}
		for _, key := range counterEditRequired {
			if !seen[key] {
				return fmt.Errorf("%s：缺少 %s", position, key)
			}
		}
		if err := counterWindow(position, edit, "startframe", "endframe", 0, 9999); err != nil {
			return err
		}
		next := counterAttrValue(edit.Attrs, "nextstate")
		if !header[next] {
			return fmt.Errorf("%s：目标状态 %s 不在动作表里", position, next)
		}
		if offset := counterAttrValue(edit.Attrs, "angleoffset"); offset != "" {
			number, err := strconv.Atoi(offset)
			if err != nil || number < -180 || number > 180 {
				return fmt.Errorf("%s：angleoffset 需要 -180..180 的整数", position)
			}
		}
		if half := counterAttrValue(edit.Attrs, "anglehalfrange"); half != "" {
			number, err := strconv.Atoi(half)
			if err != nil || number < 0 || number > 180 {
				return fmt.Errorf("%s：anglehalfrange 需要 0..180 的整数", position)
			}
		}
		// TriggerBox：要么整条不写（沿用默认），要么六个尺寸全给。
		boxSeen := map[string]bool{}
		for _, attr := range edit.Box {
			if !counterBoxKeys[attr.Key] {
				return fmt.Errorf("%s：判定盒不支持的属性 %s", position, attr.Key)
			}
			if boxSeen[attr.Key] {
				return fmt.Errorf("%s：判定盒属性 %s 重复", position, attr.Key)
			}
			boxSeen[attr.Key] = true
			if !counterSafeValue.MatchString(attr.Value) {
				return fmt.Errorf("%s：判定盒属性 %s 的值 %q 含非法字符", position, attr.Key, attr.Value)
			}
			if number, err := strconv.Atoi(attr.Value); err != nil || number < -9999 || number > 9999 {
				return fmt.Errorf("%s：判定盒属性 %s 需要 -9999..9999 的整数", position, attr.Key)
			}
		}
		for key := range counterBoxKeys {
			if !boxSeen[key] {
				return fmt.Errorf("%s：判定盒六个尺寸（%s）必须齐全", position, key)
			}
		}
	}
	return nil
}

func counterAttrValue(attrs []FrameSwitchAttr, key string) string {
	for _, attr := range attrs {
		if attr.Key == key {
			return attr.Value
		}
	}
	return ""
}

func counterWindow(position string, edit *CounterEdit, startKey, endKey string, min, max int) error {
	start, err := strconv.Atoi(counterAttrValue(edit.Attrs, startKey))
	if err != nil || start < min || start > max {
		return fmt.Errorf("%s：%s 需要 %d..%d 的整数", position, startKey, min, max)
	}
	end, err := strconv.Atoi(counterAttrValue(edit.Attrs, endKey))
	if err != nil || end < min || end > max {
		return fmt.Errorf("%s：%s 需要 %d..%d 的整数", position, endKey, min, max)
	}
	if start > end {
		return fmt.Errorf("%s：%s(%d) 不能大于 %s(%d)", position, startKey, start, endKey, end)
	}
	return nil
}

// applyCounters renders the authored parry windows onto the archive. It runs
// right after applyFrameSwitches, so a remapped stage already points at its
// private block; anything still shared gets cloned instead of edited in place.
func applyCounters(a *archive, state *weaponState, items []Item, wanted ...map[int]bool) (*archive, error) {
	if len(state.Counters) == 0 {
		return a, nil
	}
	var filter map[int]bool
	if len(wanted) > 0 {
		filter = wanted[0]
	}
	info, err := inspectFilteredWithActions(a, items, filter, projectionActions(state, filter)...)
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
	entryProperties, err := newActionCloneProperties(a)
	if err != nil {
		return nil, err
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
	weaponKeys := make([]string, 0, len(state.Counters))
	for key := range state.Counters {
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
		stages := sortedCounterStages(state.Counters[weaponKey])
		for _, stage := range stages {
			edit := state.Counters[weaponKey][stage]
			column, ok := columns[strconv.Itoa(stage)]
			if !ok || column >= len(row) {
				return nil, fmt.Errorf("状态 %d 不属于武器 %s", stage, weaponKey)
			}
			action := row[column]
			if action == "" || action == "0" {
				return nil, fmt.Errorf("状态 %d 没有动作，无法编辑招架", stage)
			}
			variants := actionVariants(info, action)
			chosen, ok := pickBlock(variants)
			if !ok {
				return nil, fmt.Errorf("动作 %s 不存在", action)
			}
			file, err := a.animationWriteFile(action)
			if err != nil {
				return nil, err
			}
			animation, err := loadAnimation(file)
			if err != nil {
				return nil, err
			}
			target, err := locateEditableBlock(animation, chosen)
			if err != nil {
				return nil, err
			}
			rewritten, changed := rewriteCounters(target, edit)
			if !changed {
				continue
			}
			if len(info.owners[action]) > 1 {
				// 这一块被别的武器共用：克隆一个私有块再改，原武器不受影响。
				cloneID := entryProperties.allocate(action, reserved)
				if cloneID == 0 {
					return nil, fmt.Errorf("%s 独立动作编号空间不足", file)
				}
				next, err := cloneBlockVariants(animation, variants, chosen.condition, rewritten, cloneID)
				if err != nil {
					return nil, err
				}
				animation = next
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
	data, err := entryProperties.replace(a, replacements)
	if err != nil {
		return nil, err
	}
	return parseArchive(data)
}

// comboCounters lists the parry window each stage of one weapon currently
// declares, so the editor can show and re-edit what the block already carries
// (including windows inherited from a donor block).
func comboCounters(info *inspection, weaponID string) []map[string]any {
	result := []map[string]any{}
	for _, weapon := range info.weapons {
		if strconv.Itoa(weapon.ID) != weaponID {
			continue
		}
		for _, stage := range weapon.Stages {
			if stage.Action == "" || stage.Action == "0" {
				continue
			}
			blocks := info.blocks[actionKey(stage.Action)]
			if len(blocks) == 0 {
				continue
			}
			found := counterWindowOf(blocks)
			if found == nil {
				continue
			}
			attrs := []FrameSwitchAttr{}
			box := []FrameSwitchAttr{}
			for _, candidate := range blocks {
				if window, ok := counterAttrsOf(candidate.node); ok {
					attrs, box = window[0], window[1]
					break
				}
			}
			result = append(result, map[string]any{
				"state": stage.State,
				"attrs": attrs,
				"box":   box,
			})
		}
		break
	}
	return result
}

// counterAttrsOf reads the raw ordered attributes of a block's live <Counter>
// and its <TriggerBox> child, so an editor round-trip neither drops nor resorts
// what the editor does not model.
func counterAttrsOf(node *xmlNode) ([][]FrameSwitchAttr, bool) {
	if node == nil || node.comment {
		return nil, false
	}
	if node.tag == "Counter" {
		if next := strings.TrimSpace(node.get("nextstate")); next != "" && next != "0" {
			attrs := make([]FrameSwitchAttr, 0, len(node.attrs))
			for _, attr := range node.attrs {
				attrs = append(attrs, FrameSwitchAttr{Key: attr.Name.Local, Value: attr.Value})
			}
			box := []FrameSwitchAttr{}
			for _, child := range node.children {
				if child.tag != "TriggerBox" {
					continue
				}
				for _, attr := range child.attrs {
					box = append(box, FrameSwitchAttr{Key: attr.Name.Local, Value: attr.Value})
				}
				break
			}
			return [][]FrameSwitchAttr{attrs, box}, true
		}
	}
	for _, child := range node.children {
		if found, ok := counterAttrsOf(child); ok {
			return found, true
		}
	}
	return nil, false
}

func sortedCounterStages(source map[int]*CounterEdit) []int {
	stages := make([]int, 0, len(source))
	for stage := range source {
		stages = append(stages, stage)
	}
	sort.Ints(stages)
	return stages
}

// counterFiles reports which animation entries a counter edit set can touch,
// so the write guard lets exactly those through and nothing else.
func counterFiles(source *archive, state *weaponState) map[string]bool {
	files := map[string]bool{}
	if len(state.Counters) == 0 {
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
	for weaponKey, perStage := range state.Counters {
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
				source.allowGroupFiles(files, action)
			}
		}
	}
	return files
}
