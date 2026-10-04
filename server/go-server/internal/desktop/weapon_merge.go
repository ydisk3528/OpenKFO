package desktop

// 合并式武器包：只导出一把（或一组）自建武器自己的配置条目，导入时逐行/逐块
// 合并进目标客户端的 config.spf2，其余条目一个字节都不动。
//
// 背景：整包发版（weapon_package）把整份 Data/config.spf2 发出去、解压覆盖。
// 线上客户端的配置包和本机不一样（更新器、别的运营改动），整包覆盖会把线上
// 差异全部抹掉。合并包解决的就是这件事：
//
//   导出 = item.txt/itemact.txt 的行 + delayacttable 的转移 + acteffect 块 +
//          comborule 块 + 动作块（按 id）+ 命中属性节点（按 SkillProId）+
//          素材文件，装进 zip 里的 manifest.json 与实体文件；
//   导入 = 打开目标客户端的 config.spf2，行级替换/追加、块级按 id 替换或插入，
//          校验「除白名单条目外逐条目字节相同」后原子写回。
//
// 归档的 replace 只能改已有条目，不能新增条目；好在武器配置全部落在已有条目
// 里（item.txt、itemact.txt、delayacttable.xml、acteffect.xml、comborule.xml、
// animation/*.xml、skillproperty.xml），不需要新增。

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const mergeFormat = "openkfo-weapon-merge"

// mergeDelayRow 是 delayacttable.xml 的一条转移，结构化保存（不存行文本），
// 导入时走 setComboRows 的正规改写路径。
type mergeDelayRow struct {
	Old  string `json:"old"`
	New  string `json:"new"`
	Key  string `json:"key"`
	Part string `json:"part,omitempty"`
}

// mergeWeapon 是 manifest.json 里一把武器的全部合并材料。
// Each list holds the raw text of the entries that belong to it, so a package
// can be merged into another client without re-deriving anything from the source
// archive.
type mergeWeapon struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	// 行级条目：首列（item.txt 是第 2 列）匹配武器号即替换。
	ItemRow    string `json:"item_row"`
	ItemactRow string `json:"itemact_row"`
	// delayacttable：该武器的全部转移（可能是空的——武器只走帧级连招）。
	DelayRows []mergeDelayRow `json:"delay_rows,omitempty"`
	// acteffect 的 <WeaponEffect> 块原文；空 = 该武器没有特效登记。
	ActEffectBlock string `json:"acteffect_block,omitempty"`
	// comborule 的 <ComboRule> 块内文；空 = 该武器没有连招限制。
	ComboRuleInner string `json:"comborule_inner,omitempty"`
	// 动作块原文，按 animation 文件前缀分组。
	AnimationBlocks map[string][]string `json:"animation_blocks,omitempty"`
	// 命中属性节点原文（<PropertyItem>）。
	SkillProperties []string `json:"skill_properties,omitempty"`
}

type mergeManifest struct {
	Format    string        `json:"format"`
	Version   int           `json:"version"`
	Generated string        `json:"generated"`
	Weapons   []mergeWeapon `json:"weapons"`
	Assets    []string      `json:"assets"`
}

// containsBlockText 判断一份块原文是否语义等价地出现在候选块里。
//
// 用 sameMergeBlock（归一化比较）而不是逐字包含：包里同一块会经过 retitleBlock /
// rewritePropertyRefs 处理，字节形态和归档里的副本常不一致，但语义相同 —— 那种
// 情况不需要改写文件，也就必须放行。
func containsBlockText(existing []string, block string) bool {
	for _, have := range existing {
		if sameMergeBlock(have, block) {
			return true
		}
	}
	return false
}

// 导出白名单：合并包允许触碰的归档条目（动画文件按前缀展开）。
func mergeAllowedEntries(manifest *mergeManifest) map[string]bool {
	allowed := map[string]bool{
		"item.txt": true, "itemact.txt": true, "delayacttable.xml": true,
		"acteffect.xml": true, "comborule.xml": true, "skillproperty.xml": true,
	}
	for _, weapon := range manifest.Weapons {
		for prefix := range weapon.AnimationBlocks {
			allowed["animation/"+prefix+".xml"] = true
		}
	}
	return allowed
}

// tabRowOf 返回表格里第一个「指定列等于 value」的原始行文本。
func tabRowOf(text string, column int, value string) (string, bool) {
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		cells := strings.Split(line, "\t")
		if column < len(cells) && strings.TrimSpace(cells[column]) == value {
			return line, true
		}
	}
	return "", false
}

// 出厂自带的 PropertyItem 是**自闭合**写法（`... />`），编辑器克隆出来的却是
// 成对写法。两条都不能漏：只认成对写法时，出厂节点既导不进包、也不会被当成
// 「目标端已存在」，于是导入会追加一条同号节点（同号两条 → 客户端取哪条看运气，
// 我们自己的唯一性校验也会挂）。
var propertyNodePattern = regexp.MustCompile(`(?s)<PropertyItem\b[^>]*?/>|<PropertyItem\b[^>]*>.*?</PropertyItem\s*>`)
var propertyIdInPattern = regexp.MustCompile(`SkillProId\s*=\s*"([^"]+)"`)

// propertyNodeText 从 skillproperty.xml 原文里抓指定 SkillProId 的节点原文。
// 保留源端格式，插入目标端时不重排。
func propertyNodeText(text, id string) (string, bool) {
	for _, match := range propertyNodePattern.FindAllStringIndex(text, -1) {
		node := text[match[0]:match[1]]
		if m := propertyIdInPattern.FindStringSubmatch(node); m != nil && m[1] == id {
			return node, true
		}
	}
	return "", false
}

