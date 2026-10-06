package desktop

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// 「按状态切换招式形态」分支块的作者态定义 + 落盘实现。
//
// 机制：同一份 <AnmDesc id> 可以注册多条 —— 一条无条件 + 若干带
// <Condition><Ustate id="N"/>。引擎按玩家是否拥有状态 N 二选一。所以给一个状态
// 加分支**不需要换动作号**（itemact 那一格不动，还是同一个 AnmDesc id），只需要：
//
//  1. 把该状态的无条件块复制一份，头部插入 <Condition>；
//  2. 复制件里每个 <Anm skillproid="X"> 换成一个**新分配**的号，并在
//     skillproperty.xml 里克隆一条对应记录（否则客户端查不到命中属性，出招会取消）；
//  3. 追加到同 id 的最后一份块之后（条件块必须排在无条件块后面）。
//
// 「不拷贝」时（要从零造段）走同一套分配逻辑：块号沿用原有的 AnmDesc id，只有
// skillproid 是新号 —— 这就是「id 分配与关联」的全部内容。

// variantSkillProPrefix 是分支块 skillproid 的分配起点。与 render 的克隆号
// （900000000 起）错开，避免撞号。保留历史范围 910000000–910999999，
// 前端预分配与后端落盘必须使用同一范围。
const (
	variantSkillProPrefix = 910000000
	variantSkillProLimit  = 911000000
)

// VariantEdit 是某个状态的一条分支定义。
type VariantEdit struct {
	// Condition 是触发的状态号（<Ustate id>）。必须 > 0。
	Condition int `json:"condition"`
	// Remove 为真表示删掉这个状态的分支（其余字段忽略）。
	Remove bool `json:"remove,omitempty"`
	// Segments 为空表示「照抄无条件块的全部动作段，只换 skillproid」。
	// 给了就按它重建分支里的 <Anm> 列表（每段一个新 skillproid）。
	Segments []VariantAnm `json:"segments,omitempty"`
}

// VariantAnm 是分支里的一段 <Anm>。
type VariantAnm struct {
	Name   string  `json:"name"`
	Start  int     `json:"start"`
	End    int     `json:"end"`
	Damage float64 `json:"damage,omitempty"`
	// DamagePresent distinguishes an omitted/null damage from an explicit 0.
	// It is populated by JSON decoding and is not serialized as a separate field.
	DamagePresent bool `json:"-"`
	// TemplateSkillProID 明确克隆源；带此字段的新段把 SkillProID 当最终分支号。
	TemplateSkillProID string `json:"template_skillproid,omitempty"`
	// ReplayTimes 是卡帧数：>0 时这一段画面定格，用来把多段判定挤进几帧。
	// 原生写法 <Anm replaytimes="2">。为 0 表示不卡帧。
	ReplayTimes int `json:"replay_times,omitempty"`
	// SkillProID 没有模板字段时保持旧克隆源语义；当前分支已有号优先原样保留。空 = 纯动作段
	// （不分配命中属性 —— 原生 253521 的 406 分支就是这样：8 段里只有 3 段有号）。
	SkillProID string `json:"skillproid,omitempty"`
	// AnmID 是原段的 <Anm id>，回填时带上以保住片断编号（范围/防护编辑按它定位）。
	AnmID string `json:"anm_id,omitempty"`
}

func (segment *VariantAnm) UnmarshalJSON(data []byte) error {
	type variantAnmFields VariantAnm
	var row struct {
		variantAnmFields
		Damage json.RawMessage `json:"damage"`
	}
	if err := json.Unmarshal(data, &row); err != nil {
		return err
	}
	*segment = VariantAnm(row.variantAnmFields)
	text := strings.TrimSpace(string(row.Damage))
	if len(row.Damage) == 0 || text == "null" {
		return nil
	}
	segment.DamagePresent = false

	if len(text) > 0 && text[0] == '"' {
		if err := json.Unmarshal(row.Damage, &text); err != nil {
			return err
		}
		text = strings.TrimSpace(text)
		if text == "" {
			return nil
		}
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return fmt.Errorf("动作分支 damage 必须为数字：%q", text)
	}
	segment.Damage = value
	segment.DamagePresent = true
	return nil
}

func (segment VariantAnm) MarshalJSON() ([]byte, error) {
	type variantAnmFields VariantAnm
	row, err := json.Marshal(variantAnmFields(segment))
	if err != nil {
		return nil, err
	}
	fields := map[string]any{}
	if err := json.Unmarshal(row, &fields); err != nil {
		return nil, err
	}
	if segment.DamagePresent || segment.Damage != 0 {
		fields["damage"] = segment.Damage
	} else {
		delete(fields, "damage")
	}
	return json.Marshal(fields)
}

// applyVariants 把作者定义的分支落盘到 animation/*.xml + skillproperty.xml。
// 无事可做时原样返回 base。
func applyVariants(a *archive, state *weaponState, items []Item, current ...*archive) (*archive, error) {
	return applyVariantsFilteredMode(a, state, items, nil, true, current...)
}

func applyVariantsFiltered(a *archive, state *weaponState, items []Item, wanted map[int]bool, current ...*archive) (*archive, error) {
	return applyVariantsFilteredMode(a, state, items, wanted, true, current...)
}

// applyVariantsProjection renders the in-memory workspace for detail/catalog views.
// A draft may still contain a preallocated 910... id that collides with the installed
// package. Keep the editor readable and defer collision rejection/reallocation to the
// explicit apply path, where the user can resolve the conflict.
func applyVariantsProjection(a *archive, state *weaponState, items []Item, wanted map[int]bool, current ...*archive) (*archive, error) {
	return applyVariantsFilteredMode(a, state, items, wanted, false, current...)
}

