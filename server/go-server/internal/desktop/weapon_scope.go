package desktop

// 招式「帧轨道」与「攻击范围」。
//
// 背景（2026-09-28 查清）：动作块 <AnmDesc> 里的 <Anm id name startframe endframe>
// 是一条**动作片断**，name 指向客户端 Data/animation/<name>.anm 这套骨骼动画。
// 库里的动作几乎都带一条尾巴片断（endframe=99，少数 999），那只是"保持姿势到被
// 中断"的占位，不是真实时长。以前 GM 直接取所有片断 endframe 的最大值 +1，于是
// 绝大多数招式都显示 100，而真实时长是 .anm 自己的帧数（几十帧，注释里也写作
// "站C4 49""zx4 50"这种 = 帧数-1）。所以这里的 Frames 改成读 .anm 的真实帧数，
// 并把片断/标记交给前端画成帧轨道。
//
// 攻击范围 <AttackScope> / 人物盒 <CharScope> 是 <Anm> 片断的子元素，六个属性：
// centerx/centery/centerz + length/width/heigth。x 是左右、y 是上下、z 是前方，
// 所以顶视图看 (x,z)、侧视图看 (y,z)。以前 GM 完全看不到，这里做成可编辑：
// 沿用既有安全做法（共用块先克隆独占块、文本级原位替换、注释区不动、
// 编辑器不认识的属性随条目往返）。

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// .anm 骨骼动画的真实帧数
//
// 文件头 24 字节里：+0 = 骨骼数(实测全是 27)、+4 = 数据区长、+20 = 记录数。
// 数据区布局是「每帧 (骨骼数+2) 条 36 字节记录 + 20 字节尾部」，
// 即 记录数 = 帧数*(骨骼数+2) + 2。实测 5020 个文件里 4894 个严格成立，
// 剩下的是老格式变体，取整兜底即可。
// ---------------------------------------------------------------------------

func anmFrames(client, name string) int {
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, `/\:`) || strings.Contains(name, "..") {
		return 0
	}
	data, err := os.ReadFile(filepath.Join(client, "Data", "animation", name+".anm"))
	if err != nil || len(data) < 24 {
		return 0
	}
	bones := int(little.Uint32(data[0:]))
	records := int(little.Uint32(data[20:]))
	if bones < 1 || bones > 1024 || records < 2 {
		return 0
	}
	per := bones + 2
	if (records-2)%per == 0 {
		return (records - 2) / per
	}
	// 老格式兜底：按每帧 (骨骼+2) 条记录估算。
	if frames := records / per; frames > 0 {
		return frames
	}
	return 0
}

// ---------------------------------------------------------------------------
// 动作片断与轨道标记
// ---------------------------------------------------------------------------

// AnmSegment 是动作块里的一条 <Anm> 片断，帧号是动作自己的时间轴。
type AnmSegment struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Start    int               `json:"start"`
	End      int               `json:"end"`
	SkillPro string            `json:"skillproid,omitempty"`
	Clip     int               `json:"clip_frames,omitempty"`
	Scope    []FrameSwitchAttr `json:"scope,omitempty"`
	CharBox  []FrameSwitchAttr `json:"char_scope,omitempty"`
}

// StageMarker 是轨道上的一条标记（特效、音效、接招窗口、防护窗口……）。
type StageMarker struct {
	Kind  string `json:"kind"`
	Start int    `json:"start"`
	End   int    `json:"end"`
	Label string `json:"label"`
}

// StageBox 是招式里"不属于某个动画片断"的盒子，供预览一起画出来：
// 力场 <ForceField><ScopeBox/></ForceField>、投技 <LockedAttackSwitch><Scope/></LockedAttackSwitch>。
// 六属性与 AttackScope/CharScope 相同（x 左右、y 上下、z 前方）。
type StageBox struct {
	Kind   string            `json:"kind"`
	Label  string            `json:"label"`
	Source string            `json:"source"`
	Start  int               `json:"start,omitempty"`
	End    int               `json:"end,omitempty"`
	Attrs  []FrameSwitchAttr `json:"attrs"`
}