var propertyTagPattern = regexp.MustCompile(`(?s)^\s*<PropertyItem\b[^>]*>`)
var skillProIdAttrPattern = regexp.MustCompile(`(?i)\bskillproid\s*=\s*"([^"]*)"`)
var comboSkillAttrPattern = regexp.MustCompile(`(?i)\b(PrevSkill|CurSkill|Skill|ExceedSkillProID)\s*=\s*"([^"]*)"`)

// propertyTagOf 取 PropertyItem 的开始标签并压缩空白。这个节点是「属性式」的 ——
// 全部字段都在属性里、没有子节点，所以开始标签就代表整条内容，拿来判断
// 「同一个号是不是同一件事」足够。
func propertyTagOf(nodeText string) string {
	match := propertyTagPattern.FindString(nodeText)
	if match == "" {
		return ""
	}
	return strings.Join(strings.Fields(match), " ")
}

// freshMergePropertyID 在编辑器自建节点的号段里找一个两边都没用到的号。
func freshMergePropertyID(used map[string]bool) string {
	for id := 800000001; id <= 899999999; id++ {
		key := strconv.Itoa(id)
		if !used[key] {
			return key
		}
	}
	return ""
}

// planMergePropertyIDs 决定包里哪些命中属性号必须换掉。
//
// 9000000xx 是**机器相关**的：两台客户端各自编辑过之后，同一个号完全可能指向
// 不同的招式。按号硬合并会出现两种坏结果 ——
//   - 目标端已有同号节点（内容却是别的招式）时，插入被当成「已存在」跳过，
//     于是包里的动作块指向了别人的命中属性，连招限制里的号也跟着错位，
//     黑/白名单等于没配；
//   - 就算没撞号，只要两边各自编过，动作块引用的号在目标端也未必是同一个意思。
//
// 这里给「同号但内容不同」的节点换一个新号，调用方再把动作块与连招限制里的
// 引用一起改写，导入后包内引用保持自洽。内容完全一致（重复导入同一个包）时
// 不换号，保持幂等。
func planMergePropertyIDs(existingText string, manifest *mergeManifest) (map[string]string, error) {
	existing := map[string]string{}
	for _, node := range propertyNodePattern.FindAllString(existingText, -1) {
		if m := propertyIdInPattern.FindStringSubmatch(node); m != nil {
			if _, seen := existing[m[1]]; !seen {
				existing[m[1]] = propertyTagOf(node)
			}
		}
	}
	incoming := map[string]string{}
	for _, weapon := range manifest.Weapons {
		for _, node := range weapon.SkillProperties {
			if m := propertyIdInPattern.FindStringSubmatch(node); m != nil {
				if _, seen := incoming[m[1]]; !seen {
					incoming[m[1]] = propertyTagOf(node)
				}
			}
		}
	}
	used := map[string]bool{}
	for id := range existing {
		used[id] = true
	}
	for id := range incoming {
		used[id] = true
	}
	// 内容相同、只是号被换过的节点：上一次导入把 X 换成了 Y，再导同一个包时 X 在
	// 目标端当然不存在，认不出来就会再插一条（而且号会来回翻）。只在「编辑器自己
	// 发的号段」里找同内容节点，免得把包里的节点接到出厂数据上。
	byTag := map[string]string{}
	for id, tag := range existing {
		if !editorIssuedID(id) {
			continue
		}
		key := propertyContentKey(tag)
		if key == "" {
			continue
		}
		if _, taken := byTag[key]; !taken {
			byTag[key] = id
		}
	}
	remap := map[string]string{}
	entries := mergeEntryActions(manifest)
	for id, tag := range incoming {
		if entries[id] {
			continue // Entry property IDs must move together with the action, not independently.
		}
		current, present := existing[id]
		if present && current == tag {
			continue // 同一件事，保持原号（重复导入幂等）
		}
		// 目标端已经有「同一条内容」的编辑器自建节点：上一次导入已经把它放进来了
		// （可能还换过号），直接复用，别再插一条 —— 否则重复导入会越积越多、
		// 号还会来回翻。只在自建号段里找，所以不会把包里的节点接到出厂数据上。
		if reused, ok := byTag[propertyContentKey(tag)]; ok {
			if reused != id {
				remap[id] = reused
			}
			continue
		}
		if !present {
			continue // 目标端没有这个号 → 直接用原号插入
		}
		next := freshMergePropertyID(used)
		if next == "" {
			return nil, fmt.Errorf("目标客户端命中属性编号空间不足，无法合并")
		}
		used[next] = true
		remap[id] = next
	}
	return remap, nil
}

// editorIssuedID 判断一个编号是否落在编辑器自己发的号段里（自建命中属性
// 800000001+ 起、克隆号 900000000+ 起）。出厂数据的编号都在更小的量级，
// 据此可以把「自己上一轮换号留下的节点」和「出厂节点」区分开。
func editorIssuedID(id string) bool {
	number, err := strconv.Atoi(id)
	return err == nil && number >= 800000001 && number <= 999999999
}

var propertyIdAttrPattern = regexp.MustCompile(`(?i)SkillProId\s*=\s*"[^"]*"`)

// propertyContentKey 抹掉节点标签里的 SkillProId 值，用来判断「同一条属性内容」——
// 号被换过的同一条属性，只有这样才认得出来。
func propertyContentKey(nodeText string) string {
	tag := propertyTagOf(nodeText)
	if tag == "" {
		return ""
	}
	return propertyIdAttrPattern.ReplaceAllString(tag, `SkillProId="*"`)
}

// rewritePropertyRefs 把动作块（以及属性节点自己的 SkillProId）里的引用换号。
// 大小写不敏感，CustomStateSwitch 之类的同名属性一并覆盖。
func rewritePropertyRefs(text string, remap map[string]string) string {
	if len(remap) == 0 {
		return text
	}
	return skillProIdAttrPattern.ReplaceAllStringFunc(text, func(match string) string {
		parts := skillProIdAttrPattern.FindStringSubmatch(match)
		if next, ok := remap[strings.TrimSpace(parts[1])]; ok {
			return strings.Replace(match, parts[1], next, 1)
		}
		return match
	})
}

