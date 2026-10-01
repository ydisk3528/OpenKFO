package desktop

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 状态/Buff 定制：把 ustate.xml 的 <Data> 节点与 ustateeventproc.lua 的函数体
// 作为编辑集存进 settings.json，随其它编辑集一起渲染、校验、落盘。
//
// 与武器编辑集完全同轨：走同一个 settings.json、同一套基线/白名单/备份/CRC 守卫。
// 区别只在写入的是 ustate.xml 与脚本文件——这两个条目原本不在白名单里，故
// checkAllowedWrites 里按编辑集是否非空单独放行。
const buffLuaEntry = "script/playereventproc/ustateeventproc.lua"

// UStateEdit 是一条 ustate.xml 编辑：action 为 "upsert"（新增或覆盖）或
// "delete"（删除）。Text 是完整的 <Data ...>...</Data> 节点文本。
type UStateEdit struct {
	Action string `json:"action"`
	Text   string `json:"text,omitempty"`
	Note   string `json:"note,omitempty"`
}

var (
	ustateDataRe    = regexp.MustCompile(`(?s)<Data\b[^>]*>.*?</Data\s*>`)
	ustateTypeRe    = regexp.MustCompile(`\btype\s*=\s*"(\d+)"`)
	ustateIconRe    = regexp.MustCompile(`\bIcon\s*=\s*"([^"]*)"`)
	ustateAttrRe    = regexp.MustCompile(`(?s)^<Data\b([^>]*)>`)
	ustateLogicRe   = regexp.MustCompile(`<LogicHandle\b[^>]*?/?>`)
	luaFuncHeaderRe = regexp.MustCompile(`(?m)^[ \t]*function\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	luaWordRe       = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
	luaStringRe     = regexp.MustCompile(`"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'`)
	ustateActiveRe  = regexp.MustCompile(`\bActiveState\s*=\s*"([^"]*)"`)
	ustateTransRe   = regexp.MustCompile(`\bTransformStop\s*=\s*"([^"]*)"`)
	// 状态 lua 里「挂/摘状态」的引用（第二参数是状态号），导出时据此找依赖。
	buffStateRefRe = regexp.MustCompile(`Player\.(?:AddUstate|DelUstate)\s*\(\s*[^,]+,\s*(\d+)`)
)

type ustateSpan struct {
	Type       string
	Start, End int
	Text       string
}

// newlineOf 采用文件里占主导的换行符，落盘时不制造整文件换行差异。
func newlineOf(text string) string {
	if strings.Count(text, "\r\n")*2 >= strings.Count(text, "\n") {
		return "\r\n"
	}
	return "\n"
}

func normalizeNewlines(text, nl string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	if nl != "\n" {
		text = strings.ReplaceAll(text, "\n", nl)
	}
	return text
}

func isSpaceByte(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n'
}

// attachedCommentStart 返回紧贴在 text 末尾（中间只有空白）的注释块的起始下标，
// 没有则返回 -1。用「最后一个 --> 配它前面最近的 <!--」定位，天然不会跨过中间
// 已存在的另一个 <!-- 或节点。
func attachedCommentStart(text string) int {
	i := len(text)
	for i > 0 && isSpaceByte(text[i-1]) {
		i--
	}
	if i < 3 || text[i-3:i] != "-->" {
		return -1
	}
	j := strings.LastIndex(text[:i-3], "<!--")
	if j < 0 {
		return -1
	}
	k := j
	for k > 0 && (text[k-1] == ' ' || text[k-1] == '\t') {
		k--
	}
	if k > 0 && text[k-1] != '\n' {
		return -1
	}
	if k > 0 {
		k--
		if k > 0 && text[k-1] == '\r' {
			k--
		}
	}
	return k
}

// ustateSpans 按出现顺序列出全部 <Data> 节点及其 type。
func ustateSpans(text string) []ustateSpan {
	var out []ustateSpan
	for _, loc := range ustateDataRe.FindAllStringIndex(text, -1) {
		node := text[loc[0]:loc[1]]
		attr := ustateAttrRe.FindStringSubmatch(node)
		if attr == nil {
			continue
		}
		m := ustateTypeRe.FindStringSubmatch(attr[1])
		if m == nil {
			continue
		}
		out = append(out, ustateSpan{Type: m[1], Start: loc[0], End: loc[1], Text: node})
	}
	return out
}

// validateUStateNode 只做结构体检：必须是单个 <Data type="N"> 节点，且 type 与预期一致。
func validateUStateNode(text, want string) (string, error) {
	trimmed := strings.TrimSpace(text)
	loc := ustateDataRe.FindString(trimmed)
	if loc == "" || loc != trimmed {
		return "", fmt.Errorf("必须是单个 <Data ...>...</Data> 节点，前后不要有别的内容")
	}
	attr := ustateAttrRe.FindStringSubmatch(trimmed)
	if attr == nil {
		return "", fmt.Errorf("<Data> 开标签不完整")
	}
	m := ustateTypeRe.FindStringSubmatch(attr[1])
	if m == nil {
		return "", fmt.Errorf("<Data> 缺少 type 属性")
	}
	if want != "" && m[1] != want {
		return "", fmt.Errorf("<Data type=%q> 与状态号 %s 不一致", m[1], want)
	}
	return m[1], nil
}

// renderUStates 把编辑集套用到 ustate.xml 文本上。新增节点插在最后一个 <Data> 之后，
// 与现有排布保持一致。
func renderUStates(text string, edits map[string]UStateEdit) (string, error) {
	keys := make([]string, 0, len(edits))
	for k := range edits {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return numericLess(keys[i], keys[j]) })

	nl := newlineOf(text)
	out := text
	for _, key := range keys {
		edit := edits[key]
		switch edit.Action {
		case "delete":
			spans := ustateSpans(out)
			removed := false
			for _, s := range spans {
				if s.Type != key {
					continue
				}
				start := s.Start
				// 紧贴其上的注释块一并删掉，避免留下孤立注释。
				if c := attachedCommentStart(out[:start]); c >= 0 {
					start = c
				}
				out = out[:start] + out[s.End:]
				removed = true
				break
			}
			if !removed {
				return "", fmt.Errorf("ustate.xml 里找不到状态 %s", key)
			}
		case "upsert":
			if _, err := validateUStateNode(edit.Text, key); err != nil {
				return "", fmt.Errorf("状态 %s：%w", key, err)
			}
			node := normalizeNewlines(strings.TrimSpace(edit.Text), nl)
			replaced := false
			for _, s := range ustateSpans(out) {
				if s.Type != key {
					continue
				}
				out = out[:s.Start] + node + out[s.End:]
				replaced = true
				break
			}
			if replaced {
				continue
			}
			comment := "<!--Buff定制：状态 " + key + "-->"
			if strings.TrimSpace(edit.Note) != "" {
				comment = "<!--Buff定制：" + strings.TrimSpace(edit.Note) + "-->"
			}
			spans := ustateSpans(out)
			if len(spans) == 0 {
				return "", fmt.Errorf("ustate.xml 里没有任何 <Data> 节点，无法定位插入点")
			}
			at := spans[len(spans)-1].End
			out = out[:at] + nl + nl + comment + nl + node + out[at:]
		default:
			return "", fmt.Errorf("状态 %s 的操作 %q 无效", key, edit.Action)
		}
	}
	return out, nil
}

