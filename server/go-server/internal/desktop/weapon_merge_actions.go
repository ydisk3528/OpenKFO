package desktop

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

func mergePropertyIDs(info *inspection, action string) []string {
	ids := propertyIDsOfAction(info, action)
	if len(info.properties[action]) > 0 && !includes(ids, action) {
		ids = append(ids, action)
	}
	return ids
}

func mergeEntryActions(manifest *mergeManifest) map[string]bool {
	ids := map[string]bool{}
	for _, w := range manifest.Weapons {
		cols := strings.Split(strings.TrimSpace(w.ItemactRow), "\t")
		for i := 2; i < len(cols); i++ {
			ids[cols[i]] = true
		}
	}
	return ids
}

func mergeBlockKey(text string) string {
	n, err := parseXML(text)
	if err != nil {
		return ""
	}
	n.set("id", "0")
	n.walk(func(child *xmlNode) {
		if strings.TrimSpace(child.text) == "" {
			child.text = ""
		}
		kept := child.children[:0]
		for _, c := range child.children {
			if !c.comment {
				kept = append(kept, c)
			}
		}
		child.children = kept
		sort.Slice(child.attrs, func(i, j int) bool { return child.attrs[i].Name.Local < child.attrs[j].Name.Local })
	})
	key, err := n.serialize()
	if err != nil {
		return ""
	}
	return key
}

func sameMergeBlock(a, b string) bool {
	key := mergeBlockKey(a)
	return key != "" && key == mergeBlockKey(b)
}

// Isolate conflicts before import. Entry properties use the action number as
// their key, so action and property renumbering must be planned together.
func planMergeActions(target *archive, manifest *mergeManifest, propertyRemap map[string]string) error {
	blocks := map[string][]string{}
	incoming := map[string]string{}
	properties := map[string]string{}
	entryNodes := map[string]string{}
	reserved := map[string]bool{}
	text, err := target.text("skillproperty.xml")
	if err != nil {
		return err
	}
	for _, node := range propertyNodePattern.FindAllString(text, -1) {
		if m := propertyIdInPattern.FindStringSubmatch(node); m != nil {
			properties[m[1]] = propertyContentKey(node)
			reserved[m[1]] = true
		}
	}
	actions, err := target.text("itemact.txt")
	if err != nil {
		return err
	}
	for _, row := range strings.Split(actions, "\n")[1:] {
		cols := strings.Split(strings.TrimSpace(row), "\t")
		for i := 2; i < len(cols); i++ {
			reserved[cols[i]] = true
		}
	}
	loaded := map[string]bool{}
	for _, w := range manifest.Weapons {
		for _, node := range w.SkillProperties {
			if m := propertyIdInPattern.FindStringSubmatch(node); m != nil {
				key := propertyContentKey(node)
				if old, ok := entryNodes[m[1]]; ok && old != key {
					return fmt.Errorf("包内技能属性 %s 内容冲突", m[1])
				}
				entryNodes[m[1]] = key
				reserved[m[1]] = true
			}
		}
		for prefix, list := range w.AnimationBlocks {
			if !loaded[prefix] {
				// 组内检索：1002/1006/3001… 这 8 组没有同名 4 位文件，块只在
				// 6 位子文件里。按 animation/<前缀>.xml 取会整组读不到。
				for _, block := range target.groupBlockTexts(prefix) {
					n, e := parseXML(block)
					if e != nil {
						return e
					}
					id, e := strconv.Atoi(strings.TrimSpace(n.get("id")))
					if e != nil {
						return e
					}
					key := prefix + fmt.Sprintf("%03d", id)
					blocks[key] = append(blocks[key], block)
					reserved[key] = true
				}
				loaded[prefix] = true
			}
			for _, block := range list {
				n, e := parseXML(block)
				if e != nil {
					return e
				}
				id, e := strconv.Atoi(strings.TrimSpace(n.get("id")))
				if e != nil {
					return e
				}
				key := prefix + fmt.Sprintf("%03d", id)
				if old, ok := incoming[key]; ok && !sameMergeBlock(old, block) {
					return fmt.Errorf("包内动作 %s 内容冲突", key)
				}
				incoming[key] = block
				reserved[key] = true
			}
		}
	}
	keys := make([]string, 0, len(incoming))
	for key := range incoming {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	remap := map[string]string{}
	for _, action := range keys {
		block := rewritePropertyRefs(incoming[action], propertyRemap)
		entry, hasEntry := entryNodes[action]
		matches := func(candidate string) bool {
			if len(blocks[candidate]) != 1 {
				return false
			}
			if hasEntry && properties[candidate] != entry {
				return false
			}
			if !hasEntry && properties[candidate] != "" {
				return false
			}
			adjusted := block
			if hasEntry {
				adjusted = rewritePropertyRefs(adjusted, map[string]string{action: candidate})
			}
			return sameMergeBlock(blocks[candidate][0], adjusted)
		}
		next := action
		if !matches(action) && (len(blocks[action]) > 0 || properties[action] != "") {
			next = ""
			candidates := make([]string, 0, len(blocks))
			for key := range blocks {
				if strings.HasPrefix(key, action[:4]) {
					candidates = append(candidates, key)
				}
			}
			sort.Strings(candidates)
			for _, candidate := range candidates {
				if matches(candidate) {
					next = candidate
					break
				}
			}
			if next == "" {
				for id := 999; id >= 1; id-- {
					candidate := action[:4] + fmt.Sprintf("%03d", id)
					if !reserved[candidate] {
						next = candidate
						break
					}
				}
			}
			if next == "" {
				return fmt.Errorf("动作 %s 的独立编号空间不足，未修改配置", action)
			}
		}
		remap[action] = next
		reserved[next] = true
		if hasEntry {
			propertyRemap[action] = next
			properties[next] = entry
			block = rewritePropertyRefs(block, map[string]string{action: next})
		}
		if len(blocks[next]) == 0 {
			blocks[next] = []string{block}
		}
	}
	for i := range manifest.Weapons {
		w := &manifest.Weapons[i]
		cols := strings.Split(w.ItemactRow, "\t")
		for j := 2; j < len(cols); j++ {
			if next, ok := remap[strings.TrimSpace(cols[j])]; ok {
				cols[j] = next
			}
		}
		w.ItemactRow = strings.Join(cols, "\t")
		for prefix, list := range w.AnimationBlocks {
			for j, block := range list {
				n, _ := parseXML(block)
				id, _ := strconv.Atoi(strings.TrimSpace(n.get("id")))
				next := remap[prefix+fmt.Sprintf("%03d", id)]
				newID, _ := strconv.Atoi(next[4:])
				list[j] = retitleBlock(block, newID)
			}
		}
	}
	return nil
}
