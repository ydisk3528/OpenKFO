package desktop

// 动作块里的「防护」与「自身状态」元素编辑。
//
// 和架招（<Counter>）一样，这些也都是动作块里的普通元素、按帧窗口生效，GM 以前完全看不到：
//
//   * 防护：霸体 <FakeUnAttack>、无敌 <DirectionalInvc>、穿人 <BodyGraze>
//     —— 每块至多一条（全库实测如此），只有帧号（无敌另有两个角度）。
//   * 自身状态：<UState id level duration> / <Ustate id> / <AddBuff frame Scope
//     UnNormalState UStateLevel UStateLastCycle Param1> —— 用这一招时给自己挂状态，
//     每块可以有多条（AddBuff 最多见 4 条）。
//     状态 id 的**中文名表**在 ustate.xml（420 条 <Data>，`type` 就是 id，前面带策划注释），
//     所以界面能做下拉而不是让作者背编号。
//
// 改写沿用同一套安全做法：共用块先克隆独占块、文本级原位替换、注释区不动、
// 属性有序 key/value 往返（编辑器不认识的属性不会被抹掉）。

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// BlockElement is one authored block-element instance: the tag plus its ordered
// attributes, and optionally one child element (ForceField 的 <ScopeBox>).
type BlockElement struct {
	Tag   string            `json:"tag"`
	Attrs []FrameSwitchAttr `json:"attrs"`
	Box   []FrameSwitchAttr `json:"box,omitempty"`
}

type blockElementSpec struct {
	Tag   string
	Group string // guard / state
	// Placement 限定元素在动作块里必须出现的位置。全库实测三类分布完全不重叠：
	//   ""          → <AnmDesc> 的直接子节点（霸体/无敌/穿人/AddBuff 等，施加给自己）
	//   "hit"       → <LockedAttackHit>（命中判定点）内部，作用对象是被打中的人
	//   "condition" → <Condition>（动作段的触发条件）内部，是判断而不是施加
	Placement string
	Label     string
	Single    bool // 每块至多一条
	Keys      map[string]bool
	Required  []string
	Ranges    map[string][2]int
	// Frames 是需要校验「前 ≤ 后」的字段对（空字符串表示不需要）。
	Frames [2]string
	// 子元素（ForceField 的 <ScopeBox>）。空 Tag 表示叶子节点。
	ChildTag      string
	ChildKeys     map[string]bool
	ChildRequired []string
	ChildRanges   map[string][2]int
}

