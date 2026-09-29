package desktop

// 发版包导出：把「一把自制武器」需要的全部东西打成 zip。
//
// zip 里的路径就是客户端根目录下的相对路径（Data/config.spf2、Data/Weapon/Model/...），
// 拿到包的人只要**整包解压、覆盖到客户端根目录**即可，不需要手工挑文件、也不需要
// 知道哪个素材放在哪。以前只有 weapon_publish：它只把 Data/config.spf2 一个文件
// 传上去，自制武器一旦带自己的模型/贴图/动作，玩家端就缺文件。
//
// 包里除了文件，还写一份「安装说明.txt」，写清用法和清单，方便交给运维。

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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

// 可勾选的内容分组。缺省全开：导包的目的就是"别漏东西"。
var packageSections = []string{"config", "model", "texture", "icon", "animation", "audio", "effect"}

var packageSectionLabels = map[string]string{
	"config":    "配置包",
	"model":     "模型",
	"texture":   "贴图",
	"icon":      "图标",
	"animation": "动作",
	"audio":     "音效",
	"effect":    "特效",
}

type packageFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	Kind string `json:"kind"`
}

type packageResult struct {
	Path    string           `json:"path"`
	Name    string           `json:"name"`
	Size    int64            `json:"size"`
	SHA256  string           `json:"sha256"`
	Files   []packageFile    `json:"files"`
	Missing []string         `json:"missing"`
	Config  []string         `json:"config_changes"`
	Weapons []map[string]any `json:"weapons"`
	// Plans 列出配置包里实际带着方案的武器：配置是整包发的，别的武器
	// 已保存/已应用的方案也会一起进包，写清楚免得事后猜测。
	Plans     []map[string]any `json:"plans"`
	Counts    map[string]int   `json:"counts"`
	Generated string           `json:"generated"`
}

var anmIDPattern = regexp.MustCompile(`\bid\s*=\s*"?([0-9A-Za-z_]+)"?`)
var lineIDPattern = regexp.MustCompile(`lineid\s*=\s*"?([0-9A-Za-z_]+)"?`)
var audioIDPattern = regexp.MustCompile(`audioid\s*=\s*"?([0-9A-Za-z_]+)"?`)
var effectIDPattern = regexp.MustCompile(`effectid\s*=\s*"?([0-9A-Za-z_]+)"?`)

// weaponPackage renders the current edit set and packs it together with every
// asset the given weapon(s) reference.
func weaponPackage(request Request, client string, folder string, source, base *archive, items []Item, state *weaponState, info *inspection, plans map[string][]Rule) (any, error) {
	if request.Weapon == 0 && !request.All {
		return nil, fmt.Errorf("请指定要导出的武器")
	}
	include := map[string]bool{}
	for _, section := range packageSections {
		include[section] = true
	}
	if len(request.Include) > 0 {
		for _, section := range packageSections {
			include[section] = false
		}
		for _, section := range request.Include {
			if _, ok := include[section]; !ok {
				return nil, fmt.Errorf("未知的导出分组：%s", section)
			}
			include[section] = true
		}
	}

	// 同一个理由：先翻译/校验连招限制，再渲染，包里才不会带死编号。
	if changed, err := reconcileComboRules(info, state); err != nil {
		return nil, err
	} else if changed {
		if base, err = applyComboRules(base, state.ComboRules); err != nil {
			return nil, err
		}
	}
	// 渲染一份"将要发版"的配置。用基线 + 当前编辑集渲染，不要求本地客户端
	// 处于已同步状态：发版包描述的是设计结果，不该被本机客户端的临时状态卡住。
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
	if err = checkAllowedWrites(source, rendered, state, info); err != nil {
		return nil, err
	}
	changed, err := changedEntries(source, rendered)
	if err != nil {
		return nil, err
	}

	ids, err := packageWeaponIDs(request, state, info)
	if err != nil {
		return nil, err
	}
	weapons := []map[string]any{}
	for _, id := range ids {
		label := strconv.Itoa(id)
		for _, weapon := range info.weapons {
			if weapon.ID == id {
				label = weapon.Name
				break
			}
		}
		weapons = append(weapons, map[string]any{"id": id, "name": label})
	}

	files, missing := collectWeaponAssets(client, rendered, items, ids, include)
	root := filepath.Dir(filepath.Dir(folder))
	name := "weapon-" + strconv.Itoa(ids[0]) + "-" + time.Now().Format("20060102-150405") + ".zip"
	if len(ids) > 1 || request.All {
		name = "weapons-" + time.Now().Format("20060102-150405") + ".zip"
	}
	out := filepath.Join(root, "dist", "weapon-packages", name)
	size, sum, err := writePackageZip(out, client, data, files, changed, weapons, ids)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, file := range files {
		counts[file.Kind]++
	}
	planned := []map[string]any{}
	keys := make([]string, 0, len(plans))
	for key := range plans {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		label := key
		if weapon, ok := weaponByID(info, key); ok {
			label = weapon.Name
		}
		planned = append(planned, map[string]any{"id": key, "name": label})
	}
	return packageResult{
		Path:      out,
		Name:      filepath.Base(out),
		Size:      size,
		SHA256:    sum,
		Files:     files,
		Missing:   missing,
		Config:    changed,
		Weapons:   weapons,
		Plans:     planned,
		Counts:    counts,
		Generated: time.Now().Format("2006-01-02 15:04:05"),
	}, nil
}

