package desktop

import (
	"os"
	"testing"
)

// 端到端（只读）：真实客户端里 253521 的 CC 第一段动作块分了两份（一份无条件、
// 一份带 <Condition><Ustate id="406"/>），stageTracks 必须把条件带出来。
func TestStageTracksCarryConditionReal(t *testing.T) {
	client := os.Getenv("OPENKFO_WEAPON_TEST_CLIENT")
	if client == "" {
		t.Skip("set OPENKFO_WEAPON_TEST_CLIENT for installed resource validation")
	}
	items, err := catalog(client, false, false)
	if err != nil {
		t.Fatalf("读取武器目录失败：%v", err)
	}
	a, err := loadArchive(configPath(client))
	if err != nil {
		t.Fatalf("读取客户端失败：%v", err)
	}
	info, err := inspect(a, items)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	tracks := stageTracks(info, client, "253521")
	if len(tracks) == 0 {
		t.Fatal("没有拿到 253521 的帧轨道")
	}
	var plain, conditioned int
	for _, track := range tracks {
		for _, raw := range track["segments"].([]AnmSegment) {
			if raw.Condition == "" {
				plain++
			} else {
				conditioned++
			}
		}
	}
	t.Logf("无条件片断 %d，条件片断 %d", plain, conditioned)
	if conditioned == 0 {
		t.Fatal("253521 应当有带条件的片断（406 分支），一个都没读到")
	}
}
