package desktop

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type field struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Min  int    `json:"min"`
	Max  int    `json:"max"`
	// Oneshot marks a non-numeric field: the editor shows a labelled picker
	// instead of a free text box, so the author cannot type a number the
	// client has no animation for.
	Oneshot bool `json:"oneshot,omitempty"`
}

var propertyFields = []field{{"SkillDamage", "基础伤害", 0, 10000, false}, {"SkillEnhanceDamage", "强化伤害", 0, 10000, false}, {"RepulseTarget", "击退参数", 0, 120, true}, {"TripTarget", "击倒参数", 0, 2, true}, {"TargetFlurr", "浮空参数", 0, 3, true}, {"StandHurt", "站立受击动作", 0, 255, true}, {"StandHurtDown", "倒地受击动作", 0, 255, true}, {"StandHurtFly", "站立受击飞行动作", 0, 255, true}, {"FlyHurt", "飞行受击动作", 0, 255, true}, {"JumpHurtDown", "空中受击动作", 0, 255, true}, {"JumpHurtFall", "空中落地动作", 0, 255, true}}

type xmlNode struct {
	tag      string
	attrs    []xml.Attr
	children []*xmlNode
	text     string
	comment  bool
}

func parseXML(text string) (*xmlNode, error) {
	if strings.Contains(strings.ToUpper(text), "<!DOCTYPE") {
		return nil, fmt.Errorf("不支持 XML DTD")
	}
	decoder := xml.NewDecoder(strings.NewReader(text))
	decoder.CharsetReader = func(_ string, reader io.Reader) (io.Reader, error) { return reader, nil }
	var root *xmlNode
	stack := []*xmlNode{}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch value := token.(type) {
		case xml.StartElement:
			node := &xmlNode{tag: value.Name.Local, attrs: append([]xml.Attr(nil), value.Attr...)}
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, node)
			} else {
				if root != nil {
					return nil, fmt.Errorf("XML 根节点重复")
				}
				root = node
			}
			stack = append(stack, node)
		case xml.EndElement:
			if len(stack) == 0 {
				return nil, fmt.Errorf("XML 层级错误")
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].text += string(value)
			}
		case xml.Comment:
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, &xmlNode{comment: true, text: string(value)})
			}
		}
	}
	if root == nil || len(stack) != 0 {
		return nil, fmt.Errorf("XML 不完整")
	}
	return root, nil
}
func (n *xmlNode) get(key string) string {
	for _, attr := range n.attrs {
		if attr.Name.Local == key {
			return attr.Value
		}
	}
	return ""
}
func (n *xmlNode) set(key, value string) {
	for index := range n.attrs {
		if n.attrs[index].Name.Local == key {
			n.attrs[index].Value = value
			return
		}
	}
	n.attrs = append(n.attrs, xml.Attr{Name: xml.Name{Local: key}, Value: value})
}
func (n *xmlNode) walk(visit func(*xmlNode)) {
	visit(n)
	for _, child := range n.children {
		child.walk(visit)
	}
}
func (n *xmlNode) clone() *xmlNode {
	copy := &xmlNode{tag: n.tag, attrs: append([]xml.Attr(nil), n.attrs...), text: n.text, comment: n.comment}
	for _, child := range n.children {
		copy.children = append(copy.children, child.clone())
	}
	return copy
}
func (n *xmlNode) encode(encoder *xml.Encoder) error {
	if n.comment {
		return encoder.EncodeToken(xml.Comment(n.text))
	}
	start := xml.StartElement{Name: xml.Name{Local: n.tag}, Attr: n.attrs}
	if err := encoder.EncodeToken(start); err != nil {
		return err
	}
	if n.text != "" {
		if err := encoder.EncodeToken(xml.CharData(n.text)); err != nil {
			return err
		}
	}
	for _, child := range n.children {
		if err := child.encode(encoder); err != nil {
			return err
		}
	}
	return encoder.EncodeToken(start.End())
}
func (n *xmlNode) serialize() (string, error) {
	var output bytes.Buffer
	encoder := xml.NewEncoder(&output)
	if err := n.encode(encoder); err != nil {
		return "", err
	}
	if err := encoder.Flush(); err != nil {
		return "", err
	}
	return output.String(), nil
}
func (a *archive) xml(name string) (*xmlNode, error) {
	text, err := a.text(name)
	if err != nil {
		return nil, err
	}
	return parseXML(text)
}

type Buff struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	// Desc 是 ustate.xml 注释里"名字"后面的那段效果说明。游戏配置经常在这一句里
	// 写明副作用（type=27 就写着「被动状态，level值无效 操作键都乱掉」），
	// 不显示出来的话，用户选了个让操作失灵的状态也看不出来。
	Desc string `json:"desc,omitempty"`
}

func buffs(a *archive) ([]Buff, error) {
	root, err := a.xml("ustate.xml")
	if err != nil {
		return nil, err
	}
	result := []Buff{
		{ID: 0, Name: "保持原效果"},
		{ID: -1, Name: "清除原有 BUFF"},
	}
	label := ""
	for _, node := range root.children {
		if node.comment {
			label = strings.Split(strings.TrimSpace(node.text), "\n")[0]
			continue
		}
		if node.tag != "Data" {
			continue
		}
		active := false
		node.walk(func(child *xmlNode) {
			if child.tag == "LogicHandle" && child.get("Type") != "" {
				active = true
			}
		})
		if node.get("ActiveState") == "0" && active {
			id, err := strconv.Atoi(node.get("type"))
			if err != nil {
				return nil, err
			}
			// 注释形如「恐惧，被动状态，level值无效操作键都乱掉」：
			// 第一个标点前是名字，后面全是效果说明。
			// 必须按 rune 切：全角逗号占 3 字节，用字节索引切会留下半个字。
			name, desc := strings.TrimSpace(label), ""
			runes := []rune(label)
			cut := -1
			for i, r := range runes {
				if r == '，' || r == ',' || r == '、' {
					cut = i
					break
				}
			}
			if cut > 0 && cut+1 < len(runes) {
				name = strings.TrimSpace(string(runes[:cut]))
				desc = strings.TrimSpace(string(runes[cut+1:]))
			}
			if id == 1 {
				name = "中毒"
			} else if id == 37 {
				name = "燃烧（献祭燃烧）"
			}
			if runes := []rune(name); len(runes) > 20 {
				name = string(runes[:20])
			}
			if runes := []rune(desc); len(runes) > 60 {
				desc = string(runes[:60])
			}
			if name == "" {
				name = fmt.Sprintf("异常状态 %d", id)
			}
			result = append(result, Buff{ID: id, Name: name, Desc: desc})
		}
		label = ""
	}
	return result, nil
}

type ComboNode struct {
	State string `json:"state"`
	Keys  string `json:"keys"`
}
type Combo struct {
	Name  string      `json:"name"`
	Nodes []ComboNode `json:"nodes"`
}

// Animation comments describe the action, not necessarily its input or public name.
func actionDescription(node *xmlNode) string {
	for _, child := range node.children {
		if !child.comment {
			break
		}
		text := strings.Join(strings.Fields(child.text), " ")
		if len([]rune(text)) > 100 || strings.ContainsAny(text, "<>�") {
			continue
		}
		if strings.ContainsFunc(text, func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
			return text
		}
	}
	return ""
}

func combos(a *archive, id string) ([]Combo, error) {
	name := "weapon/" + id + "/combotip.xml"
	result := []Combo{}
	if _, ok := a.entries[name]; !ok {
		return result, nil
	}
	root, err := a.xml(name)
	if err != nil {
		return nil, err
	}
	return readComboSequences(root), nil
}

// Each ActNode is the next input in a sequence, not an independent shortcut.
func readComboSequences(root *xmlNode) []Combo {
	result := []Combo{}
	root.walk(func(node *xmlNode) {
		if node.tag != "Sequence" {
			return
		}
		sequence := Combo{Name: node.get("Name"), Nodes: []ComboNode{}}
		prefix := ""
		complete := true
		for _, child := range node.children {
			if child.comment || child.tag != "ActNode" {
				continue
			}
			keys := []string{}
			for _, input := range child.children {
				if input.comment || input.tag != "Icon" {
					continue
				}
				key := strings.TrimSpace(input.get("Name"))
				if key == "" {
					complete = false
				} else {
					keys = append(keys, key)
				}
			}
			if len(keys) == 0 {
				complete = false
			}
			prefix += strings.Join(keys, "")
			label := prefix
			if !complete {
				label = "按键提示不完整（" + sequence.Name + "）"
			}
			sequence.Nodes = append(sequence.Nodes, ComboNode{child.get("State"), label})
		}
		result = append(result, sequence)
	})
	return result
}

type Hit struct {
	ID     string            `json:"id"`
	Values map[string]string `json:"values"`
	Buff   string            `json:"buff"`
}
type Stage struct {
	Stage       int      `json:"stage"`
	State       string   `json:"state"`
	Label       string   `json:"label"`
	Action      string   `json:"action"`
	PropertyIDs []string `json:"property_ids"`
	Hits        []Hit    `json:"hits"`
	// Effects lists the effect ids the action block references, so the remap
	// template picker can preview what the client will load for this move.
	Effects   []string `json:"effects,omitempty"`
	Supported bool     `json:"supported"`
	Reason    string   `json:"reason"`
	// Counter is the parry window this stage's action block declares — the
	// third transition channel, triggered by the opponent's attack rather than
	// by a key. It is not visible in the combo-chain or frame-switch editors.
	Counter *CounterWindow `json:"counter,omitempty"`
	// Frames is the action's real length in frames. Almost every shipped
	// <AnmDesc> ends with a placeholder segment running to frame 99/999 (hold
	// the pose until interrupted), so the raw max endframe is not the length.
	// enrichStageFrames() replaces this with the frame count of the animation
	// the stage actually plays (Data/animation/<name>.anm); Clip names it.
	Frames int `json:"frames,omitempty"`
	// RawFrames keeps the old max(endframe)+1 so callers can still spot the
	// placeholder tail when the .anm could not be read.
	RawFrames int    `json:"raw_frames,omitempty"`
	Clip      string `json:"clip,omitempty"`
}
type Weapon struct {
	ID          int              `json:"id"`
	Name        string           `json:"name"`
	Icon        string           `json:"icon"`
	Description string           `json:"description"`
	Type        string           `json:"type"`
	Model       string           `json:"model"`
	Stages      []Stage          `json:"stages"`
	Combos      []Combo          `json:"combos"`
	BuffIDs     []int            `json:"buff_ids"`
	Allowed     map[string][]int `json:"allowed_values"`
	// ComboRows is how many delayacttable.xml transitions the weapon owns.
	// Zero means the client can never advance past the first hit.
	ComboRows int `json:"combo_rows"`
	// ComboSuggestion points at the weapon whose action row this one copies,
	// used as the default donor when completing a missing combo table.
	ComboSuggestion int `json:"combo_suggestion,omitempty"`
	// Effects reports whether acteffect.xml registers action effects for it.
	Effects bool `json:"effects"`
}
type block struct {
	original string
	node     *xmlNode
}

// item.txt column 2 is the weapon subtype; retain unknown values explicitly.
func weaponType(item Item) string {
	if len(item.Fields) <= 2 || item.Fields[2] == "" || item.Fields[2] == "#" {
		return "未分类"
	}
	switch item.Fields[2] {
	case "1":
		return "刀类"
	case "2":
		return "剑类"
	case "3":
		return "长柄"
	case "4":
		return "拳套"
	case "5":
		return "拳脚"
	case "6":
		return "重型"
	case "7":
		return "奇门"
	default:
		return "类型 " + item.Fields[2]
	}
}

// weaponTypes lists the item.txt column-2 subtypes an author may pick when
// inventing a weapon. The labels mirror weaponType so the editor and the
// catalogue speak the same language.
var weaponTypes = []map[string]string{
	{"value": "1", "label": "刀类"},
	{"value": "2", "label": "剑类"},
	{"value": "3", "label": "长柄"},
	{"value": "4", "label": "拳套"},
	{"value": "5", "label": "拳脚"},
	{"value": "6", "label": "重型"},
	{"value": "7", "label": "奇门"},
}

// weaponModels lists the RenderWare clumps shipped inside the client. A
// self-made weapon has to borrow one of them, because the client binary is
// never touched and unknown model names fall back to an invisible mesh.
func weaponModels(client string) []string {
	models := []string{}
	folder := filepath.Join(client, "Data", "Weapon", "Model")
	entries, err := os.ReadDir(folder)
	if err != nil {
		return models
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".dff") {
			continue
		}
		models = append(models, entry.Name())
	}
	sort.Strings(models)
	return models
}

type inspection struct {
	weapons    []Weapon
	blocks     map[string][]block
	properties map[string][]*xmlNode
	ordered    []*xmlNode
	owners     map[string]map[string]bool
}

var animationPattern = regexp.MustCompile(`(?s)<AnmDesc\b[^>]*>.*?</AnmDesc\s*>`)

func actionKey(action string) string {
	if len(action) <= 4 {
		return ""
	}
	id, err := strconv.Atoi(action[4:])
	if err != nil {
		return ""
	}
	return action[:4] + "/" + strconv.Itoa(id)
}

// currentBlock returns the text of the one <AnmDesc> block inside animation
// whose id equals want. A single pass can rewrite the same block more than
// once — two states of one weapon may remap onto the same action — and each
// rewrite starts from the file as it stands *now*, so the pristine text
// inspect() captured is gone by the second visit. Matching by id keeps that
// rewrite working instead of failing with "动作定义无法唯一替换".
func currentBlock(animation, want string) (string, bool) {
	found := ""
	count := 0
	for _, candidate := range animationPattern.FindAllString(animation, -1) {
		node, err := parseXML(candidate)
		if err != nil {
			continue
		}
		if strings.TrimSpace(node.get("id")) == want {
			found = candidate
			count++
		}
	}
	return found, count == 1
}