func numericLess(a, b string) bool {
	x, errA := strconv.Atoi(a)
	y, errB := strconv.Atoi(b)
	if errA == nil && errB == nil {
		return x < y
	}
	return a < b
}

// checkXMLWellFormed 用流式解码器体检 XML，比宽松解析器严格。
func checkXMLWellFormed(text string) error {
	body := text
	if i := strings.Index(body, "?>"); i >= 0 && strings.HasPrefix(strings.TrimSpace(body), "<?xml") {
		body = body[i+2:]
	}
	dec := xml.NewDecoder(strings.NewReader(body))
	dec.Strict = true
	for {
		_, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// luaBalance 粗校验：按行统计块开合与括号配对。不做词法分析，只挡住明显残缺。
func luaBalance(body string) error {
	depth, paren, brace, bracket := 0, 0, 0, 0
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		code := line
		if i := strings.Index(code, "--"); i >= 0 {
			code = code[:i]
		}
		code = luaStringRe.ReplaceAllString(code, `""`)
		words := luaWordRe.FindAllString(code, -1)
		hasLoop := false
		for _, w := range words {
			if w == "for" || w == "while" {
				hasLoop = true
			}
		}
		for _, w := range words {
			switch w {
			case "function", "if", "for", "while":
				depth++
			case "do":
				if !hasLoop {
					depth++
				}
			case "end":
				depth--
			}
		}
		paren += strings.Count(code, "(") - strings.Count(code, ")")
		brace += strings.Count(code, "{") - strings.Count(code, "}")
		bracket += strings.Count(code, "[") - strings.Count(code, "]")
		if depth < 0 {
			return fmt.Errorf("lua 里多了一个 end")
		}
	}
	if depth != 0 {
		return fmt.Errorf("lua 块不闭合（缺 %d 个 end）", depth)
	}
	if paren != 0 || brace != 0 || bracket != 0 {
		return fmt.Errorf("lua 括号不配对（() %d，{} %d，[] %d）", paren, brace, bracket)
	}
	return nil
}

// luaFunctionSpan 定位一个函数的起止行号：从头行开始按块深度找到匹配的 end。
func luaFunctionSpan(text, name string) (int, int, bool) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	start := -1
	for i, line := range lines {
		m := luaFuncHeaderRe.FindStringSubmatch(line)
		if m != nil && m[1] == name {
			start = i
			break
		}
	}
	if start < 0 {
		return 0, 0, false
	}
	depth := 0
	for i := start; i < len(lines); i++ {
		code := lines[i]
		if j := strings.Index(code, "--"); j >= 0 {
			code = code[:j]
		}
		code = luaStringRe.ReplaceAllString(code, `""`)
		words := luaWordRe.FindAllString(code, -1)
		hasLoop := false
		for _, w := range words {
			if w == "for" || w == "while" {
				hasLoop = true
			}
		}
		for _, w := range words {
			switch w {
			case "function", "if", "for", "while":
				depth++
			case "do":
				if !hasLoop {
					depth++
				}
			case "end":
				depth--
				if depth == 0 {
					return start, i, true
				}
			}
		}
	}
	return 0, 0, false
}

// luaFunctionText 取出一个函数块的完整文本（含结尾 end）。
func luaFunctionText(text, name string) (string, bool) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	s, e, ok := luaFunctionSpan(text, name)
	if !ok {
		return "", false
	}
	return strings.Join(lines[s:e+1], "\n"), true
}