// rewriteSkillNumbers 把连招限制里的被动编号换号：<MaxComboForSkill Skill>、
// 黑/白名单的 PrevSkill/CurSkill、以及 ExceedSkillProID。
func rewriteSkillNumbers(text string, remap map[string]string) string {
	if len(remap) == 0 {
		return text
	}
	return comboSkillAttrPattern.ReplaceAllStringFunc(text, func(match string) string {
		parts := comboSkillAttrPattern.FindStringSubmatch(match)
		if next, ok := remap[strings.TrimSpace(parts[2])]; ok {
			return strings.Replace(match, parts[2], next, 1)
		}
		return match
	})
}

// weaponMergeExport 渲染当前编辑集后，只把指定武器自己的配置条目和素材打成
// 合并包。渲染管线与整包导出一致（基线 + 编辑集），保证包里是「设计结果」。
func weaponMergeExport(request Request, client string, folder string, base *archive, items []Item, state *weaponState, info *inspection, plans map[string][]Rule) (any, error) {
	if request.Weapon == 0 && !request.All {
		return nil, fmt.Errorf("请指定要导出的武器")
	}
	// 合并包要能被别的客户端直接用，连招限制里的编号更得先对齐再渲染。
	if changed, err := reconcileComboRules(info, state); err != nil {
		return nil, err
	} else if changed {
		if base, err = applyComboRules(base, state.ComboRules); err != nil {
			return nil, err
		}
	}
	data, err := render(base, items, plans, cloneMapOf(state))
	if err != nil {
		return nil, err
	}
	rendered, err := parseArchive(data)
	if err != nil {
		return nil, err
	}
	if err = rendered.verify(); err != nil {
		return nil, err
	}
	ids, err := packageWeaponIDs(request, state, info)
	if err != nil {
		return nil, err
	}
	renderedInfo, err := inspect(rendered, items)
	if err != nil {
		return nil, err
	}

	itemText, err := rendered.text("item.txt")
	if err != nil {
		return nil, err
	}
	actionText, err := rendered.text("itemact.txt")
	if err != nil {
		return nil, err
	}
	effectText, err := rendered.text("acteffect.xml")
	if err != nil {
		return nil, err
	}
	ruleText, err := rendered.text("comborule.xml")
	if err != nil {
		return nil, err
	}
	propertyText, err := rendered.text("skillproperty.xml")
	if err != nil {
		return nil, err
	}

	manifest := mergeManifest{Format: mergeFormat, Version: 1, Generated: time.Now().Format("2006-01-02 15:04:05")}
	for _, id := range ids {
		number := strconv.Itoa(id)
		weapon := mergeWeapon{ID: id, AnimationBlocks: map[string][]string{}}
		for _, candidate := range info.weapons {
			if candidate.ID == id {
				weapon.Name = candidate.Name
				break
			}
		}
		if weapon.Name == "" {
			weapon.Name = number
		}
		if row, ok := tabRowOf(itemText, 1, number); ok {
			weapon.ItemRow = row
		} else {
			return nil, fmt.Errorf("渲染结果里没有武器 %s 的物品行", number)
		}
		if row, ok := tabRowOf(actionText, 0, number); ok {
			weapon.ItemactRow = row
		} else {
			return nil, fmt.Errorf("渲染结果里没有武器 %s 的动作行", number)
		}
		rows, err := comboRowsOfArchive(rendered, number)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			weapon.DelayRows = append(weapon.DelayRows, mergeDelayRow{Old: row.OldState, New: row.NewState, Key: row.KeyInput, Part: row.StartPart})
		}
		weapon.ActEffectBlock = weaponEffectBlockOf(effectText, number)
		weapon.ComboRuleInner = comboRuleInnerOf(ruleText, number)
		// 动作块：沿 itemact 行取动作号，从渲染结果的归档里抓块原文。
		actions, err := weaponActions(rendered, number)
		if err != nil {
			return nil, err
		}
		seenAction := map[string]bool{}
		for _, action := range actions {
			if len(action) < 5 || seenAction[action] {
				continue
			}
			seenAction[action] = true
			key := actionKey(action)
			blocks := renderedInfo.blocks[key]
			if len(blocks) == 0 {
				continue // 死引用：渲染不会播它
			}
			prefix := action[:4]
			weapon.AnimationBlocks[prefix] = append(weapon.AnimationBlocks[prefix], blocks[0].original)
			// 命中属性节点：动作块引用的全部 skillproid。
			for _, id := range mergePropertyIDs(renderedInfo, action) {
				if node, ok := propertyNodeText(propertyText, id); ok {
					weapon.SkillProperties = append(weapon.SkillProperties, node)
				} else {
					return nil, fmt.Errorf("动作 %s 缺少技能属性 %s，未导出", action, id)
				}
			}
		}
		manifest.Weapons = append(manifest.Weapons, weapon)
	}

	include := map[string]bool{}
	for _, section := range packageSections {
		include[section] = true
	}
	files, missing := collectWeaponAssets(client, rendered, items, ids, include)
	for _, file := range files {
		manifest.Assets = append(manifest.Assets, file.Path)
	}

	root := filepath.Dir(filepath.Dir(folder))
	name := "weapon-merge-" + strconv.Itoa(ids[0]) + "-" + time.Now().Format("20060102-150405") + ".zip"
	if len(ids) > 1 || request.All {
		name = "weapons-merge-" + time.Now().Format("20060102-150405") + ".zip"
	}
	out := filepath.Join(root, "dist", "weapon-packages", name)
	size, sum, err := writeMergeZip(out, client, &manifest, files)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, file := range files {
		counts[file.Kind]++
	}
	weapons := []map[string]any{}
	for _, weapon := range manifest.Weapons {
		weapons = append(weapons, map[string]any{"id": weapon.ID, "name": weapon.Name})
	}
	return packageResult{
		Path: out, Name: filepath.Base(out), Size: size, SHA256: sum,
		Files: files, Missing: missing, Weapons: weapons, Counts: counts,
		Generated: manifest.Generated,
	}, nil
}