func applyVariantsFilteredMode(a *archive, state *weaponState, items []Item, wanted map[int]bool, strictCurrent bool, current ...*archive) (*archive, error) {
	if len(state.Variants) == 0 {
		return a, nil
	}
	var extraActions []string
	if wanted != nil {
		for key, stages := range state.Variants {
			id, parseErr := strconv.Atoi(key)
			if parseErr == nil && wanted[id] {
				for _, edits := range stages {
					for _, edit := range edits {
						for _, segment := range edit.Segments {
							if action := strings.TrimSpace(segment.Name); len(action) > 4 {
								extraActions = append(extraActions, action)
							}
						}
					}
				}
			}
		}
	}
	info, err := inspectFilteredWithActions(a, items, wanted, extraActions...)
	if err != nil {
		return nil, err
	}
	actionText, err := a.text("itemact.txt")
	if err != nil {
		return nil, err
	}
	lines := strings.Split(actionText, "\n")
	if len(lines) == 0 {
		return nil, fmt.Errorf("动作表为空")
	}
	header := strings.Split(strings.TrimSuffix(lines[0], "\r"), "\t")
	columns := map[string]int{}
	for index, name := range header {
		columns[name] = index
	}
	rowIndex := map[string]int{}
	for index, line := range lines[1:] {
		cols := strings.Split(strings.TrimSuffix(line, "\r"), "\t")
		if len(cols) >= 1 {
			rowIndex[cols[0]] = index + 1
		}
	}

	alloc, err := newVariantAllocator(a)
	if err != nil {
		return nil, err
	}
	// 当前包占用也要阻止自动分配；自己的显式号仍可从原始基线重新注册。
	alloc.reserved = map[string]bool{}
	alloc.allowExplicitReassign = !strictCurrent
	if len(current) > 0 {
		live, err := newVariantAllocator(current[0])
		if err != nil {
			return nil, err
		}
		if strictCurrent {
			for key, edits := range state.Variants {
				if err := validateVariantCurrentIDs(current[0], key, edits, state); err != nil {
					return nil, err
				}
			}
		}
		for id := range live.occupied {
			alloc.reserved[id] = true
		}
		for id, source := range live.sources {
			if alloc.sources[id] == "" {
				// 旧格式允许把当前包已分配的91号当模板，基线未必有该注册。
				alloc.sources[id] = source
			}
		}
		// 旧草稿仍按源号自动分配；只释放本轮重建分支独占的旧号，保持基线重建幂等。
		for key, stages := range state.Variants {
			for stage, edits := range stages {
				for _, edit := range edits {
					own, err := variantCurrentBranchUsage(current[0], key, stage, edit.Condition)
					if err != nil {
						return nil, err
					}
					for id, count := range own {
						if alloc.isSelfMade(id) && live.usage[id] == count {
							delete(alloc.reserved, id)
						}
					}
				}
			}
		}
	}
	// 全部显式最终号先预留，避免前面的旧格式段自动分配抢走后面的号。
	explicitIDs := map[string]bool{}
	for _, stages := range state.Variants {
		for _, edits := range stages {
			for _, edit := range edits {
				if edit.Remove {
					continue
				}
				for _, segment := range edit.Segments {
					id := strings.TrimSpace(segment.SkillProID)
					if strings.TrimSpace(segment.TemplateSkillProID) == "" || id == "" {
						continue
					}
					if !alloc.isSelfMade(id) {
						// 原生已有分支的号由 reusable 优先保留，范围校验在 attach 中进行。
						continue
					}
					if explicitIDs[id] {
						return nil, fmt.Errorf("预分配的命中属性 %s 在分支草稿中重复", id)
					}
					explicitIDs[id] = true
					alloc.reserved[id] = true
				}
			}
		}
	}

	properties, err := a.text("skillproperty.xml")
	if err != nil {
		return nil, err
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

	weaponKeys := make([]string, 0, len(state.Variants))
	for key := range state.Variants {
		if wanted != nil {
			id, parseErr := strconv.Atoi(key)
			if parseErr != nil || !wanted[id] {
				continue
			}
		}
		weaponKeys = append(weaponKeys, key)
	}
	sort.Strings(weaponKeys)
	for _, weaponKey := range weaponKeys {
		line := rowIndex[weaponKey]
		if line == 0 {
			return nil, fmt.Errorf("武器 %s 不在动作表中", weaponKey)
		}
		row := strings.Split(strings.TrimSuffix(lines[line], "\r"), "\t")
		stages := make([]int, 0, len(state.Variants[weaponKey]))
		for stage := range state.Variants[weaponKey] {
			stages = append(stages, stage)
		}
		sort.Ints(stages)
		for _, stage := range stages {
			column, ok := columns[strconv.Itoa(stage)]
			if !ok || column >= len(row) {
				return nil, fmt.Errorf("状态 %d 不属于武器 %s", stage, weaponKey)
			}
			action := strings.TrimSpace(row[column])
			if action == "" || action == "0" {
				return nil, fmt.Errorf("状态 %d 没有动作，无法编辑分支形态", stage)
			}
			variants := actionVariants(info, action)
			base, ok := pickBlock(variants)
			if !ok {
				return nil, fmt.Errorf("状态 %d 的动作 %s 不存在", stage, action)
			}
			file, err := a.animationWriteFile(action)
			if err != nil {
				return nil, err
			}
			animation, err := loadAnimation(file)
			if err != nil {
				return nil, err
			}
			id := strings.TrimSpace(base.node.get("id"))
			for _, edit := range state.Variants[weaponKey][stage] {
				if edit.Condition <= 0 {
					return nil, fmt.Errorf("状态 %d 的分支缺少触发条件", stage)
				}
				condition := strconv.Itoa(edit.Condition)
				// 旧块（同 condition）：它的号既能被本轮复用，也可能是待回收的孤儿。
				existing, hasExisting := currentConditionalBlock(animation, id, condition)
				if hasExisting && strings.Count(animation, existing) != 1 {
					return nil, fmt.Errorf("条件 %s 的分支块无法唯一定位", condition)
				}
				reusable := map[string]bool{}
				if hasExisting {
					for _, number := range blockSkillProIDs(existing) {
						reusable[number] = true
						// 先假设会回收；下面 record 里复用到就自动取消。
						alloc.retire(number)
					}
				}
				for _, segment := range edit.Segments {
					value := strings.TrimSpace(segment.SkillProID)
					if segment.TemplateSkillProID != "" && alloc.isSelfMade(value) && reusable[value] {
						count := 0
						for _, number := range blockSkillProIDs(existing) {
							if number == value {
								count++
							}
						}
						if alloc.usage[value] > count {
							return nil, fmt.Errorf("预分配的命中属性 %s 已被其他动作占用", value)
						}
					}
				}
				if edit.Remove {
					// 删除是幂等操作：workspace 可能来自已经刷新过的投影，
					// 目标条件块已经不存在时，仍应继续应用并清理其余引用。
					if hasExisting {
						animation = removeBlock(animation, existing)
					}
					continue
				}
				hints := map[string]*xmlNode{}
				if hasExisting {
					if existingNode, parseErr := parseXML(existing); parseErr == nil {
						segmentIndex := 0
						existingNode.walk(func(child *xmlNode) {
							if child.tag != "Anm" {
								return
							}
							hints[fmt.Sprintf("#%d", segmentIndex)] = child
							segmentIndex++
							if anmID := strings.TrimSpace(child.get("id")); anmID != "" {
								hints[anmID] = child
							}
						})
					}
				}
				built, props, err := buildVariant(base.original, id, condition, edit.Segments, reusable, alloc, hints)
				if err != nil {
					return nil, err
				}
				if hasExisting {
					// 原地替换：旧块本来就已经排在无条件块之后，摘掉再插回去只会
					// 多留一个换行（每保存一次文件就长一行，也不再有幂等）。
					animation = strings.Replace(animation, existing, built, 1)
				} else {
					next, err := insertAfterLastBlock(animation, id, built)
					if err != nil {
						return nil, err
					}
					animation = next
				}
				if err := alloc.record(props); err != nil {
					return nil, err
				}
			}
			if _, err := parseXML(animation); err != nil {
				return nil, fmt.Errorf("%s 分支块不合法：%w", file, err)
			}
			animations[file] = animation
		}
	}

	// 以最终动画引用决定回收，包含同一块重复引用和一次删除多个分支的情况。
	for file, animation := range animations {
		before, err := a.text(file)
		if err != nil {
			return nil, err
		}
		for id, count := range variantAnimationUsage(before) {
			alloc.usage[id] -= count
		}
		for id, count := range variantAnimationUsage(animation) {
			alloc.usage[id] += count
		}
	}
	for id := range alloc.retired {
		if alloc.usage[id] > 0 {
			delete(alloc.retired, id)
		}
	}
	// skillproperty.xml：新建 / 覆写伤害 / 回收孤儿统一收口在这里。
	properties, err = alloc.apply(properties)
	if err != nil {
		return nil, err
	}

	replacements := map[string][]byte{}
	if len(alloc.added) > 0 || len(alloc.updates) > 0 || len(alloc.retired) > 0 {
		if replacements["skillproperty.xml"], err = encodeText(properties); err != nil {
			return nil, err
		}
	}
	for file, animation := range animations {
		encoded, err := encodeText(animation)
		if err != nil {
			return nil, err
		}
		replacements[file] = encoded
	}
	if len(replacements) == 0 {
		return a, nil
	}
	data, err := a.replace(replacements)
	if err != nil {
		return nil, err
	}
	return parseArchive(data)
}

// variantProp 是一条待注册的命中属性：新号 + 克隆源 + 覆盖的伤害。
type variantProp struct {
	newID string
	// templateID 是克隆源（原生号或别人的自建号）；"" = 用 skillproperty.xml 的
	// 首条 PropertyItem 当模板（只在新号没有来源时才发生）。
	templateID string
	// reuse 表示这个号是本武器这一分支上一轮已经用过的自建号，原样保留不新建。
	reuse       bool
	damage      float64
	hasOverride bool
}

// buildVariant 从无条件块复制出带条件的分支块。segments 为空时照抄原段，只把每个
// <Anm skillproid> 换成新号（命中属性按原号克隆，伤害自然保留）；给了 segments 就
// 按它重建 <Anm> 列表。
//
// 重建时**逐段继承原文**：第 i 个段拿原块第 i 段当模板（超出就复用最后一段），
// 于是 <AttackScope>/<CharScope>/<Effect>/<Audio>/<Line>… 这些子节点都留在原地，
// 只覆盖 id / name / startframe / endframe / replaytimes / skillproid。
// 早期版本是「删光 <Anm> 再从零造」，子节点全丢，而且忘写 replaytimes、
// 把 <Anm id> 写成了动画名 —— 卡帧没了、多段判定就看着像同一段动画反复播。
//
// reusable 是「被替换掉的旧块里出现过的号」。前端把段原有的号原样带回来时，只要它
// 落在 reusable 里就直接**保留**（号不变）——既让编辑幂等（不然每保存一次就换一批
// 新号，旧号全成孤儿，skillproperty.xml 只涨不消、号还一直漂），也避免把原生分支
// 的号重铸。带不回（新加的段）或填的是别的号才分配新号。
func buildVariant(base, id, condition string, segments []VariantAnm, reusable map[string]bool, alloc *variantAllocator, hints ...map[string]*xmlNode) (string, []variantProp, error) {
	node, err := parseXML(base)
	if err != nil {
		return "", nil, err
	}
	node.set("id", id)
	// 条件必须在最前；无条件块保持原位不动。
	node.children = append([]*xmlNode{conditionNode(condition)}, node.children...)

	props := []variantProp{}
	// attach 决定一个 <Anm> 挂哪个命中属性号。want 为空 = 纯动作段，不挂号 —— 原生
	// 253521 的 406 分支 8 段里就只有 3 段有号，硬给每段都塞一个只会多出一堆没人
	// 引用的 PropertyItem。
	attach := func(anm *xmlNode, want, template string, damage float64, hasOverride bool) error {
		value := strings.TrimSpace(want)
		source := strings.TrimSpace(template)
		if value == "" {
			anm.remove("skillproid")
			return nil
		}
		explicit := source != ""
		if source == "" {
			// 旧 payload 的 skillproid 一律是克隆源，包括历史已分配的 91 号。
			source = value
		}
		if reusable[value] {
			// 这个号就是被替换掉那一块里原有的一段：原样保留，号不变。
			// 自建号（上一轮 GM 分的）和原生号（原作者手写的）都照此处理 ——
			// 否则「打开原生分支不动、直接保存」也会把原生号重铸成新号。
			anm.set("skillproid", value)
			props = append(props, variantProp{newID: value, templateID: source, reuse: true, damage: damage, hasOverride: hasOverride})
			return nil
		}
		if explicit {
			if !alloc.isSelfMade(value) {
				return fmt.Errorf("预分配的命中属性 %s 不在分支编号范围内", value)
			}
			// 新版前端会先预占最终 910... ID，再把模板源单独回传。
			// 旧块中的同号已在 reusable 中提前放行；其余已存在的号不能覆盖。
			if alloc.occupied[value] {
				if !alloc.allowExplicitReassign {
					return fmt.Errorf("预分配的命中属性 %s 已被其他动作占用", value)
				}
				newID, err := alloc.next()
				if err != nil {
					return err
				}
				value = newID
			}
			if source == "" || alloc.sources[source] == "" {
				return fmt.Errorf("命中属性 %s 缺少有效模板", value)
			}
			alloc.occupied[value] = true
			anm.set("skillproid", value)
			props = append(props, variantProp{newID: value, templateID: source, damage: damage, hasOverride: hasOverride})
			return nil
		}
		if source == "" || alloc.sources[source] == "" {
			// 号在技能属性表里不存在（原生数据里有悬空引用）：返回明确错误，避免静默变成纯动作段。
			return fmt.Errorf("命中属性模板 %s 在技能属性表里不存在", source)
		}
		newID, err := alloc.next()
		if err != nil {
			return err
		}
		anm.set("skillproid", newID)
		props = append(props, variantProp{newID: newID, templateID: source, damage: damage, hasOverride: hasOverride})
		return nil
	}

	if len(segments) == 0 {
		var attachErr error
		node.walk(func(child *xmlNode) {
			if attachErr != nil || child.tag != "Anm" {
				return
			}
			attachErr = attach(child, child.get("skillproid"), "", 0, false)
		})
		if attachErr != nil {
			return "", nil, attachErr
		}
	} else {
		kept := make([]*xmlNode, 0, len(node.children))
		templates := make([]*xmlNode, 0, 4)
		hintByID := map[string]*xmlNode{}
		if len(hints) > 0 && hints[0] != nil {
			hintByID = hints[0]
		}
		for _, child := range node.children {
			if child.tag == "Anm" {
				templates = append(templates, child)
				continue
			}
			kept = append(kept, child)
		}
		// 模板按**帧窗口重叠**挑，不按下标：把本来是一下的命中拆成三段时，三段都
		// 落在同一段原窗口里，按下标只有第一段能继承到带 <AttackScope>（命中盒）
		// 的那份模板，后两段就成了空壳 —— 打不中人。重叠最多的那份才是它真正的原型。
		pickTemplate := func(segment VariantAnm) *xmlNode {
			want := strings.TrimSpace(segment.TemplateSkillProID)
			if want == "" {
				want = strings.TrimSpace(segment.SkillProID)
			}
			hasSource := false
			for _, template := range templates {
				if want != "" && strings.TrimSpace(template.get("skillproid")) == want {
					hasSource = true
				}
			}
			best, bestOverlap := -1, -1<<30
			for index, template := range templates {
				if hasSource && strings.TrimSpace(template.get("skillproid")) != want {
					continue
				}
				start, startErr := strconv.Atoi(strings.TrimSpace(template.get("startframe")))
				end, endErr := strconv.Atoi(strings.TrimSpace(template.get("endframe")))
				if startErr != nil || endErr != nil {
					continue
				}
				low, high := start, end
				if segment.Start > low {
					low = segment.Start
				}
				if segment.End < high {
					high = segment.End
				}
				if overlap := high - low; overlap > bestOverlap {
					bestOverlap, best = overlap, index
				}
			}
			if best < 0 {
				return nil
			}
			return templates[best]
		}
		rebuilt := make([]*xmlNode, 0, len(segments))
		used := map[string]bool{}
		// 2026-10-06：作者删掉中间某个片断后，后面各段仍带着原来的号，
		// 于是块里出现「1,2,3,4,5,8」这种中间跳号。客户端按片断序遍历，
		// 遇到空洞后取不到对应状态，释放该分支动作直接崩溃。
		// 原生数据只存在「重复号」（1,2,2），从不跳号，所以跳号一律判定为删除残留，
		// 这里按顺序重排为 1..n。重排只动 id，不动 startframe/endframe/
		// skillproid/子元素 —— 攻击判定与帧区间保持作者原本的设定。
		renumbered := make([]string, len(segments))
		seen := map[string]bool{}
		gapped := false
		for index, segment := range segments {
			id := strings.TrimSpace(segment.AnmID)
			if id == "" {
				continue
			}
			if seen[id] {
				// 拆段会产生重复号，交给下面的 used 逻辑补号。
				continue
			}
			seen[id] = true
			renumbered[index] = id
		}
		next := 1
		for _, id := range renumbered {
			if id == "" {
				continue
			}
			if id != strconv.Itoa(next) {
				gapped = true
				break
			}
			next++
		}
		if gapped {
			next = 1
			for index := range segments {
				if renumbered[index] == "" {
					continue
				}
				renumbered[index] = strconv.Itoa(next)
				next++
			}
		}
		for index, segment := range segments {
			var anm *xmlNode
			hint := hintByID[strings.TrimSpace(segment.AnmID)]
			if hint == nil && strings.TrimSpace(segment.AnmID) == "" {
				hint = hintByID[fmt.Sprintf("#%d", index)]
			}
			if hint != nil {
				// 回读保存时优先使用现有条件块同一片断的完整节点，
				// 保住该片断已经选定的 AttackScope/Effect/Audio 等子元素。
				anm = hint.clone()
			} else if template := pickTemplate(segment); template != nil {
				anm = template.clone()
			} else {
				anm = &xmlNode{tag: "Anm"}
			}
			// 片断编号：沿用作者给的号，其次沿用模板的原号；重复（拆段时必然重复）
			// 或没有模板时取下一个没被占用的正整数 —— 块里 id 撞车会让「按 id 定位」
			// 的范围/防护编辑改错段。删除留下的跳号已在上面重排过了。
			anmID := strings.TrimSpace(segment.AnmID)
			if gapped {
				anmID = renumbered[index]
			}
			if anmID == "" {
				anmID = strings.TrimSpace(anm.get("id"))
			}
			if anmID == "" || used[anmID] {
				for next := 1; ; next++ {
					candidate := strconv.Itoa(next)
					if !used[candidate] {
						anmID = candidate
						break
					}
				}
			}
			used[anmID] = true
			anm.set("id", anmID)
			anm.set("name", segment.Name)
			anm.set("startframe", strconv.Itoa(segment.Start))
			anm.set("endframe", strconv.Itoa(segment.End))
			if segment.ReplayTimes > 0 {
				anm.set("replaytimes", strconv.Itoa(segment.ReplayTimes))
			} else {
				anm.remove("replaytimes")
			}
			want, source := segment.SkillProID, segment.TemplateSkillProID
			if hint != nil {
				native := strings.TrimSpace(hint.get("skillproid"))
				if native != "" && !alloc.isSelfMade(native) && strings.TrimSpace(source) == native {
					// 历史重铸草稿遇到真正原生条件段时，以当前原生号为准。
					want, source = native, ""
				}
			}
			if err := attach(anm, want, source, segment.Damage, segment.DamagePresent || segment.Damage != 0); err != nil {
				return "", nil, err
			}
			rebuilt = append(rebuilt, anm)
		}
		node.children = append(kept, rebuilt...)
	}
	text, err := node.serialize()
	if err != nil {
		return "", nil, err
	}
	return text, props, nil
}

// blockSkillProIDs 收集一个 <AnmDesc> 块里出现的全部 skillproid（含重复）。
func blockSkillProIDs(block string) []string {
	node, err := parseXML(block)
	if err != nil {
		return nil
	}
	ids := []string{}
	node.walk(func(child *xmlNode) {
		if child.tag != "Anm" {
			return
		}
		if id := strings.TrimSpace(child.get("skillproid")); id != "" {
			ids = append(ids, id)
		}
	})
	return ids
}

// conditionNode 造一个 <Condition><Ustate id="N"/></Condition>。
func conditionNode(condition string) *xmlNode {
	ustate := &xmlNode{tag: "Ustate"}
	ustate.set("id", condition)
	cond := &xmlNode{tag: "Condition"}
	cond.children = []*xmlNode{ustate}
	return cond
}

// removeBlock 从动画里摘掉一个块。块是我们自己插的，前面那个换行也一并摘掉，
// 否则每删一次就多留一个空行（也顺带保证「删分支」可幂等）。
func removeBlock(animation, block string) string {
	if strings.Contains(animation, "\n"+block) {
		return strings.Replace(animation, "\n"+block, "", 1)
	}
	return strings.Replace(animation, block, "", 1)
}

// insertAfterLastBlock 把 block 插到动画里 id 等于 want 的最后一份块之后，
// 保证无条件块在前、条件块在后。
func insertAfterLastBlock(animation, want, block string) (string, error) {
	last := -1
	for _, loc := range animationPattern.FindAllStringIndex(animation, -1) {
		node, err := parseXML(animation[loc[0]:loc[1]])
		if err != nil {
			continue
		}
		if strings.TrimSpace(node.get("id")) == want {
			last = loc[1]
		}
	}
	if last < 0 {
		// 没有同 id 的块（例如从零建分支）：退到 </AnmInfo> 之前。
		loc := anmInfoEndPattern.FindAllStringIndex(animation, -1)
		if len(loc) != 1 {
			return animation, fmt.Errorf("动作表结构错误")
		}
		last = loc[0][0]
	}
	return animation[:last] + "\n" + block + animation[last:], nil
}

// comboVariants 读出某武器各状态下「块里已经注册的条件分支」，供编辑器显示现况。
func comboVariants(info *inspection, weaponKey string, saved ...map[int][]VariantEdit) map[int][]map[string]any {
	out := map[int][]map[string]any{}
	for _, weapon := range info.weapons {
		if strconv.Itoa(weapon.ID) != weaponKey {
			continue
		}
		for _, stage := range weapon.Stages {
			variants := actionVariants(info, stage.Action)
			if len(variants) < 2 {
				continue
			}
			list := []map[string]any{}
			for _, variant := range variants {
				if variant.condition == "" {
					continue
				}
				segments := variantSegmentMaps(info, variant.node)
				if len(saved) > 0 {
					raw, _ := strconv.Atoi(strings.TrimSpace(stage.State))
					for _, edit := range saved[0][raw] {
						if !edit.Remove && strconv.Itoa(edit.Condition) == variant.condition {
							sources := edit.Segments
							if len(sources) == 0 {
								if base, ok := pickBlock(variants); ok && base.condition == "" {
									for _, segment := range segmentsOf(base.node, "") {
										sources = append(sources, VariantAnm{AnmID: segment.ID, SkillProID: segment.SkillPro})
									}
								}
							}
							variantTemplateMetadata(segments, sources)
						}
					}
				}
				list = append(list, map[string]any{
					"condition": variant.condition,
					"segments":  segments,
				})
			}
			if len(list) > 0 {
				// 键用**原始状态号**（2011…）而不是 Stage 的归一化编号（1…），
				// 与 itemact 的列名、前端 stage['state'] 保持一致。
				if raw, err := strconv.Atoi(strings.TrimSpace(stage.State)); err == nil {
					out[raw] = list
				}
			}
		}
		break
	}
	return out
}

// variantTemplateMetadata 只为生成的最终号补模板源；原生分支号保留原有语义。
func variantTemplateMetadata(segments []map[string]any, saved []VariantAnm) {
	byID := map[string]VariantAnm{}
	for _, segment := range saved {
		if id := strings.TrimSpace(segment.AnmID); id != "" {
			byID[id] = segment
		}
	}
	alloc := &variantAllocator{}
	for index, entry := range segments {
		final, _ := entry["skillproid"].(string)
		if !alloc.isSelfMade(final) {
			continue
		}
		id, _ := entry["anm_id"].(string)
		segment, ok := byID[id]
		if !ok && index < len(saved) {
			segment, ok = saved[index], true
		}
		if !ok {
			continue
		}
		source := strings.TrimSpace(segment.TemplateSkillProID)
		if source == "" && strings.TrimSpace(segment.SkillProID) != final {
			source = strings.TrimSpace(segment.SkillProID)
		}
		if source != "" {
			entry["template_skillproid"] = source
		}
	}
}

// variantBases 读出某武器各状态下**无条件块**的动作段，供编辑器「照抄本招」时
// 参考 / 回填（键同样是原始状态号）。
func variantBases(info *inspection, weaponKey string) map[int][]map[string]any {
	out := map[int][]map[string]any{}
	for _, weapon := range info.weapons {
		if strconv.Itoa(weapon.ID) != weaponKey {
			continue
		}
		for _, stage := range weapon.Stages {
			base, ok := pickBlock(actionVariants(info, stage.Action))
			if !ok {
				continue
			}
			segments := variantSegmentMaps(info, base.node)
			if len(segments) == 0 {
				continue
			}
			if raw, err := strconv.Atoi(strings.TrimSpace(stage.State)); err == nil {
				out[raw] = segments
			}
		}
		break
	}
	return out
}

// variantSegmentMaps 把分支块的 <Anm> 片断整理成编辑器可往返的形状：段本身帧区间
// + 卡帧 + 命中的 skillproid + 从 skillproperty 读到的伤害/BUFF。带上这些是为了编辑
// 已有分支时能原样回填（否则重建段会退回模板伤害、丢掉卡帧）。
func variantSegmentMaps(info *inspection, node *xmlNode) []map[string]any {
	segments := segmentsOf(node, "")
	out := make([]map[string]any, 0, len(segments))
	for _, segment := range segments {
		entry := map[string]any{
			"name":  segment.Name,
			"start": segment.Start,
			"end":   segment.End,
		}
		if segment.ID != "" {
			entry["anm_id"] = segment.ID
		}
		// 卡帧段：不造成伤害，只把画面定格，用来把多段判定挤进几帧。
		if segment.ReplayTimes > 0 {
			entry["replay_times"] = segment.ReplayTimes
		}
		if segment.SkillPro == "" {
			out = append(out, entry)
			continue
		}
		entry["skillproid"] = segment.SkillPro
		if nodes := info.properties[segment.SkillPro]; len(nodes) > 0 {
			if damage := strings.TrimSpace(nodes[0].get("SkillDamage")); damage != "" {
				if value, err := strconv.ParseFloat(damage, 64); err == nil {
					entry["damage"] = value
				}
			}
			if buff := strings.TrimSpace(nodes[0].get("UnNormalState")); buff != "" && buff != "0" {
				entry["buff"] = buff
			}
		}
		out = append(out, entry)
	}
	return out
}

func variantCurrentBranchUsage(current *archive, key string, stage, condition int) (map[string]int, error) {
	usage := map[string]int{}
	actions, err := current.text("itemact.txt")
	if err != nil {
		return nil, err
	}
	row := actionRowIndex(actions)[key]
	for column, state := range variantItemactHeader(actions) {
		if state != strconv.Itoa(stage) || column >= len(row) || row[column] == "0" {
			continue
		}
		file, err := current.animationWriteFile(row[column])
		if err != nil {
			return nil, err
		}
		text, err := current.text(file)
		if err != nil {
			return nil, err
		}
		parts := strings.Split(actionKey(row[column]), "/")
		if len(parts) == 2 {
			if block, ok := currentConditionalBlock(text, parts[1], strconv.Itoa(condition)); ok {
				usage = variantAnimationUsage(block)
			}
		}
	}
	return usage, nil
}

// cloneVariantHitProperty creates a branch-local canonical node. A legacy workspace
// can reuse one 910... number in multiple states, even when the template properties
// differ; renaming the shared node would then rewrite unrelated branches.
func cloneVariantHitProperty(state *weaponState, oldID, newID, weapon string, stage, condition int, segment VariantAnm) error {
	if state == nil || strings.TrimSpace(newID) == "" {
		return nil
	}
	property, ok := state.HitProperties[oldID]
	if !ok {
		property = HitProperty{ID: oldID, OwnerWeapon: weapon, Source: "variant"}
	}
	property.ID = newID
	if property.OwnerWeapon == "" {
		property.OwnerWeapon = weapon
	}
	if property.State == 0 {
		property.State = stage
	}
	if property.Condition == 0 {
		property.Condition = condition
	}
	if property.SegmentID == "" {
		property.SegmentID = strings.TrimSpace(segment.AnmID)
	}
	property.Source = "variant"
	property.Preallocated = true
	property.References = appendUniqueHitRef(property.References, HitPropertyRef{
		Weapon: weapon, State: stage, Condition: condition,
		Segment: property.SegmentID, Kind: "variant",
	})
	state.HitProperties[newID] = property
	return nil
}

// reassignConflictingVariantIDs 为应用阶段的预分配分支属性号解决真实占用。
//
// 910... 号在 workspace 中只是草稿最终号；用户切换武器、重复应用或外部包
// 更新后，原号可能已经被当前包的其它动作占用。此时不能覆盖旧动作，也不能
// 让用户手改 JSON，因此在正式 apply 前统一分配全库空闲号，并只修改内存 state。
// 同一武器、同一状态、同一条件分支仍在使用的历史号允许复用。
func reassignConflictingVariantIDs(current *archive, state *weaponState, candidates ...*archive) (map[string]string, error) {
	changed := map[string]string{}
	if current == nil || state == nil || len(state.Variants) == 0 {
		return changed, nil
	}
	alloc, err := newVariantAllocator(current)
	if err != nil {
		return nil, err
	}
	if alloc.reserved == nil {
		alloc.reserved = map[string]bool{}
	}
	// 先预留整个内存 state 中的 ID，避免自动分配撞上尚未落盘的对象。
	for id := range state.HitProperties {
		if id = strings.TrimSpace(id); id != "" {
			alloc.reserved[id] = true
		}
	}
	for id := range state.ExtraProperties {
		if id = strings.TrimSpace(id); id != "" {
			alloc.reserved[id] = true
		}
	}
	for _, stages := range state.Remaps {
		for _, remap := range stages {
			if remap != nil && strings.TrimSpace(remap.PropertyID) != "" {
				alloc.reserved[strings.TrimSpace(remap.PropertyID)] = true
			}
		}
	}
	for _, entries := range state.PropertyClones {
		for _, id := range entries {
			if id = strings.TrimSpace(id); id != "" {
				alloc.reserved[id] = true
			}
		}
	}
	for _, ruleSets := range []map[string][]Rule{state.Drafts, state.Applied} {
		for _, rules := range ruleSets {
			for _, rule := range rules {
				for id := range rule.Properties {
					if id = strings.TrimSpace(id); id != "" {
						alloc.reserved[id] = true
					}
				}
			}
		}
	}
	for _, stages := range state.Variants {
		for _, edits := range stages {
			for _, edit := range edits {
				if edit.Remove {
					continue
				}
				for _, segment := range edit.Segments {
					if id := strings.TrimSpace(segment.SkillProID); id != "" {
						alloc.reserved[id] = true
					}
				}
			}
		}
	}
	// Strict apply checks both the live package and the pre-variant candidate.
	// Preserve a number only when every occupied view belongs to this branch.
	archives := []*archive{current}
	allocators := []*variantAllocator{alloc}
	for _, candidate := range candidates {
		if candidate == nil || candidate == current {
			continue
		}
		candidateAlloc, err := newVariantAllocator(candidate)
		if err != nil {
			return nil, err
		}
		archives = append(archives, candidate)
		allocators = append(allocators, candidateAlloc)
		for id := range candidateAlloc.occupied {
			alloc.reserved[id] = true
		}
	}
	// oldID may be reused by legacy workspaces across independent branches.
	// Single-use IDs keep the historical global rename behavior; reused IDs are
	// split per branch context so one replacement cannot rewrite other branches.
	occurrences := map[string]int{}
	for _, stages := range state.Variants {
		for _, edits := range stages {
			for _, edit := range edits {
				if edit.Remove {
					continue
				}
				for _, segment := range edit.Segments {
					id := strings.TrimSpace(segment.SkillProID)
					if id != "" && strings.TrimSpace(segment.TemplateSkillProID) != "" && alloc.isSelfMade(id) {
						occurrences[id]++
					}
				}
			}
		}
	}
	seenContext := map[string]string{}
	changedContext := map[string]string{}
	keys := make([]string, 0, len(state.Variants))
	for key := range state.Variants {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		stages := state.Variants[key]
		stageIDs := make([]int, 0, len(stages))
		for stage := range stages {
			stageIDs = append(stageIDs, stage)
		}
		sort.Ints(stageIDs)
		for _, stage := range stageIDs {
			for editIndex := range stages[stage] {
				edit := &stages[stage][editIndex]
				if edit.Remove {
					continue
				}
				for segmentIndex := range edit.Segments {
					segment := &edit.Segments[segmentIndex]
					id := strings.TrimSpace(segment.SkillProID)
					context := fmt.Sprintf("%s|%d|%d|%d", key, stage, edit.Condition, segmentIndex)
					if replacement := changedContext[context]; replacement != "" {
						segment.SkillProID = replacement
						continue
					}
					if id == "" || strings.TrimSpace(segment.TemplateSkillProID) == "" || !alloc.isSelfMade(id) {
						continue
					}
					conflict := seenContext[id] != "" && seenContext[id] != context
					for index, view := range archives {
						viewAlloc := allocators[index]
						if !viewAlloc.occupied[id] {
							continue
						}
						branchUsage, usageErr := variantCurrentBranchUsage(view, key, stage, edit.Condition)
						if usageErr != nil {
							return nil, usageErr
						}
						owned := branchUsage[id] > 0 && viewAlloc.usage[id] == branchUsage[id]
						if index == 0 && !owned {
							owned = variantRenderedBranchOwnsID(state, key, stage, id, branchUsage, viewAlloc.usage)
						}
						if !owned {
							conflict = true
							break
						}
					}
					if !conflict {
						seenContext[id] = context
						continue
					}
					// 换号必须避让当前包、候选包和整个 workspace。
					newID, nextErr := alloc.next()
					if nextErr != nil {
						return nil, nextErr
					}
					if occurrences[id] <= 1 {
						if err := renameHitProperty(state, id, newID); err != nil {
							return nil, err
						}
						segment.SkillProID = newID
						changed[id] = newID
						seenContext[newID] = context
						continue
					}
					if err := cloneVariantHitProperty(state, id, newID, key, stage, edit.Condition, *segment); err != nil {
						return nil, err
					}
					segment.SkillProID = newID
					changedContext[context] = newID
					seenContext[newID] = context
				}
			}
		}
	}
	return changed, nil
}

// validateVariantCurrentIDs 防止基线没有、但当前应用包中已被其他动作占用的号被覆盖。
func rewriteVariantRulePropertyIDs(rules []Rule, replacements map[string]string) []Rule {
	if len(replacements) == 0 {
		return rules
	}
	for index := range rules {
		if len(rules[index].Properties) == 0 {
			continue
		}
		updated := map[string]map[string]float64{}
		for id, values := range rules[index].Properties {
			if replacement := replacements[id]; replacement != "" {
				id = replacement
			}
			updated[id] = values
		}
		rules[index].Properties = updated
	}
	return rules
}

func appendVariantPropertyIDs(weapon *Weapon, weaponKey string, variants map[string]map[int][]VariantEdit) {
	if weapon == nil || len(variants[weaponKey]) == 0 {
		return
	}
	for index := range weapon.Stages {
		stage := weapon.Stages[index].State
		rawStage, err := strconv.Atoi(strings.TrimSpace(stage))
		if err != nil {
			continue
		}
		for _, edits := range variants[weaponKey][rawStage] {
			if edits.Remove {
				continue
			}
			for _, segment := range edits.Segments {
				id := strings.TrimSpace(segment.SkillProID)
				if id == "" || includes(weapon.Stages[index].PropertyIDs, id) {
					continue
				}
				weapon.Stages[index].PropertyIDs = append(weapon.Stages[index].PropertyIDs, id)
			}
		}
	}
}

// render may replace a branch source ID with its pinned 900... clone. It is
// still owned only when that clone is referenced exclusively by this branch.
func variantRenderedBranchOwnsID(state *weaponState, key string, stage int, id string, own, usage map[string]int) bool {
	if state == nil {
		return false
	}
	clone := state.PropertyClones[key][cloneKey(ruleStageOf(stage), id)]
	return clone != "" && own[clone] > 0 && usage[clone] == own[clone] && usage[id] == 0
}

func validateVariantCurrentIDs(current *archive, key string, edits map[int][]VariantEdit, states ...*weaponState) error {
	alloc, err := newVariantAllocator(current)
	if err != nil {
		return err
	}
	for stage, branches := range edits {
		for _, edit := range branches {
			if edit.Remove {
				continue
			}
			own, err := variantCurrentBranchUsage(current, key, stage, edit.Condition)
			if err != nil {
				return err
			}
			for _, segment := range edit.Segments {
				id := strings.TrimSpace(segment.SkillProID)
				if id == "" || strings.TrimSpace(segment.TemplateSkillProID) == "" || !alloc.isSelfMade(id) {
					continue
				}
				owned := own[id] > 0 && alloc.usage[id] == own[id]
				if !owned && len(states) > 0 {
					owned = variantRenderedBranchOwnsID(states[0], key, stage, id, own, alloc.usage)
				}
				if alloc.occupied[id] && !owned {
					return fmt.Errorf("预分配的命中属性 %s 已被当前应用包中的其他动作占用", id)
				}
			}
		}
	}
	return nil
}

// variantItemactHeader 返回 itemact.txt 的状态列，避免依赖测试文件中的辅助函数。
func variantItemactHeader(text string) []string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) == 0 {
		return nil
	}
	return strings.Split(lines[0], "\t")
}