// replaceLuaFunction 覆盖已有函数，或在文件末尾追加一个新函数。
func replaceLuaFunction(text, name, body string) (string, error) {
	if err := luaBalance(body); err != nil {
		return "", fmt.Errorf("%s：%w", name, err)
	}
	trimmed := strings.TrimRight(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	nl := newlineOf(text)
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	s, e, ok := luaFunctionSpan(text, name)
	if !ok {
		out := strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
		out += "\n\n" + strings.ReplaceAll(trimmed, "\n", nl) + "\n"
		return out, nil
	}
	out := append([]string{}, lines[:s]...)
	out = append(out, strings.Split(trimmed, "\n")...)
	out = append(out, lines[e+1:]...)
	return strings.Join(out, nl), nil
}

// applyBuffEdits 把状态/Buff 编辑集渲染到基线上。
func applyBuffEdits(a *archive, state *weaponState) (*archive, error) {
	if state == nil || (len(state.UStates) == 0 && len(state.LuaScripts) == 0) {
		return a, nil
	}
	replacements := map[string][]byte{}
	if len(state.UStates) > 0 {
		if _, ok := a.entries["ustate.xml"]; !ok {
			return nil, fmt.Errorf("客户端配置里没有 ustate.xml")
		}
		text, err := a.text("ustate.xml")
		if err != nil {
			return nil, err
		}
		rendered, err := renderUStates(text, state.UStates)
		if err != nil {
			return nil, fmt.Errorf("ustate.xml：%w", err)
		}
		if err = checkXMLWellFormed(rendered); err != nil {
			return nil, fmt.Errorf("ustate.xml 校验失败：%w", err)
		}
		raw, err := encodeText(rendered)
		if err != nil {
			return nil, err
		}
		replacements["ustate.xml"] = raw
	}
	for entry, funcs := range state.LuaScripts {
		if entry != buffLuaEntry {
			return nil, fmt.Errorf("不允许改写脚本 %s", entry)
		}
		if _, ok := a.entries[entry]; !ok {
			return nil, fmt.Errorf("客户端配置里没有 %s", entry)
		}
		text, err := a.text(entry)
		if err != nil {
			return nil, err
		}
		names := make([]string, 0, len(funcs))
		for name := range funcs {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			text, err = replaceLuaFunction(text, name, funcs[name])
			if err != nil {
				return nil, fmt.Errorf("%s：%w", entry, err)
			}
		}
		raw, err := encodeText(text)
		if err != nil {
			return nil, err
		}
		replacements[entry] = raw
	}
	data, err := a.replace(replacements)
	if err != nil {
		return nil, err
	}
	return parseArchive(data)
}

// buffSourceArchive 读取「基线 + 编辑集」的当前视图，供只读操作预览。
func buffSourceArchive(state *weaponState, client, folder string) (*archive, error) {
	entry := state.baselineFor(client)
	source := entry.path(folder)
	if _, err := os.Stat(source); err != nil {
		source = configPath(client)
	}
	a, err := loadArchive(source)
	if err != nil {
		return nil, err
	}
	if err = a.verify(); err != nil {
		return nil, err
	}
	return applyBuffEdits(a, state)
}

type buffRow struct {
	Type      string   `json:"type"`
	Name      string   `json:"name"` // ustate.xml 里紧跟 <Data> 的策划注释（中文名/说明）
	Icon      string   `json:"icon"`
	Active    string   `json:"active_state"`
	Transform string   `json:"transform_stop"`
	Logic     []string `json:"logic"`
	Script    bool     `json:"script"`
	Edited    string   `json:"edited,omitempty"` // upsert / delete
	Created   bool     `json:"created"`
	LuaFunc   bool     `json:"lua_function"`
}

func buffRows(state *weaponState, a *archive) ([]buffRow, error) {
	text, err := a.text("ustate.xml")
	if err != nil {
		return nil, err
	}
	funcs := map[string]bool{}
	if lua, err := a.text(buffLuaEntry); err == nil {
		for _, m := range luaFuncHeaderRe.FindAllStringSubmatch(lua, -1) {
			funcs[m[1]] = true
		}
	}
	rows := []buffRow{}
	for _, span := range ustateSpans(text) {
		attr := ustateAttrRe.FindStringSubmatch(span.Text)
		row := buffRow{Type: span.Type, Name: ustateNameBefore(text, span.Start)}
		if attr != nil {
			if m := ustateIconRe.FindStringSubmatch(attr[1]); m != nil {
				row.Icon = m[1]
			}
			if m := ustateActiveRe.FindStringSubmatch(attr[1]); m != nil {
				row.Active = m[1]
			}
			if m := ustateTransRe.FindStringSubmatch(attr[1]); m != nil {
				row.Transform = m[1]
			}
		}
		for _, l := range ustateLogicRe.FindAllString(span.Text, -1) {
			if strings.Contains(l, `TrigerType = ""`) || strings.Contains(l, `Type = ""`) {
				continue
			}
			row.Logic = append(row.Logic, strings.Join(strings.Fields(l), " "))
		}
		row.Script = strings.Contains(span.Text, "<Script")
		row.LuaFunc = funcs["OnGetUstate_"+span.Type]
		if edit, ok := state.UStates[span.Type]; ok {
			row.Edited = edit.Action
		}
		rows = append(rows, row)
	}
	for key, edit := range state.UStates {
		if edit.Action != "upsert" {
			continue
		}
		exists := false
		for _, r := range rows {
			if r.Type == key {
				exists = true
				break
			}
		}
		if !exists {
			rows = append(rows, buffRow{Type: key, Edited: "upsert", Created: true})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return numericLess(rows[i].Type, rows[j].Type) })
	return rows, nil
}

// weaponBuff 处理状态/Buff 定制的读写。除 weapon_buff_apply 外都只动 settings.json，
// 不碰客户端包；应用仍走 prepareClient/commitClient 那条守卫链。
func weaponBuff(request Request, client, folder string, state *weaponState, statePath string) (any, error) {
	persist := func() error {
		encoded, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			return err
		}
		return atomicWrite(statePath, encoded)
	}
	switch request.Operation {
	case "weapon_buff_catalog":
		a, err := buffSourceArchive(state, client, folder)
		if err != nil {
			return nil, err
		}
		rows, err := buffRows(state, a)
		if err != nil {
			return nil, err
		}
		return map[string]any{"buffs": rows, "lua_entry": buffLuaEntry}, nil

	case "weapon_buff_detail":
		key := strings.TrimSpace(request.Key)
		if key == "" {
			return nil, fmt.Errorf("缺少状态号")
		}
		a, err := buffSourceArchive(state, client, folder)
		if err != nil {
			return nil, err
		}
		text, err := a.text("ustate.xml")
		if err != nil {
			return nil, err
		}
		node := ""
		for _, span := range ustateSpans(text) {
			if span.Type == key {
				node = span.Text
				break
			}
		}
		lua := ""
		if l, err := a.text(buffLuaEntry); err == nil {
			if body, ok := luaFunctionText(l, "OnGetUstate_"+key); ok {
				lua = body
			}
		}
		edit := state.UStates[key]
		return map[string]any{
			"type":       key,
			"node":       node,
			"lua":        lua,
			"function":   "OnGetUstate_" + key,
			"action":     edit.Action,
			"note":       edit.Note,
			"pending":    edit.Text,
			"lua_edited": state.LuaScripts[buffLuaEntry]["OnGetUstate_"+key],
		}, nil

	case "weapon_buff_save":
		edit := request.UState
		if edit == nil {
			return nil, fmt.Errorf("缺少状态配置")
		}
		key := strings.TrimSpace(request.Key)
		if key == "" {
			return nil, fmt.Errorf("缺少状态号")
		}
		if _, err := validateUStateNode(edit.Text, key); err != nil {
			return nil, err
		}
		if err := checkXMLWellFormed(edit.Text); err != nil {
			return nil, fmt.Errorf("节点不是合法 XML：%w", err)
		}
		edit.Action = "upsert"
		if state.UStates == nil {
			state.UStates = map[string]UStateEdit{}
		}
		state.UStates[key] = *edit
		if err := persist(); err != nil {
			return nil, err
		}
		return map[string]any{"message": "状态 " + key + " 已保存，尚未应用到游戏"}, nil

	case "weapon_buff_delete":
		key := strings.TrimSpace(request.Key)
		if key == "" {
			return nil, fmt.Errorf("缺少状态号")
		}
		if state.UStates == nil {
			state.UStates = map[string]UStateEdit{}
		}
		state.UStates[key] = UStateEdit{Action: "delete"}
		if state.LuaScripts[buffLuaEntry] != nil {
			delete(state.LuaScripts[buffLuaEntry], "OnGetUstate_"+key)
		}
		if err := persist(); err != nil {
			return nil, err
		}
		return map[string]any{"message": "状态 " + key + " 已标记删除，尚未应用到游戏"}, nil

	case "weapon_buff_lua_save":
		key := strings.TrimSpace(request.Key)
		if key == "" {
			return nil, fmt.Errorf("缺少状态号")
		}
		body := strings.TrimSpace(request.LuaText)
		if body == "" {
			return nil, fmt.Errorf("lua 内容为空")
		}
		if err := luaBalance(body); err != nil {
			return nil, err
		}
		if state.LuaScripts == nil {
			state.LuaScripts = map[string]map[string]string{}
		}
		if state.LuaScripts[buffLuaEntry] == nil {
			state.LuaScripts[buffLuaEntry] = map[string]string{}
		}
		state.LuaScripts[buffLuaEntry]["OnGetUstate_"+key] = body
		if err := persist(); err != nil {
			return nil, err
		}
		return map[string]any{"message": "lua 函数 OnGetUstate_" + key + " 已保存，尚未应用到游戏"}, nil

	case "weapon_buff_icons":
		dir := filepath.Join(client, "Data", "UI", "Picture", "AbnormalState")
		entries, err := os.ReadDir(dir)
		if err != nil {
			return map[string]any{"icons": []string{}, "directory": dir}, nil
		}
		icons := []string{}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if !strings.EqualFold(filepath.Ext(name), ".png") {
				continue
			}
			icons = append(icons, name)
		}
		sort.Strings(icons)
		return map[string]any{"icons": icons, "directory": dir, "prefix": "Picture\\AbnormalState\\"}, nil

	case "weapon_buff_image":
		// 客户端把 PNG 的前 32 字节换成了自有标记 + 混淆 IHDR，直接读文件是
		// 坏图。走 cachedTexture 还原头部后回传 base64，前端用 Image.memory 显示。
		names := request.Keys
		if len(names) == 0 && strings.TrimSpace(request.Key) != "" {
			names = []string{request.Key}
		}
		if len(names) < 1 || len(names) > 24 {
			return nil, fmt.Errorf("每次读取 1–24 个图标")
		}
		root, err := filepath.EvalSymlinks(filepath.Join(client, "Data", "UI", "Picture", "AbnormalState"))
		if err != nil {
			return nil, err
		}
		images := map[string]string{}
		for _, name := range names {
			// 只接受纯文件名，避免 ../ 之类的越界。
			base := filepath.Base(strings.ReplaceAll(strings.TrimSpace(name), "\\", "/"))
			if base == "" || base == "." || base == ".." || !strings.EqualFold(filepath.Ext(base), ".png") {
				continue
			}
			path, err := filepath.EvalSymlinks(filepath.Join(root, base))
			if err != nil {
				continue
			}
			if rel, err := filepath.Rel(root, path); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				continue
			}
			cached, err := cachedTexture(path)
			if err != nil {
				continue
			}
			data, err := os.ReadFile(cached)
			if err != nil {
				continue
			}
			images[base] = base64.StdEncoding.EncodeToString(data)
		}
		return map[string]any{"images": images}, nil

	case "weapon_buff_api":
		return map[string]any{"groups": buffPlayerAPI(), "events": buffEventHooks(), "fields": buffFieldReference()}, nil

	case "weapon_buff_export":
		return weaponBuffExport(request, state, client, folder)

	case "weapon_buff_packages":
		return weaponBuffPackages(folder)

	case "weapon_buff_merge_import":
		return weaponBuffMergeImport(request, state, client, folder, statePath)

	case "weapon_buff_preview":
		a, err := buffSourceArchive(state, client, folder)
		if err != nil {
			return nil, err
		}
		text, err := a.text("ustate.xml")
		if err != nil {
			return nil, err
		}
		node := ""
		if key := strings.TrimSpace(request.Key); key != "" {
			for _, span := range ustateSpans(text) {
				if span.Type == key {
					node = span.Text
					break
				}
			}
		}
		lua := ""
		if l, err := a.text(buffLuaEntry); err == nil {
			lua = l
		}
		return map[string]any{"ustate": text, "node": node, "lua": lua}, nil
	}
	return nil, fmt.Errorf("未知状态/Buff 操作")
}