// blockBoxesOf 读一个动作块上带作用盒的块级元素。
func blockBoxesOf(node *xmlNode) []StageBox {
	boxes := []StageBox{}
	var walk func(*xmlNode)
	walk = func(current *xmlNode) {
		if current == nil || current.comment || current.tag == "Anm" {
			return // <Anm> 里的 AttackScope/CharScope 按片断单独给
		}
		switch current.tag {
		case "ForceField":
			start, _ := current.attrAt("startframe")
			end, _ := current.attrAt("endframe")
			for _, child := range current.children {
				if child.tag != "ScopeBox" {
					continue
				}
				boxes = append(boxes, StageBox{
					Kind: "force", Label: "力场盒", Source: "ForceField",
					Start: start, End: end, Attrs: nodeAttrs(child),
				})
			}
		case "LockedAttackSwitch":
			start, _ := current.attrAt("framestart")
			end, _ := current.attrAt("frameend")
			for _, child := range current.children {
				if child.tag != "Scope" {
					continue
				}
				boxes = append(boxes, StageBox{
					Kind: "grab", Label: "投技盒", Source: "LockedAttackSwitch",
					Start: start, End: end, Attrs: nodeAttrs(child),
				})
			}
		}
		for _, child := range current.children {
			walk(child)
		}
	}
	walk(node)
	return boxes
}

func nodeAttrs(node *xmlNode) []FrameSwitchAttr {
	attrs := make([]FrameSwitchAttr, 0, len(node.attrs))
	for _, attr := range node.attrs {
		attrs = append(attrs, FrameSwitchAttr{Key: attr.Name.Local, Value: attr.Value})
	}
	return attrs
}

func attrInt(attrs []FrameSwitchAttr, key string) (int, bool) {
	for _, attr := range attrs {
		if attr.Key == key {
			value, err := strconv.Atoi(strings.TrimSpace(attr.Value))
			return value, err == nil
		}
	}
	return 0, false
}

// segmentsOf 读一个动作块里的全部片断（按文档顺序）。
func segmentsOf(node *xmlNode, client string) []AnmSegment {
	found := []AnmSegment{}
	var walk func(*xmlNode)
	walk = func(current *xmlNode) {
		if current == nil || current.comment {
			return
		}
		if current.tag == "Anm" {
			segment := AnmSegment{
				ID:       strings.TrimSpace(current.get("id")),
				Name:     strings.TrimSpace(current.get("name")),
				SkillPro: strings.TrimSpace(current.get("skillproid")),
			}
			segment.Start, _ = strconv.Atoi(strings.TrimSpace(current.get("startframe")))
			segment.End, _ = strconv.Atoi(strings.TrimSpace(current.get("endframe")))
			for _, child := range current.children {
				if child.comment {
					continue
				}
				switch child.tag {
				case "AttackScope":
					segment.Scope = nodeAttrs(child)
				case "CharScope":
					segment.CharBox = nodeAttrs(child)
				}
			}
			if client != "" {
				segment.Clip = anmFrames(client, segment.Name)
			}
			found = append(found, segment)
		}
		for _, child := range current.children {
			walk(child)
		}
	}
	walk(node)
	return found
}

// markersOf 读一个动作块上值得画进轨道的标记。
func markersOf(node *xmlNode) []StageMarker {
	markers := []StageMarker{}
	add := func(kind string, start, end int, label string) {
		markers = append(markers, StageMarker{Kind: kind, Start: start, End: end, Label: label})
	}
	var walk func(*xmlNode)
	walk = func(current *xmlNode) {
		if current == nil || current.comment {
			return
		}
		switch current.tag {
		case "KeyProcess":
			next := current.get("nextattackSF") + "|" + current.get("nextattackEF")
			if sf, ok := current.attrAt("nextattackSF"); ok {
				if ef, ok2 := current.attrAt("nextattackEF"); ok2 && ef >= sf {
					add("key", sf, ef, "接招窗口")
				}
			} else if next != "|" {
				add("key", 0, 0, "接招窗口")
			}
			if sf, ok := current.attrAt("reattackSF"); ok && sf > 0 {
				add("key", sf, sf, "重击窗口")
			}
		case "Effect":
			if frame, ok := current.attrAt("frame"); ok {
				add("effect", frame, frame, "特效 "+current.get("effectid"))
			}
		case "HitEffect":
			sf, ok1 := current.attrAt("startframe")
			ef, ok2 := current.attrAt("endframe")
			if ok1 && ok2 {
				add("hit", sf, ef, "命中特效 "+current.get("effectid"))
			}
		case "Audio":
			if frame, ok := current.attrAt("frame"); ok {
				add("audio", frame, frame, "音效 "+current.get("audioid"))
			}
		case "Counter":
			sf, ok1 := current.attrAt("startframe")
			ef, ok2 := current.attrAt("endframe")
			if ok1 && ok2 {
				add("counter", sf, ef, "招架 → "+current.get("nextstate"))
			}
		case "FakeUnAttack", "DirectionalInvc", "BodyGraze", "SelfControl", "HideBody", "ForceField":
			sf, ok1 := current.attrAt("startframe")
			ef, ok2 := current.attrAt("endframe")
			label := map[string]string{"FakeUnAttack": "霸体", "DirectionalInvc": "无敌",
				"BodyGraze": "穿人", "SelfControl": "转向控制", "HideBody": "身体消失", "ForceField": "力场"}[current.tag]
			if ok1 && ok2 {
				add("guard", sf, ef, label)
			}
		}
		for _, child := range current.children {
			walk(child)
		}
	}
	walk(node)
	return markers
}