// variantBranchExists reports whether an archive contains the conditional block
// selected by one weapon action state. A branch is optional: absence is a valid
// author state, while a remove request may target a branch left in the current
// package by an older applied revision.
func variantBranchExists(a *archive, key string, stage, condition int) (bool, error) {
	if a == nil {
		return false, nil
	}
	actions, err := a.text("itemact.txt")
	if err != nil {
		return false, err
	}
	header := variantItemactHeader(actions)
	row := actionRowIndex(actions)[key]
	if row == nil {
		return false, nil
	}
	wantStage := strconv.Itoa(stage)
	wantCondition := strconv.Itoa(condition)
	for column, state := range header {
		if state != wantStage || column >= len(row) {
			continue
		}
		action := strings.TrimSpace(row[column])
		if action == "" || action == "0" {
			continue
		}
		file, err := a.animationWriteFile(action)
		if err != nil {
			return false, err
		}
		text, err := a.text(file)
		if err != nil {
			return false, err
		}
		parts := strings.Split(actionKey(action), "/")
		if len(parts) == 2 {
			if _, exists := currentConditionalBlock(text, parts[1], wantCondition); exists {
				return true, nil
			}
		}
	}
	return false, nil
}

// mergeVariantEdits 按状态和条件接管，删除新建草稿直接移除，原生分支保留删除标记。
// current 是可选的当前客户端归档，用于识别“当前包有、基线没有”的历史分支。
func mergeVariantEdits(base *archive, key string, saved, updates map[int][]VariantEdit, current ...*archive) (map[int][]VariantEdit, error) {
	out := map[int][]VariantEdit{}
	for stage, edits := range saved {
		out[stage] = append([]VariantEdit(nil), edits...)
	}
	for stage, edits := range updates {
		for _, edit := range edits {
			list := out[stage]
			found := -1
			for index, previous := range list {
				if previous.Condition == edit.Condition {
					found = index
					break
				}
			}
			if edit.Remove {
				baseExists, err := variantBranchExists(base, key, stage, edit.Condition)
				if err != nil {
					return nil, err
				}
				currentExists := false
				if !baseExists && len(current) > 0 {
					currentExists, err = variantBranchExists(current[0], key, stage, edit.Condition)
					if err != nil {
						return nil, err
					}
				}
				if !baseExists && currentExists {
					// The candidate is rebuilt from base and already excludes this
					// historical branch. Remove any matching saved marker, then stop.
					if found >= 0 {
						out[stage] = append(list[:found], list[found+1:]...)
					}
					continue
				}
				if !baseExists {
					// 删除是幂等的：基线、当前包和作者态都没有该分支时，
					// 说明目标已经处于删除后的状态，直接忽略 marker。
					if found >= 0 {
						out[stage] = append(list[:found], list[found+1:]...)
					}
					continue
				}
			}
			if found >= 0 {
				list[found] = edit
			} else {
				list = append(list, edit)
			}
			out[stage] = list
		}
		if len(out[stage]) == 0 {
			delete(out, stage)
		}
	}
	return out, nil
}