// weaponBuffApply 把「基线 + 全部编辑集」重新渲染并写入客户端。状态/Buff 编辑集
// 不能单独落盘——客户端包里已经是「基线 + 武器编辑 + Buff 编辑」的合成结果，
// 只套 Buff 编辑会把武器那部分抹掉，所以这里仍然走 prepareClient 那条完整管线。
func weaponBuffApply(state *weaponState, client, folder, statePath string) (any, error) {
	return weaponBuffApplyWith(state, client, folder, statePath, false)
}

// weaponBuffApplyWith is weaponBuffApply with an explicit write mode. buffOnly
// is set by the merge-import path: it writes only the state/lua edits and skips
// the combo-rule renumber guard, which belongs to weapon applies and must not
// veto an unrelated buff import.
func weaponBuffApplyWith(state *weaponState, client, folder, statePath string, buffOnly bool) (any, error) {
	if runtime.GOOS == "windows" {
		command := exec.Command("tasklist", "/FI", "IMAGENAME eq gfld.dat", "/FO", "CSV", "/NH")
		hideWindow(command)
		output, err := command.Output()
		if err != nil {
			return nil, err
		}
		if bytes.Contains(bytes.ToLower(output), []byte("gfld.dat")) {
			return nil, fmt.Errorf("请先退出游戏客户端，再应用；可以先保存方案")
		}
	}
	entry := state.baselineFor(client)
	if err := ensureBaseline(entry, folder, false); err != nil {
		return nil, err
	}
	source, err := loadArchive(entry.path(folder))
	if err != nil {
		return nil, fmt.Errorf("基线无法读取：%w", err)
	}
	if err = source.verify(); err != nil {
		return nil, fmt.Errorf("基线校验失败：%w", err)
	}
	if entry.SourceHash != "" && digest(source.data) != entry.SourceHash {
		return nil, fmt.Errorf("基线备份已被改动，已停止写入")
	}
	base, err := buildWeaponBase(source, state)
	if err != nil {
		return nil, err
	}
	itemText, err := base.text("item.txt")
	if err != nil {
		return nil, fmt.Errorf("武器表缺失：%w", err)
	}
	items, err := itemsFromText(entry.Directory, itemText, true, true)
	if err != nil {
		return nil, err
	}
	info, err := inspect(base, items)
	if err != nil {
		return nil, err
	}
	plan, err := prepareClient(entry, folder, state, state.Applied, info, buffOnlyOptions(buffOnly)...)
	if err != nil {
		return nil, err
	}
	if err = commitClient(plan, folder); err != nil {
		return nil, err
	}
	state.SourceHash = entry.SourceHash
	state.AppliedHash = entry.AppliedHash
	encoded, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return nil, err
	}
	if err = atomicWrite(statePath, encoded); err != nil {
		return nil, err
	}
	return map[string]any{"backup": plan.Backup, "message": "状态/Buff 已写入客户端；重启游戏后加载，实战效果仍需验证"}, nil
}