var blockElementSpecs = []blockElementSpec{
	{
		Tag: "FakeUnAttack", Group: "guard", Label: "霸体", Single: true,
		Keys:     map[string]bool{"startframe": true, "endframe": true, "skillproid": true},
		Required: []string{"startframe", "endframe"},
		Ranges:   map[string][2]int{"startframe": {0, 9999}, "endframe": {0, 9999}},
		Frames:   [2]string{"startframe", "endframe"},
	},
	{
		Tag: "DirectionalInvc", Group: "guard", Label: "无敌", Single: true,
		Keys: map[string]bool{"startframe": true, "endframe": true, "angleoffset": true,
			"anglehalfrange": true, "defendshoot": true},
		Required: []string{"startframe", "endframe"},
		Ranges: map[string][2]int{"startframe": {0, 9999}, "endframe": {0, 9999},
			"angleoffset": {-180, 180}, "anglehalfrange": {0, 180}, "defendshoot": {0, 1}},
		Frames: [2]string{"startframe", "endframe"},
	},
	{
		Tag: "BodyGraze", Group: "guard", Label: "穿人", Single: true,
		Keys:     map[string]bool{"startframe": true, "endframe": true},
		Required: []string{"startframe", "endframe"},
		Ranges:   map[string][2]int{"startframe": {0, 9999}, "endframe": {0, 9999}},
		Frames:   [2]string{"startframe", "endframe"},
	},
	{
		Tag: "UState", Group: "state", Label: "命中给目标挂状态", Placement: "hit", Single: false,
		Keys:     map[string]bool{"id": true, "level": true, "duration": true, "skillproid": true},
		Required: []string{"id"},
		Ranges:   map[string][2]int{"id": {1, 99999}, "level": {-999, 999}, "duration": {0, 9999999}},
	},
	{
		Tag: "Ustate", Group: "state", Label: "触发条件（需拥有该状态）", Placement: "condition", Single: false,
		Keys:     map[string]bool{"id": true},
		Required: []string{"id"},
		Ranges:   map[string][2]int{"id": {1, 99999}},
	},
	{
		Tag: "AddBuff", Group: "state", Label: "施放时给自己挂状态", Single: false,
		Keys: map[string]bool{"frame": true, "Scope": true, "UnNormalState": true,
			"UStateLevel": true, "UStateLastCycle": true, "Param1": true, "skillproid": true},
		Required: []string{"frame", "UnNormalState"},
		// Scope 是"作用对象"枚举（1 自己 / 2 自己+队友 / 3 自己+队友+范围 / 4 敌人 /
		// 5 敌人+范围 / 6 阵亡队友中等级最高者，见 animation/2001.xml 的策划说明）。
		// 真实数据里还出现过未收录的 40（2204/149·964·965），所以上限放宽到 255，
		// 免得改一条既有 AddBuff 就被校验挡在门外。Param1 只在 3/5 上是范围。
		Ranges: map[string][2]int{"frame": {0, 9999}, "Scope": {0, 255},
			"UnNormalState": {1, 99999}, "UStateLevel": {-999, 999}, "UStateLastCycle": {0, 9999999}},
	},
	{
		Tag: "SelfControl", Group: "guard", Label: "转向控制", Single: true,
		Keys:     map[string]bool{"startframe": true, "endframe": true, "turnspeed": true, "movespeed": true, "turnadd": true},
		Required: []string{"startframe", "endframe", "turnspeed", "movespeed"},
		Ranges: map[string][2]int{"startframe": {0, 9999}, "endframe": {0, 9999},
			"turnspeed": {0, 9999}, "movespeed": {0, 9999}, "turnadd": {0, 999}},
		Frames: [2]string{"startframe", "endframe"},
	},
	{
		Tag: "HideBody", Group: "guard", Label: "身体消失", Single: true,
		Keys:     map[string]bool{"startframe": true, "endframe": true, "type": true},
		Required: []string{"startframe", "endframe", "type"},
		Ranges:   map[string][2]int{"startframe": {0, 9999}, "endframe": {0, 9999}, "type": {0, 9}},
		Frames:   [2]string{"startframe", "endframe"},
	},
	{
		Tag: "ForceField", Group: "guard", Label: "力场", Single: false,
		Keys:     map[string]bool{"startframe": true, "endframe": true, "pushspeed": true, "friendlyfire": true},
		Required: []string{"startframe", "endframe", "pushspeed", "friendlyfire"},
		Ranges:   map[string][2]int{"startframe": {0, 9999}, "endframe": {0, 9999}, "pushspeed": {0, 999}, "friendlyfire": {0, 1}},
		Frames:   [2]string{"startframe", "endframe"},
		// 子元素：力场的作用盒。
		ChildTag:      "ScopeBox",
		ChildKeys:     map[string]bool{"centerx": true, "centery": true, "centerz": true, "length": true, "width": true, "heigth": true},
		ChildRequired: []string{"centerx", "centery", "centerz", "length", "width", "heigth"},
		ChildRanges:   map[string][2]int{"centerx": {-9999, 9999}, "centery": {-9999, 9999}, "centerz": {-9999, 9999}, "length": {-9999, 9999}, "width": {-9999, 9999}, "heigth": {-9999, 9999}},
	},
}

func blockElementSpecFor(tag string) (blockElementSpec, bool) {
	for _, spec := range blockElementSpecs {
		if spec.Tag == tag {
			return spec, true
		}
	}
	return blockElementSpec{}, false
}

// blockElementGroups 是按 UI 分组的清单（保持声明顺序）。
func blockElementGroups() []map[string]any {
	groups := []map[string]any{}
	index := map[string]int{}
	for _, spec := range blockElementSpecs {
		at, ok := index[spec.Group]
		if !ok {
			at = len(groups)
			index[spec.Group] = at
			groups = append(groups, map[string]any{"group": spec.Group, "elements": []map[string]any{}})
		}
		items := groups[at]["elements"].([]map[string]any)
		groups[at]["elements"] = append(items, map[string]any{
			"tag": spec.Tag, "label": spec.Label, "single": spec.Single, "placement": spec.Placement,
		})
	}
	return groups
}

// pattern cache：<Tag …/> 或 <Tag …>…</Tag>（后者兼容带子元素的写法，如 ForceField）。
var blockElementPatterns = func() map[string]*regexp.Regexp {
	out := map[string]*regexp.Regexp{}
	for _, spec := range blockElementSpecs {
		out[spec.Tag] = regexp.MustCompile(`(?s)<` + spec.Tag + `\b[^>]*?/\s*>|<` + spec.Tag + `\b[^>]*?>.*?</` + spec.Tag + `\s*>`)
	}
	return out
}()