// validateVariants 校验分支编辑：状态必须属于该武器、有动作号；条件号必须 > 0。
func validateVariants(a *archive, weaponKey string, edits map[int][]VariantEdit) error {
	if len(edits) == 0 {
		return nil
	}
	actionText, err := a.text("itemact.txt")
	if err != nil {
		return err
	}
	lines := strings.Split(actionText, "\n")
	if len(lines) == 0 {
		return fmt.Errorf("动作表为空")
	}
	columns := map[string]int{}
	for index, name := range strings.Split(strings.TrimSuffix(lines[0], "\r"), "\t") {
		columns[name] = index
	}
	row := actionRowIndex(actionText)[weaponKey]
	if row == nil {
		return fmt.Errorf("武器 %s 不在本客户端的动作表中", weaponKey)
	}
	stages := make([]int, 0, len(edits))
	for stage := range edits {
		stages = append(stages, stage)
	}
	sort.Ints(stages)
	for _, stage := range stages {
		column, ok := columns[strconv.Itoa(stage)]
		if !ok || column >= len(row) {
			return fmt.Errorf("状态 %d 不属于武器 %s", stage, weaponKey)
		}
		if action := strings.TrimSpace(row[column]); action == "" || action == "0" {
			return fmt.Errorf("状态 %d 没有动作，无法编辑分支形态", stage)
		}
		seen := map[int]bool{}
		for _, edit := range edits[stage] {
			if edit.Condition <= 0 {
				return fmt.Errorf("状态 %d 的分支缺少触发条件（状态号需 > 0）", stage)
			}
			if seen[edit.Condition] {
				return fmt.Errorf("状态 %d 的触发条件 %d 重复", stage, edit.Condition)
			}
			seen[edit.Condition] = true
			if edit.Remove {
				continue
			}
			for _, segment := range edit.Segments {
				if segment.Name == "" {
					return fmt.Errorf("状态 %d 的分支段缺少动画名", stage)
				}
				if segment.End < segment.Start {
					return fmt.Errorf("状态 %d 的分支段帧序倒置（%d > %d）", stage, segment.Start, segment.End)
				}
			}
		}
	}
	return nil
}