// buffOnlyOptions converts the buffOnly flag into prepareClient render options.
func buffOnlyOptions(buffOnly bool) []renderOption {
	if !buffOnly {
		return nil
	}
	return []renderOption{withoutComboReconcile()}
}

// buffMergeManifest 是 buff 合并包的 manifest：一个状态的 ustate 节点 + lua 函数，
// 无需素材，纯 JSON，与武器合并包的「manifest + 素材 zip」同理但更轻。
type buffMergeManifest struct {
	Format  string          `json:"format"`
	Version int             `json:"version"`
	Buffs   []buffMergeItem `json:"buffs"`
}

type buffMergeItem struct {
	Type string `json:"type"`
	Node string `json:"node"`
	Lua  string `json:"lua"`
}

func buffPackageDir(folder string) string {
	return filepath.Join(filepath.Dir(filepath.Dir(folder)), "dist", "buff-packages")
}

func loadBuffManifest(path string) (*buffMergeManifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var manifest buffMergeManifest
	if err = json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("buff 合并包解析失败：%w", err)
	}
	if manifest.Format != "openkfo-buff-merge" || manifest.Version != 1 {
		return nil, fmt.Errorf("buff 合并包格式不认识（format=%s version=%d）", manifest.Format, manifest.Version)
	}
	if len(manifest.Buffs) == 0 {
		return nil, fmt.Errorf("buff 合并包里没有任何状态")
	}
	for i := range manifest.Buffs {
		if _, err := validateUStateNode(manifest.Buffs[i].Node, manifest.Buffs[i].Type); err != nil {
			return nil, fmt.Errorf("状态 %s：%w", manifest.Buffs[i].Type, err)
		}
		if strings.TrimSpace(manifest.Buffs[i].Lua) != "" {
			if err := luaBalance(manifest.Buffs[i].Lua); err != nil {
				return nil, fmt.Errorf("状态 %s 的 lua：%w", manifest.Buffs[i].Type, err)
			}
		}
	}
	return &manifest, nil
}

// applyBuffMerge 把合并包里的状态与 lua 函数 upsert 进归档，复用 applyBuffEdits。
func applyBuffMerge(a *archive, manifest *buffMergeManifest) (*archive, error) {
	tmp := &weaponState{
		UStates:    map[string]UStateEdit{},
		LuaScripts: map[string]map[string]string{},
	}
	funcs := map[string]string{}
	for _, b := range manifest.Buffs {
		tmp.UStates[b.Type] = UStateEdit{Action: "upsert", Text: b.Node}
		if strings.TrimSpace(b.Lua) != "" {
			funcs["OnGetUstate_"+b.Type] = b.Lua
		}
	}
	if len(funcs) > 0 {
		tmp.LuaScripts[buffLuaEntry] = funcs
	}
	return applyBuffEdits(a, tmp)
}

