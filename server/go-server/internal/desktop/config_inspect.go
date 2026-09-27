package desktop

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Inspection never writes an archive, plan or database. Exports use a new folder.
func (admin *Admin) inspectConfig(r clientConfigRequest, export bool) (any, error) {
	a, err := loadArchive(r.Base)
	if err != nil {
		return nil, err
	}
	if err = a.verify(); err != nil {
		return nil, err
	}
	hash := digest(a.data)
	if r.Revision != "" && r.Revision != hash {
		return nil, fmt.Errorf("文件已变化，请重新解析")
	}
	names := make([]string, 0, len(a.entries))
	for n := range a.entries {
		names = append(names, n)
	}
	sort.Strings(names)
	result := map[string]any{"revision": hash, "base": r.Base}
	if r.File != "" && !export {
		raw, e := a.raw(r.File)
		if e != nil {
			return nil, e
		}
		if !configReadable(r.File, raw) {
			result["content"] = "二进制资源，仅支持原样导出"
			return result, nil
		}
		text, e := decodeText(raw)
		if e != nil {
			return nil, e
		}
		result["content"] = text
		return result, nil
	}
	folder := ""
	if export {
		parent := filepath.Join(admin.Root, "runtime-local", "config-extracts")
		if err = os.MkdirAll(parent, 0700); err != nil {
			return nil, err
		}
		folder, err = os.MkdirTemp(parent, "config-")
		if err != nil {
			return nil, err
		}
		// Remove only this newly created partial export if any entry fails.
		complete := false
		defer func() {
			if !complete {
				os.RemoveAll(folder)
			}
		}()
		// Completion is recorded after the manifest below; the caller never receives a partial path.
		return admin.exportInspected(a, r.Base, hash, names, folder, &complete)
	}
	files := []map[string]any{}
	for _, name := range names {
		raw, e := a.raw(name)
		if e != nil {
			return nil, e
		}
		files = append(files, map[string]any{"name": name, "size": len(raw), "category": inspectCategory(name), "text": configReadable(name, raw)})
	}
	result["files"] = files
	if raw, e := a.text("levelup.txt"); e == nil {
		levels := []map[string]uint64{}
		seen := map[uint64]bool{}
		for _, line := range strings.Split(raw, "\n") {
			f := strings.Fields(line)
			if len(f) == 0 {
				continue
			}
			if len(f) != 7 {
				return nil, fmt.Errorf("levelup.txt 字段数错误")
			}
			n, e := strconv.ParseUint(f[0], 10, 16)
			if e != nil {
				return nil, e
			}
			xp, e := strconv.ParseUint(f[1], 10, 32)
			if e != nil {
				return nil, e
			}
			if seen[n] {
				return nil, fmt.Errorf("等级重复：%d", n)
			}
			seen[n] = true
			if n > 0 {
				levels = append(levels, map[string]uint64{"level": n, "next_experience": xp})
			}
		}
		result["levels"] = levels
	}
	return result, nil
}

func configReadable(name string, raw []byte) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".xml", ".txt", ".lua", ".ini", ".csv":
		return !bytes.ContainsRune(raw, 0)
	}
	return false
}
func inspectCategory(name string) string {
	if name == "levelup.txt" {
		return "等级经验"
	}
	switch configCategory(name) {
	case "weapons":
		return "武器与动作"
	case "effects":
		return "特效"
	case "maps":
		return "地图"
	case "items":
		return "装备与道具"
	}
	if strings.HasPrefix(name, "script/") {
		return "关卡脚本"
	}
	return "其他配置"
}
func (admin *Admin) exportInspected(a *archive, base, hash string, names []string, folder string, complete *bool) (any, error) {
	files := []map[string]any{}
	for _, name := range names {
		// Reject traversal, Windows alternate streams and separators before writing.
		clean := filepath.FromSlash(name)
		if !filepath.IsLocal(clean) || strings.ContainsAny(name, ":\\") || filepath.Clean(clean) != clean {
			return nil, fmt.Errorf("不安全的资源路径：%s", name)
		}
		raw, e := a.raw(name)
		if e != nil {
			return nil, e
		}
		p := filepath.Join(folder, "原始文件", clean)
		if e = os.MkdirAll(filepath.Dir(p), 0700); e != nil {
			return nil, e
		}
		if e = os.WriteFile(p, raw, 0600); e != nil {
			return nil, e
		}
		if configReadable(name, raw) {
			text, e := decodeText(raw)
			if e != nil {
				return nil, e
			}
			p = filepath.Join(folder, "UTF8阅读版", clean)
			if e = os.MkdirAll(filepath.Dir(p), 0700); e != nil {
				return nil, e
			}
			if e = os.WriteFile(p, []byte(text), 0600); e != nil {
				return nil, e
			}
		}
		files = append(files, map[string]any{"name": name, "sha256": digest(raw), "size": len(raw), "category": inspectCategory(name)})
	}
	manifest := map[string]any{"base": base, "sha256": hash, "files": files, "notice": "只读解析。UTF8阅读版仅供阅读；原始文件保留原编码。经验门槛需与服务器经验配置对齐，商城价格与奖励以服务器配置为准。"}
	raw, e := json.MarshalIndent(manifest, "", "  ")
	if e != nil {
		return nil, e
	}
	if e = os.WriteFile(filepath.Join(folder, "文件清单.json"), raw, 0600); e != nil {
		return nil, e
	}
	*complete = true
	return map[string]any{"folder": folder, "count": len(files), "revision": hash}, nil
}