// variantFiles 是分支编辑会写到的动画文件，供 checkAllowedWrites 放行。
func variantFiles(source *archive, state *weaponState) map[string]bool {
	files := map[string]bool{}
	if len(state.Variants) == 0 {
		return files
	}
	actionText, err := source.text("itemact.txt")
	if err != nil {
		return files
	}
	lines := strings.Split(actionText, "\n")
	if len(lines) == 0 {
		return files
	}
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
	for weaponKey, perStage := range state.Variants {
		row := rows[weaponKey]
		if row == nil {
			continue
		}
		for stage := range perStage {
			column, ok := columns[strconv.Itoa(stage)]
			if !ok || column >= len(row) {
				continue
			}
			action := strings.TrimSpace(row[column])
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

// ---------------------------------------------------------------------------
// skillproid 分配
// ---------------------------------------------------------------------------

// variantAllocator 按「归档已有 SkillProId ∪ 本次已分配」判占用，从
// variantSkillProPrefix 起取最小空闲号；并记住每个号的 PropertyItem 原文，供克隆。
//
// 它还负责**幂等与回收**：编辑已有分支时旧号原样复用（不换号），真正被移除、
// 且全库再无人引用的自建号从 skillproperty.xml 删掉 —— 否则每保存一次就多一批
// 孤儿条目，表只涨不消。
type variantAllocator struct {
	occupied map[string]bool
	reserved map[string]bool   // 本轮显式最终号，自动分配必须跳过
	sources  map[string]string // SkillProId -> PropertyItem 原文
	template string            // 兜底模板（首条 PropertyItem）
	nextID   int
	// usage[skillproid] = 操作前全库 animation 块里的引用次数。回收前用它确认
	// 「这个号只被我们要删的那一块用着」。
	usage map[string]int
	// added 记录本次新建的 PropertyItem 文本，落盘时统一追加。
	added []string
	// updates[skillproid] = 复用的号要覆写的伤害。
	updates map[string]float64
	// retired 是本次要删掉的号（不再被任何块引用）。
	retired map[string]bool
	// Projection mode may reassign a conflicting draft id for display only.
	// Strict apply keeps the explicit id and rejects the collision.
	allowExplicitReassign bool
}

// isSelfMade 判断一个号是不是本逻辑发出去的分支号（910 段）。原生号一律不碰。
func (v *variantAllocator) isSelfMade(id string) bool {
	number, err := strconv.Atoi(strings.TrimSpace(id))
	return err == nil && number >= variantSkillProPrefix && number < variantSkillProLimit
}

// retire 标记自建号待回收，applyVariants 根据最终全库引用再次确认。
func (v *variantAllocator) retire(id string) {
	id = strings.TrimSpace(id)
	if id == "" || !v.isSelfMade(id) {
		return
	}
	v.retired[id] = true
}

// record 落定一批段的分配结果：新号进 added，复用的号取消回收并登记伤害覆写。
func (v *variantAllocator) record(props []variantProp) error {
	for _, prop := range props {
		delete(v.retired, prop.newID)
		if prop.reuse {
			if prop.hasOverride {
				v.updates[prop.newID] = prop.damage
			}
			continue
		}
		source := v.sources[prop.templateID]
		if source == "" {
			source = v.template
		}
		if source == "" {
			return fmt.Errorf("技能属性表里没有可复制的模板")
		}
		node, err := parseXML(source)
		if err != nil {
			return err
		}
		node.set("SkillProId", prop.newID)
		if prop.hasOverride {
			node.set("SkillDamage", strconv.FormatFloat(prop.damage, 'f', -1, 64))
		}
		encoded, err := node.serialize()
		if err != nil {
			return err
		}
		v.added = append(v.added, encoded)
	}
	return nil
}

func newVariantAllocator(a *archive) (*variantAllocator, error) {
	root, err := a.xml("skillproperty.xml")
	if err != nil {
		return nil, err
	}
	occupied := map[string]bool{}
	sources := map[string]string{}
	template := ""
	for _, node := range root.children {
		if node.comment {
			continue
		}
		id := strings.TrimSpace(node.get("SkillProId"))
		if id == "" {
			continue
		}
		encoded, err := node.serialize()
		if err != nil {
			continue
		}
		occupied[id] = true
		if sources[id] == "" {
			// 与 inspect 一致，原生重复注册取第一条模板。
			sources[id] = encoded
		}
		if template == "" {
			template = encoded
		}
	}
	// 独立扫全库，包括未注册到 itemact 的组和 XML 坏块里的悬空引用。
	usage, err := variantArchiveUsage(a)
	if err != nil {
		return nil, err
	}
	for id := range usage {
		occupied[id] = true
	}
	return &variantAllocator{
		occupied: occupied,
		sources:  sources,
		template: template,
		nextID:   variantSkillProPrefix,
		usage:    usage,
		updates:  map[string]float64{},
		retired:  map[string]bool{},
	}, nil
}

func (v *variantAllocator) next() (string, error) {
	for v.nextID < variantSkillProLimit {
		candidate := strconv.Itoa(v.nextID)
		v.nextID++
		if !v.occupied[candidate] && !v.reserved[candidate] {
			v.occupied[candidate] = true
			return candidate, nil
		}
	}
	return "", fmt.Errorf("分支 skillproid 编号空间不足（%d-%d 已用尽）", variantSkillProPrefix, variantSkillProLimit-1)
}

// 不依赖完整 XML 解析，原生坏块中的引用也要占号；先排除注释。
var variantXMLCommentPattern = regexp.MustCompile(`(?s)<!--.*?-->`)
var variantAnmReferencePattern = regexp.MustCompile(`<[^!?/][^>]*\bskillproid\s*=\s*["']([^"']+)["']`)

func variantAnimationUsage(text string) map[string]int {
	usage := map[string]int{}
	for _, match := range variantAnmReferencePattern.FindAllStringSubmatch(variantXMLCommentPattern.ReplaceAllString(text, ""), -1) {
		if id := strings.TrimSpace(match[1]); id != "" {
			usage[id]++
		}
	}
	return usage
}

func variantArchiveUsage(a *archive) (map[string]int, error) {
	usage := map[string]int{}
	for name := range a.entries {
		if !strings.HasPrefix(name, "animation/") || !strings.HasSuffix(name, ".xml") {
			continue
		}
		text, err := a.text(name)
		if err != nil {
			return nil, err
		}
		for id, count := range variantAnimationUsage(text) {
			usage[id] += count
		}
	}
	return usage, nil
}

func variantOccupiedIDs(state *weaponState, archives ...*archive) ([]string, error) {
	taken := map[string]bool{}
	for _, a := range archives {
		alloc, err := newVariantAllocator(a)
		if err != nil {
			return nil, err
		}
		for id := range alloc.occupied {
			taken[id] = true
		}
	}
	for _, stages := range state.Variants {
		for _, edits := range stages {
			for _, edit := range edits {
				if edit.Remove {
					continue
				}
				for _, segment := range edit.Segments {
					if id := strings.TrimSpace(segment.SkillProID); id != "" && strings.TrimSpace(segment.TemplateSkillProID) != "" {
						taken[id] = true
					}
				}
			}
		}
	}
	for id := range state.ExtraProperties {
		taken[strings.TrimSpace(id)] = true
	}
	for _, entries := range state.PropertyClones {
		for _, id := range entries {
			if id = strings.TrimSpace(id); id != "" {
				taken[id] = true
			}
		}
	}
	ids := make([]string, 0, len(taken))
	for id := range taken {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

// variantPropertyPattern 匹配一条顶层 <PropertyItem>：多数是自闭合，少数带子节点。
// 复制模板必须连闭合标签一起取，否则带子节点的那 447 条会被截断。
var variantPropertyPattern = regexp.MustCompile(`(?s)<PropertyItem\b[^>]*?(?:/>|>.*?</PropertyItem\s*>)`)

// variantSkillProIDPattern 取条件里的 SkillProId。原生两种写法都有（`SkillProId="x"`
// 与 `SkillProId = "x"`），等号两侧必须容空格。
var variantSkillProIDPattern = regexp.MustCompile(`SkillProId\s*=\s*"([^"]*)"`)
var variantSkillDamagePattern = regexp.MustCompile(`SkillDamage\s*=\s*"[^"]*"`)

// apply 把本次的分配结果落到 skillproperty.xml 文本上：先按区间删/改现有条目
// （从后往前，避免位移），再把新条目追加到 </SkillProperty> 之前。
//
// **只动 910 段的自建号**；原生条目一个字节都不碰，即便它出现在更新/回收列表里。
func (v *variantAllocator) apply(properties string) (string, error) {
	if len(v.added) == 0 && len(v.updates) == 0 && len(v.retired) == 0 {
		return properties, nil
	}
	type spanEdit struct {
		start, end int
		text       string
	}
	edits := []spanEdit{}
	for _, loc := range variantPropertyPattern.FindAllStringIndex(properties, -1) {
		block := properties[loc[0]:loc[1]]
		match := variantSkillProIDPattern.FindStringSubmatch(block)
		if match == nil {
			continue
		}
		id := strings.TrimSpace(match[1])
		if !v.isSelfMade(id) {
			continue
		}
		if v.retired[id] {
			edits = append(edits, spanEdit{loc[0], loc[1], ""})
			continue
		}
		if damage, ok := v.updates[id]; ok {
			edits = append(edits, spanEdit{loc[0], loc[1], rewriteSkillDamage(block, damage)})
		}
	}
	for index := len(edits) - 1; index >= 0; index-- {
		edit := edits[index]
		properties = properties[:edit.start] + edit.text + properties[edit.end:]
	}
	if len(v.added) > 0 {
		if strings.Count(properties, "</SkillProperty>") != 1 {
			return properties, fmt.Errorf("技能属性表结构错误")
		}
		properties = strings.Replace(properties, "</SkillProperty>",
			"\n"+strings.Join(v.added, "\n")+"\n</SkillProperty>", 1)
	}
	if _, err := parseXML(properties); err != nil {
		return properties, err
	}
	return properties, nil
}

// rewriteSkillDamage 把一条 <PropertyItem> 的 SkillDamage 改成 value；原本没有这个
// 属性就在起始标签后补一个。只做属性级替换，保留原文的缩进和换行。
func rewriteSkillDamage(block string, value float64) string {
	text := strconv.FormatFloat(value, 'f', -1, 64)
	if variantSkillDamagePattern.MatchString(block) {
		return variantSkillDamagePattern.ReplaceAllString(block, `SkillDamage="`+text+`"`)
	}
	// 找起始标签的结束 '>'。不能直接 Index(block, ">")：属性值里可能出现 '>'。
	// 简单扫一遍并跳过引号内字符即可（属性值一定在引号里）。
	cut, quote := -1, byte(0)
	for index := 0; index < len(block); index++ {
		switch ch := block[index]; {
		case quote != 0:
			if ch == quote {
				quote = 0
			}
		case ch == '"' || ch == '\'':
			quote = ch
		case ch == '>':
			cut = index
		}
		if cut >= 0 {
			break
		}
	}
	if cut < 0 {
		return block
	}
	insert := ` SkillDamage="` + text + `"`
	if cut > 0 && block[cut-1] == '/' {
		// 自闭合：把 / 挪到新属性之后，保成仍然自闭合。
		return block[:cut-1] + insert + ` /` + block[cut:]
	}
	return block[:cut] + insert + block[cut:]
}