// weaponBuffExport 把某个状态的节点 + lua 函数导出成 JSON 合并包，并自动带上
// 它 lua 里 AddUstate/DelUstate 引用的其它自建状态（如 432 依赖的 433/434），
// 保证导出的是一个自包含的完整 buff 配置。
// 编辑框里的最新内容通过 request.UState.Text / request.LuaText 直接带上，避免
// 「还没保存就导不出」。
func weaponBuffExport(request Request, state *weaponState, client, folder string) (any, error) {
	key := strings.TrimSpace(request.Key)
	if key == "" {
		return nil, fmt.Errorf("缺少状态号")
	}
	a, err := buffSourceArchive(state, client, folder)
	if err != nil {
		return nil, err
	}
	xmlText := ""
	if t, err := a.text("ustate.xml"); err == nil {
		xmlText = t
	}
	luaText := ""
	if t, err := a.text(buffLuaEntry); err == nil {
		luaText = t
	}

	// node/lua 读取：选中状态的编辑框最新内容优先，其余从当前视图取。
	nodeOf := func(k string) string {
		if k == key && request.UState != nil && strings.TrimSpace(request.UState.Text) != "" {
			return strings.TrimSpace(request.UState.Text)
		}
		for _, span := range ustateSpans(xmlText) {
			if span.Type == k {
				return span.Text
			}
		}
		return ""
	}
	luaOf := func(k string) string {
		if k == key && strings.TrimSpace(request.LuaText) != "" {
			return strings.TrimSpace(request.LuaText)
		}
		if body, ok := luaFunctionText(luaText, "OnGetUstate_"+k); ok {
			return body
		}
		return ""
	}

	selfMade := buffSelfMadeSet(xmlText)
	deps := buffDependencies(key, luaText, selfMade)

	items := []buffMergeItem{}
	add := func(k string) error {
		node := nodeOf(k)
		if node != "" {
			if _, err := validateUStateNode(node, k); err != nil {
				return fmt.Errorf("状态 %s：%w", k, err)
			}
		}
		lua := luaOf(k)
		if node == "" && lua == "" {
			return nil
		}
		items = append(items, buffMergeItem{Type: k, Node: node, Lua: lua})
		return nil
	}
	if err := add(key); err != nil {
		return nil, err
	}
	for _, dep := range deps {
		if err := add(dep); err != nil {
			return nil, err
		}
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("状态 %s 没有可导出的内容", key)
	}

	manifest := buffMergeManifest{Format: "openkfo-buff-merge", Version: 1, Buffs: items}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	dir := buffPackageDir(folder)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, fmt.Sprintf("buff-merge-%s-%s.json", key, time.Now().Format("20060102-150405")))
	if err = atomicWrite(path, raw); err != nil {
		return nil, err
	}
	return map[string]any{"path": path, "manifest": manifest, "dependencies": deps}, nil
}

// buffSelfMadeSet 找出 ustate.xml 里注释含「自建」的状态号集合。
func buffSelfMadeSet(text string) map[string]bool {
	result := map[string]bool{}
	for _, span := range ustateSpans(text) {
		if strings.Contains(ustateNameBefore(text, span.Start), "自建") {
			result[span.Type] = true
		}
	}
	return result
}

// buffDependencies 递归收集 root 状态（及其自建依赖）的 lua 里 AddUstate/DelUstate
// 引用的自建状态号，返回按状态号排序的依赖列表（不含 root 自身）。原生状态只
// 当「引用」，不递归也不导出——目标包应当已自带。
func buffDependencies(root, luaText string, selfMade map[string]bool) []string {
	seen := map[string]bool{}
	found := map[string]bool{}
	var walk func(key string)
	walk = func(key string) {
		if seen[key] {
			return
		}
		seen[key] = true
		body, ok := luaFunctionText(luaText, "OnGetUstate_"+key)
		if !ok {
			return
		}
		for _, m := range buffStateRefRe.FindAllStringSubmatch(body, -1) {
			dep := m[1]
			if dep == key || !selfMade[dep] {
				continue
			}
			found[dep] = true
			walk(dep)
		}
	}
	walk(root)
	out := make([]string, 0, len(found))
	for k := range found {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return numericLess(out[i], out[j]) })
	return out
}

