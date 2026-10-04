package desktop

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// 招式特效与武器特效登记的编辑。
//
// 动作块里的特效是 <AnmDesc> 的直接子节点（和 <KeyProcess>/<Audio> 同级）：
//
//	<Effect frame="1" effectid="600120" bindtype="6" bindindex="0" />
//	<HitEffect startframe="0" endframe="99" effectid="100303b" bindtype="2" bindindex="29" />
//
// 编辑沿用既有安全做法：**共用动作块先克隆成该武器独占**再改写，原块一个字节不动，
// 注释区不动，编辑器不认识的属性（bindtype / bindindex / break…）随条目往返。
// 按状态给出的列表是**权威**的：某状态出现在编辑集里，它的特效就以该列表为准。

// StageEffect 是招式里的一条特效。
type StageEffect struct {
	Kind      string `json:"kind"` // "effect"（单帧）| "hit"（命中区间）
	EffectID  string `json:"effect_id"`
	Start     int    `json:"start"`
	End       int    `json:"end"`
	BindType  string `json:"bind_type,omitempty"`
	BindIndex string `json:"bind_index,omitempty"`
	Break     string `json:"break,omitempty"`
}

// EffectRow 是 a cteffect.xml 里 <WeaponEffect> 块的一行登记。
type EffectRow struct {
	EffectID string `json:"effect_id"`
	File     string `json:"file"`
}

var (
	// 两种写法都要认：客户端原始数据是自闭合的 <Effect ... />，而渲染过的块会
	// 变成成对闭合的 <Effect ...></Effect>（tab 还被写成 &#x9; 实体）。
	stageEffectNodePattern = regexp.MustCompile(`(?s)<(?:Effect|HitEffect)\b[^>]*(?:/>|>.*?</(?:Effect|HitEffect)\s*>)`)
	effectIdShape          = regexp.MustCompile(`^[A-Za-z0-9_]{1,16}$`)
)

func validateStageEffectValues(effects []StageEffect) error {
	for _, e := range effects {
		if !effectIdShape.MatchString(e.EffectID) {
			return fmt.Errorf("特效编号 %q 无效", e.EffectID)
		}
		if e.Kind != "effect" && e.Kind != "hit" {
			return fmt.Errorf("特效类型无效")
		}
		if e.Start < 0 || e.Start > 9999 || e.End < 0 || e.End > 9999 || (e.Kind == "hit" && e.End < e.Start) {
			return fmt.Errorf("特效 %s 的帧号无效（%d..%d）", e.EffectID, e.Start, e.End)
		}
		for _, value := range []string{e.BindType, e.BindIndex, e.Break} {
			if value == "" {
				continue
			}
			n, err := strconv.ParseUint(value, 10, 32)
			if err != nil || n > 2147483647 {
				return fmt.Errorf("特效 %s 的绑定或中断属性无效", e.EffectID)
			}
		}
	}
	return nil
}

// 只允许唯一无条件基础块；未知条件和重复条件不能安全克隆。
func stageEffectBase(blocks []block) (block, error) {
	var base block
	count := 0
	seen := map[string]bool{}
	for _, b := range blocks {
		if seen[b.condition] {
			return block{}, fmt.Errorf("动作基础块或条件分支不唯一")
		}
		seen[b.condition] = true
		if b.condition == "" {
			for _, child := range b.node.children {
				if child.tag == "Condition" {
					return block{}, fmt.Errorf("动作包含不支持的条件结构")
				}
			}
			base = b
			count++
		}
	}
	if count != 1 {
		return block{}, fmt.Errorf("动作缺少唯一无条件基础块")
	}
	return base, nil
}

// stageEffectText 渲染一条特效节点。
func stageEffectText(e StageEffect) string {
	extra := ""
	if strings.TrimSpace(e.BindType) != "" {
		extra += fmt.Sprintf(` bindtype="%s"`, strings.TrimSpace(e.BindType))
	}
	if strings.TrimSpace(e.BindIndex) != "" {
		extra += fmt.Sprintf(` bindindex="%s"`, strings.TrimSpace(e.BindIndex))
	}
	if strings.TrimSpace(e.Break) != "" {
		extra += fmt.Sprintf(` break="%s"`, strings.TrimSpace(e.Break))
	}
	if e.Kind == "hit" {
		return fmt.Sprintf(`<HitEffect startframe="%d" endframe="%d" effectid="%s"%s />`, e.Start, e.End, e.EffectID, extra)
	}
	return fmt.Sprintf(`<Effect frame="%d" effectid="%s"%s />`, e.Start, e.EffectID, extra)
}

