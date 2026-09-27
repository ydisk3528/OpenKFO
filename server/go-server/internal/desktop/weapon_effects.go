package desktop

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type effectRegistration struct {
	ID   string `json:"id"`
	File string `json:"file"`
}

// Read the live package, not an older editing baseline. Copy only proven native
// registrations; ambiguous IDs and absent resources remain explicit diagnostics.
func weaponEffects(request Request, client string, items []Item, folder string) (any, error) {
	path := filepath.Join(client, "Data", "config.spf2")
	a, err := loadArchive(path)
	if err != nil {
		return nil, err
	}
	if err = a.verify(); err != nil {
		return nil, err
	}
	if request.Operation == "weapon_effects_apply" && request.Revision != digest(a.data) {
		return nil, fmt.Errorf("客户端配置已变化，请重新检查特效")
	}
	info, err := inspect(a, items)
	if err != nil {
		return nil, err
	}
	var weapon *Weapon
	for i := range info.weapons {
		if info.weapons[i].ID == request.Weapon {
			weapon = &info.weapons[i]
			break
		}
	}
	if weapon == nil {
		return nil, fmt.Errorf("请选择当前客户端中的武器")
	}
	text, err := a.text("acteffect.xml")
	if err != nil {
		return nil, err
	}
	root, err := parseXML(text)
	if err != nil {
		return nil, err
	}
	id := strconv.Itoa(request.Weapon)
	loaded := map[string]bool{}
	registrations := map[string][]*xmlNode{}
	for _, group := range root.children {
		if group.tag != "WeaponEffect" {
			continue
		}
		for _, entry := range group.children {
			if entry.tag != "EffectFile" {
				continue
			}
			key := entry.get("EffectId")
			registrations[key] = append(registrations[key], entry)
			if group.get("ItemID") == "0" || group.get("ItemID") == id {
				loaded[key] = true
			}
		}
	}
	refs := map[string]bool{}
	issues := []string{}
	for _, stage := range weapon.Stages {
		blocks := info.blocks[actionKey(stage.Action)]
		if len(blocks) != 1 {
			issues = append(issues, "动作 "+stage.Action+" 无法唯一解析")
			continue
		}
		blocks[0].node.walk(func(n *xmlNode) {
			for _, attr := range n.attrs {
				if strings.EqualFold(attr.Name.Local, "effectid") && strings.TrimSpace(attr.Value) != "" {
					refs[attr.Value] = true
				}
			}
		})
	}
	ids := []string{}
	for ref := range refs {
		ids = append(ids, ref)
	}
	sort.Strings(ids)
	additions := []effectRegistration{}
	encodedRows := []string{}
	for _, ref := range ids {
		if loaded[ref] {
			continue
		}
		rows := registrations[ref]
		if len(rows) == 0 {
			issues = append(issues, "特效 "+ref+" 没有原始登记")
			continue
		}
		first, err := rows[0].serialize()
		if err != nil {
			return nil, err
		}
		ambiguous := false
		for _, row := range rows[1:] {
			if len(row.attrs) != len(rows[0].attrs) {
				ambiguous = true
			}
			for _, attr := range rows[0].attrs {
				if row.get(attr.Name.Local) != attr.Value {
					ambiguous = true
				}
			}
		}
		if ambiguous {
			issues = append(issues, "特效 "+ref+" 对应多个不同登记，需手动确认")
			continue
		}
		file := rows[0].get("File")
		rel := filepath.FromSlash(strings.ReplaceAll(file, "\\", "/"))
		if file == "" || !filepath.IsLocal(rel) {
			issues = append(issues, "特效 "+ref+" 资源路径无效")
			continue
		}
		stat, err := os.Stat(filepath.Join(client, "Data", "effect", "effect", rel))
		if err != nil || !stat.Mode().IsRegular() {
			issues = append(issues, "特效 "+ref+" 缺少资源文件 "+file)
			continue
		}
		additions = append(additions, effectRegistration{ref, file})
		encodedRows = append(encodedRows, first)
	}
	result := map[string]any{"revision": digest(a.data), "additions": additions, "issues": issues, "message": "检查完成", "path": path}
	if request.Operation != "weapon_effects_apply" {
		return result, nil
	}
	if len(additions) == 0 {
		result["message"] = "没有可自动补齐的攻击特效"
		return result, nil
	}
	// Preserve every other byte of this entry, including unrelated weapon groups.
	groupPattern := regexp.MustCompile(`(?s)<WeaponEffect\b[^>]*\bItemID\s*=\s*["']` + id + `["'][^>]*(?:/>|>.*?</WeaponEffect\s*>)`)
	matches := groupPattern.FindAllStringIndex(text, -1)
	if len(matches) > 1 {
		return nil, fmt.Errorf("当前武器存在重复特效分组，未写入")
	}
	insertion := "\n" + strings.Join(encodedRows, "\n") + "\n"
	updated := ""
	if len(matches) == 1 {
		start, end := matches[0][0], matches[0][1]
		group := text[start:end]
		if strings.HasSuffix(group, "/>") {
			group = strings.TrimSuffix(group, "/>") + ">" + insertion + "</WeaponEffect>"
		} else {
			pos := strings.LastIndex(group, "</WeaponEffect")
			group = group[:pos] + insertion + group[pos:]
		}
		updated = text[:start] + group + text[end:]
	} else {
		pos := strings.LastIndex(text, "</ActEffect")
		if pos < 0 {
			return nil, fmt.Errorf("特效表结构不完整")
		}
		updated = text[:pos] + "\n<WeaponEffect ItemID=\"" + id + "\">" + insertion + "</WeaponEffect>\n" + text[pos:]
	}
	if _, err = parseXML(updated); err != nil {
		return nil, err
	}
	encoded, err := encodeText(updated)
	if err != nil {
		return nil, err
	}
	data, err := a.replace(map[string][]byte{"acteffect.xml": encoded})
	if err != nil {
		return nil, err
	}
	verified, err := parseArchive(data)
	if err != nil {
		return nil, err
	}
	if err = verified.verify(); err != nil {
		return nil, err
	}
	for name := range a.entries {
		if name == "acteffect.xml" {
			continue
		}
		before, e1 := a.raw(name)
		after, e2 := verified.raw(name)
		if e1 != nil || e2 != nil || !bytes.Equal(before, after) {
			return nil, fmt.Errorf("无关配置校验失败：%s", name)
		}
	}
	current, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(current, a.data) {
		return nil, fmt.Errorf("客户端配置已变化，请重新检查特效")
	}
	backup := filepath.Join(folder, "before-effects-"+time.Now().Format("20060102-150405.000000000")+".spf2")
	if err = atomicWrite(backup, current); err != nil {
		return nil, err
	}
	if err = atomicWrite(path, data); err != nil {
		return nil, err
	}
	result["backup"] = backup
	result["message"] = fmt.Sprintf("已补齐 %d 条攻击特效；重启游戏后加载。备份：%s", len(additions), backup)
	return result, nil
}
