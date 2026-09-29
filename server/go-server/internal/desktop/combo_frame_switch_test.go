package desktop

import (
	"os"
	"path/filepath"
	"testing"
)

// 帧级按键切换（CustomStateSwitch）是 delayacttable 之外的连招通道。这个
// 测试钉死一个真实案例：253013 的 2061（跳X）动作块内按 X（keycode=7）切到
// 2131 —— delayacttable 里没有这条转移，但游戏里能连。
func TestComboFrameSwitches(t *testing.T) {
	client := os.Getenv("OPENKFO_WEAPON_TEST_CLIENT")
	if client == "" {
		t.Skip("set OPENKFO_WEAPON_TEST_CLIENT for installed resource validation")
	}
	items, err := catalog(client, false)
	if err != nil {
		t.Fatal(err)
	}
	a, err := loadArchive(filepath.Join(client, "Data", "config.spf2"))
	if err != nil {
		t.Fatal(err)
	}
	info, err := inspect(a, items)
	if err != nil {
		t.Fatal(err)
	}
	switches := comboFrameSwitches(a, info, "253013")
	found := false
	for _, f := range switches {
		if f.State != "2061" || f.Next != "2131" {
			continue
		}
		found = true
		if f.KeyCode != "7" || f.KeyLabel != "X" {
			t.Fatalf("2061→2131 按键应为 7/X，实际 %q/%q", f.KeyCode, f.KeyLabel)
		}
		if f.Window == "" {
			t.Fatal("2061→2131 应带帧窗口")
		}
	}
	if !found {
		t.Fatalf("253013 应含帧级切换 2061→2131，实际 %v", switches)
	}
}