// comboRowsOfArchive 从归档里读某武器的全部 delayacttable 转移。
func comboRowsOfArchive(a *archive, weapon string) ([]comboRow, error) {
	text, err := a.text("delayacttable.xml")
	if err != nil {
		return nil, err
	}
	return comboRowsOf(text, weapon), nil
}

// weaponEffectBlockOf 返回该武器在 acteffect.xml 里的块原文（可能为空）。
func weaponEffectBlockOf(text, weapon string) string {
	for _, block := range weaponEffectBlockPattern.FindAllString(text, -1) {
		if m := weaponEffectIdPattern.FindStringSubmatch(block); m != nil && m[1] == weapon {
			return block
		}
	}
	return ""
}

// comboRuleInnerOf 返回该武器第一个 comborule 块的内文（可能为空）。
// 同武器多块（官方 253043 写了两遍）只取第一块，导入端会先删全部再插一个，
// 顺手把这种历史脏数据修掉。
func comboRuleInnerOf(text, weapon string) string {
	for _, blk := range comboRuleBlocks(text) {
		if blk.Weapon == weapon {
			return blk.Body
		}
	}
	return ""
}

// writeMergeZip 写合并包：manifest.json + 素材文件 + 导入说明。
func writeMergeZip(out string, client string, manifest *mergeManifest, files []packageFile) (int64, string, error) {
	if err := os.MkdirAll(filepath.Dir(out), 0700); err != nil {
		return 0, "", err
	}
	var buffer bytes.Buffer
	hasher := sha256.New()
	writer := zip.NewWriter(io.MultiWriter(&buffer, hasher))
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return 0, "", err
	}
	if target, err := writer.Create("manifest.json"); err == nil {
		if _, err = target.Write(encoded); err != nil {
			return 0, "", err
		}
	} else {
		return 0, "", err
	}
	for _, file := range files {
		handle, err := os.Open(filepath.Join(client, filepath.FromSlash(file.Path)))
		if err != nil {
			return 0, "", err
		}
		target, err := writer.Create(file.Path)
		if err != nil {
			handle.Close()
			return 0, "", err
		}
		_, err = io.Copy(target, handle)
		handle.Close()
		if err != nil {
			return 0, "", err
		}
	}
	notes := mergeNotes(manifest, files)
	if target, err := writer.Create("导入说明.txt"); err == nil {
		if _, err = io.WriteString(target, notes); err != nil {
			return 0, "", err
		}
	} else {
		return 0, "", err
	}
	if err = writer.Close(); err != nil {
		return 0, "", err
	}
	raw := buffer.Bytes()
	if err = atomicWrite(out, raw); err != nil {
		return 0, "", err
	}
	return int64(len(raw)), hex.EncodeToString(hasher.Sum(nil)), nil
}

func mergeNotes(manifest *mergeManifest, files []packageFile) string {
	var builder strings.Builder
	builder.WriteString("功夫小子 · 自建武器合并包\r\n")
	builder.WriteString("生成时间：" + manifest.Generated + "\r\n")
	names := []string{}
	for _, weapon := range manifest.Weapons {
		names = append(names, fmt.Sprintf("%s（%d）", weapon.Name, weapon.ID))
	}
	builder.WriteString("包含武器：" + strings.Join(names, "、") + "\r\n\r\n")
	builder.WriteString("【安装】在 GM 管理器的武器页点「导入武器包」，选择本 zip。\r\n")
	builder.WriteString("        导入只合并这些武器自己的配置条目，客户端配置包里的\r\n")
	builder.WriteString("        其他内容不会被改动；素材文件解压到客户端对应目录。\r\n")
	builder.WriteString("        不要手工解压覆盖——那会当成整包处理。\r\n\r\n")
	builder.WriteString("配置条目：item.txt / itemact.txt 各 1 行、delayacttable 转移、\r\n")
	builder.WriteString("          acteffect 特效登记、comborule 连招限制、动作块、命中属性节点。\r\n\r\n")
	if len(files) > 0 {
		builder.WriteString("素材文件（" + strconv.Itoa(len(files)) + " 个）：\r\n")
		for _, file := range files {
			builder.WriteString("  - " + file.Path + "\r\n")
		}
	}
	return builder.String()
}

// ---------------------------------------------------------------------------
// 导入
// ---------------------------------------------------------------------------

// mergeReportItem 记录一个条目/块的处理结果，前端按条展示，导入完一目了然。
type mergeReportItem struct {
	Entry  string `json:"entry"`
	Action string `json:"action"` // replaced / inserted / unchanged / removed
	Detail string `json:"detail,omitempty"`
}

type mergeImportReport struct {
	Weapons []map[string]any  `json:"weapons"`
	Entries []mergeReportItem `json:"entries"`
	// New 列出包里新增的武器（目标客户端里没有这个编号）。
	New []map[string]any `json:"new,omitempty"`
	// Modified 列出包里已存在、会被覆盖的武器（目标客户端里已有这个编号）。
	Modified []map[string]any `json:"modified,omitempty"`
	// Reference 是本次比对用的目标 config.spf2 路径。
	Reference string `json:"reference,omitempty"`
	Assets    int    `json:"assets"`
	Backup    string `json:"backup"`
	Path      string `json:"path"`
	// Renumbered 列出为了避开目标端已有节点而改过号的命中属性（旧号 → 新号）。
	// 动作块与连招限制里的引用已同步改写，所以包里的黑/白名单在目标端依然命中。
	Renumbered map[string]string `json:"renumbered,omitempty"`
}

