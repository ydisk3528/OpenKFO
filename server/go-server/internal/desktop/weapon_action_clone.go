package desktop

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// The native special-attack gate (gfxz 9EECC0) looks up SkillProperty by
// itemact's full action number, before playing any animation. Cloning only
// AnmDesc leaves this lookup missing. Keep entry properties separate from the
// stable hit-property clones used by combo rules.
type actionCloneProperties struct {
	properties map[string][]*xmlNode
	clones     map[string]string
}

func newActionCloneProperties(a *archive) (*actionCloneProperties, error) {
	root, err := a.xml("skillproperty.xml")
	if err != nil {
		return nil, err
	}
	p := &actionCloneProperties{properties: map[string][]*xmlNode{}, clones: map[string]string{}}
	root.walk(func(n *xmlNode) {
		if n.tag == "PropertyItem" {
			id := n.get("SkillProId")
			p.properties[id] = append(p.properties[id], n)
		}
	})
	return p, nil
}

func (p *actionCloneProperties) allocate(action string, reserved map[string]bool) int {
	if len(action) <= 4 || reserved == nil {
		return 0
	}
	// Preserve legacy allocations; longer block IDs are already native data.
	// Limit new IDs to five digits so the full action stays within nine digits.
	for _, space := range [][3]int{{999, 1, -1}, {1000, 99999, 1}} {
		for id := space[0]; id != space[1]+space[2]; id += space[2] {
			key := action[:4] + "/" + strconv.Itoa(id)
			next := action[:4] + fmt.Sprintf("%03d", id)
			if reserved[key] || len(p.properties[next]) != 0 {
				continue
			}
			reserved[key] = true
			if len(p.properties[action]) != 0 {
				p.clones[next] = action
			}
			return id
		}
	}
	return 0
}

func (p *actionCloneProperties) replace(a *archive, replacements map[string][]byte) ([]byte, error) {
	if len(p.clones) == 0 {
		return a.replace(replacements)
	}
	text, err := a.text("skillproperty.xml")
	if raw, ok := replacements["skillproperty.xml"]; ok {
		text, err = decodeText(raw)
	}
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(p.clones))
	for next := range p.clones {
		keys = append(keys, next)
	}
	sort.Strings(keys)
	var addition strings.Builder
	for _, next := range keys {
		source := p.clones[next]
		// 原生表里同一个号可能被定义两遍（实测 8 个号）：按第一条克隆，和 render
		// 的写法一致；长度为 0 才是真错误（动作块引用了表里根本没有的号）。
		if len(p.properties[source]) == 0 {
			return nil, fmt.Errorf("命中属性 %s 在客户端里不存在，未修改配置", source)
		}
		node := p.properties[source][0].clone()
		node.set("SkillProId", next)
		encoded, err := node.serialize()
		if err != nil {
			return nil, err
		}
		addition.WriteString("\n" + encoded)
	}
	if strings.Count(text, "</SkillProperty>") != 1 {
		return nil, fmt.Errorf("技能属性表结构错误")
	}
	text = strings.Replace(text, "</SkillProperty>", addition.String()+"\n</SkillProperty>", 1)
	replacements["skillproperty.xml"], err = encodeText(text)
	if err != nil {
		return nil, err
	}
	return a.replace(replacements)
}