// rewriteStageEffects 用给定列表替换动作块里的全部特效节点。原块没有特效就插入，
// 列表为空就删除。内容完全一致时不改动（幂等）。
func rewriteStageEffects(block string, effects []StageEffect) (string, bool, error) {
	if err := validateStageEffectValues(effects); err != nil {
		return block, false, err
	}
	existing := []string{}
	kept := strings.Builder{}
	last := 0
	for _, loc := range stageEffectNodePattern.FindAllStringIndex(block, -1) {
		if insideComment(block, loc[0]) {
			continue
		}
		existing = append(existing, strings.Join(strings.Fields(block[loc[0]:loc[1]]), " "))
		kept.WriteString(block[last:loc[0]])
		last = loc[1]
	}
	kept.WriteString(block[last:])
	wanted := make([]string, 0, len(effects))
	for _, e := range effects {
		wanted = append(wanted, strings.Join(strings.Fields(stageEffectText(e)), " "))
	}
	if len(existing) == len(wanted) {
		same := true
		for i := range existing {
			if existing[i] != wanted[i] {
				same = false
				break
			}
		}
		if same {
			return block, false, nil
		}
	}
	cleaned := kept.String()
	cut := strings.Index(cleaned, ">")
	if cut < 0 {
		return block, false, fmt.Errorf("动作块结构错误")
	}
	if len(effects) == 0 {
		return cleaned, true, nil
	}
	lines := make([]string, 0, len(effects))
	for _, e := range effects {
		lines = append(lines, "\t\t"+stageEffectText(e))
	}
	return cleaned[:cut+1] + "\n" + strings.Join(lines, "\n") + cleaned[cut+1:], true, nil
}

// stageEffectIndex 收集动作块与「动作被谁引用」，供特效编辑使用。
// 它不依赖 items，因为写入路径（prepareClient）里拿不到完整道具表。
func stageEffectIndex(a *archive) (map[string][]block, map[string]map[string]bool, error) {
	actionText, err := a.text("itemact.txt")
	if err != nil {
		return nil, nil, err
	}
	header, rows := itemactTable(actionText)
	owners := map[string]map[string]bool{}
	for _, row := range rows {
		for index, action := range row {
			if index < 2 || index >= len(header) {
				continue
			}
			action = strings.TrimSpace(action)
			if action == "" || action == "0" {
				continue
			}
			if owners[action] == nil {
				owners[action] = map[string]bool{}
			}
			owners[action][strings.TrimSpace(row[0])+":"+strings.TrimSpace(header[index])] = true
		}
	}
	index := map[string][]block{}
	prefixes := map[string]bool{}
	for action := range owners {
		if len(action) > 4 {
			prefixes[action[:4]] = true
		}
	}
	sorted := make([]string, 0, len(prefixes))
	for prefix := range prefixes {
		sorted = append(sorted, prefix)
	}
	sort.Strings(sorted)
	// 组内检索：动作号只写 4 位组前缀，块可能落在同前缀的 6 位子文件里
	// （见 animation_groups.go）。
	for _, prefix := range sorted {
		for number, found := range a.groupBlockIndex(prefix) {
			key := prefix + "/" + strconv.Itoa(number)
			for _, entry := range found {
				index[key] = append(index[key], entry.block)
			}
		}
	}
	return index, owners, nil
}