// blockElementText renders one element back to XML. Leaf tags are self-closing;
// a spec with a child (ForceField's <ScopeBox>) renders it nested.
func blockElementText(element BlockElement) string {
	parts := []string{"<" + element.Tag}
	for _, attr := range element.Attrs {
		parts = append(parts, attr.Key+`="`+attr.Value+`"`)
	}
	if len(element.Box) == 0 {
		return strings.Join(parts, " ") + " />"
	}
	spec, _ := blockElementSpecFor(element.Tag)
	child := spec.ChildTag
	if child == "" {
		child = "ScopeBox"
	}
	head := strings.Join(parts, " ") + " >"
	childParts := []string{"<" + child}
	for _, attr := range element.Box {
		childParts = append(childParts, attr.Key+`="`+attr.Value+`"`)
	}
	return head + strings.Join(childParts, " ") + " /></" + element.Tag + ">"
}

// 动作块里的两种「容器」节点——全库实测三类标签的分布完全不重叠：
//
//	<UState> 只出现在 <LockedAttackHit>（命中判定点）内 → 命中时给被打中的人挂状态；
//	<Ustate> 只出现在 <Condition>（触发条件）内 → 拥有该状态时这段动作才成立；
//	<AddBuff> 与霸体/无敌/穿人等在 <AnmDesc> 直属层。
//
// 采集与写回都必须限定在各自的容器里，否则会把「条件」或「命中给目标」误当成自身状态。
var lockedAttackHitPattern = regexp.MustCompile(`(?s)<LockedAttackHit\b[^>]*>.*?</LockedAttackHit\s*>`)
var conditionPattern = regexp.MustCompile(`(?s)<Condition\b[^>]*>.*?</Condition\s*>`)
var lockedAttackHitOpenPattern = regexp.MustCompile(`<LockedAttackHit\b[^>]*>`)
var conditionOpenPattern = regexp.MustCompile(`<Condition\b[^>]*>`)

// blockElementContainer 返回 placement 对应的容器名与成对正则（"" 表示 AnmDesc 直属层）。
func blockElementContainer(placement string) (name string, whole, open *regexp.Regexp) {
	switch placement {
	case "hit":
		return "LockedAttackHit", lockedAttackHitPattern, lockedAttackHitOpenPattern
	case "condition":
		return "Condition", conditionPattern, conditionOpenPattern
	default:
		return "", nil, nil
	}
}

// blockElementContainers 列出块内所有容器（整体区间，用于排除直属层里的误匹配）。
func blockElementContainers(block string) [][]int {
	spans := append([][]int{}, lockedAttackHitPattern.FindAllStringIndex(block, -1)...)
	spans = append(spans, conditionPattern.FindAllStringIndex(block, -1)...)
	return spans
}

// inAnySpan reports whether offset falls inside any of the given [start,end) spans.
func inAnySpan(spans [][]int, offset int) bool {
	for _, span := range spans {
		if offset >= span[0] && offset < span[1] {
			return true
		}
	}
	return false
}

// blockElementWritable 报告块里有没有该 placement 需要的容器（直属层永远可以写）。
func blockElementWritable(block string, spec blockElementSpec) bool {
	name, whole, _ := blockElementContainer(spec.Placement)
	if name == "" {
		return true
	}
	return whole.FindStringIndex(block) != nil
}

