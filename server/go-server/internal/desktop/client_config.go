package desktop

// Client configuration plans are local artifacts. No operation here writes to a
// running client, game database, or remote server.
import (
	"archive/zip"
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type clientConfigRequest struct {
	Values       map[string]string   `json:"values"`
	EffectRows   []map[string]string `json:"effect_rows"`
	ResourceRoot string              `json:"resource_root"`
	Base         string              `json:"base"`
	Category     string              `json:"category"`
	File         string              `json:"file"`
	Record       string              `json:"record"`
	Content      string              `json:"content"`
	Name         string              `json:"name"`
	PlanID       string              `json:"plan_id"`
	Revision     string              `json:"revision"`
	Selected     []string            `json:"selected"`
	Resources    []string            `json:"resources"`
	Version      string              `json:"version"`
	Notes        string              `json:"notes"`
	Preview      string              `json:"preview"`
}
type configChange struct {
	File   string `json:"file"`
	Key    string `json:"key"`
	Before string `json:"before"`
	After  string `json:"after"`
}
type configPlan struct {
	ID        string         `json:"id"`
	Category  string         `json:"category"`
	Name      string         `json:"name"`
	BaseHash  string         `json:"base_hash"`
	Changes   []configChange `json:"changes"`
	Resources []string       `json:"resources"`
	Weapon    int            `json:"weapon,omitempty"`
	Rules     []Rule         `json:"rules,omitempty"`
}
type configUnit struct {
	Values     map[string]string   `json:"values"`
	EffectRows []map[string]string `json:"effect_rows,omitempty"`
	Key        string              `json:"key"`
	Label      string              `json:"label"`
	Content    string              `json:"content"`
	node       *xmlNode
	parent     *xmlNode
	index      int
}

var configID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,79}$`)

func configCategory(file string) string {
	switch {
	case file == "item.txt":
		return "items"
	case file == "itemact.txt" || file == "skillproperty.xml" || strings.HasPrefix(file, "animation/"):
		return "weapons"
	case file == "acteffect.xml" || strings.HasPrefix(file, "weaponeffect/"):
		return "effects"
	case file == "mapmgr.xml" || file == "mapselect.xml" || strings.HasPrefix(file, "maps/"):
		return "maps"
	}
	return ""
}

// conditionUstate 返回节点自身 <Condition> 子节点里的状态号（没有则空）。
//
// animation/*.xml 允许同一份 <AnmDesc id> 注册多条，靠 <Condition><Ustate id> 分流：
// 引擎按玩家是否带该状态二选一，这是「状态改变招式形态（段数/伤害）」的标准做法
// （实例：253521 无 406 时 CC 一段 7，有 406 时三段 4/4/4）。
//
// 除此之外，原生表里本来也存在**无条件重名**的历史冗余（实测 2001.xml 里
// 513 三份完全相同、523021 两份只差 hidebody、685 一份带注释一份不带），
// skillproperty.xml 的重复 SkillProId 同理——客户端按文档位置区分，取第一条。
// 所以这里对 AnmDesc 重名一律放行，只负责把键做唯一，不把原生数据当错误。
func conditionUstate(n *xmlNode) string {
	for _, child := range n.children {
		if child.tag != "Condition" {
			continue
		}
		for _, inner := range child.children {
			if strings.EqualFold(inner.tag, "Ustate") {
				if id := strings.TrimSpace(inner.get("id")); id != "" {
					return id
				}
			}
		}
	}
	return ""
}

// configUnitKey 给一个 Xml 节点生成在文件内唯一的键。
//
// 普通节点用 <tag>[属性=值]；同名 AnmDesc 依次尝试「条件号后缀 → 出现序号」，
// 保证每个物理节点都有独立、稳定（按文档顺序）的键，列表里可辨识、可分别编辑。
func configUnitKey(file string, n *xmlNode, base string, taken map[string]bool) string {
	if !strings.HasPrefix(file, "animation/") || n.tag != "AnmDesc" {
		return base
	}
	if cond := conditionUstate(n); cond != "" {
		candidate := base + "#ustate=" + cond
		if !taken[candidate] {
			return candidate
		}
	}
	if !taken[base] {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s#%d", base, i)
		if !taken[candidate] {
			return candidate
		}
	}
}

func configUnits(file, text string) ([]configUnit, *xmlNode, error) {
	out := []configUnit{}
	seen := map[string]bool{}
	// allowDup 列出「原生表允许同键冗余」的两类节点：它们的键由 configUnitKey 做成唯一，
	// 走到 seen 命中说明是同一物理节点被重复加入，跳过即可，不当错误。
	allowDup := func(u configUnit) bool {
		if u.node == nil {
			return false
		}
		if file == "skillproperty.xml" && u.node.tag == "PropertyItem" {
			// 客户端按文档位置区分；编辑器保留第一个。
			return true
		}
		return strings.HasPrefix(file, "animation/") && u.node.tag == "AnmDesc"
	}
	add := func(u configUnit) error {
		if seen[u.Key] {
			if !allowDup(u) {
				return fmt.Errorf("配置 %s 含重复编号：%s", file, u.Key)
			}
			return nil
		}
		u.Values = map[string]string{}
		if file == "item.txt" {
			f := strings.Split(u.Content, "\t")
			for key, index := range map[string]int{"名称": 3, "模型文件": 7, "图标文件": 9, "说明": 16} {
				u.Values[key] = f[index]
			}
		} else if u.node != nil {
			for _, a := range u.node.attrs {
				u.Values[a.Name.Local] = a.Value
			}
			if file == "acteffect.xml" {
				for _, n := range u.node.children {
					if n.tag == "EffectFile" {
						row := map[string]string{}
						for _, a := range n.attrs {
							row[a.Name.Local] = a.Value
						}
						u.EffectRows = append(u.EffectRows, row)
					}
				}
			}
		}
		seen[u.Key] = true
		out = append(out, u)
		return nil
	}
	if strings.HasSuffix(file, ".txt") {
		for i, line := range strings.Split(strings.ReplaceAll(text, "\r", ""), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			f := strings.Split(line, "\t")
			key := f[0]
			label := key
			if file == "item.txt" {
				if len(f) < 17 {
					return nil, nil, fmt.Errorf("道具字段不足")
				}
				key = f[0] + ":" + f[1]
				label = f[3] + " · " + key
			}
			if err := add(configUnit{Key: key, Label: label, Content: line, index: i}); err != nil {
				return nil, nil, err
			}
		}
		return out, nil, nil
	}
	root, err := parseXML(text)
	if err != nil {
		return nil, nil, err
	}
	var visit func(*xmlNode, string) error
	visit = func(parent *xmlNode, prefix string) error {
		for i, n := range parent.children {
			if n.comment {
				continue
			}
			key := ""
			for _, a := range n.attrs {
				switch strings.ToLower(a.Name.Local) {
				case "id", "itemid", "mapid", "skillproid", "level", "mode", "name", "key", "filepath":
					key = n.tag + "[" + a.Name.Local + "=" + a.Value + "]"
				}
				if key != "" {
					break
				}
			}
			// Selection containers are split to individual map entries.
			if file == "mapselect.xml" && (n.tag == "LobbyLevel" || n.tag == "BattleMode") {
				if err := visit(n, prefix+key+"/"); err != nil {
					return err
				}
				continue
			}
			if key != "" {
				s, e := n.serialize()
				if e != nil {
					return e
				}
				label := n.get("Name")
				if label == "" {
					label = n.get("name")
				}
				// 同名 AnmDesc 靠条件号/序号后缀保持键唯一；标签也带上条件，列表里一眼可辨。
				fullKey := configUnitKey(file, n, prefix+key, seen)
				display := label + " · " + prefix + key
				if cond := conditionUstate(n); cond != "" {
					display = label + " · " + prefix + key + "（需拥有状态 " + cond + "）"
				}
				if e = add(configUnit{Key: fullKey, Label: display, Content: s, node: n, parent: parent, index: i}); e != nil {
					return e
				}
			} else {
				if e := visit(n, prefix+n.tag+"/"); e != nil {
					return e
				}
			}
		}
		return nil
	}
	if strings.HasPrefix(file, "maps/") {
		s, e := root.serialize()
		values := map[string]string{}
		for _, child := range root.children {
			if child.tag == "MapList" {
				values["MusicChannel"] = child.get("MusicChannel")
			}
		}
		return []configUnit{{Key: "root", Label: file, Content: s, node: root, Values: values}}, root, e
	}
	if err = visit(root, ""); err != nil {
		return nil, nil, err
	}
	return out, root, nil
}
func configReplace(file, text, key, content string) (string, error) {
	units, root, err := configUnits(file, text)
	if err != nil {
		return "", err
	}
	for _, u := range units {
		if u.Key != key {
			continue
		}
		if root == nil {
			if strings.ContainsAny(content, "\r\n") {
				return "", fmt.Errorf("一项道具/动作表记录必须为单行")
			}
			f := strings.Split(content, "\t")
			old := strings.Split(u.Content, "\t")
			if len(f) != len(old) || f[0] != old[0] || (file == "item.txt" && f[1] != old[1]) {
				return "", fmt.Errorf("不能改变字段数量或编号；新编号请导入新方案")
			}
			lines := strings.Split(strings.ReplaceAll(text, "\r", ""), "\n")
			lines[u.index] = content
			return strings.Join(lines, "\r\n"), nil
		}
		n, e := parseXML(content)
		if e != nil {
			return "", e
		}
		if n.tag != u.node.tag {
			return "", fmt.Errorf("记录类型不一致")
		}
		for _, a := range u.node.attrs {
			switch strings.ToLower(a.Name.Local) {
			case "id", "itemid", "mapid", "level", "mode", "key", "filepath":
				if n.get(a.Name.Local) != a.Value {
					return "", fmt.Errorf("不能修改记录编号")
				}
			}
		}
		if u.parent == nil {
			root = n
		} else {
			u.parent.children[u.index] = n
		}
		s, e := root.serialize()
		return "<?xml version=\"1.0\" encoding=\"gb2312\"?>\n" + s, e
	}
	return "", fmt.Errorf("找不到记录：%s", key)
}
func configPlans(folder string) ([]configPlan, error) {
	out := []configPlan{}
	for _, kind := range []string{"weapons", "effects", "maps", "items"} {
		names, e := filepath.Glob(filepath.Join(folder, kind, "*.json"))
		if e != nil {
			return nil, e
		}
		for _, path := range names {
			b, e := os.ReadFile(path)
			if e != nil {
				return nil, e
			}
			var p configPlan
			if e = json.Unmarshal(b, &p); e != nil {
				return nil, e
			}
			if !configID.MatchString(p.ID) || p.Category != kind {
				return nil, fmt.Errorf("方案格式错误：%s", path)
			}
			out = append(out, p)
		}
	}
	return out, nil
}
func saveConfigPlan(folder string, p configPlan) error {
	dir := filepath.Join(folder, p.Category)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	path := filepath.Join(dir, p.ID+".json")
	if old, err := os.ReadFile(path); err == nil {
		back := filepath.Join(folder, "backups")
		if err = os.MkdirAll(back, 0700); err != nil {
			return err
		}
		if err = atomicWrite(filepath.Join(back, p.ID+"-"+time.Now().Format("20060102-150405.000000000")+".json"), old); err != nil {
			return err
		}
	}
	raw, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, raw)
}
func (admin *Admin) clientConfig(operation string, r clientConfigRequest) (any, error) {
	if operation == "client_config_inspect" || operation == "client_config_extract" {
		return admin.inspectConfig(r, operation == "client_config_extract")
	}
	folder := filepath.Join(admin.Root, "runtime-local", "client-config-plans")
	if err := os.MkdirAll(folder, 0700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(folder, "editing.lock"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("客户端配置正在操作，请稍后重试")
	}
	lock.Close()
	defer os.Remove(filepath.Join(folder, "editing.lock"))
	plans, err := configPlans(folder)
	if err != nil {
		return nil, err
	}
	directory := admin.clientDirectoryValue()
	base := ""
	if directory != "" {
		base = filepath.Join(directory, "Data", "config.spf2")
	}
	if operation == "client_config_plans" {
		return map[string]any{"plans": plans, "folder": folder, "base": base, "resource_root": directory}, nil
	}
	if directory != "" {
		if (r.Base != "" && !strings.EqualFold(filepath.Clean(r.Base), base)) ||
			(r.ResourceRoot != "" && !strings.EqualFold(filepath.Clean(r.ResourceRoot), directory)) {
			return nil, fmt.Errorf("GM 客户端目录已变化，请重新读取配置；当前目录：%s", directory)
		}
		r.Base, r.ResourceRoot = base, directory
	}
	if r.Base == "" {
		return nil, fmt.Errorf("请选择基础 config.spf2")
	}
	a, err := loadArchive(r.Base)
	if err != nil {
		return nil, err
	}
	if err = a.verify(); err != nil {
		return nil, err
	}
	if operation == "client_config_catalog" {
		files := []string{}
		for f := range a.entries {
			if configCategory(f) == r.Category {
				files = append(files, f)
			}
		}
		sort.Strings(files)
		return map[string]any{"files": files, "revision": digest(a.data), "plans": plans, "folder": folder}, nil
	}
	if operation == "client_config_records" {
		if configCategory(r.File) == "" {
			return nil, fmt.Errorf("不支持的配置分类")
		}
		t, e := a.text(r.File)
		if e != nil {
			return nil, e
		}
		for _, p := range plans {
			for _, change := range p.Changes {
				if change.File == r.File {
					units, _, err := configUnits(r.File, t)
					if err != nil {
						return nil, err
					}
					for _, u := range units {
						if u.Key == change.Key && u.Content == change.Before {
							t, err = configReplace(r.File, t, change.Key, change.After)
							if err != nil {
								return nil, err
							}
						}
					}
				}
			}
		}
		units, _, e := configUnits(r.File, t)
		return map[string]any{"records": units, "revision": digest(a.data)}, e
	}
	if operation == "client_config_save" {
		if r.Revision != digest(a.data) {
			return nil, fmt.Errorf("基础文件已变化，请重新加载")
		}
		if strings.TrimSpace(r.Name) == "" {
			return nil, fmt.Errorf("请输入方案名称")
		}
		kind := configCategory(r.File)
		if kind == "" || kind != r.Category {
			return nil, fmt.Errorf("分类不匹配")
		}
		text, e := a.text(r.File)
		if e != nil {
			return nil, e
		}
		units, _, e := configUnits(r.File, text)
		if e != nil {
			return nil, e
		}
		var before string
		for _, u := range units {
			if u.Key == r.Record {
				before = u.Content
			}
		}
		if before == "" {
			return nil, fmt.Errorf("记录不存在")
		}
		if len(r.Values) > 0 || r.EffectRows != nil {
			r.Content = before
			if r.File == "item.txt" {
				f := strings.Split(before, "\t")
				for key, index := range map[string]int{"名称": 3, "模型文件": 7, "图标文件": 9, "说明": 16} {
					if value, ok := r.Values[key]; ok {
						if strings.ContainsAny(value, "\t\r\n") {
							return nil, fmt.Errorf("字段不能含换行或制表符")
						}
						f[index] = value
					}
				}
				r.Content = strings.Join(f, "\t")
			} else {
				n, e := parseXML(before)
				if e != nil {
					return nil, e
				}
				for key, value := range r.Values {
					if strings.HasPrefix(r.File, "maps/") && key == "MusicChannel" {
						if _, err := strconv.ParseUint(value, 10, 16); err != nil {
							return nil, fmt.Errorf("音乐频道必须为数字")
						}
						for _, child := range n.children {
							if child.tag == "MapList" {
								child.set(key, value)
							}
						}
					} else {
						n.set(key, value)
					}
				}
				if r.File == "acteffect.xml" && r.EffectRows != nil {
					children := []*xmlNode{}
					for _, child := range n.children {
						if child.tag != "EffectFile" {
							children = append(children, child)
						}
					}
					ids := map[string]bool{}
					for _, row := range r.EffectRows {
						id := row["EffectId"]
						if id == "" || row["File"] == "" || ids[id] {
							return nil, fmt.Errorf("特效编号、文件不能为空且不可重复")
						}
						ids[id] = true
						child := &xmlNode{tag: "EffectFile"}
						keys := []string{}
						for key := range row {
							keys = append(keys, key)
						}
						sort.Strings(keys)
						for _, key := range keys {
							child.set(key, row[key])
						}
						children = append(children, child)
					}
					n.children = children
				}
				r.Content, e = n.serialize()
				if e != nil {
					return nil, e
				}
			}
		}
		updated, e := configReplace(r.File, text, r.Record, r.Content)
		if e != nil {
			return nil, e
		}
		check, _, e := configUnits(r.File, updated)
		if e != nil {
			return nil, e
		}
		after := ""
		for _, u := range check {
			if u.Key == r.Record {
				after = u.Content
			}
		}
		if before == after {
			return nil, fmt.Errorf("内容未修改")
		}
		id := r.PlanID
		if id == "" {
			id = kind + "-" + digest([]byte(r.File + "/" + r.Record))[:16]
		}
		if !configID.MatchString(id) {
			return nil, fmt.Errorf("方案编号无效")
		}
		p := configPlan{ID: id, Category: kind, Name: r.Name, BaseHash: digest(a.data), Changes: []configChange{{r.File, r.Record, before, after}}, Resources: r.Resources}
		if e = saveConfigPlan(folder, p); e != nil {
			return nil, e
		}
		return map[string]any{"message": "方案已单独保存，未修改客户端", "plan": p}, nil
	}
	if operation == "client_config_import_weapons" {
		b, e := os.ReadFile(filepath.Join(admin.Root, "runtime-local", "weapon-config", "settings.json"))
		if e != nil {
			return nil, e
		}
		var state weaponState
		if e = json.Unmarshal(b, &state); e != nil {
			return nil, e
		}
		if state.SourceHash != "" && state.SourceHash != digest(a.data) {
			return nil, fmt.Errorf("武器方案基准与所选文件不同，请先在武器编辑器核对；不会强行套用旧方案")
		}
		count := 0
		for id, rules := range state.Drafts {
			n, e := strconv.Atoi(id)
			if e != nil {
				return nil, e
			}
			p := configPlan{ID: "weapon-" + id, Category: "weapons", Name: "武器 " + id, BaseHash: digest(a.data), Weapon: n, Rules: rules}
			if e = saveConfigPlan(folder, p); e != nil {
				return nil, e
			}
			count++
		}
		return map[string]any{"message": fmt.Sprintf("已导入 %d 把武器，各自独立保存", count)}, nil
	}
	if operation == "client_config_preview" || operation == "client_config_build" {
		return admin.buildClientConfig(folder, a, r, plans, operation == "client_config_build")
	}
	return nil, fmt.Errorf("未知客户端配置操作")
}

func configSafeFile(root, rel string) (string, error) {
	rel = strings.ReplaceAll(strings.TrimPrefix(strings.TrimSpace(rel), ".\\"), "\\", "/")
	if !strings.HasPrefix(strings.ToLower(rel), "data/") || !filepath.IsLocal(filepath.FromSlash(rel)) || strings.Contains(rel, ":") {
		return "", fmt.Errorf("资源须使用游戏内 Data/ 相对路径：%s", rel)
	}
	for _, part := range strings.Split(rel, "/") {
		if part == ".." {
			return "", fmt.Errorf("资源路径不能包含上级目录：%s", rel)
		}
	}
	path := filepath.Join(root, filepath.FromSlash(rel))
	real, e := filepath.EvalSymlinks(path)
	if e != nil {
		return "", e
	}
	base, e := filepath.EvalSymlinks(root)
	if e != nil {
		return "", e
	}
	inside, e := filepath.Rel(base, real)
	if e != nil || !filepath.IsLocal(inside) {
		return "", fmt.Errorf("资源路径越界：%s", rel)
	}
	return real, nil
}
func (admin *Admin) buildClientConfig(folder string, base *archive, r clientConfigRequest, plans []configPlan, build bool) (any, error) {
	if len(r.Selected) == 0 {
		return nil, fmt.Errorf("请勾选要合入的方案")
	}
	selected := map[string]bool{}
	for _, id := range r.Selected {
		if selected[id] {
			return nil, fmt.Errorf("重复选择方案")
		}
		selected[id] = true
	}
	chosen := []configPlan{}
	for _, p := range plans {
		if selected[p.ID] {
			chosen = append(chosen, p)
			delete(selected, p.ID)
		}
	}
	if len(selected) > 0 {
		return nil, fmt.Errorf("方案已不存在，请刷新")
	}
	sort.Slice(chosen, func(i, j int) bool { return chosen[i].ID < chosen[j].ID })
	a := base
	weapons := map[string][]Rule{}
	weaponIDs := map[int]bool{}
	resources := append([]string{}, r.Resources...)
	changes := []string{}
	claimed := map[string]string{}
	itemText, err := a.text("item.txt")
	if err != nil {
		return nil, err
	}
	items := []Item{}
	for _, line := range strings.Split(strings.ReplaceAll(itemText, "\r", ""), "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 17 {
			continue
		}
		kind, _ := strconv.Atoi(f[0])
		id, _ := strconv.Atoi(f[1])
		items = append(items, Item{Key: f[0] + ":" + f[1], ID: uint32(id), Kind: byte(kind), Name: f[3], Fields: f})
	}
	for _, p := range chosen {
		resources = append(resources, p.Resources...)
		if p.Weapon != 0 {
			if p.BaseHash != digest(base.data) {
				return nil, fmt.Errorf("武器方案 %s 的基准已变化，需重新核对", p.Name)
			}
			key := strconv.Itoa(p.Weapon)
			if _, ok := weapons[key]; ok {
				return nil, fmt.Errorf("重复武器方案：%s", key)
			}
			weapons[key] = p.Rules
			weaponIDs[p.Weapon] = true
			changes = append(changes, "武器："+key)
		}
	}
	if len(weapons) > 0 {
		// configPlan carries rules only; per-weapon property clones belong to the
		// weapon editor state and are absent from a published plan.
		raw, e := render(a, items, weapons, nil)
		if e != nil {
			return nil, e
		}
		a, e = parseArchive(raw)
		if e != nil {
			return nil, e
		}
	}
	for _, p := range chosen {
		for _, c := range p.Changes {
			if configCategory(c.File) != p.Category {
				return nil, fmt.Errorf("方案分类与记录不匹配：%s", p.Name)
			}
			key := c.File + "/" + c.Key
			if name, ok := claimed[key]; ok {
				return nil, fmt.Errorf("方案冲突：%s 与 %s 同时修改 %s", name, p.Name, key)
			}
			claimed[key] = p.Name
			text, e := a.text(c.File)
			if e != nil {
				return nil, e
			}
			units, _, e := configUnits(c.File, text)
			if e != nil {
				return nil, e
			}
			current := ""
			for _, u := range units {
				if u.Key == c.Key {
					current = u.Content
				}
			}
			if current != c.Before && current != c.After {
				return nil, fmt.Errorf("基准记录已变化：%s，未合并方案 %s", key, p.Name)
			}
			if current != c.After {
				text, e = configReplace(c.File, text, c.Key, c.After)
				if e != nil {
					return nil, e
				}
				raw, e := encodeText(text)
				if e != nil {
					return nil, e
				}
				data, e := a.replace(map[string][]byte{c.File: raw})
				if e != nil {
					return nil, e
				}
				a, e = parseArchive(data)
				if e != nil {
					return nil, e
				}
			}
			changes = append(changes, p.Name+"："+key)
			if c.File == "item.txt" {
				f := strings.Split(c.After, "\t")
				if len(f) > 1 && f[0] == "25" {
					id, _ := strconv.Atoi(f[1])
					weaponIDs[id] = true
				}
			}
			if c.File == "mapmgr.xml" {
				n, e := parseXML(c.After)
				if e != nil {
					return nil, e
				}
				if world := n.get("worldpath"); world != "" {
					resources = append(resources, "Data/Map/"+world)
				}
				if xml := n.get("xmlfile"); xml != "" {
					if _, ok := a.entries["maps/"+strings.ToLower(xml)+".xml"]; !ok {
						return nil, fmt.Errorf("地图场景配置缺失：%s", xml)
					}
				}
				if pic := n.get("PicName"); pic != "" {
					resources = append(resources, "Data/UI/"+pic)
				}
			}
			if c.File == "acteffect.xml" {
				n, e := parseXML(c.After)
				if e != nil {
					return nil, e
				}
				id, _ := strconv.Atoi(n.get("ItemID"))
				if id > 0 {
					weaponIDs[id] = true
				}
				n.walk(func(n *xmlNode) {
					if n.tag == "EffectFile" && n.get("File") != "" {
						resources = append(resources, "Data/effect/effect/"+n.get("File"))
					}
				})
			}
		}
	}
	if len(weaponIDs) > 0 {
		info, e := inspect(a, items)
		if e != nil {
			return nil, e
		}
		root, e := a.xml("acteffect.xml")
		if e != nil {
			return nil, e
		}
		known := map[string]*xmlNode{}
		ambiguous := map[string]bool{}
		for _, g := range root.children {
			if g.tag != "WeaponEffect" {
				continue
			}
			for _, n := range g.children {
				if n.tag != "EffectFile" {
					continue
				}
				id := n.get("EffectId")
				if old := known[id]; old != nil {
					x, _ := old.serialize()
					y, _ := n.serialize()
					if x != y {
						ambiguous[id] = true
					}
				} else {
					known[id] = n
				}
			}
		}
		for _, w := range info.weapons {
			if !weaponIDs[w.ID] {
				continue
			}
			id := strconv.Itoa(w.ID)
			var group *xmlNode
			loaded := map[string]*xmlNode{}
			for _, g := range root.children {
				if g.tag == "WeaponEffect" && (g.get("ItemID") == id || g.get("ItemID") == "0") {
					if g.get("ItemID") == id {
						group = g
					}
					for _, n := range g.children {
						if loaded[n.get("EffectId")] == nil || g.get("ItemID") == id {
							loaded[n.get("EffectId")] = n
						}
					}
				}
			}
			if group == nil {
				group = &xmlNode{tag: "WeaponEffect"}
				group.set("ItemID", id)
				root.children = append(root.children, group)
			}
			for _, s := range w.Stages {
				variants := info.blocks[actionKey(s.Action)]
				if len(variants) == 0 {
					return nil, fmt.Errorf("武器 %d 动作 %s 无法解析", w.ID, s.Action)
				}
				refs := map[string]bool{}
				// 条件分支块引用的动画/特效也要一起打包，否则带上状态时那一段
				// 会指向客户端里不存在的资源。
				for _, blk := range variants {
					blk.node.walk(func(n *xmlNode) {
						if n.tag == "Anm" {
							value := n.get("name")
							if v, err := strconv.ParseUint(value, 10, 32); err == nil && v > 0 {
								resources = append(resources, "Data/animation/"+value+".anm")
							}
						}

						for _, attr := range n.attrs {
							if strings.EqualFold(attr.Name.Local, "effectid") && strings.TrimSpace(attr.Value) != "" && attr.Value != "0" {
								refs[attr.Value] = true
							}
						}
					})
				}
				refsSorted := []string{}
				for ref := range refs {
					refsSorted = append(refsSorted, ref)
				}
				sort.Strings(refsSorted)
				for _, ref := range refsSorted {
					n := loaded[ref]
					if n == nil {
						n = known[ref]
					}
					if n == nil {
						return nil, fmt.Errorf("武器 %d 缺少特效登记 %s", w.ID, ref)
					}
					if loaded[ref] == nil {
						if ambiguous[ref] {
							return nil, fmt.Errorf("特效 %s 存在不同资源绑定，请先核对", ref)
						}
						group.children = append(group.children, n.clone())
						loaded[ref] = n
						changes = append(changes, "补齐武器 "+id+" 特效 "+ref)
					}
					resources = append(resources, "Data/effect/effect/"+n.get("File"))
				}
			}
		}
		text, e := root.serialize()
		if e != nil {
			return nil, e
		}
		raw, e := encodeText("<?xml version=\"1.0\" encoding=\"gb2312\"?>\n" + text)
		if e != nil {
			return nil, e
		}
		data, e := a.replace(map[string][]byte{"acteffect.xml": raw})
		if e != nil {
			return nil, e
		}
		a, e = parseArchive(data)
		if e != nil {
			return nil, e
		}
	}
	client := r.ResourceRoot
	if client == "" {
		client = filepath.Dir(filepath.Dir(r.Base))
	}
	payload := map[string][]byte{}
	total := 0
	var add func(string) error
	add = func(rel string) error {
		rel = strings.ReplaceAll(rel, "\\", "/")
		if strings.EqualFold(rel, "Data/config.spf2") {
			return fmt.Errorf("不能用资源覆盖生成的 config.spf2")
		}
		for old := range payload {
			if strings.EqualFold(old, rel) {
				return nil
			}
		}
		path, e := configSafeFile(client, rel)
		if e != nil {
			return fmt.Errorf("缺少资源 %s：%w", rel, e)
		}
		stat, e := os.Stat(path)
		if e != nil {
			return e
		}
		if stat.IsDir() {
			return filepath.WalkDir(path, func(p string, d os.DirEntry, e error) error {
				if e != nil {
					return e
				}
				if d.Type()&os.ModeSymlink != 0 {
					return fmt.Errorf("资源目录不允许链接：%s", p)
				}
				if d.IsDir() {
					return nil
				}
				child, e := filepath.Rel(path, p)
				if e != nil {
					return e
				}
				return add(rel + "/" + filepath.ToSlash(child))
			})
		}
		if stat.Size() > 256<<20 {
			return fmt.Errorf("资源超过256MiB：%s", rel)
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".exe", ".bat", ".cmd", ".ps1":
			return fmt.Errorf("资源目录含程序文件：%s", rel)
		}
		raw, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		total += len(raw)
		if total > 400<<20 {
			return fmt.Errorf("资源包超过400MiB，请分批生成")
		}
		payload[rel] = raw
		return nil
	}
	for _, rel := range resources {
		if strings.TrimSpace(rel) != "" {
			if e := add(rel); e != nil {
				return nil, e
			}
		}
	}
	// Follow explicit file references in effects/models and XML resources.
	refPattern := regexp.MustCompile(`(?i)[a-z0-9_./\\-]+\.(?:dds|tga|png|bmp|dff|anm|rws|bsp|wav|mp3|ogg)`)
	scanned := map[string]bool{}
	var resourceIndex map[string][]string
	for {
		pending := []string{}
		for rel := range payload {
			if !scanned[rel] {
				pending = append(pending, rel)
			}
		}
		if len(pending) == 0 {
			break
		}
		sort.Strings(pending)
		for _, rel := range pending {
			scanned[rel] = true
			ext := strings.ToLower(filepath.Ext(rel))
			if ext == ".png" || ext == ".dds" || ext == ".tga" || ext == ".bmp" || ext == ".wav" || ext == ".mp3" || ext == ".ogg" {
				continue
			}
			for _, match := range refPattern.FindAll(payload[rel], -1) {
				ref := strings.ReplaceAll(string(match), "\\", "/")
				candidate := ref
				if !strings.HasPrefix(strings.ToLower(ref), "data/") {
					candidate = filepath.ToSlash(filepath.Join(filepath.Dir(filepath.FromSlash(rel)), filepath.FromSlash(ref)))
				}
				if _, e := configSafeFile(client, candidate); e == nil {
					if e = add(candidate); e != nil {
						return nil, e
					}
					continue
				}
				if resourceIndex == nil {
					resourceIndex = map[string][]string{}
					e := filepath.WalkDir(filepath.Join(client, "Data"), func(path string, d os.DirEntry, e error) error {
						if e != nil {
							return e
						}
						if d.IsDir() {
							return nil
						}
						relative, e := filepath.Rel(client, path)
						if e != nil {
							return e
						}
						key := strings.ToLower(d.Name())
						resourceIndex[key] = append(resourceIndex[key], filepath.ToSlash(relative))
						return nil
					})
					if e != nil {
						return nil, e
					}
				}
				candidates := resourceIndex[strings.ToLower(filepath.Base(ref))]
				if len(candidates) == 0 {
					return nil, fmt.Errorf("资源 %s 缺少依赖 %s", rel, ref)
				}
				if len(candidates) > 1 {
					return nil, fmt.Errorf("资源 %s 的依赖 %s 有多个同名文件，请使用明确资源路径", rel, ref)
				}
				if e := add(candidates[0]); e != nil {
					return nil, e
				}
			}
		}
	}
	md, e := a.xml("animationmd5.xml")
	if e != nil {
		return nil, e
	}
	knownMD := map[string]*xmlNode{}
	md.walk(func(n *xmlNode) {
		if n.get("FilePath") != "" {
			key := strings.ToLower(strings.TrimPrefix(strings.ReplaceAll(n.get("FilePath"), "\\", "/"), "./"))
			knownMD[key] = n
		}
	})
	var mapMD *xmlNode
	for _, n := range md.children {
		if strings.Contains(strings.ToLower(n.tag), "map") {
			mapMD = n
		}
	}
	mdChanged := false
	payloadSorted := []string{}
	for rel := range payload {
		payloadSorted = append(payloadSorted, rel)
	}
	sort.Strings(payloadSorted)
	for _, rel := range payloadSorted {
		raw := payload[rel]
		key := strings.ToLower(rel)
		n := knownMD[key]
		if n == nil && strings.HasPrefix(key, "data/map/") && (strings.HasSuffix(key, ".bsp") || strings.HasSuffix(key, ".rws")) {
			if mapMD == nil {
				return nil, fmt.Errorf("缺少地图MD5节点，请核对基础包")
			}
			n = &xmlNode{tag: "Item"}
			n.set("FilePath", ".\\"+strings.ToUpper(strings.ReplaceAll(rel, "/", "\\")))
			mapMD.children = append(mapMD.children, n)
		}
		if n != nil {
			sum := md5.Sum(raw)
			n.set("MD5", hex.EncodeToString(sum[:]))
			mdChanged = true
		}
	}
	if mdChanged {
		text, e := md.serialize()
		if e != nil {
			return nil, e
		}
		raw, e := encodeText(text)
		if e != nil {
			return nil, e
		}
		data, e := a.replace(map[string][]byte{"animationmd5.xml": raw})
		if e != nil {
			return nil, e
		}
		a, e = parseArchive(data)
		if e != nil {
			return nil, e
		}
		changes = append(changes, "已按打包文件更新资源MD5校验")
	}
	if e = a.verify(); e != nil {
		return nil, e
	}
	payload["Data/config.spf2"] = a.data
	hashes := map[string]string{}
	for name, raw := range payload {
		hashes[name] = digest(raw)
	}
	proof, _ := json.Marshal([]any{digest(base.data), chosen, hashes, r.Version, r.Notes})
	preview := digest(proof)
	result := map[string]any{"preview": preview, "changes": changes, "files": hashes, "config_hash": digest(a.data), "message": "合并检查通过；原文件未修改"}
	if !build {
		return result, nil
	}
	if r.Preview != preview {
		return nil, fmt.Errorf("方案或资源已变化，请重新预览后打包")
	}
	if !configID.MatchString(r.Version) {
		return nil, fmt.Errorf("版本号仅允许字母、数字、点、横线、下划线")
	}
	dest := filepath.Join(folder, "releases", r.Version)
	if _, e = os.Stat(dest); !os.IsNotExist(e) {
		return nil, fmt.Errorf("版本已存在，请换新版本号")
	}
	if e = os.MkdirAll(filepath.Dir(dest), 0700); e != nil {
		return nil, e
	}
	temp, e := os.MkdirTemp(filepath.Dir(dest), "building-")
	if e != nil {
		return nil, e
	}
	defer os.RemoveAll(temp)
	if e = atomicWrite(filepath.Join(temp, "base.spf2"), base.data); e != nil {
		return nil, e
	}
	if e = atomicWrite(filepath.Join(temp, "config.spf2"), a.data); e != nil {
		return nil, e
	}
	pbytes, _ := json.MarshalIndent(chosen, "", "  ")
	if e = atomicWrite(filepath.Join(temp, "plans.json"), pbytes); e != nil {
		return nil, e
	}
	zipData, e := configOSSZip(r.Version, r.Notes, payload)
	if e != nil {
		return nil, e
	}
	if e = atomicWrite(filepath.Join(temp, "OSS上传包-"+r.Version+".zip"), zipData); e != nil {
		return nil, e
	}
	report, _ := json.MarshalIndent(result, "", "  ")
	if e = atomicWrite(filepath.Join(temp, "validation.json"), report); e != nil {
		return nil, e
	}
	if e = os.Rename(temp, dest); e != nil {
		return nil, e
	}
	result["zip"] = filepath.Join(dest, "OSS上传包-"+r.Version+".zip")
	result["folder"] = dest
	result["message"] = "OSS包已生成，未上传；基础文件及方案快照已保留"
	return result, nil
}

type configOSSRow struct {
	Path   string `json:"path"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int    `json:"size"`
}
type configOSSManifest struct {
	Version    string         `json:"version"`
	Target     string         `json:"target"`
	Notes      string         `json:"notes"`
	ConfigHash string         `json:"config_hash,omitempty"`
	Files      []configOSSRow `json:"files"`
}

