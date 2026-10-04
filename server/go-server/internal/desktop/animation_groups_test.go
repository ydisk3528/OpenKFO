package desktop

import (
	"os"
	"strings"
	"testing"
)

// 「6 位子文件组」动作号的回归验证。
//
// 背景：动画文件的命名有两套规则（见 animation_groups.go）。只认
// animation/<前4位>.xml 会让 1002/1006/3001/3003/3004/3005/3006/3008 这 8 组
// 整个读不到 —— 它们的块按角色拆在 animation/100201.xml 这类子文件里。
//
// 用法：OPENKFO_CLIENT_ARCHIVE=<config.spf2 路径> go test ./internal/desktop -run TestAnimationGroup -v
func TestAnimationGroupResolution(t *testing.T) {
	path := os.Getenv("OPENKFO_CLIENT_ARCHIVE")
	if path == "" {
		t.Skip("OPENKFO_CLIENT_ARCHIVE is not configured")
	}
	a, err := loadArchive(path)
	if err != nil {
		t.Fatal(err)
	}

	// 组 1002：没有同名 4 位文件，只有三个按角色拆开的子文件。
	files := a.groupFiles("1002")
	if len(files) != 3 {
		t.Fatalf("group 1002 files = %v", files)
	}
	for _, file := range files {
		if file == "animation/1002.xml" {
			t.Fatalf("animation/1002.xml 不该存在：%v", files)
		}
		if !strings.HasPrefix(file, "animation/1002") {
			t.Fatalf("组内检索混进了别的文件：%v", files)
		}
	}
	// 1002007 = 组 1002 + 块号 7，落在子文件里。
	if file := a.animationFileFor("1002007"); file != "animation/100201.xml" {
		t.Fatalf("1002007 -> %q，期望 animation/100201.xml", file)
	}
	// 子文件组不支持改写：改一份只对一个角色生效。
	if _, err := a.animationWriteFile("1002007"); err == nil {
		t.Fatal("子文件组应当拒绝改写")
	}

	// 组 3001 同样是子文件组。
	if file := a.animationFileFor("3001010"); file != "animation/300101.xml" {
		t.Fatalf("3001010 -> %q，期望 animation/300101.xml", file)
	}

	// 规则 A：同名 4 位文件仍然照旧，可读可写。
	if file := a.animationFileFor("2001521011"); file != "animation/2001.xml" {
		t.Fatalf("2001521011 -> %q，期望 animation/2001.xml", file)
	}
	if file, err := a.animationWriteFile("2001521011"); err != nil || file != "animation/2001.xml" {
		t.Fatalf("2001521011 写出目标 = %q, %v", file, err)
	}

	// 5005 组两种都有：同名文件优先，但有一个块只在子文件里（见组内检索）。
	if files := a.groupFiles("5005"); len(files) < 2 || files[0] != "animation/5005.xml" {
		t.Fatalf("group 5005 files = %v", files)
	}

	// 组内索引必须真的取到块，且块内容与文件归属一致。
	index := a.groupBlockIndex("1002")
	found, ok := index[7]
	if !ok || len(found) == 0 {
		t.Fatalf("group 1002 里没有块号 7")
	}
	if found[0].file != "animation/100201.xml" {
		t.Fatalf("块号 7 落在 %q", found[0].file)
	}
	if strings.TrimSpace(found[0].block.node.get("id")) != "7" {
		t.Fatalf("块号对不上：%q", found[0].block.node.get("id"))
	}
	if len(a.groupBlockIndex("1006")) == 0 || len(a.groupBlockIndex("3001")) == 0 {
		t.Fatal("1006/3001 组内检索为空")
	}

	// 收口验证：inspect() 必须能把这些组的块报出来（改前是 0 块）。
	// items 传空：本用例只关心块，不关心武器名（inspect 对空表安全）。
	info, err := inspect(a, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"1002007", "3001010"} {
		if len(info.blocks[actionKey(action)]) == 0 {
			t.Fatalf("inspect 读不到 %s（键 %s）", action, actionKey(action))
		}
	}
	// 全部 6 个「无同名文件」的组都必须能读出块来。
	for _, prefix := range []string{"1002", "1006", "3001", "3003", "3004", "3005"} {
		count := 0
		for key := range info.blocks {
			if strings.HasPrefix(key, prefix+"/") {
				count++
			}
		}
		if count == 0 {
			t.Fatalf("组 %s 一个块都没读到", prefix)
		}
	}
}