// itemactTable 把动作表切成表头与数据行。
func itemactTable(actionText string) ([]string, [][]string) {
	header := []string{}
	rows := [][]string{}
	for _, line := range strings.Split(strings.ReplaceAll(actionText, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		row := strings.Split(line, "\t")
		if len(header) == 0 {
			header = row
			continue
		}
		rows = append(rows, row)
	}
	return header, rows
}

// validateStageEffects 校验招式特效编辑：状态要在本武器的动作行里、特效编号合法、
// 帧号在范围内且 start<=end。
func validateStageEffects(a *archive, weaponKey string, edits map[int][]StageEffect) error {
	blockIndex, _, err := stageEffectIndex(a)
	if err != nil {
		return err
	}
	actionText, err := a.text("itemact.txt")
	if err != nil {
		return err
	}
	header, row := itemactHeaderAndRow(actionText, weaponKey)
	if row == nil {
		return fmt.Errorf("武器 %s 不在本客户端的动作表中", weaponKey)
	}
	columnOf := map[int]int{}
	for index, name := range header {
		if stage, err := strconv.Atoi(strings.TrimSpace(name)); err == nil {
			columnOf[stage] = index
		}
	}
	for stage, effects := range edits {
		column, exists := columnOf[stage]
		if !exists || column >= len(row) {
			return fmt.Errorf("状态 %d 不属于武器 %s", stage, weaponKey)
		}
		action := strings.TrimSpace(row[column])
		if action == "" || action == "0" {
			return fmt.Errorf("状态 %d 没有动作，无法编辑特效", stage)
		}
		if _, err := stageEffectBase(blockIndex[actionKey(action)]); err != nil {
			return fmt.Errorf("动作 %s 无法编辑特效：%w", action, err)
		}
		if err := validateStageEffectValues(effects); err != nil {
			return err
		}
	}
	return nil
}

// itemactHeaderAndRow 取动作表的表头与某武器那一行。
func itemactHeaderAndRow(actionText, weapon string) ([]string, []string) {
	header := []string{}
	for _, line := range strings.Split(strings.ReplaceAll(actionText, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		row := strings.Split(line, "\t")
		if len(header) == 0 {
			header = row
			continue
		}
		if strings.TrimSpace(row[0]) == weapon {
			return header, row
		}
	}
	return header, nil
}

// itemactAction 取某武器某状态列上的动作编号（stage 为 0 时只看武器是否存在）。
func itemactAction(actionText, weapon string, stage int) (string, bool) {
	header, row := itemactHeaderAndRow(actionText, weapon)
	if row == nil {
		return "", false
	}
	if stage == 0 {
		return "", true
	}
	for index, name := range header {
		if strings.TrimSpace(name) != strconv.Itoa(stage) || index >= len(row) {
			continue
		}
		action := strings.TrimSpace(row[index])
		if action == "" || action == "0" {
			return "", false
		}
		return action, true
	}
	return "", false
}

// applyStageEffects 把招式特效写进动画块；共用块先克隆成该武器独占，并按需改写
// 动作行指向新块——原动作块与其它武器完全不受影响。
func applyStageEffects(a *archive, state *weaponState, wanted ...map[int]bool) (*archive, error) {
	if len(state.StageEffects) == 0 {
		return a, nil
	}
	blockIndex, actionOwners, err := stageEffectIndex(a)
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
	lineIndex := map[string]int{}
	for index, line := range actionLines[1:] {
		cols := strings.Split(strings.TrimSuffix(line, "\r"), "\t")
		if len(cols) >= 1 {
			lineIndex[cols[0]] = index + 1
		}
	}
	entryProperties, err := newActionCloneProperties(a)
	if err != nil {
		return nil, err
	}
	reserved := map[string]bool{}
	for key := range blockIndex {
		reserved[key] = true
	}
	animations := map[string]string{}
	tableChanged := false

	weaponKeys := make([]string, 0, len(state.StageEffects))
	for key := range state.StageEffects {
		weaponKeys = append(weaponKeys, key)
	}
	sort.Strings(weaponKeys)
	for _, weaponKey := range weaponKeys {
		if len(wanted) > 0 && wanted[0] != nil {
			id, parseErr := strconv.Atoi(weaponKey)
			if parseErr != nil || !wanted[0][id] {
				continue
			}
		}
		line := lineIndex[weaponKey]
		if line == 0 {
			return nil, fmt.Errorf("武器 %s 不在动作表中", weaponKey)
		}
		ending := ""
		if strings.HasSuffix(actionLines[line], "\r") {
			ending = "\r"
		}
		row := strings.Split(strings.TrimSuffix(actionLines[line], "\r"), "\t")
		stages := make([]int, 0, len(state.StageEffects[weaponKey]))
		for stage := range state.StageEffects[weaponKey] {
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
				return nil, fmt.Errorf("状态 %d 没有动作，无法编辑特效", stage)
			}
			actionBlocks := blockIndex[actionKey(action)]
			chosen, err := stageEffectBase(actionBlocks)
			if err != nil {
				return nil, fmt.Errorf("动作 %s 无法编辑特效：%w", action, err)
			}
			file, err := a.animationWriteFile(action)
			if err != nil {
				return nil, err
			}
			animation, ok := animations[file]
			if !ok {
				animation, err = a.text(file)
				if err != nil {
					return nil, err
				}
			}
			target, err := locateEditableBlock(animation, chosen)
			if err != nil {
				return nil, err
			}
			rewritten, changed, err := rewriteStageEffects(target, state.StageEffects[weaponKey][stage])
			if err != nil {
				return nil, err
			}
			if !changed {
				animations[file] = animation
				continue
			}
			if len(actionOwners[action]) > 1 {
				// 共用块：分配一个空闲动作编号，克隆后单独改这一份。
				cloneID := entryProperties.allocate(action, reserved)
				if cloneID == 0 {
					return nil, fmt.Errorf("%s 独立动作编号空间不足", file)
				}
				// 刷新本轮已编辑的分支，避免从旧索引克隆回过期内容。
				variants := make([]block, len(actionBlocks))
				for i, variant := range actionBlocks {
					text, locateErr := locateEditableBlock(animation, variant)
					if locateErr != nil {
						return nil, fmt.Errorf("共享动作分支无法安全克隆：%w", locateErr)
					}
					variants[i] = variant
					variants[i].original = text
				}
				animation, err = cloneBlockVariants(animation, variants, "", rewritten, cloneID)
				if err != nil {
					return nil, err
				}
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
	for file, text := range animations {
		encoded, err := encodeText(text)
		if err != nil {
			return nil, err
		}
		replacements[file] = encoded
	}
	if tableChanged {
		encoded, err := encodeText(strings.Join(actionLines, "\n"))
		if err != nil {
			return nil, err
		}
		replacements["itemact.txt"] = encoded
	}
	data, err := entryProperties.replace(a, replacements)
	if err != nil {
		return nil, err
	}
	return parseArchive(data)
}

// stageEffectFiles 列出招式特效编辑会改到的动画文件，供写盘白名单放行。
func stageEffectFiles(source *archive, state *weaponState) map[string]bool {
	files := map[string]bool{}
	if len(state.StageEffects) == 0 {
		return files
	}
	actionText, err := source.text("itemact.txt")
	if err != nil {
		return files
	}
	header, rows := itemactTable(actionText)
	columnOf := map[int]int{}
	for index, name := range header {
		if stage, err := strconv.Atoi(strings.TrimSpace(name)); err == nil {
			columnOf[stage] = index
		}
	}
	byWeapon := map[string][]string{}
	for _, row := range rows {
		if len(row) > 0 {
			byWeapon[strings.TrimSpace(row[0])] = row
		}
	}
	for weaponKey, perStage := range state.StageEffects {
		row := byWeapon[weaponKey]
		if row == nil {
			continue
		}
		for stage := range perStage {
			column, ok := columnOf[stage]
			if !ok || column >= len(row) {
				continue
			}
			action := strings.TrimSpace(row[column])
			if len(action) < 4 {
				continue
			}
			source.allowGroupFiles(files, action)
		}
	}
	return files
}

// 已有源登记允许资源不随临时副本复制；新增或改名资源必须存在。
func validateAppliedEffectRows(source *archive, client string, rows []EffectRow) error {
	if err := validateEffectRows("", rows); err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	text, err := source.text("acteffect.xml")
	if err != nil {
		return err
	}
	root, err := parseXML(text)
	if err != nil {
		return err
	}
	native := map[EffectRow]bool{}
	root.walk(func(n *xmlNode) {
		if n.tag == "EffectFile" {
			native[EffectRow{EffectID: n.get("EffectId"), File: n.get("File")}] = true
		}
	})
	for _, row := range rows {
		if native[row] {
			continue
		}
		if err := validateEffectRows(client, []EffectRow{row}); err != nil {
			return err
		}
	}
	return nil
}

// client 为空时仅校验编号、重复和路径；legacy_set 传客户端以校验资源存在性。
func validateEffectRows(client string, rows []EffectRow) error {
	seen := map[string]bool{}
	for _, row := range rows {
		id := strings.TrimSpace(row.EffectID)
		if !effectIdShape.MatchString(id) || id != row.EffectID {
			return fmt.Errorf("特效编号 %q 无效", row.EffectID)
		}
		if seen[id] {
			return fmt.Errorf("特效 %s 重复登记", id)
		}
		seen[id] = true
		file := strings.TrimSpace(row.File)
		if file == "" {
			return fmt.Errorf("特效 %s 缺少资源文件名", id)
		}
		rel := filepath.FromSlash(strings.ReplaceAll(file, "\\", "/"))
		if !filepath.IsLocal(rel) || file != row.File || strings.ContainsAny(file, ":\"'<>&|?*\x00\r\n\t") {
			return fmt.Errorf("特效 %s 的资源路径无效", id)
		}
		for _, part := range strings.Split(strings.ReplaceAll(file, "\\", "/"), "/") {
			if part == ".." || part == "." || part == "" || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
				return fmt.Errorf("特效 %s 的资源路径无效", id)
			}
		}
		if client == "" {
			continue
		}
		stat, err := os.Stat(filepath.Join(client, "Data", "effect", "effect", rel))
		if err != nil || !stat.Mode().IsRegular() {
			return fmt.Errorf("特效 %s 缺少资源文件 %s", id, file)
		}
	}
	return nil
}