// mergePreview 是导入前的差异预览：把包里的武器按「目标客户端里有没有」拆成
// 新增与已存在两类，让用户确认后再真正写盘。参照物就是所选客户端的
// Data/config.spf2（导入目标本身），所以列出来的正是这次导入会带来的变化。
type mergePreview struct {
	Reference string           `json:"reference"`
	New       []map[string]any `json:"new"`
	Modified  []map[string]any `json:"modified"`
	Unchanged []map[string]any `json:"unchanged"`
	Weapons   []map[string]any `json:"weapons"`
}

// weaponIDsOf 返回归档里已经存在的武器编号（item.txt 的 kind-25 行）。
func weaponIDsOf(a *archive) map[string]bool {
	ids := map[string]bool{}
	text, err := a.text("item.txt")
	if err != nil {
		return ids
	}
	for id := range itemRowIndex(text) {
		ids[id] = true
	}
	return ids
}

// openMergeZip 打开合并包，返回 reader（调用方负责 Close）、素材索引与 manifest。
func openMergeZip(path string) (*zip.ReadCloser, map[string]*zip.File, *mergeManifest, error) {
	if path == "" {
		return nil, nil, nil, fmt.Errorf("请选择合并包 zip 文件")
	}
	reader, err := zip.OpenReader(path)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("打不开合并包：%w", err)
	}
	var manifestFile *zip.File
	assets := map[string]*zip.File{}
	for _, file := range reader.File {
		if file.Name == "manifest.json" {
			manifestFile = file
		} else {
			assets[file.Name] = file
		}
	}
	if manifestFile == nil {
		reader.Close()
		return nil, nil, nil, fmt.Errorf("包里没有 manifest.json，不是武器合并包")
	}
	handle, err := manifestFile.Open()
	if err != nil {
		reader.Close()
		return nil, nil, nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(handle, 8<<20))
	handle.Close()
	if err != nil {
		reader.Close()
		return nil, nil, nil, err
	}
	manifest := &mergeManifest{}
	if err = json.Unmarshal(raw, manifest); err != nil {
		reader.Close()
		return nil, nil, nil, fmt.Errorf("manifest.json 解析失败：%w", err)
	}
	if manifest.Format != mergeFormat || manifest.Version != 1 || len(manifest.Weapons) == 0 {
		reader.Close()
		return nil, nil, nil, fmt.Errorf("manifest.json 格式不认识（format=%s version=%d）", manifest.Format, manifest.Version)
	}
	return reader, assets, manifest, nil
}

// weaponMergePreview 只读地算出「这个合并包导入后会给当前客户端带来什么变化」：
// 包里哪些武器是新增的、哪些是已存在会被覆盖的。参照物就是所选客户端的
// Data/config.spf2。前端拿这份清单让用户确认后再调 weapon_merge_import。
func weaponMergePreview(request Request, client string) (any, error) {
	reader, _, manifest, err := openMergeZip(request.SourcePath)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	path := configPath(client)
	if request.MergeWorkspace == "" && request.ClientConfig != nil && request.ClientConfig.Base != "" {
		path = request.ClientConfig.Base
	}
	target, err := loadArchive(path)
	if err != nil {
		return nil, err
	}
	if err = target.verify(); err != nil {
		return nil, err
	}
	existing := weaponIDsOf(target)
	preview := mergePreview{
		Reference: path,
		New:       []map[string]any{},
		Modified:  []map[string]any{},
		Weapons:   []map[string]any{},
		Unchanged: []map[string]any{},
	}
	for _, weapon := range manifest.Weapons {
		entry := map[string]any{"id": weapon.ID, "name": weapon.Name}
		preview.Weapons = append(preview.Weapons, entry)
		if existing[strconv.Itoa(weapon.ID)] {
			changes, err := mergeWeaponDifferences(target, weapon)
			if err != nil {
				return nil, err
			}
			entry["changes"] = changes
			if len(changes) == 0 {
				preview.Unchanged = append(preview.Unchanged, entry)
			} else {
				preview.Modified = append(preview.Modified, entry)
			}
		} else {
			preview.New = append(preview.New, entry)
		}
	}
	return preview, nil
}

// weaponMergePackages lists the merge packages earlier exports produced, so the
// import dialog can offer them instead of asking the user to type a full path.
// Read-only: it only scans <root>/dist/weapon-packages.
func weaponMergePackages(folder string) (any, error) {
	dir := filepath.Join(filepath.Dir(filepath.Dir(folder)), "dist", "weapon-packages")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return map[string]any{"directory": dir, "packages": []map[string]any{}}, nil
	}
	packages := []map[string]any{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".zip") || !strings.Contains(name, "merge-") {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		packages = append(packages, map[string]any{
			"name":     name,
			"path":     filepath.Join(dir, name),
			"size":     info.Size(),
			"modified": info.ModTime().Format("2006-01-02 15:04:05"),
		})
	}
	sort.Slice(packages, func(i, j int) bool {
		return packages[i]["modified"].(string) > packages[j]["modified"].(string)
	})
	return map[string]any{"directory": dir, "packages": packages}, nil
}