func (n *xmlNode) attrAt(key string) (int, bool) {
	value := strings.TrimSpace(n.get(key))
	if value == "" {
		return 0, false
	}
	number, err := strconv.Atoi(value)
	if err != nil {
		return 0, false
	}
	return number, true
}

// enrichStageFrames 把每个招式的「总帧数」改成引用动画的真实帧数，并记下主片断名。
//
// 主片断 = 时间轴跨度最大的那条片断（多条并列时取靠后的），它的 name 就是这一招
// 实际播放的动画；真实时长取该动画的帧数。读不到动画时退回旧的 endframe+1，
// 免得显示成 0。
func enrichStageFrames(info *inspection, client string) {
	if info == nil || client == "" {
		return
	}
	cache := map[string]int{}
	frames := func(name string) int {
		if value, ok := cache[name]; ok {
			return value
		}
		value := anmFrames(client, name)
		cache[name] = value
		return value
	}
	for wi := range info.weapons {
		weapon := &info.weapons[wi]
		for si := range weapon.Stages {
			stage := &weapon.Stages[si]
			blocks := info.blocks[actionKey(stage.Action)]
			if len(blocks) == 0 {
				continue
			}
			best := AnmSegment{}
			span := -1
			for _, blk := range blocks {
				for _, segment := range segmentsOf(blk.node, "") {
					width := segment.End - segment.Start + 1
					if width >= span && segment.Name != "" {
						span, best = width, segment
					}
				}
			}
			if best.Name == "" {
				continue
			}
			stage.Clip = best.Name
			if length := frames(best.Name); length > 0 {
				stage.Frames = length
			}
		}
	}
}

// stageTracks 是「帧轨道」读接口：某把武器每个招式一条轨道。
func stageTracks(info *inspection, client, weaponID string) []map[string]any {
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
			segments := []AnmSegment{}
			markers := []StageMarker{}
			boxes := []StageBox{}
			for _, blk := range blocks {
				segments = append(segments, segmentsOf(blk.node, client)...)
				markers = append(markers, markersOf(blk.node)...)
				boxes = append(boxes, blockBoxesOf(blk.node)...)
			}
			sort.SliceStable(markers, func(i, j int) bool { return markers[i].Start < markers[j].Start })
			length := stage.Frames
			if length <= 0 {
				for _, segment := range segments {
					if segment.End+1 > length {
						length = segment.End + 1
					}
				}
			}
			result = append(result, map[string]any{
				"state":    stage.State,
				"action":   stage.Action,
				"label":    stage.Label,
				"frames":   length,
				"clip":     stage.Clip,
				"segments": segments,
				"markers":  markers,
				"boxes":    boxes,
			})
		}
		break
	}
	return result
}

// ---------------------------------------------------------------------------
// 攻击范围（<AttackScope>）编辑
// ---------------------------------------------------------------------------

// scopeKeys 是允许编辑的六个盒尺寸属性。AttackScope 上还可能带 skillproid /
// dirtype，改写时按「覆盖给出的键、保留其余」处理，不会被抹掉。
var scopeKeys = map[string]bool{
	"centerx": true, "centery": true, "centerz": true,
	"length": true, "width": true, "heigth": true,
}

var scopeRequired = []string{"centerx", "centery", "centerz", "length", "width", "heigth"}

