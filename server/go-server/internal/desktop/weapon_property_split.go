package desktop

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// 自建武器的技能属性分身（SkillProId 隔离）。
//
// 背景：克隆出来的武器通过 itemact.txt 逐字复制供体的动作行，于是它的 2xxx
// 段直接沿用了供体的 skillproid —— 一个 SkillProId 同时被供体和克隆体引用。
// skillproperty.xml 里那一份 PropertyItem 是共享的，改命中伤害、挂状态都会
// 同时落到两把武器上。2026-09-30 实测：253350 完全克隆自 253504，它的 2xxx
// 段共 20 项 skillproid，20 项全部与供体共享；原生克隆对 253004/253916 也有
// 同样的问题（块 id 已分身，skillproid 只独立了一部分）。
//
// 命名规范（实测 1xxx 段共享率 98.6%，2xxx 段 44.7%；原生克隆对 253107/253946
// 的 2xxx 段 100% 独立）：
//   - 1xxx / 3xxx / 5xxx：通用动作段（普攻、受击、跳跃、死亡、拾取…），
//     复用即可，分身它们只会白白膨胀归档。
//   - 2xxx：攻击/技能段，**整段克隆，绝不共享**。动作块和 skillproid 一起
//     分身，自建武器从此对外零耦合。这是本文件整套逻辑存在的理由。
//
// 换段判断只看状态号首位，不看它挂在哪个动作文件上。

// propertySplitPrefix 是分身号的起始值，与 render 的克隆号同域（900000000
// 起）。两边共用「归档里已有的 SkillProId」作占用集，因此永不撞号。
const propertySplitPrefix = 900000000

// publicActionThreshold 是「公共动作段」的判定阈值：2xxx 段里一个动作若被
// 这么多把及以上武器共用，就当作公共资源，不克隆。
//
// 号段规则本身只看首位（1xxx/3xxx/5xxx 复用、2xxx 克隆），但 2xxx 段里混着
// 一批实际属于公共资源的动作：空手 `2001002` 被 64 把武器共用、`2006001` 43 把、
// `2001014` 42 把。它们是「攻击状态下的通用动作」，逐把复制只会让克隆武器
// 平白多出几个块副本。实测 2xxx 段里被多把武器共用的动作共 597 个，数量分布
// 在 64/43/42/41/32/24/13/13/10/10/9/…，取 10 正好切开「公共段」与「武器专属
// 段」两个族群。
const publicActionThreshold = 10

// stageNeedsSplit 判断一个状态是否属于「必须独立」的 2xxx 攻击段。
func stageNeedsSplit(state string) bool {
	return strings.HasPrefix(strings.TrimSpace(state), "2")
}

// skillPropertySurvey 是分身决策所需的只读快照。
type skillPropertySurvey struct {
	// weaponActions[武器id] = {动作key: [该段引用的 skillproid...]}
	weaponActions map[string]map[string][]string
	// users[skillproid] = {武器id...}，只统计 2xxx 段。
	users map[string]map[string]bool
	// blockUsers[动作key] = {武器id...}，动作块自身的归属。
	blockUsers map[string]map[string]bool
	// blockIDs 是动画文件里真实存在的全部块键（含 itemact 没引用的）。
	// 分块选号必须避开它们：原生就有一批 1~3 位的预留块（如 2001/999），
	// 只看 itemact 会撞号，产生两个同 id 的 <AnmDesc>。
	blockIDs map[string]bool
	// publicActions 是被 publicActionThreshold 判定为公共资源的动作块键。
	publicActions map[string]bool
	// allSlots 是全库 (武器, 状态, 动作) 三元组，用于算占用集。
	allSlots []actionSlot
	// properties[skillproid] = 该号在 skillproperty.xml 里的节点（通常 1 个）
	properties map[string][]*xmlNode
}

// actionSlot 是 itemact 里一个非空单元格。
type actionSlot struct {
	weapon string
	state  string
	action string
}