// clearStageBlockEdits 清掉一个状态上"绑在动作块/片断"上的全部编辑。
//
// 重映射换了动作以后，旧记录指向的片断与块已经不属于这个状态：留着它们，
// applyScopes / applyFrameSwitches / applyCounters / applyBlockElements 会去改
// **新块里恰好同号的片断**，还会反复触发"共用块先克隆"的分支，把 itemact 的动作列
// 改成一个和 remap 记录对不上的克隆编号（实测 253300 状态 2081 就是这样：
// remap 记的是 2204086，客户端列却被改成 2204999，技能直接放不出来）。
func clearStageBlockEdits(state *weaponState, key string, stage int) {
	if perStage, ok := state.Scopes[key]; ok {
		delete(perStage, stage)
		if len(perStage) == 0 {
			delete(state.Scopes, key)
		}
	}
	if perStage, ok := state.FrameSwitches[key]; ok {
		delete(perStage, stage)
		if len(perStage) == 0 {
			delete(state.FrameSwitches, key)
		}
	}
	if perStage, ok := state.Counters[key]; ok {
		delete(perStage, stage)
		if len(perStage) == 0 {
			delete(state.Counters, key)
		}
	}
	if perStage, ok := state.BlockElements[key]; ok {
		delete(perStage, stage)
		if len(perStage) == 0 {
			delete(state.BlockElements, key)
		}
	}
}

func inspect(a *archive, items []Item) (*inspection, error) {
	buffRows, err := buffs(a)
	if err != nil {
		return nil, err
	}
	buffIDs := []int{}
	for _, row := range buffRows {
		buffIDs = append(buffIDs, row.ID)
	}
	text, err := a.text("itemact.txt")
	if err != nil {
		return nil, err
	}
	lines := [][]string{}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, strings.Split(line, "\t"))
		}
	}
	if len(lines) < 2 {
		return nil, fmt.Errorf("武器动作表为空")
	}
	header := lines[0]
	columns := map[string]int{}
	for index, state := range header {
		columns[state] = index
	}
	owners := map[string]map[string]bool{}
	for _, row := range lines[1:] {
		if len(row) > len(header) || len(row) < 2 {
			return nil, fmt.Errorf("武器动作字段错误")
		}
		for index, action := range row[2:] {
			if action != "0" {
				if owners[action] == nil {
					owners[action] = map[string]bool{}
				}
				owners[action][row[0]+":"+header[index+2]] = true
			}
		}
	}
	result := &inspection{weapons: []Weapon{}, blocks: map[string][]block{}, properties: map[string][]*xmlNode{}, owners: owners}
	files := map[string]bool{}
	for action := range owners {
		if len(action) > 4 {
			files["animation/"+action[:4]+".xml"] = true
		}
	}
	for file := range files {
		if _, ok := a.entries[file]; !ok {
			continue
		}
		animation, err := a.text(file)
		if err != nil {
			return nil, err
		}
		prefix := strings.TrimSuffix(strings.TrimPrefix(file, "animation/"), ".xml")
		for _, original := range animationPattern.FindAllString(animation, -1) {
			node, err := parseXML(original)
			if err != nil {
				return nil, err
			}
			id, err := strconv.Atoi(strings.TrimSpace(node.get("id")))
			if err != nil {
				return nil, err
			}
			key := prefix + "/" + strconv.Itoa(id)
			result.blocks[key] = append(result.blocks[key], block{original, node})
		}
	}

	root, err := a.xml("skillproperty.xml")
	if err != nil {
		return nil, err
	}
	for _, node := range root.children {
		if node.comment {
			continue
		}
		id := node.get("SkillProId")
		result.properties[id] = append(result.properties[id], node)
		result.ordered = append(result.ordered, node)
	}
	allowed := map[string][]int{}
	for _, field := range propertyFields[2:] {
		values := map[int]bool{}
		for _, node := range result.ordered {
			value := node.get(field.Key)
			if value == "" {
				value = "0"
			}
			number, err := strconv.Atoi(value)
			if err != nil {
				return nil, err
			}
			values[number] = true
		}
		for number := range values {
			allowed[field.Key] = append(allowed[field.Key], number)
		}
		sort.Ints(allowed[field.Key])
	}
	names := map[string]Item{}
	for _, item := range items {
		if item.Kind == 25 {
			names[strconv.FormatUint(uint64(item.ID), 10)] = item
		}
	}
	for _, row := range lines[1:] {
		item, ok := names[row[0]]
		if !ok {
			continue
		}
		sequences, err := combos(a, row[0])
		if err != nil {
			return nil, err
		}
		// The action table is authoritative; combo tips are labels, not an allowlist.
		states := append([]string(nil), header[2:len(row)]...)
		sort.SliceStable(states, func(i, j int) bool {
			a, _ := strconv.Atoi(states[i])
			b, _ := strconv.Atoi(states[j])
			return a >= 2000 && a < 3000 && !(b >= 2000 && b < 3000)
		})

		id, err := strconv.Atoi(row[0])
		if err != nil {
			return nil, err
		}
		model := ""
		if len(item.Fields) > 7 {
			model = item.Fields[7]
		}
		weapon := Weapon{ID: id, Name: item.Name, Icon: item.Icon, Description: item.Description, Type: weaponType(item), Model: model, Stages: []Stage{}, Combos: sequences, BuffIDs: buffIDs, Allowed: allowed}
		for _, state := range states {
			column, ok := columns[state]
			if !ok || column >= len(row) {
				return nil, fmt.Errorf("招式列缺失")
			}
			action := row[column]
			if action == "0" || action == "" {
				continue
			}
			number, err := strconv.Atoi(state)
			if err != nil {
				return nil, err
			}
			if number >= 2011 && number <= 2016 {
				number -= 2010
			}
			candidates := result.blocks[actionKey(action)]
			refs := map[string]bool{}
			for _, candidate := range candidates {
				candidate.node.walk(func(node *xmlNode) {
					ref := node.get("skillproid")
					if ref != "" && ref != "0" {
						refs[ref] = true
					}
				})
			}
			refIDs := []string{}
			unique := true
			for ref := range refs {
				refIDs = append(refIDs, ref)
				if len(result.properties[ref]) != 1 {
					unique = false
				}
			}
			sort.Strings(refIDs)
			reason := ""
			if len(candidates) != 1 || len(refs) == 0 || !unique {
				reason = "动作或命中属性未能唯一对应，暂不可应用"
				if len(candidates) == 1 && len(refs) == 0 {
					reason = "该动作没有直接命中属性（移动、受击或间接效果），不提供伤害编辑"
				}
			} else {
				for _, ref := range refIDs {
					node := result.properties[ref][0]
					if node.get("TargetSelf") != "0" || node.get("TargetEnemy") != "1" {
						reason = "该段包含非敌方命中效果，暂不可应用"
					}
				}
			}
			hits := []Hit{}
			for _, ref := range refIDs {
				if len(result.properties[ref]) != 1 {
					continue
				}
				node := result.properties[ref][0]
				values := map[string]string{}
				for _, field := range propertyFields {
					value := node.get(field.Key)
					if value == "" {
						value = "0"
					}
					values[field.Key] = value
				}
				buff := "0"
				for _, attr := range node.attrs {
					if attr.Name.Local == "UnNormalState" {
						buff = attr.Value
					}
				}
				for _, key := range []string{"UStateLevel", "UStateLastCycle"} {
					if value := node.get(key); value != "" {
						values[key] = value
					}
				}
				hits = append(hits, Hit{ref, values, buff})
			}
			labels := []string{}
			labelSeen := map[string]bool{}
			for _, sequence := range sequences {
				for _, node := range sequence.Nodes {
					if node.State == state && !labelSeen[node.Keys] {
						labels = append(labels, node.Keys)
						labelSeen[node.Keys] = true
					}
				}
			}
			label := strings.Join(labels, " / ")
			if label == "" {
				label = "动作说明缺失（按键待核实）"
				if len(candidates) == 1 {
					if description := actionDescription(candidates[0].node); description != "" {
						label = description + "（动画说明，非按键）"
					}
				}
			}

			raw := actionFrames(candidates)
			stage := Stage{
				Stage: number, State: state, Label: label, Action: action,
				PropertyIDs: refIDs, Hits: hits, Effects: effectPreviews(candidates),
				Supported: reason == "", Reason: reason,
				Frames: raw, RawFrames: raw,
			}
			stage.Counter = counterWindowOf(candidates)
			weapon.Stages = append(weapon.Stages, stage)
		}
		// 招架落到空处是不生效的：目标状态必须是本武器动作行里真实存在的一列。
		reachable := map[int]bool{}
		for _, stage := range weapon.Stages {
			if number, err := strconv.Atoi(stage.State); err == nil {
				reachable[number] = true
			}
		}
		for index := range weapon.Stages {
			counter := weapon.Stages[index].Counter
			if counter == nil {
				continue
			}
			number, err := strconv.Atoi(counter.NextState)
			if err != nil || !reachable[number] {
				counter.NextUnreachable = true
			}
		}
		// A donor-less weapon starts with every state zeroed, so it has no
		// stages yet; it must still appear in the catalogue so the author can
		// apply it and then remap states onto it.
		result.weapons = append(result.weapons, weapon)
	}
	return result, nil
}

type Rule struct {
	Stage      int                           `json:"stage"`
	Buff       int                           `json:"buff"`
	Level      int                           `json:"level"`
	Duration   int                           `json:"duration"`
	Properties map[string]map[string]float64 `json:"properties,omitempty"`
}

func includes[T comparable](items []T, value T) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}
func validateRules(rules []Rule, weapon Weapon) ([]Rule, error) {
	if rules == nil || len(rules) > len(weapon.Stages) {
		return nil, fmt.Errorf("连招配置格式错误")
	}
	result := []Rule{}
	stages := map[int]Stage{}
	for _, stage := range weapon.Stages {
		stages[stage.Stage] = stage
	}
	seen := map[int]bool{}
	fields := map[string]field{}
	for _, field := range propertyFields {
		fields[field.Key] = field
	}
	for _, rule := range rules {
		stage, ok := stages[rule.Stage]
		if !ok || seen[rule.Stage] || !includes(weapon.BuffIDs, rule.Buff) || rule.Level < 1 || rule.Level > 3 || rule.Duration < 1 || rule.Duration > 60000 {
			return nil, fmt.Errorf("效果参数超出范围")
		}
		seen[rule.Stage] = true
		for ref, values := range rule.Properties {
			if !includes(stage.PropertyIDs, ref) || values == nil {
				return nil, fmt.Errorf("命中属性 %s 不属于状态 %d（可用：%v）", ref, rule.Stage, stage.PropertyIDs)
			}
			for key, value := range values {
				field, ok := fields[key]
				if !ok || math.IsNaN(value) || math.IsInf(value, 0) || value < float64(field.Min) || value > float64(field.Max) {
					return nil, fmt.Errorf("伤害或攻击效果超出范围")
				}
				if key != "SkillDamage" && key != "SkillEnhanceDamage" && (value != math.Trunc(value) || !includes(weapon.Allowed[key], int(value))) {
					return nil, fmt.Errorf("受击参数不属于客户端原生配置")
				}
			}
		}
		if rule.Buff != 0 || len(rule.Properties) > 0 {
			if !stage.Supported {
				return nil, fmt.Errorf("%s", stage.Reason)
			}
			result = append(result, rule)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Stage < result[j].Stage })
	return result, nil
}

// option is one choice of a non-numeric hit field. The editor renders the
// label (with the raw number appended) so an author picks "常规被击飞" instead
// of guessing what 11 means. Detail carries the consequence of the choice.
type option struct {
	Value  int    `json:"value"`
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
}

