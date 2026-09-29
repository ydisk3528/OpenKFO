package desktop

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"hash/crc32"
	"sort"
	"strings"
	"testing"
)

// synthArchive 造一个最小可用的 SGDP：根目录节点平铺全部条目（标签上限 8 字节）。
// 只依赖格式本身，所以不需要真实客户端就能跑。
func synthArchive(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	count := len(names)

	blocks := make([]byte, 0, 1<<16)
	table := make([]byte, count*8)
	checksums := make([]byte, count*8)
	for i, name := range names {
		var buf bytes.Buffer
		writer := zlib.NewWriter(&buf)
		if _, err := writer.Write(entries[name]); err != nil {
			t.Fatalf("压缩 %s：%v", name, err)
		}
		if err := writer.Close(); err != nil {
			t.Fatalf("压缩 %s：%v", name, err)
		}
		encoded := append([]byte(nil), buf.Bytes()...)
		for j := range encoded {
			encoded[j] ^= 255 ^ configKey[j%len(configKey)]
		}
		block := append([]byte{0, 0x22, 0, 0}, encoded...)
		little.PutUint32(table[i*8:], uint32(64+len(blocks)))
		little.PutUint32(table[i*8+4:], uint32(len(encoded)))
		blocks = append(blocks, block...)
		little.PutUint32(checksums[i*8:], crc32.ChecksumIEEE(block))
	}

	tree := make([]byte, 4+count*12)
	little.PutUint32(tree, uint32(count))
	for i, name := range names {
		if len(name) > 8 {
			t.Fatalf("标签不能超过 8 字节：%q", name)
		}
		copy(tree[4+i*12:], name)
		little.PutUint32(tree[4+i*12+8:], uint32(0xff000000|i))
	}

	header := make([]byte, 64)
	copy(header, "SGDP Package File.\x00")
	little.PutUint32(header[32:], 0x20)
	little.PutUint32(header[36:], 2)
	treeAt := 64 + len(blocks)
	tableAt := treeAt + len(tree)
	checksumsAt := tableAt + count*8
	little.PutUint32(header[40:], uint32(treeAt))
	little.PutUint32(header[44:], uint32(tableAt))
	little.PutUint32(header[48:], uint32(checksumsAt))
	little.PutUint32(header[52:], uint32(count))
	summary := make([]byte, 0, count*4)
	for i := 0; i < count; i++ {
		summary = append(summary, checksums[i*8:i*8+4]...)
	}
	little.PutUint32(header[56:], crc32.ChecksumIEEE(summary))
	copy(header[60:], []byte{0x8f, 0xec, 0x07, 0x1f})

	out := append([]byte(nil), header...)
	out = append(out, blocks...)
	out = append(out, tree...)
	out = append(out, table...)
	out = append(out, checksums...)
	return out
}

func synthFixture(t *testing.T) map[string][]byte {
	t.Helper()
	big := strings.Repeat("<AnmDesc id=\"1\"></AnmDesc>\n", 400)
	return map[string][]byte{
		"item.txt": []byte(strings.Repeat("1\trow\n", 300)),
		"skill1":   []byte("<SkillProperty>" + strings.Repeat("<PropertyItem/>", 500) + "</SkillProperty>"),
		"anim1":    []byte("<AnmInfo>" + big + "</AnmInfo>"),
		"anim2":    []byte("<AnmInfo>" + big + "</AnmInfo>"),
		"delay1":   []byte("<DelayActTable></DelayActTable>"),
		"effect1":  []byte("<ActEffect></ActEffect>"),
		"combo1":   []byte("<ComboRuleList></ComboRuleList>"),
		"mapmgr":   []byte("<MapMgr></MapMgr>"),
		"tiny.txt": []byte("x"),
		"other1":   []byte(strings.Repeat("0123456789", 100)),
	}
}

// 追加式写入每改一次就留一份旧副本；紧凑重建必须把它们全部收掉，
// 而且**重建后的体积与「改了多少次」无关**。
func TestCompactArchiveDropsStaleCopies(t *testing.T) {
	data := synthArchive(t, synthFixture(t))
	base, err := parseArchive(data)
	if err != nil {
		t.Fatalf("初始归档解析失败：%v", err)
	}
	if err = base.verify(); err != nil {
		t.Fatalf("初始归档校验失败：%v", err)
	}
	initial := len(data)

	// 模拟一次「应用到游戏」：连着改写同一个条目好几轮。
	round := func(n int) {
		t.Helper()
		for i := 0; i < n; i++ {
			a, err := parseArchive(data)
			if err != nil {
				t.Fatalf("第 %d 轮解析失败：%v", i, err)
			}
			payload := fmt.Sprintf("round %d\n", i) + strings.Repeat("y", 3000)
			data, err = a.replace(map[string][]byte{"item.txt": []byte(payload)})
			if err != nil {
				t.Fatalf("第 %d 轮写入失败：%v", i, err)
			}
		}
	}
	round(6)

	leaked, err := parseArchive(data)
	if err != nil {
		t.Fatalf("泄漏后解析失败：%v", err)
	}
	if err = leaked.verify(); err != nil {
		t.Fatalf("泄漏后校验失败：%v", err)
	}
	if len(data) <= initial {
		t.Fatalf("前置条件不成立：改写 6 次后应该变大，实际 %d -> %d", initial, len(data))
	}

	compact, err := compactArchive(data)
	if err != nil {
		t.Fatalf("紧凑重建失败：%v", err)
	}
	if len(compact) >= len(data) {
		t.Fatalf("没有回收：%d -> %d", len(data), len(compact))
	}
	after, err := parseArchive(compact)
	if err != nil {
		t.Fatalf("紧凑包解析失败：%v", err)
	}
	if err = after.verify(); err != nil {
		t.Fatalf("紧凑包校验失败：%v", err)
	}
	if len(after.entries) != len(leaked.entries) {
		t.Fatalf("条目数变了：%d -> %d", len(leaked.entries), len(after.entries))
	}

	// 逐条目内容必须一字不差
	for name := range leaked.entries {
		before, err := leaked.raw(name)
		if err != nil {
			t.Fatalf("读旧条目 %s：%v", name, err)
		}
		now, err := after.raw(name)
		if err != nil {
			t.Fatalf("读新条目 %s：%v", name, err)
		}
		if !bytes.Equal(before, now) {
			t.Fatalf("条目 %s 内容变了", name)
		}
	}

	// 再改 6 次，紧凑体积必须一模一样 —— 这就是「文件不再膨胀」的判据
	size1 := len(compact)
	round(6)
	compact2, err := compactArchive(data)
	if err != nil {
		t.Fatalf("二次紧凑失败：%v", err)
	}
	if len(compact2) != size1 {
		t.Fatalf("又改了 6 次之后紧凑体积变了：%d -> %d", size1, len(compact2))
	}
	t.Logf("初始 %d → 改 6 次膨胀到 %d → 紧凑回 %d；又改 6 次仍为 %d",
		initial, len(data), size1, len(compact2))
}

