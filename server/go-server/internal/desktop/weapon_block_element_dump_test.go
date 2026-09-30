package desktop

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// 全库口径核对：用真实 spf2 解包目录跑一遍采集，确认真实数据里
// 「按容器限定」与旧的「递归整棵树」结果一致 —— 即本次修复不会漏掉任何
// 本来就该采到的元素，只纠正语义标签与新增时的落点。
//
// 用法：OPENKFO_DUMP_DIR=<spf2 解包目录> go test ./internal/desktop -run TestBlockElementDumpParity -v
func TestBlockElementDumpParity(t *testing.T) {
	dir := os.Getenv("OPENKFO_DUMP_DIR")
	if dir == "" {
		t.Skip("set OPENKFO_DUMP_DIR to a spf2 dump directory")
	}
	files, err := filepath.Glob(filepath.Join(dir, "animation*.xml"))
	if err != nil {
		t.Fatalf("glob 失败：%v", err)
	}
	if len(files) == 0 {
		t.Fatalf("%s 下没有 animation*.xml", dir)
	}

	scoped := map[string]int{}   // 新口径：按 Placement 限定容器
	recursive := map[string]int{} // 旧口径：递归整棵 AnmDesc 树
	blocks := 0
	var badFiles []string

	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("读取 %s 失败：%v", file, err)
		}
		root, err := parseXML(string(raw))
		if err != nil {
			// 数据本身就有个别 XML 不合法（如 animation__2011.xml），跳过即可。
			badFiles = append(badFiles, filepath.Base(file))
			continue
		}
		root.walk(func(n *xmlNode) {
			if n == nil || n.comment || n.tag != "AnmDesc" {
				return
			}
			blocks++
			for _, spec := range blockElementSpecs {
				scoped[spec.Tag] += len(blockElementsOf(n, spec))
				recursive[spec.Tag] += countTagDeep(n, spec.Tag)
			}
		})
	}
	if len(badFiles) > 0 {
		t.Logf("跳过 %d 个 XML 本身不合法的文件：%v", len(badFiles), badFiles)
	}

	if blocks == 0 {
		t.Fatalf("%s 下没解析出任何 <AnmDesc>", dir)
	}
	t.Logf("AnmDesc 块数：%d", blocks)
	for _, spec := range blockElementSpecs {
		t.Logf("%-16s placement=%-9s scoped=%-5d recursive=%-5d",
			spec.Tag, spec.Placement, scoped[spec.Tag], recursive[spec.Tag])
		if scoped[spec.Tag] != recursive[spec.Tag] {
			t.Errorf("%s：限定容器后采集数 %d 与递归口径 %d 不一致（可能漏采）",
				spec.Tag, scoped[spec.Tag], recursive[spec.Tag])
		}
	}

	// 命中点 / 条件这两类必须在真实数据里确实存在，否则这条约束就没被测到。
	if scoped["UState"] == 0 || scoped["Ustate"] == 0 {
		t.Errorf("真实数据里应当同时存在 <LockedAttackHit> 内的 UState 与 <Condition> 内的 Ustate")
	}

	// 文本级写回：拿一个真实块改 UState，确认它始终待在 <LockedAttackHit> 里，
	// 而且删光后重新插入也回落到容器内 —— 这正是以前会插到 </AnmDesc> 前的 bug。
	anmDesc := regexp.MustCompile(`(?s)<AnmDesc\b[^>]*>.*?</AnmDesc\s*>`)
	sample := ""
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		for _, block := range anmDesc.FindAllString(string(raw), -1) {
			if strings.Contains(block, "<LockedAttackHit") && strings.Contains(block, "<UState") {
				sample = block
				break
			}
		}
		if sample != "" {
			break
		}
	}
	if sample == "" {
		t.Fatalf("真实数据里没找到带 <LockedAttackHit> + <UState> 的动作块")
	}

	rewritten, changed := rewriteBlockElements(sample, "UState",
		[]BlockElement{element("UState", "id", "1", "level", "1", "duration", "1000")})
	if !changed {
		t.Fatalf("改写命中点里的 UState 应当有变化")
	}
	assertUStateInsideHit(t, rewritten)

	emptied, _ := rewriteBlockElements(sample, "UState", nil)
	if strings.Contains(emptied, "<UState") {
		t.Fatalf("空列表应当删光 UState")
	}
	reinserted, ok := rewriteBlockElements(emptied, "UState",
		[]BlockElement{element("UState", "id", "2", "level", "1", "duration", "1000")})
	if !ok || !strings.Contains(reinserted, "<UState") {
		t.Fatalf("删光后应当还能插回容器")
	}
	assertUStateInsideHit(t, reinserted)
}

// assertUStateInsideHit 断言所有 <UState> 都落在 <LockedAttackHit> 区间内，且文本仍是合法 XML。
func assertUStateInsideHit(t *testing.T, block string) {
	t.Helper()
	if _, err := parseXML(block); err != nil {
		t.Fatalf("改写后不再是合法 XML：%v", err)
	}
	spans := lockedAttackHitPattern.FindAllStringIndex(block, -1)
	for _, loc := range blockElementPatterns["UState"].FindAllStringIndex(block, -1) {
		if !inAnySpan(spans, loc[0]) {
			t.Fatalf("UState 跑到 <LockedAttackHit> 外面了：%s", block)
		}
	}
}

// countTagDeep 是旧的递归口径，仅用于对照。
func countTagDeep(n *xmlNode, tag string) int {
	if n == nil || n.comment {
		return 0
	}
	count := 0
	if n.tag == tag {
		count++
	}
	for _, child := range n.children {
		count += countTagDeep(child, tag)
	}
	return count
}