// hitOptions translates the enumerated hit parameters into readable choices.
// Values are drawn from what the official skillproperty.xml actually ships, so
// the picker can never offer a number the client has no animation for. The
// labels below were reverse-engineered from the action blocks the numbers point
// at (animation/300501.xml and neighbours), not invented.
var hitOptions = map[string][]option{
	"RepulseTarget": {
		{0, "不击退", "原地不动，只结算伤害与受击动作"},
		{1, "标准击退", "横向推开一小段距离"},
		{2, "强力击退", "推得更远，对手落地更晚"},
		{120, "极限击退", "官方最大值，龙拳跑 X1、合金双臂跑 X1 在用"},
	},
	"TargetFlurr": {
		{0, "不浮空", "对手不离地。击倒/击退要靠本字段以外的开关"},
		{1, "标准浮空（最常用）", "打成击飞。必须配合「站立受击飞行动作」= 常规被击飞"},
		{2, "高浮空", "飞得更高，滞空更久。空手重击、棒球类在用"},
		{3, "超高浮空", "最高档。棒球 C、跑动 C4 在用"},
	},
	"TripTarget": {
		{0, "不击倒", "对手不会摔趴"},
		{1, "标准击倒", "直接打倒趴地。要同时把「浮空参数」设为 0，否则判定成击飞"},
		{2, "强击倒", "倒地更重，起身更慢。徒手 3 段、跳轻 2/4 段在用"},
	},
	"StandHurt": {
		{0, "无受击动作", "不播受击动画，效果会很怪，慎用"},
		{1, "站立硬直（默认）", "最普通的挨打抖动"},
		{2, "侧翻 / 被打歪", "身体侧向踉跄"},
		{4, "轻微后仰", "位移很小的挨打姿势"},
		{5, "站姿被击倒（轻）", "较轻的倒地"},
		{7, "站立被击倒", "较重的倒地"},
		{11, "不击飞效果", "官方策划注释原话，用于「击飞版」招式的对照组"},
		{12, "受击变体 12", "变体动画，按招式风格挑"},
		{13, "受击变体 13", "变体动画，按招式风格挑"},
		{14, "受击变体 14", "变体动画，按招式风格挑"},
		{15, "受击变体 15", "变体动画，按招式风格挑"},
		{21, "受击变体 21", "变体动画，按招式风格挑"},
	},
	"StandHurtDown": {
		{0, "无倒地动作", "打倒后没有倒地动画，会卡住，勿用"},
		{1, "常规倒地（默认）", "最通用的摔趴"},
		{2, "较矮倒地", "倒地姿态更贴地"},
		{5, "侧向倒地", "往侧面摔"},
		{6, "倒地变体 6", "变体动画"},
		{7, "常规被击倒", "最常用的「被击倒」专用倒地"},
		{8, "倒地变体 8", "变体动画"},
		{9, "蜷缩滑行倒地", "倒地后带一段滑行"},
		{10, "倒地变体 10", "变体动画"},
		{23, "倒地变体 23", "变体动画"},
	},
	"StandHurtFly": {
		{0, "不播放飞行动作", "浮空却没有飞行动画，对手会原地抖动，勿用"},
		{1, "站立硬直（错配档）", "这是硬直不是飞行。配浮空会原地抖动"},
		{2, "小浮空飞行", "飞得矮、位移小，常配「不浮空」做短距离击飞"},
		{5, "中距击飞", "常见的中等距离飞出去"},
		{6, "击飞变体 6", "变体飞行轨迹"},
		{7, "常规击飞（最常用）", "标准飞出去，配「标准浮空」"},
		{9, "击飞变体 9", "变体飞行轨迹"},
		{10, "击飞变体 10", "变体飞行轨迹"},
		{11, "常规被击飞", "「上升 / 悬空击飞」三件套用的就是这个"},
		{12, "击飞变体 12", "变体飞行轨迹"},
		{20, "超远击飞", "飞得最远，回马枪类在用"},
	},
	"FlyHurt": {
		{0, "无动作", "已在飞行中被再次打中没有反应，勿用"},
		{1, "飞行中受击（默认）", "空中连击续接的通用档"},
		{2, "飞行中受击变体", "变体动画"},
		{5, "飞行中受击变体 5", "变体动画"},
		{6, "飞行中受击变体 6", "变体动画"},
		{7, "飞行中受击变体 7", "变体动画"},
		{11, "飞行中击飞", "被打得继续飞"},
		{12, "飞行中受击变体 12", "变体动画"},
	},
	"JumpHurtDown": {
		{0, "无动作", "空中被打没有反应，勿用"},
		{1, "空中受击倒地", "被打落后倒地（最常用之一）"},
		{2, "空中受击变体", "变体动画"},
		{4, "空中被打落（默认）", "最常见：跳到一半被打下来"},
		{5, "空中受击变体 5", "变体动画"},
		{6, "空中受击变体 6", "变体动画"},
		{7, "空中重击落", "坠得更快更重"},
		{10, "空中受击变体 10", "变体动画"},
		{23, "空中受击变体 23", "变体动画"},
	},
	"JumpHurtFall": {
		{0, "无落地动作", "落地无动画，勿用"},
		{1, "快速落地", "下落快、僵直短"},
		{3, "常规空中落地（默认）", "99% 招式使用的默认档"},
		{5, "落地变体 5", "变体动画"},
		{6, "落地变体 6", "变体动画"},
		{7, "落地变体 7", "变体动画"},
	},
}

// fieldMetaLabel keeps the picker honest: a value the client uses but this
// table does not name still shows up, marked rather than silently dropped.
func fieldOptionLabel(field string, value int) string {
	for _, item := range hitOptions[field] {
		if item.Value == value {
			return item.Label
		}
	}
	return ""
}

func effects(info *inspection) []map[string]any {
	result := []map[string]any{}
	for _, choice := range [][2]string{{"repulse", "击退"}, {"float", "上升 / 悬空击飞"}, {"fall", "击倒"}} {
		for _, node := range info.ordered {
			match := choice[0] == "repulse" && node.get("RepulseTarget") == "1" || choice[0] == "float" && node.get("TargetFlurr") == "1" && node.get("StandHurtDown") == "1" && node.get("StandHurtFly") == "11" || choice[0] == "fall" && node.get("TripTarget") == "1" && node.get("TargetFlurr") == "0"
			if !match || node.get("TargetEnemy") != "1" || node.get("TargetSelf") != "0" {
				continue
			}
			values := map[string]int{}
			for _, field := range propertyFields[2:] {
				values[field.Key], _ = strconv.Atoi(node.get(field.Key))
			}
			result = append(result, map[string]any{"id": choice[0], "name": choice[1], "source": node.get("SkillProId"), "values": values})
			break
		}
	}
	return result
}

// cloneKey identifies the hit-property clone of one stage: the stage number
// the rules use (2011..2016 normalized to 1..6) plus the property id before
// the clone.
func cloneKey(stage int, oldID string) string {
	return strconv.Itoa(stage) + "|" + oldID
}

// cloneMapOf returns the live clone-id map of the edit set, creating it on
// first use so render can record its allocations for later runs.
func cloneMapOf(state *weaponState) map[string]map[string]string {
	if state.PropertyClones == nil {
		state.PropertyClones = map[string]map[string]string{}
	}
	return state.PropertyClones
}

func copyCloneMap(source map[string]map[string]string) map[string]map[string]string {
	out := make(map[string]map[string]string, len(source))
	for weapon, entries := range source {
		copied := make(map[string]string, len(entries))
		for key, value := range entries {
			copied[key] = value
		}
		out[weapon] = copied
	}
	return out
}

// assignCloneIDs fills clones with a stable SkillProId for every (weapon,
// stage, hit-property) a render of plans will clone. A persisted entry is
// authoritative — it is what keeps combo-rule numbers valid across edits, so it
// is never re-issued. Missing entries follow render's allocation order (weapons
// sorted, stages sorted) starting at 900000000, skipping numbers the archive
// already uses and ones handed out earlier in this pass. clones is mutated in
// place; pass a copy (copyCloneMap) when only predicting.
func assignCloneIDs(info *inspection, plans map[string][]Rule, clones map[string]map[string]string) error {
	if clones == nil {
		return nil
	}
	weapons := map[string]Weapon{}
	for _, weapon := range info.weapons {
		weapons[strconv.Itoa(weapon.ID)] = weapon
	}
	keys := make([]string, 0, len(plans))
	for key := range plans {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	nextID := 900000000
	assigned := map[string]bool{}
	occupied := func(id string) bool {
		return assigned[id] || len(info.properties[id]) > 0
	}
	for _, key := range keys {
		weapon, ok := weapons[key]
		if !ok {
			return fmt.Errorf("武器配置已变化")
		}
		rules, err := validateRules(plans[key], weapon)
		if err != nil {
			return err
		}
		for _, rule := range rules {
			var stage Stage
			for _, candidate := range weapon.Stages {
				if candidate.Stage == rule.Stage {
					stage = candidate
					break
				}
			}
			for _, oldID := range stage.PropertyIDs {
				reference := cloneKey(rule.Stage, oldID)
				if id := clones[key][reference]; id != "" {
					assigned[id] = true
					continue
				}
				for occupied(strconv.Itoa(nextID)) {
					nextID++
				}
				next := strconv.Itoa(nextID)
				nextID++
				if clones[key] == nil {
					clones[key] = map[string]string{}
				}
				clones[key][reference] = next
				assigned[next] = true
			}
		}
	}
	return nil
}

func render(a *archive, items []Item, plans map[string][]Rule, cloneIDs map[string]map[string]string) ([]byte, error) {
	info, err := inspect(a, items)
	if err != nil {
		return nil, err
	}
	if cloneIDs == nil {
		cloneIDs = map[string]map[string]string{}
	}
	if err = assignCloneIDs(info, plans, cloneIDs); err != nil {
		return nil, err
	}
	weapons := map[string]Weapon{}
	for _, weapon := range info.weapons {
		weapons[strconv.Itoa(weapon.ID)] = weapon
	}
	animations := map[string]string{}
	actionTable, err := a.text("itemact.txt")
	if err != nil {
		return nil, err
	}
	actionLines := strings.Split(actionTable, "\n")
	header := strings.Split(strings.TrimSuffix(actionLines[0], "\r"), "\t")
	tableChanged := false
	entryProperties, err := newActionCloneProperties(a)
	if err != nil {
		return nil, err
	}
	reserved := map[string]bool{}
	for key := range info.blocks {
		reserved[key] = true
	}
	for action := range info.owners {
		reserved[actionKey(action)] = true
	}

	properties, err := a.text("skillproperty.xml")
	if err != nil {
		return nil, err
	}
	keys := []string{}
	for key := range plans {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	clones := []string{}
	propertiesChanged := 0
	for _, key := range keys {
		weapon, ok := weapons[key]
		if !ok {
			return nil, fmt.Errorf("武器配置已变化")
		}
		rules, err := validateRules(plans[key], weapon)
		if err != nil {
			return nil, err
		}
		for _, rule := range rules {
			var stage Stage
			for _, candidate := range weapon.Stages {
				if candidate.Stage == rule.Stage {
					stage = candidate
					break
				}
			}
			source := info.blocks[actionKey(stage.Action)][0]
			file := "animation/" + stage.Action[:4] + ".xml"
			animation, ok := animations[file]
			if !ok {
				animation, err = a.text(file)
				if err != nil {
					return nil, err
				}
			}
			changed := source.node.clone()
			shared := len(info.owners[stage.Action]) > 1
			if shared {
				cloneID := entryProperties.allocate(stage.Action, reserved)
				if cloneID == 0 {
					return nil, fmt.Errorf("%s 独立动作编号空间不足，未修改配置", file)
				}
				changed.set("id", strconv.Itoa(cloneID))
				newAction := stage.Action[:4] + fmt.Sprintf("%03d", cloneID)
				replaced := false
				for i, line := range actionLines[1:] {
					ending := ""
					if strings.HasSuffix(line, "\r") {
						ending = "\r"
					}
					cols := strings.Split(strings.TrimSuffix(line, "\r"), "\t")
					if len(cols) < 2 || cols[0] != key {
						continue
					}
					for j, state := range header {
						if j >= 2 && j < len(cols) && state == stage.State {
							cols[j] = newAction
							replaced = true
						}
					}
					actionLines[i+1] = strings.Join(cols, "\t") + ending
				}
				if !replaced {
					return nil, fmt.Errorf("找不到待隔离招式")
				}
				tableChanged = true
			}
			remap := map[string]string{}
			for _, oldID := range stage.PropertyIDs {
				newID := cloneIDs[key][cloneKey(rule.Stage, oldID)]
				if newID == "" {
					return nil, fmt.Errorf("克隆编号缺失：%s 状态 %d 属性 %s", key, rule.Stage, oldID)
				}
				prop := info.properties[oldID][0].clone()
				prop.set("SkillProId", newID)
				if rule.Buff != 0 {
					buff, level, duration := rule.Buff, rule.Level, rule.Duration
					if buff < 0 {
						buff = 0
						level = 0
						duration = 0
					}
					prop.set("UnNormalState", strconv.Itoa(buff))
					prop.set("UStateLevel", strconv.Itoa(level))
					prop.set("UStateLastCycle", strconv.Itoa(duration))
				}
				for _, field := range propertyFields {
					if value, ok := rule.Properties[oldID][field.Key]; ok {
						prop.set(field.Key, strconv.FormatFloat(value, 'f', -1, 64))
					}
				}
				encoded, err := prop.serialize()
				if err != nil {
					return nil, err
				}
				if previous, ok := propertyNodeText(properties, newID); ok {
					// 归档里已经有这个号 —— 基线吸收了上一轮的克隆节点（重新采集
					// 基线后必然如此）。**原位替换**，不能再追加一条：同号两条既会
					// 让客户端取到哪条全看运气，也会让我们自己的唯一性校验挂掉。
					if previous != encoded {
						properties = strings.Replace(properties, previous, encoded, 1)
						propertiesChanged++
					}
				} else {
					clones = append(clones, encoded)
				}
				remap[oldID] = newID
			}
			changed.walk(func(node *xmlNode) {
				if id, ok := remap[node.get("skillproid")]; ok {
					node.set("skillproid", id)
				}
			})
			target := source.original
			if strings.Count(animation, target) != 1 {
				// An earlier rule in this pass may already have rewritten the
				// block, so the pristine text is gone: fall back to matching by
				// id against the current file contents.
				found, ok := currentBlock(animation, strings.TrimSpace(source.node.get("id")))
				if !ok {
					return nil, fmt.Errorf("动作定义无法唯一替换")
				}
				target = found
			}
			encoded, err := changed.serialize()
			if err != nil {
				return nil, err
			}
			if shared {
				ending := regexp.MustCompile(`</AnmInfo\s*>`)
				if len(ending.FindAllStringIndex(animation, -1)) != 1 {
					return nil, fmt.Errorf("动作表结构错误")
				}
				animation = ending.ReplaceAllStringFunc(animation, func(string) string { return "\n" + encoded + "\n</AnmInfo>" })
			} else {
				animation = strings.Replace(animation, target, encoded, 1)
			}
			animations[file] = animation
		}
	}
	if len(clones) == 0 && propertiesChanged == 0 {
		return append([]byte(nil), a.data...), nil
	}
	ending := regexp.MustCompile(`</SkillProperty\s*>`)
	if len(ending.FindAllStringIndex(properties, -1)) != 1 {
		return nil, fmt.Errorf("技能属性表结构错误")
	}
	properties = ending.ReplaceAllStringFunc(properties, func(string) string { return "\n" + strings.Join(clones, "\n") + "\n</SkillProperty>" })
	if _, err = parseXML(properties); err != nil {
		return nil, err
	}
	replacements := map[string][]byte{}
	for file, animation := range animations {
		if _, err := parseXML(animation); err != nil {
			return nil, err
		}
		encoded, err := encodeText(animation)
		if err != nil {
			return nil, err
		}
		replacements[file] = encoded
	}
	encoded, err := encodeText(properties)
	if err != nil {
		return nil, err
	}
	replacements["skillproperty.xml"] = encoded
	if tableChanged {
		encoded, err := encodeText(strings.Join(actionLines, "\n"))
		if err != nil {
			return nil, err
		}
		replacements["itemact.txt"] = encoded
	}
	return entryProperties.replace(a, replacements)
}
func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func atomicWrite(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	temp := file.Name()
	defer os.Remove(temp)
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

// snapshotState copies the current settings.json aside before a destructive
// edit (清空定制 / 删除状态 / 删除武器), so a mistaken click can be undone by
// hand. Best-effort: never fails the operation it guards.
func snapshotState(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	backup := path + ".bak-" + time.Now().Format("20060102-150405")
	_ = atomicWrite(backup, data)
}

// Blueprint describes a weapon invented without touching the client binary.
// Its item row and action row are copied from a donor weapon that already ships
// in the client's config.spf2, so the untouched client still draws an existing
// RenderWare clump while the name, subtype and per-stage effects are new.
type Blueprint struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Type  string `json:"type"`
	Model string `json:"model"`
	Donor int    `json:"donor"`
	Icon  string `json:"icon,omitempty"`
	// Description feeds item.txt's description column (16), which is what the
	// client shows as the weapon blurb. Empty keeps the pre-description
	// behaviour of reusing the name.
	Description string `json:"description,omitempty"`
	Note        string `json:"note,omitempty"`
	// Glow names the effect script in Data/effect/effect/ that plays as a
	// standing glow on this weapon — how the shipped "退魔/黑暗" versions work
	// (item.txt column 54). Empty keeps the plain look.
	Glow string `json:"glow,omitempty"`
}

const blueprintMinID, blueprintMaxID = 253000, 253999

// splitRows splits a tab separated table, dropping blank lines.
func splitRows(text string) [][]string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	rows := make([][]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		rows = append(rows, strings.Split(line, "\t"))
	}
	return rows
}

// appendTabRow appends one row, matching the file's existing line ending so the
// untouched client keeps parsing the table the same way.
func appendTabRow(text, row string) string {
	ending := "\n"
	if strings.Contains(text, "\r\n") {
		ending = "\r\n"
	}
	body := strings.TrimRight(text, "\r\n")
	if body == "" {
		return row + ending
	}
	return body + ending + row + ending
}

// dropTabRow removes every table row whose given cell equals value, keeping
// the file's line ending style. Header rows never match a weapon id, so they
// survive untouched.
func dropTabRow(text string, column int, value string) string {
	crlf := strings.Contains(text, "\r\n")
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		cells := strings.Split(line, "\t")
		if column < len(cells) && cells[column] == value {
			continue
		}
		kept = append(kept, line)
	}
	ending := "\n"
	if crlf {
		ending = "\r\n"
	}
	return strings.Join(kept, ending)
}