// weaponMergeImport 把合并包里的武器配置逐条合并进当前客户端的 config.spf2。
// 目标是客户端当前的配置包本身（不是 GM 的基线），所以线上客户端已有的其他
// 改动原样保留——这正是合并包存在的意义。
func weaponMergeImport(request Request, client string, folder string) (any, error) {
	reader, assets, manifest, err := openMergeZip(request.SourcePath)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	if len(request.MergeWeapons) > 0 {
		chosen := map[int]bool{}
		for _, id := range request.MergeWeapons {
			chosen[id] = true
		}
		weapons := []mergeWeapon{}
		for _, w := range manifest.Weapons {
			if chosen[w.ID] {
				weapons = append(weapons, w)
				delete(chosen, w.ID)
			}
		}
		if len(chosen) > 0 || len(weapons) == 0 {
			return nil, fmt.Errorf("所选武器不在合并包中")
		}
		manifest.Weapons = weapons
	}

	target, err := loadArchive(configPath(client))
	if err != nil {
		return nil, err
	}
	if err = target.verify(); err != nil {
		return nil, err
	}
	allowed := mergeAllowedEntries(manifest)
	// 以目标客户端当前配置为参照，把包里的武器分成「新增」与「已存在（会被覆盖）」，
	// 一并回报。正常流程是前端先调 weapon_merge_preview 让用户确认再进来。
	// replace 只能改已有条目，包里用到的动画文件目标端也必须先存在。
	existing := weaponIDsOf(target)
	// 命中属性号是机器相关的：目标端已经占用的号必须换掉，否则包里的动作块会
	// 指向别人的命中属性，连招限制里的黑/白名单跟着错位（等于没配）。
	propertyText, err := target.text("skillproperty.xml")
	if err != nil {
		return nil, err
	}
	propertyRemap, err := planMergePropertyIDs(propertyText, manifest)
	if err != nil {
		return nil, err
	}
	if err = planMergeActions(target, manifest, propertyRemap); err != nil {
		return nil, err
	}
	importable := []mergeWeapon{}
	newWeapons := []map[string]any{}
	modified := []map[string]any{}
	for _, weapon := range manifest.Weapons {
		if existing[strconv.Itoa(weapon.ID)] {
			modified = append(modified, map[string]any{"id": weapon.ID, "name": weapon.Name})
		} else {
			newWeapons = append(newWeapons, map[string]any{"id": weapon.ID, "name": weapon.Name})
		}
		for prefix, blocks := range weapon.AnimationBlocks {
			if target.groupWriteFile(prefix) != "" {
				continue
			}
			// 子文件组（1002/1006/3001… 这 8 组没有同名 4 位文件）：按角色骨骼拆成
			// 多份、XML 本身不规范，GM 不改写这些文件。只有包里这些块已经逐字存在
			// 于目标里才放行（同族客户端之间合并的正常情形），否则明确拒绝。
			existing := target.groupBlockTexts(prefix)
			if len(existing) == 0 {
				return nil, fmt.Errorf("目标客户端没有动作组 %s（%s）", prefix, strings.Join(target.groupFiles(prefix), "、"))
			}
			for _, block := range blocks {
				if !containsBlockText(existing, block) {
					return nil, fmt.Errorf("动作块 %s 只存在于原生多角色共享文件（%s），GM 不支持改写该文件，无法合并",
						prefix, strings.Join(target.groupFiles(prefix), "、"))
				}
			}
		}
		importable = append(importable, weapon)
	}

	replacements := map[string][]byte{}
	report := []mergeReportItem{}
	changedEntries := map[string]bool{}
	loadText := func(name string) (string, error) {
		if encoded, ok := replacements[name]; ok {
			return decodeText(encoded)
		}
		return target.text(name)
	}
	saveText := func(name, text string) error {
		encoded, err := encodeText(text)
		if err != nil {
			return err
		}
		replacements[name] = encoded
		changedEntries[name] = true
		return nil
	}

	for _, weapon := range importable {
		number := strconv.Itoa(weapon.ID)
		// 行级条目：删旧行、追加新行（幂等）。
		itemText, err := loadText("item.txt")
		if err != nil {
			return nil, err
		}
		existed := false
		if _, ok := tabRowOf(itemText, 1, number); ok {
			existed = true
			itemText = dropTabRow(itemText, 1, number)
		}
		itemText = appendTabRow(itemText, weapon.ItemRow)
		if err = saveText("item.txt", itemText); err != nil {
			return nil, err
		}
		report = append(report, mergeReportItem{Entry: "item.txt", Action: map[bool]string{true: "replaced", false: "inserted"}[existed], Detail: number})

		actionText, err := loadText("itemact.txt")
		if err != nil {
			return nil, err
		}
		actionText = dropTabRow(actionText, 0, number)
		actionText = appendTabRow(actionText, weapon.ItemactRow)
		if err = saveText("itemact.txt", actionText); err != nil {
			return nil, err
		}

		// delayacttable：结构化转移，走 setComboRows 的正规改写。
		rows := make([]comboRow, 0, len(weapon.DelayRows))
		for _, row := range weapon.DelayRows {
			part := row.Part
			if part == "" {
				part = "1"
			}
			rows = append(rows, comboRow{OldState: row.Old, NewState: row.New, KeyInput: row.Key, StartPart: part})
		}
		delayText, err := loadText("delayacttable.xml")
		if err != nil {
			return nil, err
		}
		delayText, err = setComboRows(delayText, number, rows)
		if err != nil {
			return nil, err
		}
		if err = saveText("delayacttable.xml", delayText); err != nil {
			return nil, err
		}
		report = append(report, mergeReportItem{Entry: "delayacttable.xml", Action: "replaced", Detail: fmt.Sprintf("%d 条转移", len(rows))})

		// acteffect：整块替换（无则插入；包里空块 = 移除登记）。
		effectText, err := loadText("acteffect.xml")
		if err != nil {
			return nil, err
		}
		effectText, err = replaceActEffectBlockText(effectText, number, weapon.ActEffectBlock)
		if err != nil {
			return nil, err
		}
		if err = saveText("acteffect.xml", effectText); err != nil {
			return nil, err
		}

		// comborule：删该武器全部块，有内文则插一个新块。
		ruleText, err := loadText("comborule.xml")
		if err != nil {
			return nil, err
		}
		ruleText, err = replaceComboRuleInner(ruleText, number, rewriteSkillNumbers(weapon.ComboRuleInner, propertyRemap))
		if err != nil {
			return nil, err
		}
		if err = saveText("comborule.xml", ruleText); err != nil {
			return nil, err
		}

		// 动作块：按文件聚合，逐块替换或插入。
		prefixes := make([]string, 0, len(weapon.AnimationBlocks))
		for prefix := range weapon.AnimationBlocks {
			prefixes = append(prefixes, prefix)
		}
		sort.Strings(prefixes)
		for _, prefix := range prefixes {
			name := target.groupWriteFile(prefix)
			if name == "" {
				// 子文件组：预检已确认包里这些块与目标逐字一致，不需要（也不能）改写。
				report = append(report, mergeReportItem{
					Entry:  "animation/" + prefix + "*.xml",
					Action: "skipped",
					Detail: "原生多角色共享动作，目标已有同样内容，未改写",
				})
				continue
			}
			animation, err := loadText(name)
			if err != nil {
				return nil, err
			}
			replaced, inserted := 0, 0
			for _, blockText := range weapon.AnimationBlocks[prefix] {
				animation, replaced, inserted, err = mergeAnimationBlock(
					animation, rewritePropertyRefs(blockText, propertyRemap))
				if err != nil {
					return nil, fmt.Errorf("%s：%w", name, err)
				}
			}
			if err = saveText(name, animation); err != nil {
				return nil, err
			}
			if replaced+inserted > 0 {
				report = append(report, mergeReportItem{Entry: name, Action: "merged", Detail: fmt.Sprintf("替换 %d / 新增 %d 个动作块", replaced, inserted)})
			}
		}

		// 命中属性节点：只插入缺失的；已存在的一律不动（官方节点以目标端为准）。
		propertyText, err := loadText("skillproperty.xml")
		if err != nil {
			return nil, err
		}
		insertedProps := 0
		for _, raw := range weapon.SkillProperties {
			nodeText := rewritePropertyRefs(raw, propertyRemap)
			id := ""
			if m := propertyIdInPattern.FindStringSubmatch(nodeText); m != nil {
				id = m[1]
			}
			if id == "" {
				continue
			}
			if _, ok := propertyNodeText(propertyText, id); ok {
				// 已存在且内容一致（planMergePropertyIDs 已经把「同号不同内容」换掉了），
				// 说明是重复导入，保持目标端原样。
				continue
			}
			propertyText, err = insertPropertyNode(propertyText, nodeText)
			if err != nil {
				return nil, err
			}
			insertedProps++
		}
		if insertedProps > 0 || changedEntries["skillproperty.xml"] {
			if err = saveText("skillproperty.xml", propertyText); err != nil {
				return nil, err
			}
		}
		if insertedProps > 0 {
			report = append(report, mergeReportItem{Entry: "skillproperty.xml", Action: "inserted", Detail: fmt.Sprintf("%d 个命中属性节点", insertedProps)})
		}
	}

	if len(replacements) == 0 {
		return nil, fmt.Errorf("合并包里没有可合并的配置")
	}
	data, err := target.replace(replacements)
	if err != nil {
		return nil, err
	}
	// 与「应用到游戏」一致：写盘前紧凑重建。replace 是追加式写入，不重建的话
	// 每导入一次就往目标配置里留一批旧副本（同一包重复导入还会导致文件字节变化）。
	if data, err = compactArchive(data); err != nil {
		return nil, err
	}
	merged, err := parseArchive(data)
	if err != nil {
		return nil, err
	}
	if err = merged.verify(); err != nil {
		return nil, err
	}
	// 安全校验：除白名单条目外，其余条目必须逐字节相同。
	for name := range target.entries {
		before, err := target.raw(name)
		if err != nil {
			return nil, err
		}
		after, err := merged.raw(name)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(before, after) && !allowed[name] {
			return nil, fmt.Errorf("合并意外改动了 %s，已放弃写入", name)
		}
	}

	// 素材文件解压（先校验路径，全部合法才开始写）。
	extracted := 0
	type pendingAsset struct {
		source *zip.File
		target string
	}
	pending := []pendingAsset{}
	for _, relative := range manifest.Assets {
		file, ok := assets[relative]
		if !ok {
			continue
		}
		clean := strings.ReplaceAll(relative, "\\", "/")
		if strings.Contains(clean, "..") || strings.Contains(clean, ":") || strings.HasPrefix(clean, "/") {
			return nil, fmt.Errorf("素材路径不安全：%s", relative)
		}
		absolute := filepath.Join(client, filepath.FromSlash(clean))
		if rel, err := filepath.Rel(client, absolute); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("素材路径越界：%s", relative)
		}
		pending = append(pending, pendingAsset{source: file, target: absolute})
	}
	for _, asset := range pending {
		if err = extractZipEntry(asset.source, asset.target); err != nil {
			return nil, fmt.Errorf("解压素材失败：%w", err)
		}
		extracted++
	}

	// 备份 + 原子写回。
	path := configPath(client)
	backup := filepath.Join(folder, "before-import-"+time.Now().Format("20060102-150405")+".spf2")
	if err = atomicWrite(backup, target.data); err != nil {
		return nil, fmt.Errorf("写备份失败：%w", err)
	}
	if err = atomicWrite(path, merged.data); err != nil {
		return nil, err
	}
	weapons := append([]map[string]any{}, newWeapons...)
	weapons = append(weapons, modified...)
	return mergeImportReport{
		Weapons: weapons, Entries: report,
		New: newWeapons, Modified: modified, Reference: configPath(client),
		Assets: extracted, Backup: backup, Path: path,
		Renumbered: propertyRemap,
	}, nil
}

