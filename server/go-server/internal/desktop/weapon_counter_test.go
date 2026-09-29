package desktop

import (
	"os"
	"strings"
	"testing"
)

// 招架窗口的解析：`<Counter nextstate>` 才算状态转移，没有 nextstate 的按“没有”算。
func TestCounterWindowReadsParryWindow(t *testing.T) {
	node, err := parseXML(`<AnmDesc id="119011">` +
		`<Counter startframe="3" endframe="25" nextstate="2012" angleoffset="0" anglehalfrange="55">` +
		`<TriggerBox centerx="0" centery="15" centerz="6" length="22" width="20" heigth="20"/>` +
		`</Counter></AnmDesc>`)
	if err != nil {
		t.Fatalf("构造块失败：%v", err)
	}
	window := counterWindowOf([]block{{node: node}})
	if window == nil {
		t.Fatal("应当读出招架窗口")
	}
	if window.StartFrame != 3 || window.EndFrame != 25 || window.NextState != "2012" {
		t.Fatalf("窗口不对：%+v", window)
	}
	if window.AngleHalfRange != 55 || window.AngleOffset != 0 {
		t.Fatalf("角度不对：%+v", window)
	}
	if window.TriggerBox != "22×20×20" {
		t.Fatalf("判定盒尺寸不对：%q", window.TriggerBox)
	}

	// 注释里的示例不算数（<!----> 里的内容不参与）。
	commented, err := parseXML(`<AnmDesc><!-- <Counter nextstate="2012"/> --><Anm id="1"/></AnmDesc>`)
	if err != nil {
		t.Fatalf("构造注释块失败：%v", err)
	}
	if counterWindowOf([]block{{node: commented}}) != nil {
		t.Fatal("注释里的 Counter 不该被当成真数据")
	}

	// 没有 nextstate（或为 0）的 Counter 不构成转移。
	for _, body := range []string{
		`<Counter startframe="1" endframe="999" anglehalfrange="55"/>`,
		`<Counter startframe="1" endframe="999" nextstate="0"/>`,
	} {
		node, err := parseXML(`<AnmDesc>` + body + `</AnmDesc>`)
		if err != nil {
			t.Fatalf("构造块失败：%v", err)
		}
		if counterWindowOf([]block{{node: node}}) != nil {
			t.Fatalf("没有 nextstate 的 Counter 不该算转移：%s", body)
		}
	}
	if counterWindowOf(nil) != nil {
		t.Fatal("没有动作块时应当返回 nil")
	}
}

// 白架只提示、不拦写盘：架住后切不到动作只影响那一招，武器其它功能照旧，
// 为它拒掉整个「应用到游戏/发版」会让复用供体块的自建武器直接提交不了。
func TestCounterWarningsAreAdvisoryOnly(t *testing.T) {
	info := &inspection{weapons: []Weapon{{
		ID: 253011, Name: "测试锤",
		Stages: []Stage{
			{Stage: 1, State: "2011", Action: "2001936", Supported: true,
				Counter: &CounterWindow{StartFrame: 0, EndFrame: 7, NextState: "2023", NextUnreachable: true}},
			{Stage: 2021, State: "2021", Action: "2001934", Supported: true,
				Counter: &CounterWindow{StartFrame: 0, EndFrame: 7, NextState: "2023"}},
		},
	}}}

	warnings := counterWarnings(info)
	if len(warnings) != 1 {
		t.Fatalf("只有那个目标状态缺失的招架该告警，实际 %v", warnings)
	}
	if !strings.Contains(warnings[0], "2023") || !strings.Contains(warnings[0], "测试锤") {
		t.Fatalf("告警应当点名武器与目标状态：%v", warnings)
	}

	// 目标可达就不提示。
	info.weapons[0].Stages[0].Counter.NextUnreachable = false
	if got := counterWarnings(info); len(got) != 0 {
		t.Fatalf("目标可达时不该提示：%v", got)
	}
	if got := counterWarnings(nil); got != nil {
		t.Fatalf("没有目录信息时应当返回 nil：%v", got)
	}
}

