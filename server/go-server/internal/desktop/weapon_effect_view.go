package desktop

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// 特效预览：客户端里的资源链路是
//
//	acteffect.xml <EffectFile EffectId="X" File="Y"/>
//	  → Data/effect/effect/<Y>（sgk1.3 文本脚本）
//	  → 脚本里写着的贴图名 → Data/effect/Textures/<name>.png
//
// 这里把它读成「招式引用了什么、武器登记了什么、图长什么样」，供武器页与招式页展示。
// 原有武器可预览不可编辑，自建（带标记）的才能改，门禁在写入侧。

// effectThumbnailLimit 一次最多给几个特效取缩略图，避免响应过大。
const effectThumbnailLimit = 12

// effectThumbnailMaxBytes 超过这个大小的贴图不进响应。
const effectThumbnailMaxBytes = 160 << 10

var effectPngPattern = regexp.MustCompile(`[A-Za-z0-9_][A-Za-z0-9_.\-]*\.png`)

// effectScript reads the sgk script an EffectFile row points at.
func effectScript(client, file string) string {
	file = strings.TrimSpace(file)
	if file == "" {
		return ""
	}
	name := filepath.Base(filepath.FromSlash(strings.ReplaceAll(file, "\\", "/")))
	if name == "" || name == "." || name == ".." {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(client, "Data", "effect", "effect", name))
	if err != nil {
		return ""
	}
	return string(data)
}

// effectTextureNames lists the textures a script references, in first-use order.
func effectTextureNames(script string) []string {
	seen := map[string]bool{}
	names := []string{}
	for _, match := range effectPngPattern.FindAllString(script, -1) {
		if seen[match] {
			continue
		}
		seen[match] = true
		names = append(names, match)
	}
	return names
}

// effectThumbnail returns the first readable texture as a data URI, so the GM can
// show what an effect looks like without shipping the asset itself.
//
// 贴图必须走 cachedTexture：客户端把 PNG 的前 16 字节换成了自己的标记
// （实测所有特效贴图都是 8122a3b7…），直接 base64 出去前端解码必然失败，
// 和道具图标是同一个坑。
func effectThumbnail(client string, names []string) string {
	for _, name := range names {
		path := filepath.Join(client, "Data", "effect", "Textures", name)
		stat, err := os.Stat(path)
		if err != nil || !stat.Mode().IsRegular() || stat.Size() == 0 || stat.Size() > effectThumbnailMaxBytes {
			continue
		}
		recovered, err := cachedTexture(path)
		if err != nil {
			continue
		}
		data, err := os.ReadFile(recovered)
		if err != nil {
			continue
		}
		return "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
	}
	return ""
}

// effectReferences lists every <Effect> / <HitEffect> node the weapon's action
// blocks declare, with the state and frame window each one fires on. Effects sit
// directly under <AnmDesc> (next to <Audio> / <KeyProcess>), not inside a segment.
func effectReferences(weapon *Weapon, info *inspection, loaded map[string]bool) []map[string]any {
	result := []map[string]any{}
	for _, stage := range weapon.Stages {
		for _, blk := range info.blocks[actionKey(stage.Action)] {
			blk.node.walk(func(node *xmlNode) {
				if node == nil || node.comment {
					return
				}
				kind := ""
				switch node.tag {
				case "Effect":
					kind = "effect"
				case "HitEffect":
					kind = "hit"
				default:
					return
				}
				effectID := strings.TrimSpace(node.get("effectid"))
				start, end := 0, 0
				if kind == "effect" {
					start, _ = node.attrAt("frame")
					end = start
				} else {
					start, _ = node.attrAt("startframe")
					end, _ = node.attrAt("endframe")
				}
				result = append(result, map[string]any{
					"effect_id":  effectID,
					"kind":       kind,
					"state":      stage.State,
					"stage":      stage.Stage,
					"start":      start,
					"end":        end,
					"bind_type":  node.get("bindtype"),
					"bind_index": node.get("bindindex"),
					"registered": loaded[effectID],
				})
			})
		}
	}
	return result
}

// effectPreviewView assembles the read side of the effect editor: what the actions
// reference, what the weapon's own ledger registers, and asset thumbnails.
func effectPreviewView(a *archive, client string, weapon *Weapon, info *inspection, owned, common map[string]*xmlNode) map[string]any {
	loaded := map[string]bool{}
	for key := range owned {
		loaded[key] = true
	}
	for key := range common {
		loaded[key] = true
	}
	registered := []map[string]any{}
	thumbs := 0
	for _, key := range sortedNodeKeys(owned) {
		file := strings.TrimSpace(owned[key].get("File"))
		item := map[string]any{"effect_id": key, "file": file}
		script := effectScript(client, file)
		item["script_exists"] = script != ""
		names := effectTextureNames(script)
		item["textures"] = names
		missing := []string{}
		for _, name := range names {
			if stat, err := os.Stat(filepath.Join(client, "Data", "effect", "Textures", name)); err != nil || !stat.Mode().IsRegular() {
				missing = append(missing, name)
			}
		}
		item["missing_textures"] = missing
		if thumbs < effectThumbnailLimit {
			if data := effectThumbnail(client, names); data != "" {
				item["thumbnail"] = data
				thumbs++
			}
		}
		registered = append(registered, item)
	}
	references := effectReferences(weapon, info, loaded)
	unregistered := []string{}
	seen := map[string]bool{}
	for _, ref := range references {
		id, _ := ref["effect_id"].(string)
		if id == "" || loaded[id] || seen[id] {
			continue
		}
		seen[id] = true
		unregistered = append(unregistered, id)
	}
	sort.Strings(unregistered)
	return map[string]any{
		"weapon":          weapon.ID,
		"weapon_name":     weapon.Name,
		"registered":      registered,
		"references":      references,
		"unregistered":    unregistered,
		"common_count":    len(common),
		"glow":            weaponGlowOf(a, strconv.Itoa(weapon.ID)),
		"glow_candidates": glowCandidates(client, strconv.Itoa(weapon.ID)),
		"message":         "预览完成",
	}
}

// weaponGlowOf 读取某把武器 item.txt 第 54 列上的光效脚本编号。
func weaponGlowOf(a *archive, weapon string) string {
	itemText, err := a.text("item.txt")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(strings.ReplaceAll(itemText, "\r\n", "\n"), "\n") {
		f := strings.Split(line, "\t")
		if len(f) <= 53 || strings.TrimSpace(f[1]) != weapon {
			continue
		}
		value := strings.TrimSpace(f[53])
		if value == "#" {
			return ""
		}
		return value
	}
	return ""
}

// glowCandidates 列出武器光效可用的脚本编号（Data/effect/effect/ 下的 sgk 脚本）。
// 数量上千，默认只给 253xxx 段 —— 那是武器光效专用段。
func glowCandidates(client, weapon string) []string {
	folder := filepath.Join(client, "Data", "effect", "effect")
	entries, err := os.ReadDir(folder)
	if err != nil {
		return nil
	}
	names := []string{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "253") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names
}

func sortedNodeKeys(m map[string]*xmlNode) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
