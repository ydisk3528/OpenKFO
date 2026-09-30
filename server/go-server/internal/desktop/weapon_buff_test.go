package desktop

import (
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
