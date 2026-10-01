package desktop

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const sampleUState = "<?xml version=\"1.0\" encoding=\"gb2312\"?>\r\n<UState>\r\n\t<!--甲-->\r\n\t<Data type=\"1\" ActiveState = \"1\" Icon = \"a.png\">\r\n\t\t<Logic>\r\n\t\t\t<LogicHandle TrigerType = \"2\" Type = \"12\" level1 = \"0\"/>\r\n\t\t</Logic>\r\n\t</Data>\r\n\r\n\t<!--乙-->\r\n\t<Data type=\"2\" ActiveState = \"0\" Icon = \"\">\r\n\t\t<Logic>\r\n\r\n\t\t</Logic>\r\n\t</Data>\r\n\r\n\t<AreaAttackParam>\r\n\t</AreaAttackParam>\r\n</UState>\r\n"

func TestRenderUStatesUpsertExisting(t *testing.T) {
	node := "<Data type=\"2\" ActiveState = \"0\" Icon = \"abnormalstate8.png\">\n<Logic>\n</Logic>\n</Data>"
	out, err := renderUStates(sampleUState, map[string]UStateEdit{"2": {Action: "upsert", Text: node}})
	if err != nil {
		t.Fatalf("renderUStates: %v", err)
	}
	spans := ustateSpans(out)
	if len(spans) != 2 {
		t.Fatalf("节点数应为 2，得到 %d", len(spans))
	}
	for _, s := range spans {
		if s.Type == "2" && !strings.Contains(s.Text, "abnormalstate8.png") {
			t.Fatalf("状态 2 未被替换：%s", s.Text)
		}
	}
	if !strings.Contains(out, "\r\n") {
		t.Fatal("未沿用原文件的 CRLF 换行")
	}
	if err := checkXMLWellFormed(out); err != nil {
		t.Fatalf("结果 XML 不合法：%v", err)
	}
}

func TestRenderUStatesInsertNewAfterLastData(t *testing.T) {
	node := "<Data type=\"433\" ActiveState = \"0\" Icon = \"x.png\"><Logic>\n</Logic></Data>"
	out, err := renderUStates(sampleUState, map[string]UStateEdit{"433": {Action: "upsert", Text: node}})
	if err != nil {
		t.Fatalf("renderUStates: %v", err)
	}
	if idx := strings.Index(out, "type=\"433\""); idx < 0 {
		t.Fatal("新节点未写入")
	} else if idx > strings.Index(out, "<AreaAttackParam>") {
		t.Fatal("新节点应插在最后一个 <Data> 之后、<AreaAttackParam> 之前")
	}
	if err := checkXMLWellFormed(out); err != nil {
		t.Fatalf("结果 XML 不合法：%v", err)
	}
}

func TestRenderUStatesDeleteRemovesNodeAndComment(t *testing.T) {
	out, err := renderUStates(sampleUState, map[string]UStateEdit{"2": {Action: "delete"}})
	if err != nil {
		t.Fatalf("renderUStates: %v", err)
	}
	if strings.Contains(out, "type=\"2\"") {
		t.Fatal("状态 2 未删除")
	}
	if strings.Contains(out, "<!--乙-->") {
		t.Fatal("紧贴的注释未一并删除")
	}
	if !strings.Contains(out, "<!--甲-->") {
		t.Fatal("无关注释被误删")
	}
}

func TestRenderUStatesRejectsMismatchedType(t *testing.T) {
	_, err := renderUStates(sampleUState, map[string]UStateEdit{"9": {Action: "upsert", Text: "<Data type=\"8\"><Logic></Logic></Data>"}})
	if err == nil {
		t.Fatal("type 不一致应当报错")
	}
}

func TestLuaBalance(t *testing.T) {
	good := "function OnGetUstate_432( ustate, self, attackerid )\n\tif ustate ~= nil then\n\t\tPlayer.AddMP( self, -1 );\n\tend\n\treturn;\nend"
	if err := luaBalance(good); err != nil {
		t.Fatalf("合法 lua 被拒：%v", err)
	}
	bad := "function f()\n\tif x then\n\t\treturn;\nend"
	if err := luaBalance(bad); err == nil {
		t.Fatal("缺 end 应当报错")
	}
	bad2 := "function f()\n\tlocal t = { 1, 2 ;\nend"
	if err := luaBalance(bad2); err == nil {
		t.Fatal("括号不配对应当报错")
	}
}