const configOSSBase = "https://openkfo.oss-cn-hangzhou.aliyuncs.com/"

func configOSSRead(path string, out any) error {
	if !strings.HasPrefix(path, configOSSBase) {
		return fmt.Errorf("OSS清单地址不匹配")
	}
	client := http.Client{Timeout: 20 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if !strings.HasPrefix(req.URL.String(), configOSSBase) {
			return fmt.Errorf("OSS重定向地址不匹配")
		}
		if len(via) > 3 {
			return fmt.Errorf("重定向过多")
		}
		return nil
	}}
	response, e := client.Get(path)
	if e != nil {
		return e
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("读取OSS清单失败：HTTP %d", response.StatusCode)
	}
	raw, e := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if e != nil {
		return e
	}
	if len(raw) > 1<<20 {
		return fmt.Errorf("清单过大")
	}
	return json.Unmarshal(raw, out)
}
func configOSSZip(version, notes string, payload map[string][]byte) ([]byte, error) {
	var pointer map[string]string
	if e := configOSSRead(configOSSBase+"version/version.json", &pointer); e != nil {
		return nil, e
	}
	if pointer["version"] == version {
		return nil, fmt.Errorf("该版本已发布，请使用新版本号")
	}
	var launcher, client configOSSManifest
	if e := configOSSRead(pointer["manifest"], &launcher); e != nil {
		return nil, e
	}
	if e := configOSSRead(pointer["client_manifest"], &client); e != nil {
		return nil, e
	}
	if launcher.Target != "launcher" || client.Target != "client" || launcher.Version != pointer["version"] || client.Version != pointer["version"] {
		return nil, fmt.Errorf("线上清单版本不一致")
	}
	launcher.Version = version
	launcher.Notes = notes
	client.Version = version
	client.Notes = notes
	client.ConfigHash = digest(payload["Data/config.spf2"])
	rows := map[string]configOSSRow{}
	for _, row := range client.Files {
		key := strings.ToLower(strings.ReplaceAll(row.Path, "\\", "/"))
		if _, exists := rows[key]; exists {
			return nil, fmt.Errorf("线上清单含重复路径：%s", row.Path)
		}
		rows[key] = row
	}
	files := map[string][]byte{}
	for name, raw := range payload {
		key := "releases/" + version + "/client/" + name
		files[key] = raw
		u := url.URL{Scheme: "https", Host: "openkfo.oss-cn-hangzhou.aliyuncs.com", Path: "/" + key}
		rows[strings.ToLower(name)] = configOSSRow{name, u.String(), digest(raw), len(raw)}
	}
	client.Files = nil
	names := []string{}
	for name := range rows {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		client.Files = append(client.Files, rows[name])
	}
	for _, m := range []configOSSManifest{launcher, client} {
		b, e := json.MarshalIndent(m, "", "  ")
		if e != nil {
			return nil, e
		}
		files["manifest/"+version+"/"+m.Target+".json"] = b
	}
	p := map[string]string{"version": version, "manifest": configOSSBase + "manifest/" + version + "/launcher.json", "client_manifest": configOSSBase + "manifest/" + version + "/client.json"}
	b, _ := json.MarshalIndent(p, "", "  ")
	files["version/version.json"] = b
	var buffer bytes.Buffer
	z := zip.NewWriter(&buffer)
	keys := []string{}
	for key := range files {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		w, e := z.Create(key)
		if e != nil {
			return nil, e
		}
		if _, e = w.Write(files[key]); e != nil {
			return nil, e
		}
	}
	if e := z.Close(); e != nil {
		return nil, e
	}
	return buffer.Bytes(), nil
}
