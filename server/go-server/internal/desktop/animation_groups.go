package desktop

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// 动作号 → animation 文件的匹配规则。两套并存，编码方式其实是同一套：
//
//	动作号 = 4 位组前缀 + 块号（三位补零，块号 >= 1000 时不补）
//	动作键 = actionKey(动作号) = 组前缀 + "/" + atoi(块号后段)
//
// 区别只在**块写在哪个文件**：
//
//	A. 同名 4 位文件 animation/<组前缀>.xml —— 主流写法。
//	   2001521011 -> animation/2001.xml 里的 <AnmDesc id="521011">。
//
//	B. 同前缀的 6 位子文件组 animation/<组前缀><2 位序号>.xml。
//	   这几组把同一套动作**按角色骨骼各存一份**：100201/100202/100203 里同一个
//	   「龙牙个性动作」（块号 17）的 <Anm name> 分别是 100217/100221/100220，
//	   即三个角色各自的骨骼文件。动作号只写 4 位组前缀：1002007 = 组 1002 + 块号 7。
//
// 客户端 88 个 animation/*.xml 里 11 个前缀带多文件，其中 8 个（1002/1006/3001/
// 3003/3004/3005/3006/3008）**没有**同名 4 位文件 —— 只认规则 A 会让这几组的动作
// 整组读不到，被误判成「itemact 指向客户端没带的动作块」，其实块一直都在。
// 1001/2002/5005 两种都有（1001faint/2002up 是语义后缀；5005 组还有一个块只存在于
// 子文件里，同名文件里没有）。
//
// 读：组内检索 —— 块号在哪个文件就去哪个文件取（子文件的块集合并不总相同）。
// 写：只有规则 A 的「同名 4 位文件」可以改写。子文件组是按角色拆开的：改一份只对
// 一个角色的骨骼生效，改全部又会覆盖角色专属的块；而且这些文件的 XML 本身不规范
// （100201.xml 少一个 </AnmDesc>），写完过不了整份 XML 校验。所以明确报错，不猜。

// groupFiles 返回承载某个 4 位组前缀的候选动画文件，按优先级排序：
// 同名 4 位文件在前，其后是同前缀的 6 位子文件（文件名字典序）。
func (a *archive) groupFiles(prefix string) []string {
	if len(prefix) != 4 || strings.TrimLeft(prefix, "0123456789") != "" {
		return nil
	}
	if a.groups == nil {
		a.groups = map[string][]string{}
	}
	if cached, ok := a.groups[prefix]; ok {
		return cached
	}
	files := []string{}
	if _, ok := a.entries["animation/"+prefix+".xml"]; ok {
		files = append(files, "animation/"+prefix+".xml")
	}
	subs := []string{}
	for name := range a.entries {
		if !strings.HasPrefix(name, "animation/") {
			continue
		}
		stem := strings.TrimSuffix(strings.TrimPrefix(name, "animation/"), ".xml")
		if len(stem) != 6 || !strings.HasPrefix(stem, prefix) {
			continue
		}
		if strings.TrimLeft(stem, "0123456789") != "" {
			continue
		}
		subs = append(subs, name)
	}
	sort.Strings(subs)
	files = append(files, subs...)
	a.groups[prefix] = files
	return files
}

// groupBlock 是组内检索到的一个动作块：块内容 + 承载它的文件。
type groupBlock struct {
	file  string
	block block
}