// surveyForWeapons 只扫自建武器实际用到的动作文件。
//
// 不遍历全库动画是刻意的：原生 animation/*.xml 里有 3 个文件本身 XML 不规范
// （100201.xml 少一个 </AnmDesc>、2002up.xml 注释写成 <!-、2011.xml 的 <Param>
// 被 </AnmDesc> 关闭）。全量解析会直接失败，而现有 inspect() 只在用户选中对应
// 武器时才碰这些文件，所以从没暴露。这里按需加载，顺手绕开它们。
//
// 共享判定用两把尺子并集：动作块被别的武器/状态引用（itemact 纯文本），或
// skillproid 被别的 2xxx 段引用。任一条命中就分身。
func surveyForWeapons(a *archive, wanted map[string]Blueprint) (*skillPropertySurvey, error) {
	actionTable, err := a.text("itemact.txt")
	if err != nil {
		return nil, err
	}
	lines := strings.Split(actionTable, "\n")
	if len(lines) == 0 {
		return nil, fmt.Errorf("动作表为空")
	}
	header := strings.Split(strings.TrimSuffix(lines[0], "\r"), "\t")

	// 一遍扫出：每个 (武器,状态) -> 动作号；每个动作号 -> 引用它的武器数。
	slots := []actionSlot{}
	actionUsers := map[string]map[string]bool{}
	for _, line := range lines[1:] {
		cols := strings.Split(strings.TrimSuffix(line, "\r"), "\t")
		if len(cols) < 3 || cols[0] == "" {
			continue
		}
		weaponKey := cols[0]
		for j, state := range header {
			if j < 2 || j >= len(cols) {
				continue
			}
			action := strings.TrimSpace(cols[j])
			if action == "" || action == "0" {
				continue
			}
			slots = append(slots, actionSlot{weaponKey, state, action})
			if actionUsers[action] == nil {
				actionUsers[action] = map[string]bool{}
			}
			actionUsers[action][weaponKey] = true
		}
	}

	// 只解析自建武器用到的动作文件。
	needFiles := map[string]bool{}
	for _, s := range slots {
		if _, ok := wanted[s.weapon]; !ok {
			continue
		}
		if !stageNeedsSplit(s.state) {
			continue
		}
		if key := actionKey(s.action); key != "" {
			needFiles["animation/"+s.action[:4]+".xml"] = true
		}
	}
	blockRefs := map[string][]string{}
	blockIDs := map[string]bool{}
	files := make([]string, 0, len(needFiles))
	for file := range needFiles {
		files = append(files, file)
	}
	sort.Strings(files)
	for _, file := range files {
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
				// 原生脏数据：跳过该块，不让它拖垮整次分身。
				continue
			}
			id := strings.TrimSpace(node.get("id"))
			if id == "" {
				continue
			}
			key := prefix + "/" + id
			blockIDs[key] = true
			refs := []string{}
			node.walk(func(child *xmlNode) {
				if child.tag == "Anm" {
					if sp := strings.TrimSpace(child.get("skillproid")); sp != "" {
						refs = append(refs, sp)
					}
				}
			})
			blockRefs[key] = refs
		}
	}

	survey := &skillPropertySurvey{
		weaponActions: map[string]map[string][]string{},
		users:         map[string]map[string]bool{},
		blockUsers:    map[string]map[string]bool{},
		blockIDs:      blockIDs,
		allSlots:      slots,
		properties:    map[string][]*xmlNode{},
	}
	// 块归属：只要 itemact 里出现过就要统计，判断「这块是不是只有我在用」。
	publicActions := map[string]bool{}
	for _, s := range slots {
		key := actionKey(s.action)
		if key == "" {
			continue
		}
		if survey.blockUsers[key] == nil {
			survey.blockUsers[key] = map[string]bool{}
		}
		for other := range actionUsers[s.action] {
			survey.blockUsers[key][other] = true
		}
		if len(actionUsers[s.action]) >= publicActionThreshold {
			publicActions[key] = true
		}
	}
	survey.publicActions = publicActions
	// 自建武器的 2xxx 段：记录待分身清单（其余段一律不碰）。
	for _, s := range slots {
		if _, ok := wanted[s.weapon]; !ok {
			continue
		}
		if !stageNeedsSplit(s.state) {
			continue
		}
		key := actionKey(s.action)
		if key == "" {
			continue
		}
		refs := blockRefs[key]
		if len(refs) == 0 {
			continue
		}
		if survey.weaponActions[s.weapon] == nil {
			survey.weaponActions[s.weapon] = map[string][]string{}
		}
		survey.weaponActions[s.weapon][key] = append(survey.weaponActions[s.weapon][key], refs...)
		for _, ref := range refs {
			if survey.users[ref] == nil {
				survey.users[ref] = map[string]bool{}
			}
			survey.users[ref][s.weapon] = true
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
		if id == "" {
			continue
		}
		survey.properties[id] = append(survey.properties[id], node)
	}
	return survey, nil
}