// rewriteBlockElements replaces every live instance of a tag with the given
// list, keeping the position of the first one. Commented-out instances are left
// alone. An empty list deletes them all.
//
// 替换范围按标签的 Placement 限定：需要容器的标签（UState 在 <LockedAttackHit>、
// Ustate 在 <Condition>）只动容器内的实例；直属层标签则跳过落在容器里的同名实例。
// 块里没有需要的容器时一个字节都不动（调用方负责给出可读的错误）。
func rewriteBlockElements(block string, tag string, fresh []BlockElement) (string, bool) {
	pattern, ok := blockElementPatterns[tag]
	if !ok {
		return block, false
	}
	spec, _ := blockElementSpecFor(tag)
	containerName, containerPattern, containerOpen := blockElementContainer(spec.Placement)
	containers := [][]int{}
	if containerName != "" {
		containers = containerPattern.FindAllStringIndex(block, -1)
		if len(containers) == 0 {
			return block, false
		}
	}
	excluded := blockElementContainers(block)
	matches := pattern.FindAllStringIndex(block, -1)
	locs := make([][]int, 0, len(matches))
	for _, loc := range matches {
		if insideComment(block, loc[0]) {
			continue
		}
		if containerName == "" {
			if inAnySpan(excluded, loc[0]) {
				continue
			}
		} else if !inAnySpan(containers, loc[0]) {
			continue
		}
		locs = append(locs, loc)
	}
	if len(locs) == 0 && len(fresh) == 0 {
		return block, false
	}
	pieces := make([]string, 0, len(fresh))
	for _, element := range fresh {
		pieces = append(pieces, blockElementText(element))
	}
	joined := strings.Join(pieces, "\n\t")
	if len(locs) == 0 {
		// 没有实例：直属层插到 </AnmDesc> 前，带容器的插进第一个容器内。
		insert := -1
		if containerName == "" {
			insert = strings.LastIndex(block, "</AnmDesc")
		} else if head := containerOpen.FindStringIndex(block); head != nil {
			insert = head[1]
		}
		if insert < 0 {
			return block, false
		}
		if containerName == "" {
			return block[:insert] + joined + "\n\t" + block[insert:], true
		}
		// 容器内多缩进一层，跟数据里 <LockedAttackHit>/<Condition> 的写法一致。
		return block[:insert] + "\n\t\t" + joined + "\n\t" + block[insert:], true
	}
	// 删除第一段时把它前面的缩进/换行一起收掉，避免留下空行。
	start := locs[0][0]
	if len(fresh) == 0 {
		for start > 0 && (block[start-1] == '\t' || block[start-1] == ' ') {
			start--
		}
		if start > 0 && block[start-1] == '\n' {
			start--
		}
	}
	var out strings.Builder
	out.WriteString(block[:start])
	out.WriteString(joined)
	last := locs[0][1]
	for _, loc := range locs[1:] {
		out.WriteString(block[last:loc[0]])
		last = loc[1]
	}
	out.WriteString(block[last:])
	return out.String(), true
}

// validateBlockElements checks an edit set against the weapon's own action row:
// the state must exist, every tag must be one we model, attributes must be in
// the whitelist / within range, and single-instance tags must appear once.
//
// 编辑集按「状态 → 元素标签 → 条目」组织：**没出现在里面的状态/标签完全不碰**
// （作者只选了某一项时才整理那一项对应的块），标签给空列表才是「删掉这个标签」。
func validateBlockElements(a *archive, weaponKey string, edits map[int]map[string][]BlockElement) error {
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
		tags := make([]string, 0, len(edits[stage]))
		for tag := range edits[stage] {
			tags = append(tags, tag)
		}
		sort.Strings(tags)
		for _, tag := range tags {
			spec, ok := blockElementSpecFor(tag)
			if !ok {
				return fmt.Errorf("状态 %d：不支持的元素 %s", stage, tag)
			}
			position := fmt.Sprintf("状态 %d 的%s", stage, spec.Label)
			list := edits[stage][tag]
			if spec.Single && len(list) > 1 {
				return fmt.Errorf("%s：每个状态最多一条", position)
			}
			for index, element := range list {
				if element.Tag != "" && element.Tag != tag {
					return fmt.Errorf("%s：条目里的标签 %s 与分组不符", position, element.Tag)
				}
				seen := map[string]bool{}
				for _, attr := range element.Attrs {
					if !spec.Keys[attr.Key] {
						return fmt.Errorf("%s 第 %d 条：不支持的属性 %s", position, index+1, attr.Key)
					}
					if seen[attr.Key] {
						return fmt.Errorf("%s 第 %d 条：属性 %s 重复", position, index+1, attr.Key)
					}
					seen[attr.Key] = true
					if !counterSafeValue.MatchString(attr.Value) {
						return fmt.Errorf("%s 第 %d 条：属性 %s 的值 %q 含非法字符", position, index+1, attr.Key, attr.Value)
					}
					if bounds, ranged := spec.Ranges[attr.Key]; ranged {
						number, err := strconv.Atoi(attr.Value)
						if err != nil || number < bounds[0] || number > bounds[1] {
							return fmt.Errorf("%s 第 %d 条：%s 需要 %d..%d 的整数", position, index+1, attr.Key, bounds[0], bounds[1])
						}
					}
				}
				for _, key := range spec.Required {
					if !seen[key] {
						return fmt.Errorf("%s 第 %d 条：缺少 %s", position, index+1, key)
					}
				}
				if spec.Frames[0] != "" && seen[spec.Frames[0]] && seen[spec.Frames[1]] {
					if err := counterWindow(position, &CounterEdit{Attrs: element.Attrs},
						spec.Frames[0], spec.Frames[1], 0, 9999); err != nil {
						return err
					}
				}
				if spec.ChildTag != "" {
					boxSeen := map[string]bool{}
					for _, attr := range element.Box {
						if !spec.ChildKeys[attr.Key] {
							return fmt.Errorf("%s 第 %d 条：子元素不支持的属性 %s", position, index+1, attr.Key)
						}
						if boxSeen[attr.Key] {
							return fmt.Errorf("%s 第 %d 条：子元素属性 %s 重复", position, index+1, attr.Key)
						}
						boxSeen[attr.Key] = true
						if !counterSafeValue.MatchString(attr.Value) {
							return fmt.Errorf("%s 第 %d 条：子元素属性 %s 的值 %q 含非法字符", position, index+1, attr.Key, attr.Value)
						}
						if bounds, ranged := spec.ChildRanges[attr.Key]; ranged {
							number, err := strconv.Atoi(attr.Value)
							if err != nil || number < bounds[0] || number > bounds[1] {
								return fmt.Errorf("%s 第 %d 条：%s 需要 %d..%d 的整数", position, index+1, attr.Key, bounds[0], bounds[1])
							}
						}
					}
					for _, key := range spec.ChildRequired {
						if !boxSeen[key] {
							return fmt.Errorf("%s 第 %d 条：子元素缺少 %s", position, index+1, key)
						}
					}
				}
			}
		}
	}
	return nil
}