// 对已经紧凑的归档再跑一次，必须逐字节不变（幂等），并原样返回。
func TestCompactArchiveIsIdempotent(t *testing.T) {
	first, err := compactArchive(synthArchive(t, synthFixture(t)))
	if err != nil {
		t.Fatalf("首次紧凑失败：%v", err)
	}
	second, err := compactArchive(first)
	if err != nil {
		t.Fatalf("二次紧凑失败：%v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("不幂等：%d -> %d", len(first), len(second))
	}
	third, err := compactArchive(second)
	if err != nil {
		t.Fatalf("三次紧凑失败：%v", err)
	}
	if !bytes.Equal(second, third) {
		t.Fatal("第三次又变了")
	}
}

// 已经在最小体积上时，compactArchive 必须原样返回（不做无意义的改写）。
func TestCompactArchiveKeepsMinimalInput(t *testing.T) {
	minimal := synthArchive(t, synthFixture(t))
	got, err := compactArchive(minimal)
	if err != nil {
		t.Fatalf("紧凑失败：%v", err)
	}
	if !bytes.Equal(got, minimal) {
		t.Fatalf("最小体积的归档被改动了：%d -> %d", len(minimal), len(got))
	}
}

// 真正的改动必须一字不差地留下，而且体积要跟着真内容涨 ——
// 紧凑只丢「没人引用的旧副本」，绝不会把有效改动吃掉。
func TestCompactArchiveKeepsRealChanges(t *testing.T) {
	compact1, err := compactArchive(synthArchive(t, synthFixture(t)))
	if err != nil {
		t.Fatalf("首次紧凑失败：%v", err)
	}
	a, err := parseArchive(compact1)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}

	// 一次「有效改动」：把 item.txt 加长（多了 500 行）
	changed := []byte(strings.Repeat("1\tnew-row-added-by-the-editor\n", 500))
	leaked, err := a.replace(map[string][]byte{"item.txt": changed})
	if err != nil {
		t.Fatalf("写入失败：%v", err)
	}

	compact2, err := compactArchive(leaked)
	if err != nil {
		t.Fatalf("二次紧凑失败：%v", err)
	}
	b, err := parseArchive(compact2)
	if err != nil {
		t.Fatalf("紧凑包解析失败：%v", err)
	}
	if err = b.verify(); err != nil {
		t.Fatalf("紧凑包校验失败：%v", err)
	}

	// 1) 改动还在，且一字不差
	got, err := b.raw("item.txt")
	if err != nil {
		t.Fatalf("读 item.txt：%v", err)
	}
	if !bytes.Equal(got, changed) {
		t.Fatalf("有效改动被吃掉了：%d 字节 -> %d 字节", len(changed), len(got))
	}

	// 2) 体积跟着真内容涨（而不是被压回原样）
	if len(compact2) <= len(compact1) {
		t.Fatalf("有效改动没有体现在体积上：%d -> %d", len(compact1), len(compact2))
	}

	// 3) 但仍然把旧副本丢掉了 —— 比「追加式写入」的结果小得多
	if len(compact2) >= len(leaked) {
		t.Fatalf("没有回收旧副本：%d -> %d", len(leaked), len(compact2))
	}

	// 4) 其余条目不受影响
	for name := range b.entries {
		if name == "item.txt" {
			continue
		}
		before, err := a.raw(name)
		if err != nil {
			t.Fatalf("读旧条目 %s：%v", name, err)
		}
		now, err := b.raw(name)
		if err != nil {
			t.Fatalf("读新条目 %s：%v", name, err)
		}
		if !bytes.Equal(before, now) {
			t.Fatalf("无关条目 %s 被改动了", name)
		}
	}
	t.Logf("紧凑后 %d，加长 item.txt 后追加式写入 %d、再紧凑 %d（改动保留、旧副本丢弃）",
		len(compact1), len(leaked), len(compact2))
}

// 坏归档必须报错，不能靠紧凑把问题藏起来。
func TestCompactArchiveRejectsBrokenInput(t *testing.T) {
	if _, err := compactArchive([]byte("not an archive")); err == nil {
		t.Fatal("坏输入应当报错")
	}
	data := synthArchive(t, synthFixture(t))
	data[64+2] = 0x99 // 把第一个块的标志改坏
	if _, err := compactArchive(data); err == nil {
		t.Fatal("块标志坏掉时应当报错")
	}
}
