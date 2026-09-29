package desktop

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// The catalogue is rendered from the selected client plus the global editing
// set, so a self-made weapon shows up on every client. The list marks the ones
// the client's own config.spf2 does not ship; this pins that classification.
func TestUndeployedWeaponIDs(t *testing.T) {
	client := os.Getenv("OPENKFO_WEAPON_TEST_CLIENT")
	if client == "" {
		t.Skip("set OPENKFO_WEAPON_TEST_CLIENT for installed resource validation")
	}
	data, err := os.ReadFile(filepath.Join(client, "Data", "config.spf2"))
	if err != nil {
		t.Fatal(err)
	}
	archive, err := parseArchive(data)
	if err != nil {
		t.Fatal(err)
	}
	text, err := archive.text("item.txt")
	if err != nil {
		t.Fatal(err)
	}
	rows := itemRowIndex(text)
	shipped := 0
	for id := range rows {
		if number, err := strconv.Atoi(id); err == nil {
			shipped = number
			break
		}
	}
	if shipped == 0 {
		t.Fatal("客户端 item.txt 里没有武器行")
	}
	absent := 299999
	if _, ok := rows[strconv.Itoa(absent)]; ok {
		t.Fatalf("测试前提被破坏：客户端里已经有 %d", absent)
	}
	got := undeployedWeaponIDs(data, []Weapon{{ID: shipped}, {ID: absent}})
	if len(got) != 1 || got[0] != absent {
		t.Fatalf("undeployed = %v，期望 [%d]", got, absent)
	}
	// 读不出客户端配置时一律不标记，免得整列表被误报成未部署。
	if marked := undeployedWeaponIDs([]byte("not an archive"), []Weapon{{ID: absent}}); marked != nil {
		t.Fatalf("损坏的配置包应当返回 nil，得到 %v", marked)
	}
}