const sampleLua = "--头注释\nfunction OnGetUstate_432( ustate, self, attackerid )\n\tlocal level = 1;\n\tif ustate ~= nil then\n\t\tlevel = ustate.level;\n\tend\n\treturn;\nend\n\nfunction OnGetUstate_999( u )\n\treturn;\nend\n"

func TestReplaceLuaFunctionExisting(t *testing.T) {
	body := "function OnGetUstate_432( ustate, self, attackerid )\n\tPlayer.AddMP( self, -10 );\n\treturn;\nend"
	out, err := replaceLuaFunction(sampleLua, "OnGetUstate_432", body)
	if err != nil {
		t.Fatalf("replaceLuaFunction: %v", err)
	}
	if strings.Contains(out, "level = ustate.level") {
		t.Fatal("旧函数体未被替换")
	}
	if !strings.Contains(out, "Player.AddMP( self, -10 )") {
		t.Fatal("新函数体未写入")
	}
	if !strings.Contains(out, "OnGetUstate_999") {
		t.Fatal("相邻函数被破坏")
	}
	if got := strings.Count(out, "OnGetUstate_432"); got != 1 {
		t.Fatalf("函数应恰好出现一次，得到 %d", got)
	}
}

func TestReplaceLuaFunctionAppend(t *testing.T) {
	body := "function OnGetUstate_433( u )\n\treturn;\nend"
	out, err := replaceLuaFunction(sampleLua, "OnGetUstate_433", body)
	if err != nil {
		t.Fatalf("replaceLuaFunction: %v", err)
	}
	if !strings.Contains(out, "OnGetUstate_433") {
		t.Fatal("新函数未追加")
	}
	if !strings.Contains(out, "OnGetUstate_999") {
		t.Fatal("原内容被破坏")
	}
	if err := luaBalance(out); err != nil {
		t.Fatalf("结果 lua 不闭合：%v", err)
	}
}

func TestReplaceLuaFunctionRejectsBrokenBody(t *testing.T) {
	if _, err := replaceLuaFunction(sampleLua, "OnGetUstate_432", "function OnGetUstate_432( u )\n\tif x then\nend"); err == nil {
		t.Fatal("残缺函数体应当被拒")
	}
}

func TestBuffPlayerAPINotEmpty(t *testing.T) {
	groups := buffPlayerAPI()
	if len(groups) < 3 {
		t.Fatalf("API 分组过少：%d", len(groups))
	}
	total := 0
	for _, g := range groups {
		items, _ := g["items"].([]map[string]string)
		for _, it := range items {
			if it["name"] == "" || it["signature"] == "" {
				t.Fatalf("API 条目缺字段：%v", it)
			}
			if !strings.HasPrefix(it["name"], "Player.") {
				t.Fatalf("Player API 名称不合法：%s", it["name"])
			}
			total++
		}
	}
	if total < 10 {
		t.Fatalf("API 条目过少：%d", total)
	}
}