// 招架改写：替换保留原位置与兄弟节点；空编辑 = 删除（连缩进一起清掉）；
// 原本没有时插到 </AnmDesc> 前；注释里的示例不算数。
func TestRewriteCountersReplaceDeleteInsert(t *testing.T) {
	block := `<AnmDesc id="119011">` + "\n\t" +
		`<Counter startframe="3" endframe="25" nextstate="2012" angleoffset="0" anglehalfrange="55" >` +
		`<TriggerBox centerx="0" centery="15" centerz="6" length="22" width="20" heigth="20" /></Counter>` + "\n\t" +
		`<Audio frame="12" audioid="6001180" break="0" />` + "\n</AnmDesc>"
	fresh := &CounterEdit{
		Attrs: []FrameSwitchAttr{{"startframe", "5"}, {"endframe", "30"}, {"nextstate", "2013"},
			{"angleoffset", "0"}, {"anglehalfrange", "60"}},
		Box: []FrameSwitchAttr{{"centerx", "0"}, {"centery", "15"}, {"centerz", "6"},
			{"length", "22"}, {"width", "20"}, {"heigth", "20"}},
	}
	out, changed := rewriteCounters(block, fresh)
	if !changed {
		t.Fatal("应当报告改动")
	}
	if strings.Count(out, "<Counter") != 1 || strings.Count(out, "</Counter>") != 1 {
		t.Fatalf("替换后应当只有一个 Counter：%s", out)
	}
	if !strings.Contains(out, `nextstate="2013"`) || !strings.Contains(out, `endframe="30"`) {
		t.Fatalf("新属性没写进去：%s", out)
	}
	if !strings.Contains(out, "<Audio") {
		t.Fatalf("兄弟节点必须原样保留：%s", out)
	}
	if strings.Index(out, "<Counter") > strings.Index(out, "<Audio") {
		t.Fatalf("应当保留原来的位置（在 Audio 之前）：%s", out)
	}

	// 空编辑 = 删除，且不留空行。
	deleted, changed := rewriteCounters(out, nil)
	if !changed || strings.Contains(deleted, "<Counter") {
		t.Fatalf("应当删掉 Counter：%s", deleted)
	}
	if strings.Contains(deleted, "\n\n") {
		t.Fatalf("删除后不该留下空行：%q", deleted)
	}
	if !strings.Contains(deleted, "<Audio") {
		t.Fatal("删除时不该动别的节点")
	}

	// 原本没有 → 插到 </AnmDesc> 前。
	plain := `<AnmDesc id="1"><Audio frame="1" /></AnmDesc>`
	inserted, changed := rewriteCounters(plain, fresh)
	if !changed || strings.Count(inserted, "<Counter") != 1 {
		t.Fatalf("应当插入一条招架：%s", inserted)
	}
	if strings.Index(inserted, "<Counter") > strings.Index(inserted, "</AnmDesc") {
		t.Fatalf("不该插到 AnmDesc 之外：%s", inserted)
	}

	// 注释里的示例不算真数据：只删注释内的不该报告改动。
	commented := `<AnmDesc><!-- <Counter startframe="1" endframe="2" nextstate="2011">` +
		`<TriggerBox /></Counter> --><Audio frame="1" /></AnmDesc>`
	if _, changed := rewriteCounters(commented, nil); changed {
		t.Fatal("注释里的 Counter 不该被当成真数据")
	}
}

// 招架校验：状态要在动作表里、属性白名单、帧窗口成对且有序、判定盒六值齐全、
// 目标状态必须在表头里（**不要求**在本武器行里非空 —— 那是只提示的白架）。
func TestValidateCountersRejectsBadEdits(t *testing.T) {
	client := os.Getenv("OPENKFO_WEAPON_TEST_CLIENT")
	if client == "" {
		t.Skip("set OPENKFO_WEAPON_TEST_CLIENT for installed resource validation")
	}
	a, err := loadArchive(configPath(client))
	if err != nil {
		t.Fatalf("读取客户端失败：%v", err)
	}
	box := []FrameSwitchAttr{{"centerx", "0"}, {"centery", "15"}, {"centerz", "6"},
		{"length", "22"}, {"width", "20"}, {"heigth", "20"}}
	edit := func(next string, start, end string, extra ...FrameSwitchAttr) *CounterEdit {
		attrs := []FrameSwitchAttr{{"startframe", start}, {"endframe", end}, {"nextstate", next},
			{"angleoffset", "0"}, {"anglehalfrange", "55"}}
		return &CounterEdit{Attrs: append(attrs, extra...), Box: box}
	}

	// 合法（253119 的 2011 本来就有一条 3–25 → 2012）。
	if err := validateCounters(a, "253119", map[int]*CounterEdit{2011: edit("2012", "3", "25")}); err != nil {
		t.Fatalf("合法编辑被拒：%v", err)
	}
	// 删除（nil）合法。
	if err := validateCounters(a, "253119", map[int]*CounterEdit{2011: nil}); err != nil {
		t.Fatalf("删除被拒：%v", err)
	}

	for label, bad := range map[string]*CounterEdit{
		"目标状态不在表头":    edit("9999", "3", "25"),
		"缺 nextstate": {Attrs: []FrameSwitchAttr{{"startframe", "3"}, {"endframe", "25"}}, Box: box},
		"帧倒置":         edit("2012", "30", "5"),
		"帧超范围":        edit("2012", "0", "10000"),
		"未知属性":        edit("2012", "3", "25", FrameSwitchAttr{"bogus", "1"}),
		"判定盒不齐":       {Attrs: edit("2012", "3", "25").Attrs, Box: box[:3]},
	} {
		if err := validateCounters(a, "253119", map[int]*CounterEdit{2011: bad}); err == nil {
			t.Fatalf("%s 应当被拒", label)
		}
	}
	// 状态必须属于这张动作表。
	if err := validateCounters(a, "253119", map[int]*CounterEdit{1: edit("2012", "3", "25")}); err == nil {
		t.Fatal("不存在的状态号应当被拒")
	}
}