// mergeAnimationBlock only inserts missing blocks or reuses identical ones.
// Conflicting IDs must be isolated by planMergeActions before any write.
func mergeAnimationBlock(animation, blockText string) (string, int, int, error) {
	node, err := parseXML(blockText)
	if err != nil {
		return "", 0, 0, fmt.Errorf("动作块解析失败：%w", err)
	}
	id := strings.TrimSpace(node.get("id"))
	if id == "" {
		return "", 0, 0, fmt.Errorf("动作块缺少 id")
	}
	if number, err := strconv.Atoi(id); err == nil {
		id = strconv.Itoa(number) // 前导零归一
	}
	for _, piece := range animationPattern.FindAllString(animation, -1) {
		head := piece
		if end := strings.Index(head, ">"); end >= 0 {
			head = head[:end+1]
		}
		match := anmIDPattern.FindStringSubmatch(head)
		if match == nil {
			continue
		}
		found := strings.TrimSpace(match[1])
		if number, err := strconv.Atoi(found); err == nil {
			found = strconv.Itoa(number)
		}
		if found != id {
			continue
		}
		if sameMergeBlock(piece, blockText) {
			return animation, 0, 0, nil
		}
		return "", 0, 0, fmt.Errorf("动作 %s 内容冲突且未完成独立编号分配，未覆盖", id)
	}
	idx := strings.LastIndex(animation, "</AnmInfo>")
	if idx < 0 {
		return "", 0, 0, fmt.Errorf("缺少 AnmInfo 结束标签")
	}
	insertion := "\n" + blockText + "\n"
	return animation[:idx] + insertion + animation[idx:], 0, 1, nil
}