// actionsToSplit 返回某武器各动作块上要去重的 skillproid 列表：一个块可能被
// 两条状态同时引用，只该分身一次。
func (s *skillPropertySurvey) actionsToSplit(weaponKey string) map[string][]string {
	out := map[string][]string{}
	for action, refs := range s.weaponActions[weaponKey] {
		seen := map[string]bool{}
		unique := []string{}
		for _, ref := range refs {
			if seen[ref] {
				continue
			}
			seen[ref] = true
			unique = append(unique, ref)
		}
		out[action] = unique
	}
	return out
}

// ownedExclusively 判断某个动作块上的 skillproid 是否已经分身过。
//
// 判据：块内的 skillproid 落在分身号段（>= propertySplitPrefix）。这个号段
// 只有本逻辑发过，因此是最可靠的幂等标记 —— 基线重采后归档里那条记录还在，
// 判据仍成立，重复渲染不会漂号。
//
// 不用「块是否被别的武器引用」来判断：那需要跨全部动画文件统计引用者，而
// 原生数据里有 3 个文件 XML 不规范、无法安全全量解析。号段判据零成本且绝对。
func (s *skillPropertySurvey) ownedExclusively(weaponKey, action string) bool {
	refs := s.weaponActions[weaponKey][action]
	if len(refs) == 0 {
		return false
	}
	for _, ref := range refs {
		number, err := strconv.Atoi(ref)
		if err != nil || number < propertySplitPrefix {
			return false
		}
	}
	return true
}