// applyBlockElements renders the authored guard / self-state elements onto the
// archive. Runs after applyCounters so a shared block was already cloned when a
// remap needed one; anything still shared is cloned here.
func applyBlockElements(a *archive, state *weaponState, items []Item) (*archive, error) {
	if len(state.BlockElements) == 0 {
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
	weaponKeys := make([]string, 0, len(state.BlockElements))
	for key := range state.BlockElements {
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
		stages := make([]int, 0, len(state.BlockElements[weaponKey]))
		for stage := range state.BlockElements[weaponKey] {
			stages = append(stages, stage)
		}
		sort.Ints(stages)
		for _, stage := range stages {
			edits := state.BlockElements[weaponKey][stage]
			column, ok := columns[strconv.Itoa(stage)]
			if !ok || column >= len(row) {
				return nil, fmt.Errorf("状态 %d 不属于武器 %s", stage, weaponKey)
			}
			action := row[column]
			if action == "" || action == "0" {
				return nil, fmt.Errorf("状态 %d 没有动作，无法编辑防护/自身状态", stage)
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
			// 只改编辑集里出现的标签：没动过的元素一个字节都不碰。
			rewritten := target
			changed := false
			tags := make([]string, 0, len(edits))
			for tag := range edits {
				tags = append(tags, tag)
			}
			sort.Strings(tags)
			for _, tag := range tags {
				spec, known := blockElementSpecFor(tag)
				if !known {
					return nil, fmt.Errorf("不支持的元素 %s", tag)
				}
				if len(edits[tag]) > 0 && !blockElementWritable(rewritten, spec) {
					container, _, _ := blockElementContainer(spec.Placement)
					return nil, fmt.Errorf("状态 %d 的动作块里没有 <%s>，写不了「%s」；命中给目标挂状态请用 AddBuff",
						stage, container, spec.Label)
				}
				next, touched := rewriteBlockElements(rewritten, tag, edits[tag])
				if touched {
					rewritten = next
					changed = true
				}
			}
			if !changed {
				continue
			}
			if len(info.owners[action]) > 1 {
				cloneID := entryProperties.allocate(action, reserved)
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
	data, err := entryProperties.replace(a, replacements)
	if err != nil {
		return nil, err
	}
	return parseArchive(data)
}

// blockElementFiles reports which animation entries an edit set can touch, so
// the write guard lets exactly those through.
func blockElementFiles(source *archive, state *weaponState) map[string]bool {
	files := map[string]bool{}
	if len(state.BlockElements) == 0 {
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
	for weaponKey, perStage := range state.BlockElements {
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

// comboBlockElements lists the guard / self-state elements each stage of one
// weapon currently carries, so the editor can show and re-edit the block's own
// declarations (including the ones inherited from a donor block).
func comboBlockElements(info *inspection, weaponID string) []map[string]any {
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
			elements := []map[string]any{}
			for _, spec := range blockElementSpecs {
				for _, blk := range blocks {
					for _, found := range blockElementsOf(blk.node, spec) {
						entry := map[string]any{"tag": spec.Tag, "attrs": found.Attrs}
						if len(found.Box) > 0 {
							entry["box"] = found.Box
						}
						elements = append(elements, entry)
					}
				}
			}
			if len(elements) == 0 {
				continue
			}
			result = append(result, map[string]any{
				"state":    stage.State,
				"elements": elements,
			})
		}
		break
	}
	return result
}

// blockElementsOf reads the raw ordered attributes of every live instance of the
// spec's tag **inside the container the spec allows** —— UState 只认
// <LockedAttackHit> 内的、Ustate 只认 <Condition> 内的，其余标签只认 <AnmDesc>
// 直属子节点；也包括首个子元素的属性（ForceField 的 <ScopeBox>）。
func blockElementsOf(node *xmlNode, spec blockElementSpec) []BlockElement {
	found := []BlockElement{}
	appendOne := func(current *xmlNode) {
		element := BlockElement{Tag: spec.Tag}
		for _, attr := range current.attrs {
			element.Attrs = append(element.Attrs, FrameSwitchAttr{Key: attr.Name.Local, Value: attr.Value})
		}
		if spec.ChildTag != "" {
			for _, child := range current.children {
				if child.tag != spec.ChildTag {
					continue
				}
				for _, attr := range child.attrs {
					element.Box = append(element.Box, FrameSwitchAttr{Key: attr.Name.Local, Value: attr.Value})
				}
				break
			}
		}
		found = append(found, element)
	}
	if containerName, _, _ := blockElementContainer(spec.Placement); containerName != "" {
		var walk func(*xmlNode)
		walk = func(current *xmlNode) {
			if current == nil || current.comment {
				return
			}
			if current.tag == containerName {
				for _, child := range current.children {
					if child != nil && !child.comment && child.tag == spec.Tag {
						appendOne(child)
					}
				}
			}
			for _, child := range current.children {
				walk(child)
			}
		}
		walk(node)
		return found
	}
	for _, child := range node.children {
		if child != nil && !child.comment && child.tag == spec.Tag {
			appendOne(child)
		}
	}
	return found
}

// ---------------------------------------------------------------------------
// ustate.xml：异常/主动状态的目录。每条 <Data type="N"> 前面都带策划注释，
// type 就是动作块里 UState/Ustate/AddBuff 用的编号，所以界面可以直接给中文名。
// ---------------------------------------------------------------------------

var ustateDataPattern = regexp.MustCompile(`(?s)<Data\b([^>]*)>`)
var ustateTypePattern = regexp.MustCompile(`\btype\s*=\s*"?(\d+)"?`)

func ustateCatalog(a *archive) []map[string]string {
	text, err := a.text("ustate.xml")
	if err != nil {
		return []map[string]string{}
	}
	result := []map[string]string{}
	seen := map[string]bool{}
	for _, match := range ustateDataPattern.FindAllStringSubmatchIndex(text, -1) {
		id := ""
		if m := ustateTypePattern.FindStringSubmatch(text[match[2]:match[3]]); m != nil {
			id = m[1]
		}
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		result = append(result, map[string]string{
			"id":   id,
			"name": ustateNameBefore(text, match[0]),
		})
	}
	sort.Slice(result, func(i, j int) bool {
		left, _ := strconv.Atoi(result[i]["id"])
		right, _ := strconv.Atoi(result[j]["id"])
		return left < right
	})
	return result
}

// ustateNameBefore 取紧挨在元素前面的那条注释（数据里就是它的中文名+说明）。
func ustateNameBefore(text string, offset int) string {
	open := strings.LastIndex(text[:offset], "<!--")
	if open < 0 {
		return ""
	}
	close := strings.Index(text[open:offset], "-->")
	if close < 0 {
		return ""
	}
	close += open
	if strings.TrimSpace(text[close+3:offset]) != "" {
		return ""
	}
	name := strings.TrimSpace(text[open+4 : close])
	if at := strings.IndexAny(name, "\r\n"); at >= 0 {
		name = name[:at]
	}
	name = strings.TrimSpace(name)
	if len(name) > 40 {
		name = name[:40]
	}
	return name
}
