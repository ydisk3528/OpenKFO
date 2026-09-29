package desktop

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 客户端的更新器会自己替换 config.spf2，此时编辑器读到的配置已经和上次写入的
// 不一致。合并后的编辑器按「每客户端基线」工作：它不静默接管新包，而是如实报告
// 差异、拒绝覆盖，把接管交给用户显式触发的「重新采集基线」。这条测试守住这个
// 契约——旧方案不丢、客户端包不被碰、重新采集基线后新包里的自建武器才进入编辑器。
func TestWeaponWriteStopsWhenClientConfigReplaced(t *testing.T) {
	client := os.Getenv("OPENKFO_WEAPON_TEST_CLIENT")
	if client == "" {
		t.Skip("set OPENKFO_WEAPON_TEST_CLIENT")
	}
	current, err := loadArchive(filepath.Join(client, "Data", "config.spf2"))
	if err != nil {
		t.Fatal(err)
	}
	items, err := catalog(client, false)
	if err != nil {
		t.Fatal(err)
	}
	table, err := current.text("itemact.txt")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(table, "\n")
	found := false
	for i, line := range lines {
		if strings.HasPrefix(line, "253300\t") {
			lines[i] = ""
			found = true
		}
	}
	if !found {
		t.Skip("fixture needs custom weapon 253300")
	}
	encoded, err := encodeText(strings.Join(lines, "\n"))
	if err != nil {
		t.Fatal(err)
	}
	// 基线是「客户端还不带 253300」的样子：更新器刚把这一行补了进去。
	old, err := current.replace(map[string][]byte{"itemact.txt": encoded})
	if err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	os.Mkdir(filepath.Join(target, "Data"), 0700)
	packagePath := filepath.Join(target, "Data", "config.spf2")
	os.WriteFile(packagePath, current.data, 0600)
	folder := filepath.Join(t.TempDir(), "plans")
	os.Mkdir(folder, 0700)
	os.WriteFile(filepath.Join(folder, "original.spf2"), old, 0600)
	previous := weaponState{SourceHash: digest(old), AppliedHash: digest(old), Drafts: map[string][]Rule{"253013": {{Stage: 1, Buff: 1, Level: 1, Duration: 3000}}}, Applied: map[string][]Rule{}}
	saved, _ := json.Marshal(previous)
	os.WriteFile(filepath.Join(folder, "settings.json"), saved, 0600)

	// 编辑器仍然读得到客户端，但要如实报告配置和上次写入的不一致。
	result, err := weaponHandle(Request{Operation: "weapon_catalog"}, target, items, folder)
	if err != nil {
		t.Fatal(err)
	}
	c := result.(map[string]any)
	if got := c["client"].(map[string]any)["state"]; got != "有差异" {
		t.Fatalf("want state 有差异, got %v", got)
	}
	// 用户没确认之前，旧方案不能被悄悄清掉。
	if len(c["drafts"].(map[string][]Rule)) != 1 {
		t.Fatal("old plans discarded before the user confirmed")
	}

	// 写入被拒绝，且连客户端包都不许碰。
	_, err = weaponHandle(Request{Operation: "weapon_apply", Weapon: 253013, Rules: []Rule{}, Revision: c["revision"].(string)}, target, items, folder)
	if err != nil && strings.Contains(err.Error(), "请先退出游戏客户端") {
		t.Skip("game client is running; the write path refuses for an unrelated reason")
	}
	if err == nil || !strings.Contains(err.Error(), "重新采集基线") {
		t.Fatalf("want a refusal mentioning 重新采集基线, got %v", err)
	}
	if b, _ := os.ReadFile(packagePath); !bytes.Equal(b, current.data) {
		t.Fatal("a refused write still touched the client package")
	}
	if b, _ := os.ReadFile(filepath.Join(folder, "settings.json")); !bytes.Equal(b, saved) {
		t.Fatal("a refused write still rewrote the saved plans")
	}

	// 显式重新采集基线之后，新配置包里的自建武器才进入编辑器。
	if _, err = weaponHandle(Request{Operation: "weapon_client_rebase"}, target, items, folder); err != nil {
		t.Fatal(err)
	}
	result, err = weaponHandle(Request{Operation: "weapon_catalog"}, target, items, folder)
	if err != nil {
		t.Fatal(err)
	}
	c = result.(map[string]any)
	if got := c["client"].(map[string]any)["state"]; got == "有差异" {
		t.Fatalf("rebase did not clear the difference: %v", got)
	}
	found = false
	for _, w := range c["weapons"].([]Weapon) {
		if w.ID == 253300 && len(w.Stages) > 0 {
			found = true
		}
	}
	if !found {
		t.Fatal("custom weapon missing after rebase")
	}
}
