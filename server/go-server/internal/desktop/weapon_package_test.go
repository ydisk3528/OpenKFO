package desktop

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 发版包唯一的安装动作是"整包解压、覆盖客户端根目录"，所以 zip 里的路径
// 必须就是客户端根目录下的相对路径。这条测试把包解开到一个空目录，直接
// 验证落点——比逐个断言条目名更贴近真实用法。
func TestWritePackageZipIsUnzipOverClientRoot(t *testing.T) {
	client := t.TempDir()
	files := []packageFile{
		{Path: "Data/Weapon/Model/253300.dff", Kind: "model"},
		{Path: "Data/animation/133031.anm", Kind: "animation"},
		{Path: "Data/UI/item/weapon/253300.png", Kind: "icon"},
	}
	for _, file := range files {
		target := filepath.Join(client, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		body := bytes.Repeat([]byte{'x'}, 64)
		if err := os.WriteFile(target, body, 0600); err != nil {
			t.Fatal(err)
		}
		file.Size = int64(len(body))
		_ = file
	}
	out := filepath.Join(t.TempDir(), "nested", "weapon-253300.zip")
	size, sum, err := writePackageZip(out, client, []byte("config-bytes"), files,
		[]string{"itemact.txt"}, []map[string]any{{"id": 253300, "name": "流氓拳"}}, []int{253300})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(raw)) != size || len(sum) != 64 {
		t.Fatalf("size/hash mismatch: %d %s", size, sum)
	}
	reader, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, entry := range reader.File {
		names = append(names, entry.Name)
		if strings.Contains(entry.Name, "\\") {
			t.Fatalf("zip 条目必须用正斜杠: %s", entry.Name)
		}
	}
	// 顺序就是收集顺序：配置包打头，安装说明收尾，中间是素材。
	want := []string{
		"Data/config.spf2",
		"Data/Weapon/Model/253300.dff",
		"Data/animation/133031.anm",
		"Data/UI/item/weapon/253300.png",
		"安装说明.txt",
	}
	if strings.Join(names, "|") != strings.Join(want, "|") {
		t.Fatalf("unexpected layout: %v", names)
	}
	// 解到空目录后应当得到与客户端一致的目录树。
	landed := t.TempDir()
	for _, entry := range reader.File {
		target := filepath.Join(landed, filepath.FromSlash(entry.Name))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		handle, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		var buffer bytes.Buffer
		if _, err = buffer.ReadFrom(handle); err != nil {
			t.Fatal(err)
		}
		handle.Close()
		if err = os.WriteFile(target, buffer.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
	}
	config, err := os.ReadFile(filepath.Join(landed, "Data", "config.spf2"))
	if err != nil || string(config) != "config-bytes" {
		t.Fatalf("config.spf2 没落到客户端根目录: %v", err)
	}
	notes, err := os.ReadFile(filepath.Join(landed, "安装说明.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(notes), "流氓拳") || !strings.Contains(string(notes), "itemact.txt") {
		t.Fatalf("安装说明缺少关键信息: %s", notes)
	}
}

// collectWeaponAssets 曾经把 GM 解析出来的图标**缓存绝对路径**当成客户端相对
// 路径，于是 zip 里出现 Data/UI/C:/Users/... 这种条目。图标只能取 item.txt
// 第 10 列（Fields[9]）的原始值。
func TestCollectWeaponAssetsUsesItemTxtIconColumn(t *testing.T) {
	client := t.TempDir()
	write := func(relative string) {
		target := filepath.Join(client, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("Data/Weapon/Model/253300.dff")
	write("Data/UI/item/weapon/253300.png")
	items := []Item{{
		ID:     253300,
		Name:   "流氓拳",
		Icon:   filepath.Join("C:", "Users", "someone", "AppData", "Local", "OpenKFO", "gm-icons", "v1", "deadbeef.png"),
		Fields: []string{"253300", "", "", "", "", "", "", "253300.dff", "", `item/weapon/253300.png`},
	}}
	include := map[string]bool{"model": true, "texture": true, "icon": true}
	files, missing := collectWeaponAssets(client, nil, items, []int{253300}, include)
	if len(missing) != 0 {
		t.Fatalf("不该报缺失: %v", missing)
	}
	paths := []string{}
	for _, file := range files {
		paths = append(paths, file.Path)
	}
	joined := strings.Join(paths, "|")
	if !strings.Contains(joined, "Data/UI/item/weapon/253300.png") {
		t.Fatalf("图标没从 item.txt 列取到: %v", paths)
	}
	if !strings.Contains(joined, "Data/Weapon/Model/253300.dff") {
		t.Fatalf("模型没带上: %v", paths)
	}
	for _, path := range paths {
		if strings.Contains(path, ":") || strings.Contains(path, "..") {
			t.Fatalf("出现了非法 zip 条目: %s", path)
		}
	}

	// 反过来：列里真的是绝对路径时，要报"不是客户端相对路径"而不是塞进包里。
	bad := []Item{{
		ID:     253300,
		Fields: []string{"253300", "", "", "", "", "", "", "253300.dff", "", `C:\Users\someone\icon.png`},
	}}
	files, missing = collectWeaponAssets(client, nil, bad, []int{253300}, include)
	if len(missing) == 0 {
		t.Fatal("绝对路径图标应当被报出来")
	}
	for _, file := range files {
		if file.Kind == "icon" {
			t.Fatalf("非法图标不该进包: %s", file.Path)
		}
	}
}

// 找不到素材时：必需的（模型/图标/动作）要报 missing，可选的（贴图、音效、
// 特效）静默跳过——音效/特效编号并不能稳定推出文件名，报出来只会是噪音。
func TestCollectWeaponAssetsSkipsOptionalMisses(t *testing.T) {
	items := []Item{{
		ID:     253300,
		Fields: []string{"253300", "", "", "", "", "", "", "253300.dff", "", `item/weapon/253300.png`},
	}}
	include := map[string]bool{"model": true, "texture": true, "icon": true}
	_, missing := collectWeaponAssets(t.TempDir(), nil, items, []int{253300}, include)
	if len(missing) != 2 {
		t.Fatalf("模型和图标必须报缺失: %v", missing)
	}
	optional := map[string]bool{"texture": true}
	files, missing := collectWeaponAssets(t.TempDir(), nil, items, []int{253300}, optional)
	if len(missing) != 0 {
		t.Fatalf("贴图是可选的，不该报缺失: %v", missing)
	}
	if len(files) != 0 {
		t.Fatalf("客户端里没有的文件不该进包: %v", files)
	}
}

// 导出范围：all=false 只导指定的那把（且不认识就报错），all=true 导全部自建
// 并按编号排序，一台机器上一把都没有时要给出能看懂的提示。
func TestPackageWeaponIDs(t *testing.T) {
	state := &weaponState{Created: map[string]Blueprint{"253301": {}, "253300": {}}}
	info := &inspection{weapons: []Weapon{{ID: 253013}}}
	ids, err := packageWeaponIDs(Request{Weapon: 253013}, state, info)
	if err != nil || len(ids) != 1 || ids[0] != 253013 {
		t.Fatalf("单把导出失败: %v %v", ids, err)
	}
	ids, err = packageWeaponIDs(Request{Weapon: 253300}, state, info)
	if err != nil || len(ids) != 1 || ids[0] != 253300 {
		t.Fatalf("自建武器即使不在配置包里也该能导出: %v %v", ids, err)
	}
	if _, err = packageWeaponIDs(Request{Weapon: 999999}, state, info); err == nil {
		t.Fatal("未知武器应当报错")
	}
	ids, err = packageWeaponIDs(Request{All: true}, state, info)
	if err != nil || len(ids) != 2 || ids[0] != 253300 || ids[1] != 253301 {
		t.Fatalf("全部导出应当按编号排序: %v %v", ids, err)
	}
	if _, err = packageWeaponIDs(Request{All: true}, &weaponState{}, info); err == nil {
		t.Fatal("没有自建武器时应当报错")
	}
}

// itemact.txt 只给动作号，真正的 .anm / .wav / 特效都在 animation/<前4位>.xml
// 的块里。导包必须顺着这条链走，否则玩家端会"能装备、打不出动作"。
func TestWeaponBlockAssetsFollowsItemact(t *testing.T) {
	// runtime-local 在 server/ 下（go-server 的上一级），admin_test.go 的
	// installedRoot 指的不是这个位置，所以这里单独定位。
	root := os.Getenv("OPENKFO_TEST_RUNTIME")
	if root == "" {
		abs, err := filepath.Abs("../../../../runtime-local")
		if err != nil {
			t.Fatal(err)
		}
		root = abs
	}
	source, err := loadArchive(filepath.Join(root, "weapon-config/original.spf2"))
	if err != nil {
		t.Skip("runtime-local 固定装置不可用")
	}
	itemact, err := source.text("itemact.txt")
	if err != nil {
		t.Fatal(err)
	}
	// 基线里可能已经采集到真实的 253300 行（武器落盘后重新采过基线），
	// 而 weaponActions 只认第一行。先清掉已有的 253300 行，保证读到的是
	// 下面追加的假行——测试不依赖基线里有没有这把武器。
	kept := []string{}
	for _, line := range strings.Split(itemact, "\r\n") {
		cells := strings.Split(line, "\t")
		if len(cells) > 0 && strings.TrimSpace(cells[0]) == "253300" {
			continue
		}
		kept = append(kept, line)
	}
	// 追加一行：即便原文件结尾没有换行，先补一个也不会产生空行（splitRows 会跳过）。
	row := "\r\n253300\t2001133031\t2001133041\t0\t0\t0\t0\t0\t0\t0\t0\r\n"
	replaced, err := source.replace(map[string][]byte{
		"itemact.txt": []byte(strings.Join(kept, "\r\n") + row),
		// animationPattern 认的是成对的 <AnmDesc>…</AnmDesc>，自闭合写法匹配不到。
		"animation/2001.xml": []byte(`<?xml version="1.0" encoding="gb2312"?>
<AnmList>
	<AnmDesc id="133031">
		<Line lineid="133031"/>
		<Audio audioid="300001"/>
		<Effect effectid="500001"/>
	</AnmDesc>
	<AnmDesc id="133041">
		<Line lineid="133041"/>
		<Audio audioid="300002"/>
	</AnmDesc>
</AnmList>`),
	})
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := parseArchive(replaced)
	if err != nil {
		t.Fatal(err)
	}
	assets := weaponBlockAssets(rendered, "253300")
	if len(assets.lines) != 2 || assets.lines[0] != "133031" || assets.lines[1] != "133041" {
		t.Fatalf("lineid 没跟出来: %v", assets.lines)
	}
	if len(assets.audios) != 2 || assets.audios[0] != "300001" {
		t.Fatalf("audioid 没跟出来: %v", assets.audios)
	}
	if len(assets.effects) != 1 || assets.effects[0] != "500001" {
		t.Fatalf("effectid 没跟出来: %v", assets.effects)
	}
	// 别的武器的块不能被误带进来。
	other := weaponBlockAssets(rendered, "253013")
	for _, line := range other.lines {
		if line == "133031" {
			t.Fatal("把别的武器的动作算进来了")
		}
	}
}