var (
	anmSegmentPattern  = regexp.MustCompile(`(?s)<Anm\b([^>]*?)(/\s*>|>(.*?)</Anm\s*>)`)
	anmAttrPattern     = regexp.MustCompile(`([A-Za-z_][\w]*)\s*=\s*"([^"]*)"`)
	attackScopePattern = regexp.MustCompile(`(?s)<AttackScope\b[^>]*?/\s*>|<AttackScope\b[^>]*?>.*?</AttackScope\s*>`)
)

func scopeText(attrs []FrameSwitchAttr) string {
	parts := []string{"<AttackScope"}
	for _, attr := range attrs {
		parts = append(parts, attr.Key+`="`+attr.Value+`"`)
	}
	return strings.Join(parts, " ") + " />"
}

// mergeScopeAttrs 保留元素原本不被编辑的属性（skillproid / dirtype…），
// 用编辑给出的六个键覆盖。顺序沿用原标题。
func mergeScopeAttrs(existing, updates []FrameSwitchAttr) []FrameSwitchAttr {
	merged := make([]FrameSwitchAttr, 0, len(existing)+len(updates))
	seen := map[string]bool{}
	for _, attr := range existing {
		value := attr.Value
		for _, update := range updates {
			if update.Key == attr.Key {
				value = update.Value
				break
			}
		}
		merged = append(merged, FrameSwitchAttr{Key: attr.Key, Value: value})
		seen[attr.Key] = true
	}
	for _, update := range updates {
		if !seen[update.Key] {
			merged = append(merged, update)
			seen[update.Key] = true
		}
	}
	return merged
}

// rewriteSegmentScope 把某个片断的 <AttackScope> 换成给定属性；原本没有就新建。
// 只动块文本，注释区不动。返回是否发生了改变。
func rewriteSegmentScope(block, segmentID string, attrs []FrameSwitchAttr) (string, bool, error) {
	if strings.TrimSpace(segmentID) == "" {
		return block, false, fmt.Errorf("片断编号为空")
	}
	for _, loc := range anmSegmentPattern.FindAllStringSubmatchIndex(block, -1) {
		if insideComment(block, loc[0]) {
			continue
		}
		head := block[loc[2]:loc[3]]
		id := ""
		for _, m := range anmAttrPattern.FindAllStringSubmatch(head, -1) {
			if m[1] == "id" {
				id = strings.TrimSpace(m[2])
			}
		}
		if id != segmentID {
			continue
		}
		selfClosing := strings.TrimSpace(block[loc[4]:loc[5]]) != "" && loc[6] < 0
		bodyStart, bodyEnd := -1, -1
		if !selfClosing {
			bodyStart, bodyEnd = loc[6], loc[7]
		}
		want := scopeText(attrs)
		if !selfClosing {
			body := block[bodyStart:bodyEnd]
			existing := []FrameSwitchAttr{}
			if found := attackScopePattern.FindString(body); found != "" {
				for _, m := range anmAttrPattern.FindAllStringSubmatch(found, -1) {
					existing = append(existing, FrameSwitchAttr{Key: m[1], Value: m[2]})
				}
				replacement := scopeText(mergeScopeAttrs(existing, attrs))
				if replacement == found {
					return block, false, nil
				}
				start := bodyStart + strings.Index(body, found)
				return block[:start] + replacement + block[start+len(found):], true, nil
			}
			// 没有就插到 </Anm> 之前。
			insert := bodyStart + len(body)
			for insert > bodyStart && (block[insert-1] == '\t' || block[insert-1] == ' ') {
				insert--
			}
			return block[:insert] + want + "\n\t" + block[insert:], true, nil
		}
		// 自闭合片断：展开成容器再塞进去。
		tagText := "<Anm" + head + ">"
		return block[:loc[0]] + tagText + "\n\t\t" + want + "\n\t</Anm>" + block[loc[5]:], true, nil
	}
	return block, false, fmt.Errorf("动作里没有片断 %s", segmentID)
}