// weaponBuffPackages 列出之前导出的 buff 合并包。
func weaponBuffPackages(folder string) (any, error) {
	dir := buffPackageDir(folder)
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
		if !strings.HasSuffix(name, ".json") {
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

// weaponBuffMergeImport 把 buff 合并包里的状态与 lua 函数合并进当前客户端。
// 走「合并到编辑集 → weaponBuffApply」的完整管线，而不是直接改客户端包——
// 这样才会同步基线哈希，避免 GM 后续「基线备份已变化」死锁，也不会在下次
// 应用时把导入的状态抹掉。
func weaponBuffMergeImport(request Request, state *weaponState, client, folder, statePath string) (any, error) {
	if strings.TrimSpace(request.SourcePath) == "" {
		return nil, fmt.Errorf("缺少合并包路径")
	}
	manifest, err := loadBuffManifest(request.SourcePath)
	if err != nil {
		return nil, err
	}

	// 用「基线 + 编辑集」当前视图判断新增 / 覆盖。
	a, err := buffSourceArchive(state, client, folder)
	if err != nil {
		return nil, err
	}
	existing := map[string]bool{}
	if text, err := a.text("ustate.xml"); err == nil {
		for _, span := range ustateSpans(text) {
			existing[span.Type] = true
		}
	}
	newStates, modified := []string{}, []string{}
	for _, b := range manifest.Buffs {
		if existing[b.Type] {
			modified = append(modified, b.Type)
		} else {
			newStates = append(newStates, b.Type)
		}
	}

	// 合并进编辑集（upsert），随其它编辑集一起渲染、校验、落盘。
	if state.UStates == nil {
		state.UStates = map[string]UStateEdit{}
	}
	if state.LuaScripts == nil {
		state.LuaScripts = map[string]map[string]string{}
	}
	if state.LuaScripts[buffLuaEntry] == nil {
		state.LuaScripts[buffLuaEntry] = map[string]string{}
	}
	for _, b := range manifest.Buffs {
		state.UStates[b.Type] = UStateEdit{Action: "upsert", Text: b.Node}
		if strings.TrimSpace(b.Lua) != "" {
			state.LuaScripts[buffLuaEntry]["OnGetUstate_"+b.Type] = b.Lua
		}
	}

	result, err := weaponBuffApplyWith(state, client, folder, statePath, true)
	if err != nil {
		return nil, err
	}
	if resultMap, ok := result.(map[string]any); ok {
		resultMap["new"] = newStates
		resultMap["modified"] = modified
		resultMap["message"] = fmt.Sprintf("已合并 %d 个状态（新增 %d、覆盖 %d）到当前客户端", len(manifest.Buffs), len(newStates), len(modified))
		return resultMap, nil
	}
	return map[string]any{
		"new":      newStates,
		"modified": modified,
		"message":  fmt.Sprintf("已合并 %d 个状态（新增 %d、覆盖 %d）到当前客户端", len(manifest.Buffs), len(newStates), len(modified)),
	}, nil
}

// buffEventHooks 列出引擎会回调的 lua 函数名模板。
func buffEventHooks() []map[string]string {
	return []map[string]string{
		{"name": "OnGetUstate_<N>", "desc": "状态号 N 生效时被引擎回调（按状态号派发，与 LogicHandle 的 Type 无关）"},
		{"name": "OnDelUstate_<N>", "desc": "状态号 N 解除时被引擎回调"},
		{"name": "OnUstateTimer_<N>", "desc": "状态号 N 计时回调（需状态声明 TimerInvoke）"},
		{"name": "OnUstateHitEvent_<N>", "desc": "持有该状态时命中敌人"},
		{"name": "OnUstateBeHitEvent_<N>", "desc": "持有该状态时被击中"},
		{"name": "OnUstatePlayerStateChange_<N>", "desc": "持有该状态时角色状态切换"},
	}
}

// buffFieldReference 是 ustate.xml 各字段与取值的说明，内容整理自
// ustate.xml 头部的策划注释 + 全库 <Data> 节点的中文注释（权威来源）。
// 按分组返回，前端直接渲染成折叠参考面板。
func buffFieldReference() []map[string]any {
	return []map[string]any{
		{"title": "<Data> 属性（状态声明）", "items": []map[string]string{
			{"name": "type", "desc": "状态号（唯一 ID）。引擎按它派发 lua：OnGetUstate_<type> / OnDelUstate_<type> / OnUstateTimer_<type>"},
			{"name": "DealType", "desc": "叠加规则：1=替换（高等级可替换低等级） 2=叠加 3=丢弃"},
			{"name": "ActiveState", "desc": "激活态标记（0/1）"},
			{"name": "TransformStop", "desc": "变身时是否清除该状态（1=清除）"},
			{"name": "Icon", "desc": "状态图标路径，如 Picture\\AbnormalState\\abnormalstate8.png"},
			{"name": "DeadAction", "desc": "死亡动作编号（0=无）"},
			{"name": "AudioEffectId", "desc": "音效编号（空=无）"},
			{"name": "HitDownStop", "desc": "受击倒地时是否清除（0/1）"},
			{"name": "HitStop", "desc": "受击硬直时是否清除（0/1）"},
			{"name": "ReliveStop", "desc": "复活时是否清除（0/1）"},
			{"name": "SwitchWeaponStop", "desc": "切换武器时是否清除（0/1）"},
			{"name": "KeepAttackingEffect", "desc": "持续攻击特效标记"},
			{"name": "TalismanState", "desc": "法宝状态标记"},
		}},
		{"title": "<LogicHandle> 属性（数值效果）", "items": []map[string]string{
			{"name": "TrigerType", "desc": "触发时机：2=状态生效时 5=被伤害时 4=用大招时 1=命中时(罕见) 0=无/纯特效"},
			{"name": "Type", "desc": "效果类型（见下方 Type 枚举）。注意：Type=12 不是「调 lua」，lua 由状态号派发"},
			{"name": "level1..level8", "desc": "各等级效果数值，按状态等级索引；具体含义由 Type 决定。可写小数（如 0.25）"},
		}},
		{"title": "Type 枚举（效果类型，整理自策划注释）", "items": []map[string]string{
			{"name": "0", "desc": "纯特效 / 占位，无数值效果"},
			{"name": "1", "desc": "持续减血 DoT（中毒/灼烧/瘟疫），level=总减血量（引擎换算每帧）"},
			{"name": "2", "desc": "速度：level 负=减速，正=加速（速度属性值）"},
			{"name": "3", "desc": "减攻（level=减攻击百分比）"},
			{"name": "4", "desc": "虚弱 / 伤害加深（level=减防或加伤百分比）"},
			{"name": "5", "desc": "混乱（操作键乱）"},
			{"name": "6", "desc": "恐惧（操作键乱，高级）"},
			{"name": "7", "desc": "加攻（level=加攻击百分比）"},
			{"name": "8", "desc": "加防（level=减被伤害百分比）"},
			{"name": "9", "desc": "无敌"},
			{"name": "10", "desc": "隐身"},
			{"name": "11", "desc": "变身"},
			{"name": "12", "desc": "改血量：level 正=加血，负=扣血（如 18 加血 / 34 内伤 / 265 闪电扣血）。不是 lua"},
			{"name": "13", "desc": "冰冻 / 禁锢（僵直），level 无效"},
			{"name": "14", "desc": "眩晕，level 无效"},
			{"name": "15", "desc": "加技能点"},
			{"name": "16", "desc": "加瞄准（level=距离）"},
			{"name": "17", "desc": "麻痹"},
			{"name": "18", "desc": "无法防御（破防）"},
			{"name": "19", "desc": "范围光环（痛苦结界等，level=半径）"},
			{"name": "20", "desc": "全面怒气增长（level=倍乘系数）"},
			{"name": "21", "desc": "快速怒气增长（level=倍乘系数）"},
			{"name": "22", "desc": "伤害反弹（level=反弹系数，0.3=30%、1.0=100%，支持小数）"},
			{"name": "23", "desc": "缴械（空手）"},
			{"name": "24", "desc": "致盲 / 闪光"},
			{"name": "25", "desc": "息怒（封怒气）"},
			{"name": "26", "desc": "重生（level=复活后生命量，0=满血）"},
			{"name": "27", "desc": "假无敌 / 霸体"},
			{"name": "28", "desc": "吸血（固定血量）"},
			{"name": "29", "desc": "吸血（按比例，1=100%）"},
			{"name": "30", "desc": "免疫（Antibuf 配置索引）"},
			{"name": "31", "desc": "清除负面状态（level=清除/免疫配置索引）"},
			{"name": "32", "desc": "免疫"},
			{"name": "33", "desc": "加血（加血量条，如生命吊坠）"},
			{"name": "35", "desc": "综合减速（减攻减防减移速）"},
			{"name": "36", "desc": "封技能 / 禁怒"},
			{"name": "37", "desc": "延时攻击"},
			{"name": "38", "desc": "换装（红/绿队初始换装）"},
			{"name": "39", "desc": "束缚"},
			{"name": "41", "desc": "百分比减血（感电，level=-20 即减当前血 20%）"},
			{"name": "42", "desc": "武器切换动作加快"},
		}},
		{"title": "<Script> 回调声明", "items": []map[string]string{
			{"name": "TimerInvoke", "desc": "计时回调间隔（毫秒），触发 OnUstateTimer_<type>"},
			{"name": "HitInvoke", "desc": "持该状态命中敌人时回调 OnUstateHitEvent_<type>"},
			{"name": "BeHitInvoke", "desc": "持该状态被击中时回调 OnUstateBeHitEvent_<type>"},
			{"name": "StateInvoke", "desc": "持该状态角色状态切换时回调 OnUstatePlayerStateChange_<type>"},
		}},
		{"title": "<Behave> 表现（特效/动画）", "items": []map[string]string{
			{"name": "<Effect>", "desc": "EffectId=特效编号 BoneId=骨骼挂点 EffectBindType=绑定方式(3=跟随)"},
			{"name": "<Anim>", "desc": "AnimId=动画编号 AnimHandleType=播放方式 PRI=优先级 CanBreak=可打断"},
		}},
	}
}

// buffPlayerAPI 是从客户端现有 lua 全量反查出来的 Player.* 方法清单，
// 按用途分组，直接喂给编辑页做参考面板。
func buffPlayerAPI() []map[string]any {
	return []map[string]any{
		{"title": "状态（Buff）", "items": []map[string]string{
			{"name": "Player.AddUstate", "signature": "Player.AddUstate(目标ID, 状态号, level, 时长ms, 来源ID)", "desc": "给目标挂状态；时长毫秒"},
			{"name": "Player.DelUstate", "signature": "Player.DelUstate(目标ID, 状态号)", "desc": "移除目标身上的状态"},
			{"name": "Player.GetUstateByID", "signature": "Player.GetUstateByID(目标ID, 状态号)", "desc": "取状态表，无则 nil；字段 id/level/duration/overlap/xml"},
			{"name": "Player.SetState", "signature": "Player.SetState(目标ID, 状态码, 动作码)", "desc": "播放角色状态动作（如变身）"},
		}},
		{"title": "数值", "items": []map[string]string{
			{"name": "Player.AddMP", "signature": "Player.AddMP(目标ID, 增量)", "desc": "怒气（蓝量）增减，负数为扣"},
			{"name": "Player.GetAttributes", "signature": "Player.GetAttributes(目标ID)", "desc": "取属性表（HP/x/y/z/State 等）"},
		}},
		{"title": "变量", "items": []map[string]string{
			{"name": "Player.SetVar", "signature": "Player.SetVar(目标ID, \"键\", 值)", "desc": "写自定义变量"},
			{"name": "Player.GetVar", "signature": "Player.GetVar(目标ID, \"键\")", "desc": "读自定义变量"},
			{"name": "Player.DelVar", "signature": "Player.DelVar(目标ID, \"键\")", "desc": "删除自定义变量"},
		}},
		{"title": "外观 / 装备", "items": []map[string]string{
			{"name": "Player.SetEquipmentSet", "signature": "Player.SetEquipmentSet(目标ID, {Hair=,Face=,Body=,Hand=,Leg=,Foot=})", "desc": "整套换装"},
			{"name": "Player.ClearEquipmentSet", "signature": "Player.ClearEquipmentSet(目标ID)", "desc": "还原换装"},
			{"name": "Player.SetTransformWeapon", "signature": "Player.SetTransformWeapon(目标ID, 武器ID)", "desc": "变身武器"},
			{"name": "Player.ClearTransformWeapon", "signature": "Player.ClearTransformWeapon(目标ID)", "desc": "还原武器"},
		}},
		{"title": "位置 / 其它", "items": []map[string]string{
			{"name": "Player.GetPosition", "signature": "Player.GetPosition(目标ID)", "desc": "返回 x, y, z"},
			{"name": "Player.Teleport", "signature": "Player.Teleport(目标ID, x, y, z, s)", "desc": "瞬移"},
			{"name": "Player.GetWeaponDetailID", "signature": "Player.GetWeaponDetailID(目标ID)", "desc": "取当前武器编号，常用于按武器区分效果"},
			{"name": "Player.SetDisableTalisman", "signature": "Player.SetDisableTalisman(目标ID, 0/1)", "desc": "开关法宝"},
		}},
	}
}