// groupBlockIndex 返回该组的块索引：块号 -> 承载它的文件与块内容。
//
// 一个块号在同一份文件里可能注册多条（无条件 + <Condition> 分支），全部保留；
// 不同文件里同号的块按候选顺序先到先得（同号块内容多数一致，但角色专属块有差）。
// XML 不规范的块单独跳过 —— 100201.xml 少一个 </AnmDesc>，不能让它拖垮整组。
func (a *archive) groupBlockIndex(prefix string) map[int][]groupBlock {
	if a.groupBlocks == nil {
		a.groupBlocks = map[string]map[int][]groupBlock{}
	}
	if cached, ok := a.groupBlocks[prefix]; ok {
		return cached
	}
	index := map[int][]groupBlock{}
	for _, file := range a.groupFiles(prefix) {
		text, err := a.text(file)
		if err != nil {
			continue
		}
		for _, original := range animationPattern.FindAllString(text, -1) {
			node, err := parseXML(original)
			if err != nil {
				continue
			}
			number, err := strconv.Atoi(strings.TrimSpace(node.get("id")))
			if err != nil {
				continue
			}
			found := index[number]
			if len(found) > 0 && found[0].file != file {
				// 这个块号已由更靠前的候选文件提供，不再收后面那份。
				continue
			}
			index[number] = append(found, groupBlock{file, block{original, node, conditionUstate(node)}})
		}
	}
	a.groupBlocks[prefix] = index
	return index
}

// animationFileFor 返回承载某动作号的动画文件。组里没有这个块号时退回首选候选
// （调用方会照常在「块不存在」处报错），组本身不存在则返回空串。
func (a *archive) animationFileFor(action string) string {
	if len(action) <= 4 {
		return ""
	}
	files := a.groupFiles(action[:4])
	if len(files) == 0 {
		return ""
	}
	number, err := strconv.Atoi(action[4:])
	if err != nil {
		return files[0]
	}
	if found := a.groupBlockIndex(action[:4])[number]; len(found) > 0 {
		return found[0].file
	}
	return files[0]
}

// groupBlockTexts 返回该组前缀下全部现存动作块的原文，按块号排序。
//
// 给「按 animation/<前缀>.xml 取块」的调用方（武器包导出/合并、工作区差异）用：
// 1002/1006/3001/3003/3004/3005/3006/3008 这 8 组没有同名 4 位文件，只认规则 A
// 会整组取不到块。同名多份（无条件 + 条件分支）都返回。
func (a *archive) groupBlockTexts(prefix string) []string {
	index := a.groupBlockIndex(prefix)
	numbers := make([]int, 0, len(index))
	for number := range index {
		numbers = append(numbers, number)
	}
	sort.Ints(numbers)
	out := []string{}
	for _, number := range numbers {
		for _, found := range index[number] {
			out = append(out, found.block.original)
		}
	}
	return out
}

// groupWriteFile 返回该组前缀可安全改写的动画文件；只存在于 6 位子文件组时返回空串。
//
// 子文件组是按角色骨骼拆开的多份（改一份只对一个角色生效，改全部会覆盖角色专属
// 块），而且这几个文件的 XML 本身不规范，写完过不了整份校验 —— 合并/改写一律拒绝。
func (a *archive) groupWriteFile(prefix string) string {
	if len(prefix) != 4 {
		return ""
	}
	canonical := "animation/" + prefix + ".xml"
	if _, ok := a.entries[canonical]; ok {
		return canonical
	}
	return ""
}

// allowGroupFiles 把承载该动作号的候选动画文件全部加进写盘白名单。
func (a *archive) allowGroupFiles(files map[string]bool, action string) {
	if len(action) < 4 {
		return
	}
	for _, file := range a.groupFiles(action[:4]) {
		files[file] = true
	}
}

// animationWriteFile 返回该动作号可安全改写的动画文件，不可写时返回带说明的错误。
//
// 只有规则 A 的「同名 4 位文件」可以写；落在 6 位子文件组里的块是原生多角色共享
// 动作，改写会只对一个角色生效（或覆盖角色专属块），且这些文件的 XML 不规范、
// 写完过不了整份校验 —— 直接报错说清楚，好过写坏客户端配置。
func (a *archive) animationWriteFile(action string) (string, error) {
	files := a.groupFiles(action[:4])
	if len(files) == 0 {
		return "", fmt.Errorf("客户端里没有承载动作 %s 的动画文件", action)
	}
	canonical := "animation/" + action[:4] + ".xml"
	if a.animationFileFor(action) == canonical {
		return canonical, nil
	}
	return "", fmt.Errorf("动作 %s 是原生多角色共享动作，分散在 %s，GM 不支持改写；请改用其它动作",
		action, strings.Join(files, "、"))
}