// validateScopes 校验攻击范围编辑：状态要在本武器的动作行里，片断要真的存在于
// 该状态的动作块，六个属性齐全、在范围内。
func validateScopes(a *archive, weaponKey string, edits map[int]map[string][]FrameSwitchAttr, info *inspection) error {
	actionText, err := a.text("itemact.txt")
	if err != nil {
		return err
	}
	lines := strings.Split(actionText, "\n")
	header := map[string]bool{}
	for _, state := range strings.Split(strings.TrimSuffix(lines[0], "\r"), "\t")[2:] {
		header[strings.TrimSpace(state)] = true
	}
	row := actionRowIndex(actionText)[weaponKey]
	if row == nil {
		return fmt.Errorf("武器 %s 不在本客户端的动作表中", weaponKey)
	}
	columns := map[string]int{}
	for index, name := range strings.Split(strings.TrimSuffix(lines[0], "\r"), "\t") {
		columns[name] = index
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
		column, ok := columns[strconv.Itoa(stage)]
		if !ok || column >= len(row) {
			return fmt.Errorf("状态 %d 不属于该武器", stage)
		}
		action := row[column]
		if action == "" || action == "0" {
			return fmt.Errorf("状态 %d 没有动作，无法编辑攻击范围", stage)
		}
		blocks := info.blocks[actionKey(action)]
		if len(blocks) == 0 {
			return fmt.Errorf("状态 %d 的动作 %s 不存在", stage, action)
		}
		existing := map[string]bool{}
		for _, blk := range blocks {
			for _, segment := range segmentsOf(blk.node, "") {
				if segment.ID != "" {
					existing[segment.ID] = true
				}
			}
		}
		ids := make([]string, 0, len(edits[stage]))
		for id := range edits[stage] {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			if !existing[id] {
				return fmt.Errorf("状态 %d 的动作里没有片断 %s", stage, id)
			}
			attrs := edits[stage][id]
			seen := map[string]bool{}
			for _, attr := range attrs {
				if !scopeKeys[attr.Key] {
					return fmt.Errorf("状态 %d 片断 %s：不支持的范围属性 %s", stage, id, attr.Key)
				}
				if seen[attr.Key] {
					return fmt.Errorf("状态 %d 片断 %s：属性 %s 重复", stage, id, attr.Key)
				}
				seen[attr.Key] = true
				if !counterSafeValue.MatchString(attr.Value) {
					return fmt.Errorf("状态 %d 片断 %s：属性 %s 的值 %q 含非法字符", stage, id, attr.Key, attr.Value)
				}
				number, err := strconv.Atoi(attr.Value)
				if err != nil || number < -9999 || number > 9999 {
					return fmt.Errorf("状态 %d 片断 %s：%s 需要 -9999..9999 的整数", stage, id, attr.Key)
				}
			}
			for _, key := range scopeRequired {
				if !seen[key] {
					return fmt.Errorf("状态 %d 片断 %s：缺少 %s", stage, id, key)
				}
			}
		}
	}
	return nil
}

// applyScopes 把攻击范围编辑写进动作块。跑在 applyBlockElements 之后：
// 需要独占块时在这里克隆，原武器一个字节不动。
func applyScopes(a *archive, state *weaponState, items []Item) (*archive, error) {
	if len(state.Scopes) == 0 {
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
	weaponKeys := make([]string, 0, len(state.Scopes))
	for key := range state.Scopes {
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
		stages := make([]int, 0, len(state.Scopes[weaponKey]))
		for stage := range state.Scopes[weaponKey] {
			stages = append(stages, stage)
		}
		sort.Ints(stages)
		for _, stage := range stages {
			column, ok := columns[strconv.Itoa(stage)]
			if !ok || column >= len(row) {
				return nil, fmt.Errorf("状态 %d 不属于武器 %s", stage, weaponKey)
			}
			action := row[column]
			if action == "" || action == "0" {
				return nil, fmt.Errorf("状态 %d 没有动作，无法编辑攻击范围", stage)
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
			rewritten := target
			changed := false
			ids := make([]string, 0, len(state.Scopes[weaponKey][stage]))
			for id := range state.Scopes[weaponKey][stage] {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			for _, id := range ids {
				next, touched, err := rewriteSegmentScope(rewritten, id, state.Scopes[weaponKey][stage][id])
				if err != nil {
					return nil, err
				}
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

// scopeFiles 报告攻击范围编辑会碰到的动画条目，写盘守卫据此放行。
func scopeFiles(source *archive, state *weaponState) map[string]bool {
	files := map[string]bool{}
	if len(state.Scopes) == 0 {
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
	for weaponKey, perStage := range state.Scopes {
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