// replaceActEffectBlockText 用整块原文替换该武器的特效登记块：已有则原位换
// （并删掉可能的历史重复块），没有则插到 </ActEffect> 前；空块 = 移除登记。
func replaceActEffectBlockText(text, weapon, blockText string) (string, error) {
	blocks := weaponEffectBlockPattern.FindAllString(text, -1)
	matched := []string{}
	for _, block := range blocks {
		if m := weaponEffectIdPattern.FindStringSubmatch(block); m != nil && m[1] == weapon {
			matched = append(matched, block)
		}
	}
	if len(matched) == 1 && blockText != "" && matched[0] == blockText {
		return text, nil // 幂等
	}
	for _, block := range matched {
		text = strings.Replace(text, block, "", 1)
	}
	if blockText == "" {
		// 删块留下的空行收一收，避免重复导入越积越多。
		for strings.Contains(text, "\n\n\n") {
			text = strings.ReplaceAll(text, "\n\n\n", "\n\n")
		}
		return text, nil
	}
	idx := strings.LastIndex(text, "</ActEffect>")
	if idx < 0 {
		return "", fmt.Errorf("特效登记表缺少 ActEffect 结束标签")
	}
	insertion := "\n\n\t<!-- 导入的武器 " + weapon + " 特效登记 -->\n\t" + blockText + "\n"
	return text[:idx] + insertion + text[idx:], nil
}

// replaceComboRuleInner 重写该武器的连招限制：原位替换第一个块（内文不变则
// 字节不动），删掉其余重复块；内文空 = 删掉全部块。与 setComboRules 同一条
// 「原位替换 + 删重复」路线，保证重复导入字节稳定、且不碰其他武器的块。
func replaceComboRuleInner(text, weapon, inner string) (string, error) {
	owned := []comboRuleBlock{}
	for _, block := range comboRuleBlocks(text) {
		if block.Weapon == weapon {
			owned = append(owned, block)
		}
	}
	if len(owned) == 0 {
		if inner == "" {
			return text, nil
		}
		closing := comboRuleRootEnd.FindStringIndex(text)
		if closing == nil {
			return "", fmt.Errorf("连招限制表结构错误")
		}
		return text[:closing[0]] + mergeRuleBlock(weapon, inner) + text[closing[0]:], nil
	}
	var builder strings.Builder
	cursor := 0
	for index, block := range owned {
		builder.WriteString(text[cursor:comboRuleOwnedStart(text, block)])
		cursor = comboRuleOwnedEnd(text, block)
		if index == 0 && inner != "" {
			builder.WriteString(mergeRuleBlock(weapon, inner))
		}
	}
	builder.WriteString(text[cursor:])
	return builder.String(), nil
}

func mergeRuleBlock(weapon, inner string) string {
	return "\t<ComboRule Weapon=\"" + weapon + "\">" + inner + "</ComboRule>\n"
}

// insertPropertyNode 把一个 <PropertyItem> 节点插到 </SkillProperty> 前。
func insertPropertyNode(text, nodeText string) (string, error) {
	idx := strings.LastIndex(text, "</SkillProperty>")
	if idx < 0 {
		return "", fmt.Errorf("命中属性表缺少 SkillProperty 结束标签")
	}
	insertion := "\n" + nodeText + "\n"
	return text[:idx] + insertion + text[idx:], nil
}

// extractZipEntry 解压一个 zip 条目到目标路径（父目录自动创建）。
func extractZipEntry(file *zip.File, target string) error {
	if file.UncompressedSize64 > 64<<20 {
		return fmt.Errorf("资源超过 64 MiB：%s", file.Name)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return err
	}
	handle, err := file.Open()
	if err != nil {
		return err
	}
	defer handle.Close()
	var buffer bytes.Buffer
	if _, err = io.Copy(&buffer, io.LimitReader(handle, (64<<20)+1)); err != nil {
		return err
	}
	if buffer.Len() > 64<<20 || uint64(buffer.Len()) != file.UncompressedSize64 {
		return fmt.Errorf("资源长度异常：%s", file.Name)
	}
	return atomicWrite(target, buffer.Bytes())
}