// blueprintDescription returns what item.txt's description column (16) should
// carry. An empty description keeps the pre-description behaviour of showing
// the weapon name.
func blueprintDescription(blueprint Blueprint) string {
	if strings.TrimSpace(blueprint.Description) == "" {
		return blueprint.Name
	}
	return blueprint.Description
}

func setCell(row []string, index int, value string) []string {
	for len(row) <= index {
		row = append(row, "")
	}
	row[index] = value
	return row
}

// itemRowIndex maps a level item id to its item.txt row (kind 25 only).
func itemRowIndex(text string) map[string][]string {
	index := map[string][]string{}
	for _, row := range splitRows(text) {
		if len(row) >= 2 && row[0] == "25" {
			index[row[1]] = row
		}
	}
	return index
}

// actionRowIndex maps a weapon id to its itemact.txt row.
func actionRowIndex(text string) map[string][]string {
	index := map[string][]string{}
	for _, row := range splitRows(text) {
		if len(row) >= 2 {
			index[row[0]] = row
		}
	}
	return index
}

// firstItemTemplate returns the first kind-25 row as a structural template for
// a donor-less weapon: only the identity columns are later overwritten.
func firstItemTemplate(itemText string) []string {
	for _, row := range splitRows(itemText) {
		if len(row) >= 2 && row[0] == "25" {
			return row
		}
	}
	return nil
}

// emptyActionRow builds a donor-less action row: weapon id and internal name
// (filled later) plus every state column zeroed.
func emptyActionRow(actionText string) []string {
	rows := splitRows(actionText)
	if len(rows) == 0 {
		return nil
	}
	row := make([]string, len(rows[0]))
	for i := 2; i < len(row); i++ {
		row[i] = "0"
	}
	return row
}

// weaponItemIDs lists every item.txt id of kind 25. The reserved range must be
// validated against this table rather than against the renderable weapon list:
// item.txt is what validateBlueprint writes into, and a shipped id is taken
// even if it happens to carry no usable action row.
func weaponItemIDs(a *archive) []int {
	text, err := a.text("item.txt")
	if err != nil {
		return nil
	}
	ids := []int{}
	for key := range itemRowIndex(text) {
		if number, err := strconv.Atoi(key); err == nil {
			ids = append(ids, number)
		}
	}
	sort.Ints(ids)
	return ids
}