func packageWeaponIDs(request Request, state *weaponState, info *inspection) ([]int, error) {
	if !request.All {
		_, created := state.Created[strconv.Itoa(request.Weapon)]
		if !infoHasWeapon(info, request.Weapon) && !created {
			return nil, fmt.Errorf("配置包里没有这把武器：%d", request.Weapon)
		}
		return []int{request.Weapon}, nil
	}
	ids := []int{}
	seen := map[int]bool{}
	for key := range state.Created {
		id, err := strconv.Atoi(key)
		if err != nil || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	sort.Ints(ids)
	if len(ids) == 0 {
		return nil, fmt.Errorf("还没有自建武器可以导出")
	}
	return ids, nil
}

func infoHasWeapon(info *inspection, id int) bool {
	for _, weapon := range info.weapons {
		if weapon.ID == id {
			return true
		}
	}
	return false
}

// changedEntries lists the archive entries the edit set actually rewrote, so the
// manifest can show that nothing unexpected is being shipped.
func changedEntries(source, rendered *archive) ([]string, error) {
	names := make([]string, 0, len(source.entries))
	for name := range source.entries {
		names = append(names, name)
	}
	sort.Strings(names)
	changed := []string{}
	for _, name := range names {
		before, err := source.raw(name)
		if err != nil {
			return nil, err
		}
		after, err := rendered.raw(name)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(before, after) {
			changed = append(changed, name)
		}
	}
	return changed, nil
}

// collectWeaponAssets walks everything one weapon needs. Paths are stored the
// way the client lays them out, with forward slashes, so unzipping over the
// client root lands every file where the game expects it.
func collectWeaponAssets(client string, rendered *archive, items []Item, ids []int, include map[string]bool) ([]packageFile, []string) {
	files := []packageFile{}
	missing := []string{}
	seen := map[string]bool{}
	// required 表示"这东西缺了武器就用不了"，找不到要报给用户；
	// 其余（贴图、音效、特效、_s 简版模型）是"能带上就带上"，
	// 客户端里没有就静默跳过——它们的编号并不总能推出文件名。
	add := func(relative, kind string, required bool) bool {
		relative = strings.ReplaceAll(relative, "\\", "/")
		relative = strings.TrimPrefix(relative, "/")
		if relative == "" || seen[relative] {
			return false
		}
		if strings.Contains(relative, "..") || strings.Contains(relative, ":") {
			if required {
				missing = append(missing, relative+"（路径不安全，已跳过）")
			}
			return false
		}
		absolute := filepath.Join(client, filepath.FromSlash(relative))
		rel, err := filepath.Rel(client, absolute)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			if required {
				missing = append(missing, relative+"（路径越界，已跳过）")
			}
			return false
		}
		stat, err := os.Stat(absolute)
		if err != nil || stat.IsDir() {
			if required {
				missing = append(missing, relative+"（客户端里找不到）")
			}
			return false
		}
		seen[relative] = true
		files = append(files, packageFile{Path: filepath.ToSlash(relative), Size: stat.Size(), Kind: kind})
		return true
	}

	for _, id := range ids {
		number := strconv.Itoa(id)
		// 图标必须取 item.txt 的原始列：Weapon.Icon 是 GM 解析出来的
		// **缓存绝对路径**，拿它拼客户端路径会得到一串很荒唐的 zip 条目。
		model := ""
		icon := ""
		for _, item := range items {
			if int(item.ID) == id {
				if len(item.Fields) >= 8 {
					model = strings.TrimSpace(item.Fields[7])
				}
				if len(item.Fields) >= 10 {
					icon = strings.TrimSpace(item.Fields[9])
				}
			}
		}
		if include["model"] && model != "" && model != "#" {
			add(filepath.ToSlash(filepath.Join("Data", "Weapon", "Model", filepath.Base(model))), "model", true)
			if stem := strings.TrimSuffix(filepath.Base(model), filepath.Ext(model)); stem != "" {
				// 不少武器还有一个 _s 的简版模型，能带上就带上。
				add(filepath.ToSlash(filepath.Join("Data", "Weapon", "Model", stem+"_s.dff")), "model", false)
			}
		}
		if include["texture"] {
			if stem := textureStem(model); stem != "" {
				add(filepath.ToSlash(filepath.Join("Data", "Weapon", "Texture", stem+".png")), "texture", false)
			}
			add(filepath.ToSlash(filepath.Join("Data", "Weapon", "Texture", number+".png")), "texture", false)
		}
		if include["icon"] && icon != "" && icon != "#" {
			if strings.Contains(icon, ":") || strings.HasPrefix(icon, "/") || strings.HasPrefix(icon, "\\") {
				missing = append(missing, "图标列不是客户端相对路径（"+icon+"），已跳过")
			} else {
				add(filepath.ToSlash(filepath.Join("Data", "UI", icon)), "icon", true)
			}
		}
		if include["animation"] || include["audio"] || include["effect"] {
			assets := weaponBlockAssets(rendered, number)
			if include["animation"] {
				for _, line := range assets.lines {
					add("Data/animation/"+line+".anm", "animation", true)
				}
				// 部分官方武器自带 Data/animation/weapon/<编号>/ 目录。
				folder := filepath.Join(client, "Data", "animation", "weapon", number)
				if entries, err := os.ReadDir(folder); err == nil {
					for _, entry := range entries {
						if entry.IsDir() {
							continue
						}
						add("Data/animation/weapon/"+number+"/"+entry.Name(), "animation", true)
					}
				}
			}
			if include["audio"] {
				for _, audio := range assets.audios {
					// 3deffect 是绝大多数武器音效的位置，2deffect 兜底。
					if !add("Data/audio/3deffect/"+audio+".wav", "audio", false) {
						add("Data/audio/2deffect/"+audio+".wav", "audio", false)
					}
				}
			}
			if include["effect"] {
				for _, effect := range assets.effects {
					add("Data/effect/effect/"+effect, "effect", false)
				}
			}
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, missing
}

type blockAssets struct {
	lines   []string
	audios  []string
	effects []string
}

// weaponBlockAssets reads the weapon's own action row out of the *rendered*
// archive and follows it into the animation blocks: itemact.txt only names the
// action, the .anm / .wav / effect files it needs live inside the block.
func weaponBlockAssets(rendered *archive, weapon string) blockAssets {
	result := blockAssets{}
	actions, err := weaponActions(rendered, weapon)
	if err != nil {
		return result
	}
	lines := map[string]bool{}
	audios := map[string]bool{}
	effects := map[string]bool{}
	texts := map[string]string{}
	for _, action := range actions {
		if len(action) < 5 {
			continue
		}
		prefix, block := action[:4], action[4:]
		key := "animation/" + prefix + ".xml"
		text, ok := texts[key]
		if !ok {
			raw, err := rendered.text(key)
			if err != nil {
				texts[key] = ""
				continue
			}
			text = raw
			texts[key] = raw
		}
		for _, piece := range animationPattern.FindAllString(text, -1) {
			head := piece
			if end := strings.Index(head, ">"); end >= 0 {
				head = head[:end+1]
			}
			match := anmIDPattern.FindStringSubmatch(head)
			if match == nil || strings.TrimSpace(match[1]) != strings.TrimSpace(block) {
				continue
			}
			for _, m := range lineIDPattern.FindAllStringSubmatch(piece, -1) {
				lines[strings.TrimSpace(m[1])] = true
			}
			for _, m := range audioIDPattern.FindAllStringSubmatch(piece, -1) {
				audios[strings.TrimSpace(m[1])] = true
			}
			for _, m := range effectIDPattern.FindAllStringSubmatch(piece, -1) {
				effects[strings.TrimSpace(m[1])] = true
			}
		}
	}
	for _, value := range sortedKeys(lines) {
		result.lines = append(result.lines, value)
	}
	for _, value := range sortedKeys(audios) {
		result.audios = append(result.audios, value)
	}
	for _, value := range sortedKeys(effects) {
		result.effects = append(result.effects, value)
	}
	return result
}

func sortedKeys(set map[string]bool) []string {
	values := make([]string, 0, len(set))
	for value := range set {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}

// weaponActions returns every action id in a weapon's itemact.txt row.
func weaponActions(a *archive, weapon string) ([]string, error) {
	text, err := a.text("itemact.txt")
	if err != nil {
		return nil, err
	}
	for _, row := range splitRows(text) {
		if strings.TrimSpace(row[0]) != weapon {
			continue
		}
		actions := []string{}
		for _, cell := range row[1:] {
			cell = strings.TrimSpace(cell)
			if len(cell) >= 5 && isDigits(cell) {
				actions = append(actions, cell)
			}
		}
		return actions, nil
	}
	return nil, fmt.Errorf("itemact.txt 里没有武器 %s", weapon)
}

func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// writePackageZip lays the files out exactly as the client keeps them, so the
// only installation step is "unzip over the client root".
func writePackageZip(out string, client string, config []byte, files []packageFile, changed []string, weapons []map[string]any, ids []int) (int64, string, error) {
	if err := os.MkdirAll(filepath.Dir(out), 0700); err != nil {
		return 0, "", err
	}
	var buffer bytes.Buffer
	hasher := sha256.New()
	writer := zip.NewWriter(io.MultiWriter(&buffer, hasher))
	copyToZip := func(name string, source io.Reader) error {
		target, err := writer.Create(name)
		if err != nil {
			return err
		}
		if source == nil {
			return nil
		}
		_, err = io.Copy(target, source)
		return err
	}
	if err := copyToZip("Data/config.spf2", bytes.NewReader(config)); err != nil {
		return 0, "", err
	}
	for _, file := range files {
		handle, err := os.Open(filepath.Join(client, filepath.FromSlash(file.Path)))
		if err != nil {
			return 0, "", err
		}
		err = copyToZip(file.Path, handle)
		handle.Close()
		if err != nil {
			return 0, "", err
		}
	}
	notes := packageNotes(weapons, files, changed, ids)
	if err := copyToZip("安装说明.txt", strings.NewReader(notes)); err != nil {
		return 0, "", err
	}
	if err := writer.Close(); err != nil {
		return 0, "", err
	}
	raw := buffer.Bytes()
	if err := atomicWrite(out, raw); err != nil {
		return 0, "", err
	}
	return int64(len(raw)), hex.EncodeToString(hasher.Sum(nil)), nil
}

func packageNotes(weapons []map[string]any, files []packageFile, changed []string, ids []int) string {
	var builder strings.Builder
	builder.WriteString("功夫小子 · 自制武器发版包\r\n")
	builder.WriteString("生成时间：" + time.Now().Format("2006-01-02 15:04:05") + "\r\n")
	names := []string{}
	for _, weapon := range weapons {
		names = append(names, fmt.Sprintf("%v（%v）", weapon["name"], weapon["id"]))
	}
	builder.WriteString("包含武器：" + strings.Join(names, "、") + "\r\n\r\n")
	builder.WriteString("【安装】直接把本压缩包解压，用解压出来的全部文件覆盖到客户端根目录\r\n")
	builder.WriteString("        （Data 这一层与客户端里的 Data 对齐），重开游戏即可。\r\n")
	builder.WriteString("        本包所有路径都与客户端目录结构一致，不需要手工挑文件。\r\n\r\n")
	builder.WriteString("配置包里本次改动的条目：\r\n")
	if len(changed) == 0 {
		builder.WriteString("  （无）\r\n")
	}
	for _, name := range changed {
		builder.WriteString("  - " + name + "\r\n")
	}
	builder.WriteString("\r\n文件清单（" + strconv.Itoa(len(files)+1) + " 项，含 Data/config.spf2）：\r\n")
	kinds := map[string][]string{}
	order := []string{}
	for _, file := range files {
		if _, ok := kinds[file.Kind]; !ok {
			order = append(order, file.Kind)
		}
		kinds[file.Kind] = append(kinds[file.Kind], file.Path)
	}
	builder.WriteString("  - Data/config.spf2（配置包）\r\n")
	for _, kind := range order {
		for _, path := range kinds[kind] {
			builder.WriteString("  - " + path + "（" + packageSectionLabels[kind] + "）\r\n")
		}
	}
	return builder.String()
}
