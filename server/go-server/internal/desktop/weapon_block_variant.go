package desktop

import (
	"fmt"
	"strings"
)

// 「同名 <AnmDesc> + <Condition>」分支块（按状态切换招式形态）的公共工具。
//
// 背景：同一份 AnmDesc id 允许注册多条 —— 一条无条件 + 若干带 <Condition>。引擎
// 按玩家是否拥有条件里的状态二选一。两份块的文字不同（差一个 <Condition> 头），
// 但 id 相同，所以：
//   - 按原文替换天然只命中自己那份（文字不同）；
//   - 按 id 定位会命中多份（currentBlock 直接失败），必须连条件一起匹配；
//   - 独占化（分身）时必须把**所有变体**一起复制，否则条件块会成为孤儿。

// actionVariants 取同一动作键下的全部块，按文档顺序（无条件块通常在最前）。
func actionVariants(info *inspection, action string) []block {
	return info.blocks[actionKey(action)]
}

// cloneBlockVariants 把动作块的**全部变体**一起改名成 cloneID 后追加到动画文件
// 末尾，原来的块原样留给供体。
//
// editedCondition 指定哪一份用 editedText（被编辑过的文本）替换；editedText 为空
// 表示所有变体都按原文复制。
//
// refs 是「命中属性号旧→新」的重映射，会套在**每一份复制出来的变体**上（编辑过的
// 那份已经在 editedText 里改好了，这里不重复处理）。无条件块的号改了、分支块的号
// 不改的话，分支条目会继续指向旧属性，规则里给分支号设的数值就永远落不到实处。
func cloneBlockVariants(animation string, variants []block, editedCondition, editedText string, cloneID int, refSets ...[]splitRef) (string, error) {
	var refs []splitRef
	if len(refSets) > 0 {
		refs = refSets[0]
	}
	if len(variants) == 0 {
		return animation, fmt.Errorf("动作块不存在")
	}
	appended := make([]string, 0, len(variants))
	for _, variant := range variants {
		text := variant.original
		if editedText != "" && variant.condition == editedCondition {
			text = editedText
		} else if len(refs) > 0 {
			rewritten, err := rewriteBlockSkillProIDs(text, refs)
			if err != nil {
				return animation, err
			}
			text = rewritten
		}
		appended = append(appended, retitleBlock(text, cloneID))
	}
	if len(anmInfoEndPattern.FindAllStringIndex(animation, -1)) != 1 {
		return animation, fmt.Errorf("动作表结构错误")
	}
	return anmInfoEndPattern.ReplaceAllStringFunc(animation, func(string) string {
		return "\n" + strings.Join(appended, "\n") + "\n</AnmInfo>"
	}), nil
}

// locateEditableBlock 在动画文本里定位「要编辑的那一份」块。优先按原文精确匹配
// （文字唯一，条件分支不会互相干扰）；原文已被本轮的另一次改写顶掉时，退回到按
// (id, 条件) 精确定位。
func locateEditableBlock(animation string, chosen block) (string, error) {
	if chosen.original != "" && strings.Count(animation, chosen.original) == 1 {
		return chosen.original, nil
	}
	found, ok := currentConditionalBlock(animation, strings.TrimSpace(chosen.node.get("id")), chosen.condition)
	if !ok {
		return "", fmt.Errorf("动作定义无法唯一替换（id=%s 条件=%q）",
			strings.TrimSpace(chosen.node.get("id")), chosen.condition)
	}
	return found, nil
}

// variantTexts 取动画里 id == want 的全部 <AnmDesc> 段（按文档顺序，无条件在前）。
func variantTexts(animation, want string) []string {
	out := []string{}
	for _, candidate := range animationPattern.FindAllString(animation, -1) {
		node, err := parseXML(candidate)
		if err != nil {
			continue
		}
		if strings.TrimSpace(node.get("id")) == want {
			out = append(out, candidate)
		}
	}
	return out
}