// splitClonedProperties 给自建武器的 2xxx 段做整段克隆。
//
// 规则：2xxx 段绝不共享 —— 动作块和 skillproid 一起分身。
//   - 动作块已被供体以外的武器引用（含块 id 与供体相同、或块 id 独立但仍被
//     别人挂着）：复制出一份新块，itemact 指向新块 id。
//   - skillproid：一律复制一份新号，块内 <Anm skillproid> 改指新号。
//
// 非 2xxx 段一律不碰。返回新归档；无事可做时原样返回 base。
func splitClonedProperties(base *archive, state *weaponState) (*archive, error) {
	if len(state.Created) == 0 {
		return base, nil
	}
	survey, err := surveyForWeapons(base, state.Created)
	if err != nil {
		return nil, err
	}

	// skillproid 占用集：归档已有的 + 本次分配的。
	occupied := map[string]bool{}
	for id := range survey.properties {
		occupied[id] = true
	}
	nextID := propertySplitPrefix
	allocate := func() (string, error) {
		for nextID < propertySplitPrefix+1000000 {
			candidate := strconv.Itoa(nextID)
			nextID++
			if !occupied[candidate] {
				occupied[candidate] = true
				return candidate, nil
			}
		}
		return "", fmt.Errorf("技能属性分身编号空间不足")
	}

	// 动作块占用集：XML 里真实存在的块 + itemact 引用过的块 + 本次分配的。
	reservedBlocks := map[string]bool{}
	for key := range survey.blockIDs {
		reservedBlocks[key] = true
	}
	for key := range survey.blockUsers {
		reservedBlocks[key] = true
	}
	for _, s := range survey.allSlots {
		if key := actionKey(s.action); key != "" {
			reservedBlocks[key] = true
		}
	}

	// 块分身必须连带它的「出招属性」。客户端在播动画之前会先按 itemact 里的
	// 完整动作号查 skillproperty.xml（原生特攻门 gfxz 9EECC0），查不到就直接
	// 取消出招 —— 表现就是按了键没反应。所以选块号这件事统一交给
	// actionCloneProperties：它选号时顺带把「新号要从旧号复制属性」记下来，
	// 落盘时统一补上。自造选号逻辑会漏掉这一步。
	entryProperties, err := newActionCloneProperties(base)
	if err != nil {
		return nil, err
	}

	// ---- 规划阶段：块分身 + skillproid 分身 ----
	actionTable, err := base.text("itemact.txt")
	if err != nil {
		return nil, err
	}
	actionLines := strings.Split(actionTable, "\n")
	if len(actionLines) == 0 {
		return nil, fmt.Errorf("动作表为空")
	}
	header := strings.Split(strings.TrimSuffix(actionLines[0], "\r"), "\t")
	tableChanged := false

	type stagePlan struct {
		action    string     // 原动作键 前缀/块号
		newAction string     // 分身后的动作号（块没动时等于 action 的原动作号）
		refs      []splitRef // skillproid 旧->新
	}
	plans := []stagePlan{}
	weaponKeys := make([]string, 0, len(survey.weaponActions))
	for key := range survey.weaponActions {
		weaponKeys = append(weaponKeys, key)
	}
	sort.Strings(weaponKeys)
	for _, key := range weaponKeys {
		actions := make([]string, 0, len(survey.weaponActions[key]))
		for action := range survey.weaponActions[key] {
			actions = append(actions, action)
		}
		sort.Strings(actions)
		for _, action := range actions {
			parts := strings.SplitN(action, "/", 2)
			if len(parts) != 2 {
				return nil, fmt.Errorf("动作键异常：%s", action)
			}
			prefix := parts[0]

			// 公共动作段（空手、通用起手…）不克隆：它本就属于所有武器，
			// 逐把复制只会平白多出块副本。
			if survey.publicActions[action] {
				continue
			}

			// 已经分过身：块内的 skillproid 落在分身号段，说明上一轮已经
			// 隔离过。这一条是幂等性的依据 —— 基线重采后归档里已有分身记录，
			// 若这里再分一次，每次改动都会换号，连招规则按号匹配会全空转。
			if survey.ownedExclusively(key, action) {
				continue
			}

			// 块被别的武器用时，复制出新块，**只把自建武器那一格**改指新块；
			// 供体的行保持不动。
			newActionNumber := ""
			if len(survey.blockUsers[action]) > 1 {
				// actionCloneProperties 要的是完整动作号（前缀+3 位块号）。
				cloneID := entryProperties.allocate(prefix+parts[1], reservedBlocks)
				if cloneID == 0 {
					return nil, fmt.Errorf("%s 独立动作编号空间不足", prefix)
				}
				newActionNumber = prefix + fmt.Sprintf("%03d", cloneID)
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
					for j := 2; j < len(cols) && j < len(header); j++ {
						if actionKey(strings.TrimSpace(cols[j])) == action {
							cols[j] = newActionNumber
							replaced = true
						}
					}
					actionLines[i+1] = strings.Join(cols, "\t") + ending
				}
				if !replaced {
					return nil, fmt.Errorf("动作表里找不到 %s 的 %s", key, action)
				}
				tableChanged = true
			}

			// skillproid 一律分身。原生动画里存在悬空引用（如 2001/435 的
			// <Anm skillproid="60011780">，skillproperty.xml 里根本没有这条），
			// 这类号无处可克隆，跳过即可 —— 报错会让整次应用失败。
			refs := []splitRef{}
			for _, oldID := range survey.actionsToSplit(key)[action] {
				if len(survey.properties[oldID]) == 0 {
					continue
				}
				newID, err := allocate()
				if err != nil {
					return nil, err
				}
				refs = append(refs, splitRef{oldID, newID})
			}
			if len(refs) == 0 && newActionNumber == "" {
				continue
			}
			plans = append(plans, stagePlan{action: action, newAction: newActionNumber, refs: refs})
		}
	}
	if len(plans) == 0 {
		return base, nil
	}

	// ---- 写入阶段 1：skillproperty.xml 追加分身记录 ----
	properties, err := base.text("skillproperty.xml")
	if err != nil {
		return nil, err
	}
	added := []string{}
	for _, p := range plans {
		for _, ref := range p.refs {
			source := survey.properties[ref.oldID]
			if len(source) == 0 {
				return nil, fmt.Errorf("技能属性 %s 不存在，无法分身", ref.oldID)
			}
			node := source[0].clone()
			node.set("SkillProId", ref.newID)
			encoded, err := node.serialize()
			if err != nil {
				return nil, err
			}
			added = append(added, encoded)
		}
	}
	if len(added) > 0 {
		ending := regexp.MustCompile(`</SkillProperty\s*>`)
		if len(ending.FindAllStringIndex(properties, -1)) != 1 {
			return nil, fmt.Errorf("技能属性表结构错误")
		}
		properties = ending.ReplaceAllStringFunc(properties, func(string) string {
			return "\n" + strings.Join(added, "\n") + "\n</SkillProperty>"
		})
		if _, err := parseXML(properties); err != nil {
			return nil, err
		}
	}

	// ---- 写入阶段 2：animation/*.xml 复制/改写块 ----
	fileBlocks := map[string]string{}    // 文件 -> 改后的文本
	blockPlan := map[string][]splitRef{} // 原动作键 -> skillproid 映射
	blockTarget := map[string]string{}   // 原动作键 -> 目标块号（分身时是新号）
	for _, p := range plans {
		blockPlan[p.action] = p.refs
		if p.newAction != "" {
			blockTarget[p.action] = p.newAction[4:]
		} else {
			blockTarget[p.action] = strings.SplitN(p.action, "/", 2)[1]
		}
	}
	actionKeys := make([]string, 0, len(blockPlan))
	for action := range blockPlan {
		actionKeys = append(actionKeys, action)
	}
	sort.Strings(actionKeys)
	for _, action := range actionKeys {
		parts := strings.SplitN(action, "/", 2)
		file := "animation/" + parts[0] + ".xml"
		animation, ok := fileBlocks[file]
		if !ok {
			animation, err = base.text(file)
			if err != nil {
				return nil, err
			}
		}
		target, ok := currentBlock(animation, parts[1])
		if !ok {
			return nil, fmt.Errorf("%s 缺少动作块 %s", file, parts[1])
		}
		rewritten, err := rewriteBlockSkillProIDs(target, blockPlan[action])
		if err != nil {
			return nil, err
		}
		newID := blockTarget[action]
		if newID != parts[1] {
			// 块分身：改 id 后追加到文件末尾，原块原样保留给供体。
			renamed, err := renameBlockID(rewritten, newID)
			if err != nil {
				return nil, err
			}
			ending := regexp.MustCompile(`</AnmInfo\s*>`)
			if len(ending.FindAllStringIndex(animation, -1)) != 1 {
				return nil, fmt.Errorf("%s 结构错误", file)
			}
			animation = ending.ReplaceAllStringFunc(animation, func(string) string {
				return "\n" + renamed + "\n</AnmInfo>"
			})
			fileBlocks[file] = animation
			continue
		}
		if rewritten == target {
			continue
		}
		if strings.Count(animation, target) != 1 {
			return nil, fmt.Errorf("%s 的动作块 %s 无法唯一替换", file, parts[1])
		}
		fileBlocks[file] = strings.Replace(animation, target, rewritten, 1)
	}

	// ---- 落盘 ----
	replacements := map[string][]byte{}
	propertiesBytes, err := encodeText(properties)
	if err != nil {
		return nil, err
	}
	replacements["skillproperty.xml"] = propertiesBytes
	if tableChanged {
		actionsBytes, err := encodeText(strings.Join(actionLines, "\n"))
		if err != nil {
			return nil, err
		}
		replacements["itemact.txt"] = actionsBytes
	}
	for file, animation := range fileBlocks {
		if _, err := parseXML(animation); err != nil {
			return nil, err
		}
		encoded, err := encodeText(animation)
		if err != nil {
			return nil, err
		}
		replacements[file] = encoded
	}
	// 交给 actionCloneProperties 落盘：它会先给块分身补上出招属性记录
	// （skillproperty.xml 里 2201504 -> 2201996 这种），再写归档。
	data, err := entryProperties.replace(base, replacements)
	if err != nil {
		return nil, err
	}
	return parseArchive(data)
}

// renameBlockID 把块的最外层 id 改成新值。
func renameBlockID(block, newID string) (string, error) {
	node, err := parseXML(block)
	if err != nil {
		return "", err
	}
	node.set("id", newID)
	return node.serialize()
}

// splitRef 是一次 skillproid 分身：旧号换新号。
type splitRef struct{ oldID, newID string }

// rewriteBlockSkillProIDs 把块里 <Anm skillproid="旧号"> 换成对应的新号。
func rewriteBlockSkillProIDs(block string, refs []splitRef) (string, error) {
	node, err := parseXML(block)
	if err != nil {
		return "", err
	}
	mapping := map[string]string{}
	for _, ref := range refs {
		mapping[ref.oldID] = ref.newID
	}
	changed := false
	node.walk(func(child *xmlNode) {
		if child.tag != "Anm" {
			return
		}
		if next, ok := mapping[strings.TrimSpace(child.get("skillproid"))]; ok {
			child.set("skillproid", next)
			changed = true
		}
	})
	if !changed {
		return block, nil
	}
	return node.serialize()
}