// usedWeaponIDs is what the reserved-range picker must treat as taken: the
// shipped ids from item.txt plus the ids already registered as self-made
// weapons, which the pristine item.txt cannot know about.
func usedWeaponIDs(a *archive, created map[string]Blueprint) []int {
	taken := map[int]bool{}
	for _, id := range weaponItemIDs(a) {
		taken[id] = true
	}
	for key := range created {
		if number, err := strconv.Atoi(key); err == nil {
			taken[number] = true
		}
	}
	ids := make([]int, 0, len(taken))
	for id := range taken {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

// undeployedWeaponIDs lists the weapons the catalogue shows but the client's own
// config.spf2 has no row for. The catalogue is rendered from the client plus the
// global editing set, and that set is shared by every client, so without this a
// self-made weapon would look installed on every client the GM points at.
func undeployedWeaponIDs(current []byte, weapons []Weapon) []int {
	native, err := parseArchive(current)
	if err != nil {
		return nil
	}
	text, err := native.text("item.txt")
	if err != nil {
		return nil
	}
	rows := itemRowIndex(text)
	missing := []int{}
	for _, w := range weapons {
		if _, ok := rows[strconv.Itoa(w.ID)]; !ok {
			missing = append(missing, w.ID)
		}
	}
	return missing
}

// applyBlueprints returns a configuration whose item.txt and itemact.txt carry
// one extra row per blueprint. Both files already exist in the archive, so the
// SGDP writer can replace them without growing the entry table.
func applyBlueprints(a *archive, created map[string]Blueprint) (*archive, error) {
	if len(created) == 0 {
		return a, nil
	}
	itemText, err := a.text("item.txt")
	if err != nil {
		return nil, err
	}
	actionText, err := a.text("itemact.txt")
	if err != nil {
		return nil, err
	}
	itemIndex := itemRowIndex(itemText)
	actionIndex := actionRowIndex(actionText)
	keys := make([]string, 0, len(created))
	for key := range created {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		blueprint := created[key]
		number := strconv.Itoa(blueprint.ID)
		if itemIndex[number] != nil {
			// The row already shipped in an earlier render (a re-captured
			// baseline after the client's own updater, or a package read
			// before any baseline exists). Drop it and rebuild from the
			// current blueprint below, otherwise renaming, re-iconing or
			// re-describing the weapon would never reach the client.
			itemText = dropTabRow(itemText, 1, number)
			actionText = dropTabRow(actionText, 0, number)
			itemIndex = itemRowIndex(itemText)
			actionIndex = actionRowIndex(actionText)
		}
		donorItem := itemIndex[strconv.Itoa(blueprint.Donor)]
		if blueprint.Donor == 0 {
			// 无供体：借用第一把 kind 25 武器的行结构，动作行全空，之后由
			// 逐状态重映射填入动作与命中属性。
			donorItem = firstItemTemplate(itemText)
			if donorItem == nil {
				return nil, fmt.Errorf("客户端缺少可用的武器行结构")
			}
		} else if donorItem == nil {
			return nil, fmt.Errorf("供体武器 %d 缺少物品配置", blueprint.Donor)
		}
		var donorAction []string
		if blueprint.Donor == 0 {
			donorAction = emptyActionRow(actionText)
			if donorAction == nil {
				return nil, fmt.Errorf("动作表结构为空")
			}
		} else {
			donorAction = actionIndex[strconv.Itoa(blueprint.Donor)]
			if donorAction == nil {
				return nil, fmt.Errorf("供体武器 %d 缺少动作配置", blueprint.Donor)
			}
		}
		itemRow := append([]string(nil), donorItem...)
		itemRow = setCell(itemRow, 0, "25")
		itemRow = setCell(itemRow, 1, number)
		itemRow = setCell(itemRow, 2, blueprint.Type)
		itemRow = setCell(itemRow, 3, blueprint.Name)
		itemRow = setCell(itemRow, 7, blueprint.Model)
		// Icon: a custom path wins; otherwise inherit the donor's icon so the
		// new weapon shows the donor's picture instead of a blank tile. A
		// donor-less weapon only takes an explicit icon.
		icon := strings.TrimSpace(blueprint.Icon)
		if icon == "" && blueprint.Donor != 0 && len(donorItem) > 9 {
			icon = donorItem[9]
		}
		if icon == "" {
			icon = "#"
		}
		itemRow = setCell(itemRow, 9, icon)
		itemRow = setCell(itemRow, 16, blueprintDescription(blueprint))
		// An empty setting inherits the donor's standing effect. "#" explicitly
		// removes it; a script ID replaces it.
		if glow := strings.TrimSpace(blueprint.Glow); glow != "" {
			itemRow = setCell(itemRow, 53, glow)
		}
		itemText = appendTabRow(itemText, strings.Join(itemRow, "\t"))

		actionRow := append([]string(nil), donorAction...)
		actionRow = setCell(actionRow, 0, number)
		actionRow = setCell(actionRow, 1, blueprint.Name)
		actionText = appendTabRow(actionText, strings.Join(actionRow, "\t"))
		// Register the emitted rows back into the indexes: a later blueprint
		// may borrow this one as its donor, and a weapon created after another
		// must see the donor's fresh row, not the state before this loop.
		itemIndex[number] = itemRow
		actionIndex[number] = actionRow
	}
	items, err := encodeText(itemText)
	if err != nil {
		return nil, err
	}
	actions, err := encodeText(actionText)
	if err != nil {
		return nil, err
	}
	data, err := a.replace(map[string][]byte{"item.txt": items, "itemact.txt": actions})
	if err != nil {
		return nil, err
	}
	return parseArchive(data)
}

// validateBlueprintInfo checks the editable identity fields of a blueprint:
// name, icon, description and note. Unlike validateBlueprint it never consults
// item.txt, so editing an already-applied weapon does not trip over its own
// row living in the client package.
func validateBlueprintInfo(blueprint Blueprint) error {
	if name := blueprint.Name; name == "" || len([]rune(name)) > 24 || strings.ContainsAny(name, "\t\r\n") {
		return fmt.Errorf("武器名称需为 1..24 个字符且不含制表符")
	}
	if blueprint.Icon != "" {
		icon := strings.ReplaceAll(blueprint.Icon, "\\", "/")
		if strings.Contains(icon, ":") || strings.HasPrefix(icon, "/") || strings.Contains(icon, "..") {
			return fmt.Errorf("图标路径无效")
		}
	}
	if len([]rune(blueprint.Description)) > 200 {
		return fmt.Errorf("武器简介过长（最多 200 字）")
	}
	if strings.ContainsAny(blueprint.Description, "\t\r\n") {
		return fmt.Errorf("武器简介不能包含制表符或换行")
	}
	if len([]rune(blueprint.Note)) > 200 {
		return fmt.Errorf("备注过长")
	}
	glow := strings.TrimSpace(blueprint.Glow)
	if glow != "" && glow != "#" && !effectIdShape.MatchString(glow) {
		return fmt.Errorf("光效脚本编号 %q 无效", glow)
	}
	return nil
}

// validateBlueprint rejects anything the untouched client could not render or
// resolve. The model must already exist on disk: new RenderWare clumps cannot be
// authored from here.
func validateBlueprint(client string, source *archive, blueprint Blueprint) error {
	if blueprint.ID < blueprintMinID || blueprint.ID > blueprintMaxID {
		return fmt.Errorf("武器编号需在 %d..%d 之间", blueprintMinID, blueprintMaxID)
	}
	if err := validateBlueprintInfo(blueprint); err != nil {
		return err
	}
	switch blueprint.Type {
	case "1", "2", "3", "4", "5", "6", "7":
	default:
		return fmt.Errorf("武器子类无效")
	}
	if blueprint.Model == "" || filepath.Base(blueprint.Model) != blueprint.Model || !strings.EqualFold(filepath.Ext(blueprint.Model), ".dff") {
		return fmt.Errorf("模型文件名无效")
	}
	if _, err := os.Stat(filepath.Join(client, "Data", "Weapon", "Model", blueprint.Model)); err != nil {
		return fmt.Errorf("客户端缺少模型文件 %s，自建武器必须复用已有模型", blueprint.Model)
	}
	if blueprint.Donor == blueprint.ID {
		return fmt.Errorf("供体武器不能是自身")
	}
	if blueprint.Donor < 0 {
		return fmt.Errorf("供体武器编号无效")
	}
	itemText, err := source.text("item.txt")
	if err != nil {
		return err
	}
	actionText, err := source.text("itemact.txt")
	if err != nil {
		return err
	}
	if itemRowIndex(itemText)[strconv.Itoa(blueprint.ID)] != nil {
		return fmt.Errorf("武器编号 %d 已存在", blueprint.ID)
	}
	if blueprint.Donor != 0 {
		donor := strconv.Itoa(blueprint.Donor)
		if itemRowIndex(itemText)[donor] == nil || actionRowIndex(actionText)[donor] == nil {
			return fmt.Errorf("供体武器 %d 不在本客户端可用的武器表中", blueprint.Donor)
		}
	}
	return nil
}

type weaponState struct {
	Drafts      map[string][]Rule    `json:"drafts"`
	Applied     map[string][]Rule    `json:"applied"`
	Created     map[string]Blueprint `json:"created,omitempty"`
	Combos      map[string]int       `json:"combos,omitempty"`
	SourceHash  string               `json:"source_hash,omitempty"`
	AppliedHash string               `json:"applied_hash,omitempty"`
	// Targets holds one baseline and hash pair per managed client. The edit set
	// above is shared; each client is rendered onto its own baseline.
	Baselines map[string]*clientBaseline `json:"baselines,omitempty"`
	// Remaps rewires individual states: which action plays there and which
	// hit-property node it points at. ExtraProperties holds editor-authored
	// hit-property nodes cloned from a template under a fresh SkillProId.
	// Cleared zeroes a state column of a self-made weapon: the state vanishes
	// from its action row, and any remap for it is ignored.
	Remaps map[string]map[int]*StageRemap `json:"remaps,omitempty"`
	// FrameSwitches authors the frame-level combo channel: which
	// <CustomStateSwitch> nodes each state's action block declares. A state
	// present here is authoritative (an empty list means "no switches at all");
	// a state absent keeps whatever the block already ships. Shared blocks are
	// cloned before the rewrite, so the donor weapon is never touched.
	FrameSwitches map[string]map[int]frameSwitchStageEdit `json:"frame_switches,omitempty"`
	// Counters holds the authored parry window (<Counter> + <TriggerBox>) of
	// one stage — the third transition channel, triggered by the opponent's
	// attack rather than by a key. At most one per stage; nil deletes it.
	Counters map[string]map[int]*CounterEdit `json:"counters,omitempty"`
	// BlockElements holds the authored 「防护」(霸体/无敌/穿人) and 「自身状态」
	// (UState/Ustate/AddBuff) elements, keyed by weapon → stage → tag. Only the
	// tags an author actually touched appear here: a missing tag means "leave the
	// block alone", an empty list means "remove that tag".
	BlockElements map[string]map[int]map[string][]BlockElement `json:"block_elements,omitempty"`
	// Scopes holds the authored attack range (<AttackScope>) per weapon → stage
	// → animation segment id. A stage/segment absent here keeps the block's own
	// declaration (usually inherited from the donor weapon).
	Scopes          map[string]map[int]map[string][]FrameSwitchAttr `json:"scopes,omitempty"`
	Cleared         map[string]map[int]bool                         `json:"cleared,omitempty"`
	ExtraProperties map[string]ExtraProperty                        `json:"extra_properties,omitempty"`
	// Chains holds an author-authored combo state machine per weapon. When a
	// weapon has an entry here, it replaces whatever delayacttable.xml says
	// (including a borrowed donor table) with exactly these transitions.
	Chains map[string][]ComboTransition `json:"chains,omitempty"`
	// ComboRules holds an author-authored rule set per weapon for
	// comborule.xml (per-skill hit limits and the black/white connection
	// lists). Unlike Chains it is additive: an entry replaces only the blocks
	// that weapon owns, and the shipped rules of other weapons stay intact.
	ComboRules map[string]ComboRuleSet `json:"combo_rules,omitempty"`
	// PropertyClones pins the SkillProId each (weapon, stage, hit-property)
	// receives when render clones it. render allocates fresh 9000000xx ids in
	// weapon-then-stage order, so without this map every rule-set change would
	// renumber them and silently break the combo rules that name the previous
	// ids — the client matches rules by number and never notices the miss.
	// Keyed by weapon id, then "stage|pre-clone property id".
	PropertyClones map[string]map[string]string `json:"property_clones,omitempty"`
	// StageEffects authors the effect nodes (<Effect> / <HitEffect>) of one state.
	// A state present here is authoritative: an empty list means "no effects".
	StageEffects map[string]map[int][]StageEffect `json:"stage_effects,omitempty"`
	// EffectRows is the weapon's own acteffect.xml ledger. A weapon present here
	// keeps exactly these rows instead of the ones derived from its actions.
	EffectRows map[string][]EffectRow `json:"effect_rows,omitempty"`
}

// 武器配置操作的独占锁。两个细节缺一不可：
//
//   - **排队重试**：GM 前端每发一个 RPC 就新起一个后端进程，页面选中一把武器
//     时 weapon_combo_chain 与 weapon_combo_rule 是并发发出的，两个都要这把锁。
//     以前谁先创建成功谁赢，后到的直接返回错误——表现为「连招限制卡片时有时无」，
//     而且失败信息只在前端 catch 里被吞掉，看起来就像这把武器没有限制。
//   - **接管陈锁**：进程被强杀（taskkill /F）时 defer 不会执行，锁文件会留成
//     永久路障，之后每一次操作都失败。锁文件比 staleLockAge 还旧就认为持有者
//     已经没了（正常操作最多几十秒）。
const (
	weaponLockWait  = 30 * time.Second
	weaponLockRetry = 50 * time.Millisecond
	staleLockAge    = 2 * time.Minute
)

// readOnlyWeaponOperation 列出只读 statePath、不落盘的操作。这些不进写锁：
// 它们并发执行安全，而 GM 界面上切一把武器就会同时拉起好几个，串行排队
// 会让紧随其后的请求撞上锁等待上限。带 _set / _apply / _create / _save /
// _rebase / import / publish 的操作一律是写，不走这里。
func readOnlyWeaponOperation(operation string) bool {
	switch operation {
	case "weapon_catalog",
		"weapon_combo_chain",
		"weapon_combo_rule",
		"weapon_stage_track",
		"weapon_remap_options",
		"weapon_template_resolve",
		"weapon_effect_view",
		"weapon_effects_preview":
		return true
	}
	return false
}

func acquireWeaponLock(folder string) (func(), error) {
	lockPath := filepath.Join(folder, "editing.lock")
	deadline := time.Now().Add(weaponLockWait)
	for {
		file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err == nil {
			fmt.Fprintf(file, "pid %d\n%s\n", os.Getpid(), time.Now().Format(time.RFC3339))
			file.Close()
			return func() { os.Remove(lockPath) }, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		if info, statErr := os.Stat(lockPath); statErr == nil &&
			time.Since(info.ModTime()) > staleLockAge {
			os.Remove(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("另一项武器配置操作正在进行，请稍后重试：%w", err)
		}
		time.Sleep(weaponLockRetry)
	}
}

func weaponHandle(request Request, client string, items []Item, folder string) (any, error) {
	if request.Operation == "weapon_icon_upload" {
		return uploadWeaponIcon(client, request.SourcePath)
	}
	if folder == "" {
		folder = filepath.Join(filepath.Dir(client), "weapon-config")
	}
	if err := os.MkdirAll(folder, 0700); err != nil {
		return nil, err
	}
	// 只读操作不抢写锁。整个 handler 原先全程持锁，而 GM 切武器时会并发发多个
	// 请求（chain + rule），一个解析包要十几到几十秒，排在后面的必然撞上 30 秒
	// 上限报「另一项武器配置操作正在进行」。这些操作只读 statePath、不落盘，
	// 加了锁也保护不了什么。
	if !readOnlyWeaponOperation(request.Operation) {
		release, err := acquireWeaponLock(folder)
		if err != nil {
			return nil, err
		}
		defer release()
	}
	if request.Operation == "weapon_effects_preview" || request.Operation == "weapon_effects_apply" || request.Operation == "weapon_effect_view" {
		return weaponEffects(request, client, items, folder)
	}
	statePath := filepath.Join(folder, "settings.json")
	state := weaponState{Drafts: map[string][]Rule{}, Applied: map[string][]Rule{}, Created: map[string]Blueprint{}}
	stateBytes, err := os.ReadFile(statePath)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if len(stateBytes) > 0 {
		if err = json.Unmarshal(stateBytes, &state); err != nil {
			return nil, err
		}
	}
	if state.Drafts == nil || state.Applied == nil {
		return nil, fmt.Errorf("配置方案状态不完整")
	}
	if state.Created == nil {
		state.Created = map[string]Blueprint{}
	}
	if state.Combos == nil {
		state.Combos = map[string]int{}
	}
	if state.Remaps == nil {
		state.Remaps = map[string]map[int]*StageRemap{}
	}
	if state.Cleared == nil {
		state.Cleared = map[string]map[int]bool{}
	}
	if state.ExtraProperties == nil {
		state.ExtraProperties = map[string]ExtraProperty{}
	}
	if state.Chains == nil {
		state.Chains = map[string][]ComboTransition{}
	}
	if state.ComboRules == nil {
		state.ComboRules = map[string]ComboRuleSet{}
	}
	if state.Scopes == nil {
		state.Scopes = map[string]map[int]map[string][]FrameSwitchAttr{}
	}
	if state.StageEffects == nil {
		state.StageEffects = map[string]map[int][]StageEffect{}
	}
	if state.EffectRows == nil {
		state.EffectRows = map[string][]EffectRow{}
	}
	// 合并式导入不碰编辑集、也不依赖基线：直接读目标客户端的 config.spf2，
	// 逐条合并包里武器自己的配置。放在基线校验之前，免得客户端配置被改过
	// （比如线上更新器）就挡在门外——那正是合并导入要处理的场景。
	if request.Operation == "weapon_merge_preview" {
		return weaponMergePreview(request, client)
	}
	if request.Operation == "weapon_merge_packages" {
		return weaponMergePackages(folder)
	}
	if request.Operation == "weapon_merge_import" {
		return weaponMergeImport(request, client, folder)
	}
	// The edit set is rendered onto whichever client the GM currently points at,
	// each client directory keeping its own baseline: the user can switch
	// clients, and a client can be refreshed by its own updater, so a single
	// shared baseline would silently overwrite those differences.
	entry := state.baselineFor(client)
	baseline := entry.path(folder)
	packagePath := configPath(client)
	sourcePath := baseline
	if _, err = os.Stat(baseline); os.IsNotExist(err) {
		sourcePath = packagePath
	} else if err != nil {
		return nil, err
	}
	source, err := loadArchive(sourcePath)
	if err != nil {
		return nil, err
	}
	if err = source.verify(); err != nil {
		return nil, err
	}
	if entry.SourceHash != "" && digest(source.data) != entry.SourceHash {
		return nil, fmt.Errorf("该客户端的基线备份已变化，已停止写入")
	}
	// Self-made weapons live in the same tables as the shipped ones, so the
	// inspection, isolation and rendering path below applies to them unchanged.
	// Combo registrations are layered on top: itemact.txt only says which
	// animation each state plays, delayacttable.xml is what lets the player
	// actually reach the next state, and a weapon without transitions cannot
	// chain attacks however complete its action row looks.
	base, err := buildWeaponBase(source, &state)
	if err != nil {
		return nil, err
	}
	if len(state.Created) > 0 || len(state.Combos) > 0 || len(state.Chains) > 0 {
		text, err := base.text("item.txt")
		if err != nil {
			return nil, err
		}
		if items, err = itemsFromText(client, text, true, true); err != nil {
			return nil, err
		}
	}
	// The stage list, the damage editors and every validation below must see
	// the structure the remaps will actually produce, not the raw donor row:
	// apply them once here so saved edits can only ever bind to live nodes.
	// A broken remap must not lock the editor shut, so the catalogue falls
	// back to the pre-remap view and reports the failure instead.
	remapError := ""
	if len(state.Remaps) > 0 || len(state.ExtraProperties) > 0 || len(state.Cleared) > 0 {
		if remapped, remapErr := applyRemaps(base, &state, items); remapErr == nil {
			base = remapped
		} else {
			remapError = remapErr.Error()
		}
	}
	// Frame-level switches are the second combo channel and must be visible in
	// the editor exactly as they will be rendered, so apply them here too.
	if len(state.FrameSwitches) > 0 {
		if framed, frameErr := applyFrameSwitches(base, &state, items); frameErr == nil {
			base = framed
		} else {
			remapError = frameErr.Error()
		}
	}
	// 招架（<Counter>）是第三条通道，同样要让编辑器看到"应用后的样子"。
	if len(state.Counters) > 0 {
		if countered, counterErr := applyCounters(base, &state, items); counterErr == nil {
			base = countered
		} else {
			remapError = counterErr.Error()
		}
	}
	// 防护（霸体/无敌/穿人）与自身状态（UState/AddBuff）。
	if len(state.BlockElements) > 0 {
		if applied, applyErr := applyBlockElements(base, &state, items); applyErr == nil {
			base = applied
		} else {
			remapError = applyErr.Error()
		}
	}
	// 攻击范围（<AttackScope>）：同样是动作块里的元素，编辑后要立刻在轨道上看到。
	if len(state.Scopes) > 0 {
		if applied, applyErr := applyScopes(base, &state, items); applyErr == nil {
			base = applied
		} else {
			remapError = applyErr.Error()
		}
	}
	// 招式特效（<Effect> / <HitEffect>）：编辑后同样要立刻在武器页与招式页看到。
	if len(state.StageEffects) > 0 {
		if applied, applyErr := applyStageEffects(base, &state); applyErr == nil {
			base = applied
		} else {
			remapError = applyErr.Error()
		}
	}
	// The acteffect registration follows the remapped itemact row, so publish
	// and package see the same effect set the client will load on equip.
	if len(state.Created) > 0 {
		if synced, syncErr := syncWeaponEffects(base, state.Created, state.EffectRows); syncErr == nil {
			base = synced
		} else {
			return nil, syncErr
		}
	}
	info, err := inspect(base, items)
	if err != nil {
		return nil, err
	}
	// 招式时长要看动画自己的帧数，不是动作块尾巴那条 99/999 的占位片断。
	enrichStageFrames(info, client)
	if err = annotateComboState(info, base); err != nil {
		return nil, err
	}
	// Saved edits that predate a remap reference nodes that no longer belong
	// to their stage; drop them instead of failing every later apply, and let
	// a remap's own label name the state in the stage list.
	pruneStaleRules(&state, info)
	overlayRemapLabels(&state, info)
	current, err := os.ReadFile(packagePath)
	if err != nil {
		return nil, err
	}
	revision := digest(append(append([]byte(nil), current...), stateBytes...))
	if request.Operation == "weapon_catalog" {
		buffRows, err := buffs(base)
		if err != nil {
			return nil, err
		}
		// A self-made weapon lives in the global editing set, so it is rendered
		// into the catalogue of whichever client is selected even when that
		// client's own config.spf2 has no such row. Compare against the client
		// file itself (not the baseline) and report the ids it does not ship, so
		// the list can mark them instead of claiming they are installed here.
		undeployed := undeployedWeaponIDs(current, info.weapons)
		result := map[string]any{"weapons": info.weapons, "effects": effects(info), "fields": propertyFields, "hit_options": hitOptions, "buffs": buffRows, "drafts": state.Drafts, "applied": state.Applied, "created": state.Created, "combos": state.Combos, "chains": state.Chains, "combo_rules": state.ComboRules, "remaps": state.Remaps, "extra_properties": state.ExtraProperties, "cleared": state.Cleared, "states": itemactStates(base), "client": describeClient(entry, folder), "clients": describeBaselines(&state, folder), "models": weaponModels(client), "types": weaponTypes, "used_ids": usedWeaponIDs(source, state.Created), "undeployed": undeployed, "blueprint_min": blueprintMinID, "blueprint_max": blueprintMaxID, "revision": revision, "folder": folder}
		if remapError != "" {
			result["remap_error"] = remapError
		}
		// 招架（<Counter>）切到空状态只提示、不拦写盘：白架只影响那一招。
		if warnings := counterWarnings(info); len(warnings) > 0 {
			result["counter_warnings"] = warnings
		}
		// 状态目录（ustate.xml）：动作块里的 UState/Ustate/AddBuff 都按它的
		// type 编号引用，带策划注释，给界面做下拉用。
		result["ustates"] = ustateCatalog(base)
		return result, nil
	}
	if request.Operation == "weapon_create" || request.Operation == "weapon_forget" {
		message := ""
		if request.Operation == "weapon_forget" {
			key := strconv.Itoa(request.Weapon)
			if _, ok := state.Created[key]; !ok {
				return nil, fmt.Errorf("该武器不是自建武器")
			}
			snapshotState(statePath)
			delete(state.Created, key)
			delete(state.Drafts, key)
			delete(state.Applied, key)
			delete(state.Combos, key)
			delete(state.Remaps, key)
			delete(state.Cleared, key)
			delete(state.Chains, key)
			delete(state.ComboRules, key)
			delete(state.FrameSwitches, key)
			delete(state.Counters, key)
			delete(state.BlockElements, key)
			delete(state.PropertyClones, key)
			message = "已移除自建武器；重新应用或发布后才会从配置包消失"
		} else {
			if request.Blueprint == nil {
				return nil, fmt.Errorf("缺少武器蓝图")
			}
			blueprint := *request.Blueprint
			blueprint.Name = strings.TrimSpace(blueprint.Name)
			blueprint.Type = strings.TrimSpace(blueprint.Type)
			blueprint.Model = strings.TrimSpace(blueprint.Model)
			blueprint.Icon = strings.TrimSpace(blueprint.Icon)
			blueprint.Note = strings.TrimSpace(blueprint.Note)
			if err := validateBlueprint(client, source, blueprint); err != nil {
				return nil, err
			}
			key := strconv.Itoa(blueprint.ID)
			for other, existing := range state.Created {
				if other != key && existing.Name == blueprint.Name {
					return nil, fmt.Errorf("已有同名自建武器 %s", blueprint.Name)
				}
			}
			if _, exists := state.Created[key]; exists {
				return nil, fmt.Errorf("编号 %d 已经是一把自建武器；请先移除它或换一个编号", blueprint.ID)
			}
			state.Created[key] = blueprint
			message = "已登记自建武器；保存效果并应用后写入配置包"
		}
		encoded, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			return nil, err
		}
		if err = atomicWrite(statePath, encoded); err != nil {
			return nil, err
		}
		return map[string]any{"created": state.Created, "revision": digest(append(append([]byte(nil), current...), encoded...)), "message": message}, nil
	}
	// weapon_blueprint_update renames, re-icons or re-describes a registered
	// self-made weapon. Only the identity fields move: id, donor, subtype and
	// model stay fixed, because the action row was cloned from the donor and
	// swapping the structure amounts to inventing a different weapon. The
	// change lands in settings.json only; the next render (apply or publish)
	// rebuilds the item/itemact rows from the updated blueprint.
	if request.Operation == "weapon_blueprint_update" {
		key := strconv.Itoa(request.Weapon)
		existing, ok := state.Created[key]
		if !ok {
			return nil, fmt.Errorf("只有自建武器可以编辑信息")
		}
		blueprint := existing
		if request.Blueprint != nil {
			blueprint.Name = strings.TrimSpace(request.Blueprint.Name)
			blueprint.Icon = strings.TrimSpace(request.Blueprint.Icon)
			blueprint.Description = strings.TrimSpace(request.Blueprint.Description)
			blueprint.Note = strings.TrimSpace(request.Blueprint.Note)
			blueprint.Glow = strings.TrimSpace(request.Blueprint.Glow)
		}
		if err := validateBlueprintInfo(blueprint); err != nil {
			return nil, err
		}
		for other, created := range state.Created {
			if other != key && created.Name == blueprint.Name {
				return nil, fmt.Errorf("已有同名自建武器 %s", blueprint.Name)
			}
		}
		state.Created[key] = blueprint
		encoded, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			return nil, err
		}
		if err = atomicWrite(statePath, encoded); err != nil {
			return nil, err
		}
		return map[string]any{"created": state.Created, "revision": digest(append(append([]byte(nil), current...), encoded...)), "message": "已更新武器信息；应用到游戏后写入配置包"}, nil
	}
	// 招式特效：按状态接管动作块里的 <Effect> / <HitEffect> 节点。共用动作块会先
	// 克隆成该武器独占，所以其它武器与原有招式一个字节都不动。
	if request.Operation == "weapon_effect_stage_set" {
		key := strconv.Itoa(request.Weapon)
		if request.Stage == 0 {
			return nil, fmt.Errorf("请选择要编辑特效的招式")
		}
		effects := request.StageEffects
		if effects == nil {
			effects = []StageEffect{}
		}
		if err := validateStageEffects(base, key, map[int][]StageEffect{request.Stage: effects}); err != nil {
			return nil, err
		}
		snapshotState(statePath)
		if state.StageEffects[key] == nil {
			state.StageEffects[key] = map[int][]StageEffect{}
		}
		state.StageEffects[key][request.Stage] = effects
		encoded, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			return nil, err
		}
		if err = atomicWrite(statePath, encoded); err != nil {
			return nil, err
		}
		return map[string]any{"stage_effects": state.StageEffects, "revision": digest(append(append([]byte(nil), current...), encoded...)), "message": fmt.Sprintf("已保存状态 %d 的特效（%d 条）；应用到游戏后写入配置包", request.Stage, len(effects))}, nil
	}
	// 武器特效登记：接管该武器在 acteffect.xml 里的 <WeaponEffect> 块。登记不全
	// 时特效不会加载，所以这里允许手动增删，而不是只做自动补齐。
	if request.Operation == "weapon_effect_ledger_set" {
		key := strconv.Itoa(request.Weapon)
		if err := validateEffectRows(client, request.EffectRows); err != nil {
			return nil, err
		}
		snapshotState(statePath)
		rows := append([]EffectRow(nil), request.EffectRows...)
		sort.Slice(rows, func(i, j int) bool { return rows[i].EffectID < rows[j].EffectID })
		if len(rows) == 0 {
			delete(state.EffectRows, key)
		} else {
			state.EffectRows[key] = rows
		}
		encoded, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			return nil, err
		}
		if err = atomicWrite(statePath, encoded); err != nil {
			return nil, err
		}
		return map[string]any{"effect_rows": state.EffectRows, "revision": digest(append(append([]byte(nil), current...), encoded...)), "message": fmt.Sprintf("已保存 %d 条特效登记；应用到游戏后写入配置包", len(rows))}, nil
	}
	// weapon_combo completes the combo registration of an existing weapon. A
	// shipped weapon can carry a full action row yet own no transitions in
	// delayacttable.xml, which leaves it unchainable in game; borrowing another
	// weapon's state machine fixes it without touching the client binary.
	if request.Operation == "weapon_combo" {
		if request.Weapon == 0 {
			return nil, fmt.Errorf("请选择要补齐的武器")
		}
		target := strconv.Itoa(request.Weapon)
		actionText, err := base.text("itemact.txt")
		if err != nil {
			return nil, err
		}
		if actionRowIndex(actionText)[target] == nil {
			return nil, fmt.Errorf("武器 %s 不在本客户端的动作表中", target)
		}
		message := ""
		if request.Donor == 0 {
			if _, ok := state.Combos[target]; !ok {
				return nil, fmt.Errorf("该武器没有登记过连招补齐")
			}
			delete(state.Combos, target)
			message = "已取消连招补齐；重新应用后恢复原样"
		} else {
			donor := strconv.Itoa(request.Donor)
			if donor == target {
				return nil, fmt.Errorf("参考武器不能是自身")
			}
			itemText, err := base.text("item.txt")
			if err != nil {
				return nil, err
			}
			if itemRowIndex(itemText)[donor] == nil {
				return nil, fmt.Errorf("参考武器 %s 不在本客户端的武器表中", donor)
			}
			tableText, err := base.text("delayacttable.xml")
			if err != nil {
				return nil, fmt.Errorf("本客户端没有连招表，无法补齐")
			}
			tables := comboRowCounts(tableText)
			if tables[target] > 0 {
				return nil, fmt.Errorf("该武器已有连招表，无需补齐")
			}
			state.Combos[target] = request.Donor
			if tables[donor] == 0 {
				message = "参考武器没有连招表：已登记，应用时只复制它有的特效等登记；连招需自行配置"
			} else {
				message = fmt.Sprintf("已登记：借用参考武器的 %d 条连招；应用后生效", tables[donor])
			}
		}
		encoded, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			return nil, err
		}
		if err = atomicWrite(statePath, encoded); err != nil {
			return nil, err
		}
		return map[string]any{"combos": state.Combos, "revision": digest(append(append([]byte(nil), current...), encoded...)), "message": message}, nil
	}
	// weapon_combo_chain_set replaces a weapon's combo state machine with the
	// author-provided transitions.
	if request.Operation == "weapon_frame_switch_set" {
		if request.Weapon == 0 {
			return nil, fmt.Errorf("请选择武器")
		}
		key := strconv.Itoa(request.Weapon)
		actionText, err := base.text("itemact.txt")
		if err != nil {
			return nil, err
		}
		if actionRowIndex(actionText)[key] == nil {
			return nil, fmt.Errorf("武器 %s 不在本客户端的动作表中", key)
		}
		if err := validateFrameSwitches(base, key, request.FrameSwitches); err != nil {
			return nil, err
		}
		count := 0
		for _, list := range request.FrameSwitches {
			count += len(list)
		}
		// 整把武器一次性替换：map 里没有的状态 = 不改（沿用块里原有的切换）。
		if len(request.FrameSwitches) == 0 {
			snapshotState(statePath)
			delete(state.FrameSwitches, key)
		} else {
			if state.FrameSwitches == nil {
				state.FrameSwitches = map[string]map[int]frameSwitchStageEdit{}
			}
			state.FrameSwitches[key] = request.FrameSwitches
		}
		encoded, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			return nil, err
		}
		if err := atomicWrite(statePath, encoded); err != nil {
			return nil, err
		}
		message := fmt.Sprintf("已保存帧级连招（%d 条）；应用到游戏后写入配置包", count)
		if len(request.FrameSwitches) == 0 {
			message = "已清除该武器的帧级连招编辑，恢复动作块原样"
		} else if count == 0 {
			message = "已保存：这些状态改为没有任何帧级连招"
		}
		return map[string]any{
			"frame_switches": state.FrameSwitches[key],
			"saved":          count,
			"revision":       digest(append(append([]byte(nil), current...), encoded...)),
			"message":        message,
		}, nil
	}
	if request.Operation == "weapon_counter_set" {
		if request.Weapon == 0 {
			return nil, fmt.Errorf("请选择武器")
		}
		key := strconv.Itoa(request.Weapon)
		actionText, err := base.text("itemact.txt")
		if err != nil {
			return nil, err
		}
		if actionRowIndex(actionText)[key] == nil {
			return nil, fmt.Errorf("武器 %s 不在本客户端的动作表中", key)
		}
		if err := validateCounters(base, key, request.Counters); err != nil {
			return nil, err
		}
		count := 0
		for _, edit := range request.Counters {
			if edit != nil {
				count++
			}
		}
		// 整把武器一次性替换：map 里没有的状态 = 不改（沿用块里原有的招架）。
		if len(request.Counters) == 0 {
			snapshotState(statePath)
			delete(state.Counters, key)
		} else {
			if state.Counters == nil {
				state.Counters = map[string]map[int]*CounterEdit{}
			}
			state.Counters[key] = request.Counters
		}
		encoded, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			return nil, err
		}
		if err := atomicWrite(statePath, encoded); err != nil {
			return nil, err
		}
		message := fmt.Sprintf("已保存招架（%d 条）；应用到游戏后写入配置包", count)
		if len(request.Counters) == 0 {
			message = "已清除该武器的招架编辑，动作块回到原样"
		} else if count == 0 {
			message = "已保存：这些状态改为没有招架"
		}
		return map[string]any{
			"counters": state.Counters[key],
			"saved":    count,
			"revision": digest(append(append([]byte(nil), current...), encoded...)),
			"message":  message,
		}, nil
	}
	if request.Operation == "weapon_block_elements_set" {
		if request.Weapon == 0 {
			return nil, fmt.Errorf("请选择武器")
		}
		key := strconv.Itoa(request.Weapon)
		actionText, err := base.text("itemact.txt")
		if err != nil {
			return nil, err
		}
		if actionRowIndex(actionText)[key] == nil {
			return nil, fmt.Errorf("武器 %s 不在本客户端的动作表中", key)
		}
		if err := validateBlockElements(base, key, request.BlockElements); err != nil {
			return nil, err
		}
		count := 0
		for _, perTag := range request.BlockElements {
			for _, list := range perTag {
				count += len(list)
			}
		}
		// **合并**而不是整把替换：payload 里出现过的 (状态,标签) 才动 —— 传空列表是
		// 删掉那个标签；没出现的一律保留（作者只选了某一项，就只整理那一项对应的块）。
		if len(request.BlockElements) == 0 {
			snapshotState(statePath)
			delete(state.BlockElements, key)
		} else {
			if state.BlockElements == nil {
				state.BlockElements = map[string]map[int]map[string][]BlockElement{}
			}
			perStage := state.BlockElements[key]
			if perStage == nil {
				perStage = map[int]map[string][]BlockElement{}
			}
			for stage, perTag := range request.BlockElements {
				stageMap := perStage[stage]
				if stageMap == nil {
					stageMap = map[string][]BlockElement{}
				}
				for tag, list := range perTag {
					if len(list) == 0 {
						delete(stageMap, tag)
					} else {
						stageMap[tag] = list
					}
				}
				if len(stageMap) == 0 {
					delete(perStage, stage)
				} else {
					perStage[stage] = stageMap
				}
			}
			if len(perStage) == 0 {
				delete(state.BlockElements, key)
			} else {
				state.BlockElements[key] = perStage
			}
		}
		encoded, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			return nil, err
		}
		if err := atomicWrite(statePath, encoded); err != nil {
			return nil, err
		}
		message := fmt.Sprintf("已保存防护/自身状态（%d 条）；应用到游戏后写入配置包", count)
		if len(request.BlockElements) == 0 {
			message = "已清除该武器的防护/自身状态编辑，动作块回到原样"
		} else if count == 0 {
			message = "已保存：这些状态改为没有额外防护与自身状态"
		}
		return map[string]any{
			"block_elements": state.BlockElements[key],
			"saved":          count,
			"revision":       digest(append(append([]byte(nil), current...), encoded...)),
			"message":        message,
		}, nil
	}
	if request.Operation == "weapon_combo_chain_set" {
		key := strconv.Itoa(request.Weapon)
		if request.Weapon == 0 {
			return nil, fmt.Errorf("请选择武器")
		}
		actionText, err := base.text("itemact.txt")
		if err != nil {
			return nil, err
		}
		if actionRowIndex(actionText)[key] == nil {
			return nil, fmt.Errorf("武器 %s 不在本客户端的动作表中", key)
		}
		valid := keyInputLabels(base)
		for _, transition := range request.Transitions {
			for _, field := range []string{transition.OldState, transition.NewState} {
				number, err := strconv.Atoi(field)
				if err != nil || number < 1000 || number > 9999 {
					return nil, fmt.Errorf("状态必须是四位数字（如 2011）")
				}
			}
			if number, err := strconv.Atoi(transition.KeyInput); err != nil || number < 1 || number > 99 {
				return nil, fmt.Errorf("按键编号无效")
			}
			// <KeyInputList> is the only authority on which ids exist; a key the
			// client cannot read leaves a transition that never fires.
			if len(valid) > 0 && valid[transition.KeyInput] == "" {
				known := make([]string, 0, len(valid))
				for id := range valid {
					known = append(known, id)
				}
				sort.Strings(known)
				return nil, fmt.Errorf("按键 %s 不在本客户端的按键表里（可用：%s）",
					transition.KeyInput, strings.Join(known, " "))
			}
		}
		if len(request.Transitions) == 0 {
			delete(state.Chains, key)
		} else {
			state.Chains[key] = request.Transitions
		}
		encoded, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			return nil, err
		}
		if err = atomicWrite(statePath, encoded); err != nil {
			return nil, err
		}
		message := fmt.Sprintf("已保存连招链（%d 条转移）；应用后生效", len(request.Transitions))
		if len(request.Transitions) == 0 {
			message = "已清除定制的连招链；恢复继承（借用供体或原生）"
		}
		return map[string]any{
			"chains":   state.Chains,
			"revision": digest(append(append([]byte(nil), current...), encoded...)),
			"message":  message,
		}, nil
	}
	// weapon_stage_track returns the frame timeline of one weapon: each stage's
	// animation segments (with clip frame counts and attack/char boxes) plus the
	// effect/audio/key/guard markers, so the editor can draw a frame track.
	if request.Operation == "weapon_stage_track" {
		if request.Weapon == 0 {
			return nil, fmt.Errorf("请选择武器")
		}
		return map[string]any{
			"weapon":   request.Weapon,
			"tracks":   stageTracks(info, client, strconv.Itoa(request.Weapon)),
			"saved":    state.Scopes[strconv.Itoa(request.Weapon)],
			"revision": revision,
		}, nil
	}
	// weapon_scope_set saves the authored attack range of one weapon. Missing
	// (stage, segment) pairs are left alone; the values are rendered into the
	// action block on 应用到游戏.
	if request.Operation == "weapon_scope_set" {
		if request.Weapon == 0 {
			return nil, fmt.Errorf("请选择武器")
		}
		key := strconv.Itoa(request.Weapon)
		actionText, err := base.text("itemact.txt")
		if err != nil {
			return nil, err
		}
		if actionRowIndex(actionText)[key] == nil {
			return nil, fmt.Errorf("武器 %s 不在本客户端的动作表中", key)
		}
		if err := validateScopes(base, key, request.Scopes, info); err != nil {
			return nil, err
		}
		count := 0
		for _, perStage := range request.Scopes {
			count += len(perStage)
		}
		// 合并而不是整把替换：payload 里出现过的 (状态,片断) 才动。
		if len(request.Scopes) == 0 {
			snapshotState(statePath)
			delete(state.Scopes, key)
		} else {
			if state.Scopes == nil {
				state.Scopes = map[string]map[int]map[string][]FrameSwitchAttr{}
			}
			perStage := state.Scopes[key]
			if perStage == nil {
				perStage = map[int]map[string][]FrameSwitchAttr{}
			}
			for stage, perSegment := range request.Scopes {
				stageMap := perStage[stage]
				if stageMap == nil {
					stageMap = map[string][]FrameSwitchAttr{}
				}
				for id, attrs := range perSegment {
					if len(attrs) == 0 {
						delete(stageMap, id)
					} else {
						stageMap[id] = attrs
					}
				}
				if len(stageMap) == 0 {
					delete(perStage, stage)
				} else {
					perStage[stage] = stageMap
				}
			}
			if len(perStage) == 0 {
				delete(state.Scopes, key)
			} else {
				state.Scopes[key] = perStage
			}
		}
		encoded, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			return nil, err
		}
		if err := atomicWrite(statePath, encoded); err != nil {
			return nil, err
		}
		message := fmt.Sprintf("已保存攻击范围（%d 处）；应用到游戏后写入配置包", count)
		if len(request.Scopes) == 0 {
			message = "已清除该武器的攻击范围编辑，动作块回到原样"
		}
		return map[string]any{
			"scopes":   state.Scopes[key],
			"saved":    count,
			"revision": digest(append(append([]byte(nil), current...), encoded...)),
			"message":  message,
		}, nil
	}
	// weapon_combo_chain returns the state-transition chain for one weapon so the
	// editor can draw it as a readable flow instead of a flat stage list.
	if request.Operation == "weapon_combo_chain" {
		if request.Weapon == 0 {
			return nil, fmt.Errorf("请选择武器")
		}
		info, err := inspect(base, items)
		if err != nil {
			return nil, err
		}
		// The key ids are non-contiguous and the editor must offer exactly the
		// ones this client understands, so ship the client's own table along
		// with the chain instead of a hardcoded list.
		return map[string]any{
			"weapon":         request.Weapon,
			"chain":          comboChain(base, info, strconv.Itoa(request.Weapon)),
			"dead_ends":      comboDeadEnds(base, info, strconv.Itoa(request.Weapon)),
			"frame_switches": comboFrameSwitches(base, info, strconv.Itoa(request.Weapon)),
			// 已保存的帧级连招编辑（按状态），编辑器据此区分"改过的"和"原样"。
			"frame_switches_saved": state.FrameSwitches[strconv.Itoa(request.Weapon)],
			// 第三条通道：动作块里被对手攻击触发的招架。
			"counters":       comboCounters(info, strconv.Itoa(request.Weapon)),
			"counters_saved": state.Counters[strconv.Itoa(request.Weapon)],
			// 防护（霸体/无敌/穿人）与自身状态（UState/AddBuff）：
			// 现状 + 已保存的编辑 + 可写元素的清单（供界面按类型分组）。
			"block_elements":       comboBlockElements(info, strconv.Itoa(request.Weapon)),
			"block_elements_saved": state.BlockElements[strconv.Itoa(request.Weapon)],
			"block_element_groups": blockElementGroups(),
			"frame_keys":           frameKeyOptions(),
			"keys":                 keyInputs(base),
			"revision":             revision,
		}, nil
	}
	// weapon_combo_rule returns the comborule.xml limits of one weapon: the
	// per-skill hit limits, the black/white connection lists, the rules the
	// client ships with, and every skillproid the weapon's action blocks
	// actually declare. Offering the last one is what lets the editor hand out
	// a dropdown instead of a free-text box, because a rule naming a skill no
	// block references simply never fires.
	if request.Operation == "weapon_combo_rule" {
		if request.Weapon == 0 {
			return nil, fmt.Errorf("请选择武器")
		}
		return comboRuleView(source, info, &state, strconv.Itoa(request.Weapon), revision), nil
	}
	// weapon_combo_rule_set replaces the rule blocks a weapon owns. It refuses
	// to rewrite a block that ships in the client, so official data stays
	// recoverable from the baseline alone; adding limits to a weapon the client
	// does not constrain yet is allowed.
	if request.Operation == "weapon_combo_rule_set" {
		if request.Weapon == 0 {
			return nil, fmt.Errorf("请选择武器")
		}
		key := strconv.Itoa(request.Weapon)
		actionText, err := base.text("itemact.txt")
		if err != nil {
			return nil, err
		}
		if actionRowIndex(actionText)[key] == nil {
			return nil, fmt.Errorf("武器 %s 不在本客户端的动作表中", key)
		}
		set := ComboRuleSet{}
		if request.ComboRule != nil {
			set = *request.ComboRule
		}
		if err := validateComboRuleSet(set); err != nil {
			return nil, err
		}
		_, overridden := state.ComboRules[key]
		official := false
		if text, err := source.text("comborule.xml"); err == nil {
			official = comboRuleWeapons(text)[key]
		}
		if set.empty() {
			delete(state.ComboRules, key)
		} else {
			state.ComboRules[key] = set
		}
		encoded, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			return nil, err
		}
		if err = atomicWrite(statePath, encoded); err != nil {
			return nil, err
		}
		message := fmt.Sprintf("已保存连招限制（%d 条命中上限 / %d 条黑名单 / %d 条白名单）；应用后生效",
			len(set.Max), len(set.Black), len(set.White))
		if set.empty() {
			message = "已清除定制的连招限制"
			if !overridden && !official {
				message = "该武器没有定制或内置的连招限制，未做改动"
			}
		}
		view := comboRuleView(source, info, &state, key,
			digest(append(append([]byte(nil), current...), encoded...)))
		view["message"] = message
		return view, nil
	}
	// weapon_clients reports the selected client and every client we have a
	// baseline for. weapon_client_rebase re-captures the selected client's
	// baseline after something else replaced its config.spf2 — its own updater
	// does exactly that.
	if request.Operation == "weapon_clients" || request.Operation == "weapon_client_rebase" {
		message := ""
		if request.Operation == "weapon_client_rebase" {
			if err = ensureBaseline(entry, folder, true); err != nil {
				return nil, err
			}
			state.SourceHash = entry.SourceHash
			state.AppliedHash = entry.AppliedHash
			encoded, err := json.MarshalIndent(state, "", "  ")
			if err != nil {
				return nil, err
			}
			if err = atomicWrite(statePath, encoded); err != nil {
				return nil, err
			}
			message = "已按该客户端当前配置重新采集基线；旧基线已备份"
		}
		encoded, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"client":   describeClient(entry, folder),
			"clients":  describeBaselines(&state, folder),
			"revision": digest(append(append([]byte(nil), current...), encoded...)),
			"message":  message,
		}, nil
	}
	// weapon_remap rewires one state of one weapon (action and/or hit property),
	// either directly or by reusing another weapon's state as a template.
	// weapon_property_add clones a template hit-property node under a fresh id.
	// weapon_remap_options exposes the picker catalogues on demand.
	if request.Operation == "weapon_remap_options" {
		info, err := inspect(base, items)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"actions":    actionCatalog(info),
			"properties": propertyCatalog(info),
			"revision":   revision,
		}, nil
	}
	// weapon_template_resolve returns the action and hit property a donor weapon
	// plays in a donor state, so the editor can fill them into the remap inputs
	// for the user to see and tweak before committing.
	if request.Operation == "weapon_template_resolve" {
		info, err := inspect(base, items)
		if err != nil {
			return nil, err
		}
		if request.TemplateWeapon == 0 || request.TemplateStage == 0 {
			return nil, fmt.Errorf("请选择武器和状态")
		}
		action, propertyID, err := resolveTemplate(base, info, strconv.Itoa(request.TemplateWeapon), request.TemplateStage)
		if err != nil {
			return nil, err
		}
		actionLabel := ""
		for _, entry := range actionCatalog(info) {
			if entry["id"] == action {
				actionLabel = entry["label"]
				break
			}
		}
		return map[string]any{
			"action":       action,
			"property_id":  propertyID,
			"action_label": actionLabel,
		}, nil
	}
	if request.Operation == "weapon_remap" || request.Operation == "weapon_property_add" {
		info, err := inspect(base, items)
		if err != nil {
			return nil, err
		}
		message := ""
		newPropertyID := ""
		if request.Operation == "weapon_property_add" {
			template := strings.TrimSpace(request.Template)
			if template == "" {
				return nil, fmt.Errorf("请选择命中属性模板")
			}
			if len(info.properties[template]) != 1 {
				return nil, fmt.Errorf("命中属性模板 %s 不存在或不唯一", template)
			}
			id := freshPropertyID(info)
			if id == "" {
				return nil, fmt.Errorf("命中属性编号空间不足")
			}
			state.ExtraProperties[id] = ExtraProperty{Template: template}
			newPropertyID = id
			message = "已新增命中属性节点 " + id
		} else {
			key := strconv.Itoa(request.Weapon)
			if request.Weapon == 0 || request.Stage == 0 {
				return nil, fmt.Errorf("请选择武器和状态")
			}
			action := strings.TrimSpace(request.Action)
			propertyID := strings.TrimSpace(request.PropertyID)
			if request.TemplateWeapon > 0 && request.TemplateStage > 0 {
				action, propertyID, err = resolveTemplate(base, info, strconv.Itoa(request.TemplateWeapon), request.TemplateStage)
				if err != nil {
					return nil, err
				}
			}
			if err = validateRemap(base, info, key, request.Stage, action, propertyID); err != nil {
				return nil, err
			}
			if action == "" && propertyID == "" {
				if state.Remaps[key] != nil {
					delete(state.Remaps[key], request.Stage)
					if len(state.Remaps[key]) == 0 {
						delete(state.Remaps, key)
					}
				}
				clearStageBlockEdits(&state, key, request.Stage)
				message = "已取消该状态的重映射"
			} else {
				if state.Remaps[key] == nil {
					state.Remaps[key] = map[int]*StageRemap{}
				}
				state.Remaps[key][request.Stage] = &StageRemap{Action: action, PropertyID: propertyID, Label: strings.TrimSpace(request.Label)}
				// 换了动作，旧动作块上那些"绑片断/绑块"的编辑全部失效，必须一起清掉。
				clearStageBlockEdits(&state, key, request.Stage)
				// Defining a state again re-activates a column the author had
				// deleted earlier: the remap wins over the clearing.
				if state.Cleared[key] != nil {
					delete(state.Cleared[key], request.Stage)
					if len(state.Cleared[key]) == 0 {
						delete(state.Cleared, key)
					}
				}
				// A remap that changes the action or hit property invalidates
				// any hit-property edits already saved for this state: they
				// referenced the old nodes and would fail validation against
				// the remapped structure.
				if action != "" || propertyID != "" {
					clearStageRule(&state, key, request.Stage, action != "")
				}
				message = "已登记重映射；保存效果并应用后写入配置包"
			}
		}
		encoded, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			return nil, err
		}
		if err = atomicWrite(statePath, encoded); err != nil {
			return nil, err
		}
		return map[string]any{
			"remaps":           state.Remaps,
			"extra_properties": state.ExtraProperties,
			"property_id":      newPropertyID,
			"revision":         digest(append(append([]byte(nil), current...), encoded...)),
			"message":          message,
		}, nil
	}
	// weapon_state_clear deletes a state of a self-made weapon: the itemact
	// column is zeroed, its remap and saved edits are dropped, and combo-chain
	// transitions touching the state are removed. Re-defining the state later
	// through weapon_remap brings the column back.
	if request.Operation == "weapon_state_clear" {
		key := strconv.Itoa(request.Weapon)
		if request.Weapon == 0 || request.Stage == 0 {
			return nil, fmt.Errorf("请选择武器和状态")
		}
		snapshotState(statePath)
		text, err := base.text("itemact.txt")
		if err != nil {
			return nil, err
		}
		if actionRowIndex(text)[key] == nil {
			return nil, fmt.Errorf("武器 %s 不在本客户端的动作表中", key)
		}
		column := strconv.Itoa(request.Stage)
		if _, err = strconv.Atoi(column); err != nil {
			return nil, fmt.Errorf("状态编号无效")
		}
		// 状态被删掉，它上面所有绑动作块/片断的编辑（重映射、帧级按键、招架、
		// 防护、攻击范围）一并失效，必须一起清。
		clearStageBlockEdits(&state, key, request.Stage)
		if state.Remaps[key] != nil {
			delete(state.Remaps[key], request.Stage)
			if len(state.Remaps[key]) == 0 {
				delete(state.Remaps, key)
			}
		}
		if state.Cleared[key] == nil {
			state.Cleared[key] = map[int]bool{}
		}
		state.Cleared[key][request.Stage] = true
		clearStageRule(&state, key, request.Stage, true)
		if transitions := state.Chains[key]; len(transitions) > 0 {
			kept := make([]ComboTransition, 0, len(transitions))
			for _, transition := range transitions {
				if transition.OldState != column && transition.NewState != column {
					kept = append(kept, transition)
				}
			}
			if len(kept) == 0 {
				delete(state.Chains, key)
			} else {
				state.Chains[key] = kept
			}
		}
		encoded, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			return nil, err
		}
		if err = atomicWrite(statePath, encoded); err != nil {
			return nil, err
		}
		return map[string]any{
			"cleared":  state.Cleared,
			"chains":   state.Chains,
			"revision": digest(append(append([]byte(nil), current...), encoded...)),
			"message":  "已删除状态 " + column + "；保存并应用后写入配置包",
		}, nil
	}
	if request.Revision != revision {
		return nil, fmt.Errorf("配置已被其他操作更新，请重新打开武器配置后再保存")
	}
	var weapon *Weapon
	for index := range info.weapons {
		if info.weapons[index].ID == request.Weapon {
			weapon = &info.weapons[index]
			break
		}
	}
	if weapon == nil {
		return nil, fmt.Errorf("请选择本客户端的武器")
	}
	rules, err := validateRules(request.Rules, *weapon)
	if err != nil {
		return nil, err
	}
	key := strconv.Itoa(weapon.ID)
	if request.Operation == "weapon_publish" {
		if strings.TrimSpace(request.Notes) == "" || len(request.Notes) > 8000 {
			return nil, fmt.Errorf("请填写更新说明（最多 8000 字节）")
		}
		// 连招限制按 skillproid 匹配，而 render 会给已应用招式换号：发布前先把
		// 对得上的旧号翻译成新号，翻不动的直接拒绝，别把死规则发上线。
		if changed, err := reconcileComboRules(info, &state); err != nil {
			return nil, err
		} else if changed {
			if base, err = applyComboRules(base, state.ComboRules); err != nil {
				return nil, err
			}
		}
		// Publish saved plans plus this editor, without overwriting local resources.
		plans := make(map[string][]Rule)
		for id, rules := range state.Applied {
			plans[id] = rules
		}
		for id, rules := range state.Drafts {
			plans[id] = rules
		}
		plans[key] = rules
		data, err := render(base, items, plans, cloneMapOf(&state))
		if err != nil {
			return nil, err
		}
		check, err := parseArchive(data)
		if err != nil {
			return nil, err
		}
		if err = check.verify(); err != nil {
			return nil, err
		}
		names := []string{}
		for _, entry := range info.weapons {
			if _, exists := plans[strconv.Itoa(entry.ID)]; exists {
				names = append(names, entry.Name)
			}
		}
		state.Drafts[key] = rules
		encoded, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			return nil, err
		}
		if err = atomicWrite(statePath, encoded); err != nil {
			return nil, err
		}
		return weaponRelease{Data: data, Notes: "包含武器：" + strings.Join(names, "、") + "\n\n" + request.Notes}, nil
	}
	// 导出发版包：配置和素材一起给，解压即覆盖客户端根目录。weapon_publish
	// 只传 Data/config.spf2，自制武器一旦带自己的模型/动作就会缺文件。
	if request.Operation == "weapon_package" || request.Operation == "weapon_merge_export" {
		plans := make(map[string][]Rule)
		for id, saved := range state.Applied {
			plans[id] = saved
		}
		// 默认和「更新到线上」一致：草稿也进包。勾选"只包含已应用"时
		// 只发本机客户端里已经验证过的那部分，未应用的编辑不带走。
		if !request.AppliedOnly {
			for id, saved := range state.Drafts {
				plans[id] = saved
			}
		}
		// 只有页面里真的有规则时才覆盖：导包时传空 rules 是常见情况，
		// 拿它覆盖会把这把武器已保存的方案抹掉（包里就少了一套效果）。
		if len(rules) > 0 {
			plans[key] = rules
		}
		if request.Operation == "weapon_merge_export" {
			return weaponMergeExport(request, client, folder, base, items, &state, info, plans)
		}
		return weaponPackage(request, client, folder, source, base, items, &state, info, plans)
	}
	backup := ""
	message := "方案已保存，尚未应用到游戏"
	prepared := []*preparedClient{}
	switch request.Operation {
	case "weapon_save":
	case "weapon_apply", "weapon_restore":
		if runtime.GOOS == "windows" {
			command := exec.Command("tasklist", "/FI", "IMAGENAME eq gfld.dat", "/FO", "CSV", "/NH")
			hideWindow(command)
			output, err := command.Output()
			if err != nil {
				return nil, err
			}
			if bytes.Contains(bytes.ToLower(output), []byte("gfld.dat")) {
				return nil, fmt.Errorf("请先退出游戏客户端，再应用；可以先保存方案")
			}
		}
		if request.Operation == "weapon_restore" {
			delete(state.Applied, key)
			message = "该武器已恢复原效果；保存的方案仍保留"
		} else {
			state.Applied[key] = rules
			message = "配置已写入；启动游戏后加载，实战效果仍需验证"
		}
		// Render and validate before touching the disk: a failed guard must not
		// leave the client half-updated.
		plan, err := prepareClient(entry, folder, &state, state.Applied, info)
		if err != nil {
			return nil, err
		}
		prepared = append(prepared, plan)
		if err = commitClient(plan, folder); err != nil {
			return nil, err
		}
		backup = plan.Backup
		state.SourceHash = entry.SourceHash
		state.AppliedHash = entry.AppliedHash
	default:
		return nil, fmt.Errorf("未知武器配置操作")
	}
	if request.Operation != "weapon_restore" {
		state.Drafts[key] = rules
	}
	encoded, err := json.MarshalIndent(state, "", "  ")
	if err == nil {
		err = atomicWrite(statePath, encoded)
	}
	if err != nil {
		if len(prepared) > 0 {
			for _, plan := range prepared {
				if rollbackErr := atomicWrite(configPath(plan.Entry.Directory), plan.Current); rollbackErr != nil {
					return nil, fmt.Errorf("保存方案失败且回滚失败；请使用备份 %s：%v；%v", plan.Backup, err, rollbackErr)
				}
			}
		}
		return nil, err
	}
	if request.Operation == "weapon_save" {
		planFolder := filepath.Join(filepath.Dir(folder), "client-config-plans")
		plan := configPlan{ID: "weapon-" + key, Category: "weapons", Name: weapon.Name, BaseHash: digest(source.data), Weapon: weapon.ID, Rules: rules}
		if err = saveConfigPlan(planFolder, plan); err != nil {
			return nil, fmt.Errorf("武器方案已保存，但分类方案同步失败：%w", err)
		}
	}
	var backupValue any
	if backup != "" {
		backupValue = backup
	}
	return map[string]any{"backup": backupValue, "message": message}, nil
}