func TestBuffSelfMadeAndDependencies(t *testing.T) {
	xml := "<?xml version=\"1.0\"?>\n<UState>\n" +
		"\t<!--中毒-->\n\t<Data type=\"30\" Icon=\"a30.png\"><Logic></Logic></Data>\n" +
		"\t<!--吸魔（自建 432）：机制载体-->\n\t<Data type=\"432\" Icon=\"\"><Logic></Logic></Data>\n" +
		"\t<!--吸魔图标（自建 433）：虚弱图标-->\n\t<Data type=\"433\" Icon=\"abnormalstate8.png\"><Logic></Logic></Data>\n" +
		"\t<!--吸魔图标（自建 434）：怒气图标-->\n\t<Data type=\"434\" Icon=\"abnormalstate30.png\"><Logic></Logic></Data>\n" +
		"</UState>\n"

	selfMade := buffSelfMadeSet(xml)
	for _, n := range []string{"432", "433", "434"} {
		if !selfMade[n] {
			t.Errorf("预期 %s 为自建", n)
		}
	}
	if selfMade["30"] {
		t.Error("原生状态 30 不应被标记为自建")
	}

	lua := "" +
		"function OnGetUstate_432( ustate, self, attackerid )\n" +
		"\tPlayer.AddUstate( attackerid, 434, 4, 500, attackerid );\n" +
		"\tPlayer.DelUstate( self, 432 );\n" +
		"\tPlayer.AddUstate( self, 433, 1, 500, attackerid );\n" +
		"\tPlayer.AddUstate( self, 30, 4, 500, attackerid );\n" + // 原生状态，不应导出
		"\treturn;\n" +
		"end\n" +
		"function OnGetUstate_433( ustate, self, attackerid )\n" +
		"\tPlayer.AddUstate( self, 434, 1, 500, attackerid );\n" + // 二级依赖
		"\treturn;\n" +
		"end\n"

	deps := buffDependencies("432", lua, selfMade)
	got := strings.Join(deps, ",")
	if got != "433,434" {
		t.Errorf("依赖应为 433,434，实际 %q", got)
	}
}

// TestBuffOnlyApplySkipsComboReconcile pins the contract behind the merge
// import fix: a stale combo rule (one that names hit properties the next
// render would renumber) must not veto a buff-only write, because that write
// never renumbers stages. A full weapon apply still refuses it, so the guard
// is preserved for the path it was written for.
func TestBuffOnlyApplySkipsComboReconcile(t *testing.T) {
	installed := os.Getenv("OPENKFO_WEAPON_TEST_CLIENT")
	if installed == "" {
		t.Skip("set OPENKFO_WEAPON_TEST_CLIENT for installed resource validation")
	}
	raw, err := os.ReadFile(configPath(installed))
	if err != nil {
		t.Fatal(err)
	}
	// Work in a temp client so nothing is written inside the installed one.
	client := t.TempDir()
	if err = os.MkdirAll(filepath.Join(client, "Data"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(configPath(client), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	entry := &clientBaseline{Directory: client, File: "baseline.spf2", SourceHash: digest(raw), AppliedHash: digest(raw)}
	if err = os.WriteFile(filepath.Join(client, "baseline.spf2"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	source, err := loadArchive(filepath.Join(client, "baseline.spf2"))
	if err != nil {
		t.Fatal(err)
	}
	if err = source.verify(); err != nil {
		t.Fatal(err)
	}
	itemText, err := source.text("item.txt")
	if err != nil {
		t.Fatal(err)
	}
	items, err := itemsFromText(entry.Directory, itemText, true, true)
	if err != nil {
		t.Fatal(err)
	}
	info, err := inspect(source, items)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.weapons) == 0 {
		t.Fatal("no weapons inspected")
	}
	selfMade := ""
	for _, weapon := range info.weapons {
		if len(weapon.Stages) > 0 {
			selfMade = strconv.Itoa(weapon.ID)
			break
		}
	}
	if selfMade == "" {
		t.Fatal("no weapon with stages")
	}
	// A rule naming numbers the render cannot produce: the reference is dead
	// and reconcile must reject it.
	state := &weaponState{
		Baselines:  map[string]*clientBaseline{normalizeDir(client): entry},
		ComboRules: map[string]ComboRuleSet{selfMade: {Black: []ComboRuleLink{{Prev: "not-a-real-number", Cur: "not-a-real-number"}}}},
	}
	_, err = prepareClient(entry, client, state, state.Applied, info)
	if err == nil {
		t.Fatal("完整武器应用路径应当拒绝失效的连招编号")
	}
	if !strings.Contains(err.Error(), "连招限制引用了客户端读不到的被动编号") {
		t.Fatalf("报错文案不符：%v", err)
	}
	if _, err = prepareClient(entry, client, state, state.Applied, info, withoutComboReconcile()); err != nil {
		t.Fatalf("buff-only 路径不应被连招校验拦下：%v", err)
	}
}
