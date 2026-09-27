package desktop

import (
	"crypto/sha256"
	"os"
	"testing"
)

func TestStageCatalogueClientArchive(t *testing.T) {
	path := os.Getenv("OPENKFO_CLIENT_ARCHIVE")
	if path == "" {
		t.Skip("OPENKFO_CLIENT_ARCHIVE is not configured")
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	maps, err := ReadStageMaps(path)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatal("client archive changed")
	}
	// This fixture belongs to the current native client, not a generated XML.
	for difficulty := uint32(1); difficulty <= 4; difficulty++ {
		found := false
		for _, m := range maps {
			if m.Logic == 1 && m.Group == 1 && m.Difficulty == difficulty && m.MapID == 8109+difficulty && m.MapType == 10 {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing native group 1 difficulty %d", difficulty)
		}
	}
	t.Logf("read %d native map selections; archive unchanged", len(maps))
	for _, row := range maps {
		if row.MapID == 8110 {
			archive, e := loadArchive(path)
			if e != nil {
				t.Fatal(e)
			}
			raw, e := archive.raw(row.Script)
			if e != nil {
				t.Fatal(e)
			}
			for _, changed := range []string{"script", "runtime", "config"} {
				candidate, runtime, catalog := raw, row.RuntimeHash, row.FosterTemplates
				switch changed {
				case "script":
					candidate = append(append([]byte(nil), raw...), '\n')
				case "runtime":
					runtime = "different-runtime"
				case "config":
					catalog = &FosterTemplateCatalogue{ConfigHash: "different-config"}
				}
				if preview, e := fosterPlanPreview(candidate, runtime, catalog); e != nil || preview != nil {
					t.Fatal("unverified combination received a Foster plan", changed, e)
				}
			}
			p := row.FosterPreview
			if p == nil || len(p.InitialHP) != 262 || p.InitialHP[251] != 8 || p.InitialHP[51] != 75 {
				t.Fatal("Foster preview lost HP catalogue")
			}
			if p == nil || p.GlobalLimit != 32 || p.PlayerLimit != 6 || len(p.Groups) != 2 || len(p.Groups[0].Spawns) != 2 || len(p.Groups[1].Spawns) != 21 {
				t.Fatal("missing native Foster parallel event plan", p)
			}
			counts := map[uint32]int{}
			for _, g := range p.Groups {
				if g.SubLimit != 2 || g.GroupLimit != 20 || g.TriggerBox != [6]float32{-2300, -5, -100, 1450, 10, 50} {
					t.Fatal("Foster trigger or limits changed")
				}
				for _, spawn := range g.Spawns {
					counts[spawn.Template]++
				}
			}
			first, last := p.Groups[0].Spawns[0], p.Groups[1].Spawns[20]
			if counts[251] != 16 || counts[0] != 6 || counts[51] != 1 || len(counts) != 3 || p.Groups[0].Block != 100 || p.Groups[1].Block != 0 || first.Position != [3]float32{-1610, -4, -15} || first.Direction != 2 || last.Position != [3]float32{-2260, -4, -15} || last.Direction != 0 {
				t.Fatal("Foster native order/template/default direction changed", counts, first, last)
			}
		} else if row.MapType == 10 && row.FosterPreview == nil {
			t.Fatal("native map missing Foster plan", row.MapID)
		}
		if row.MapType == 10 {
			catalog := row.FosterTemplates
			if catalog == nil || catalog.ConfigHash != fosterConfigHash || len(catalog.Names) != 262 || catalog.Names[0] != " 喽罗乙" || catalog.Names[254] != "喽罗乙" || catalog.Names[251] != "喽罗甲" {
				t.Fatal("Foster native indices or whitespace lost", row.MapID)
			}
			if len(catalog.InitialHP) != len(catalog.Names) || catalog.InitialHP[251] != 8 || catalog.InitialHP[51] != 75 {
				t.Fatal("Foster initial HP was scaled or detached from native template indices", row.MapID)
			}
			for _, hp := range catalog.InitialHP {
				if hp <= 0 {
					t.Fatal("missing initial HP in verified template catalogue")
				}
			}
			if row.RuntimeScript != "script/pve/include" || row.RuntimeHash != "0a083607cab1456c0976038f1e78658a208607355c015060a4492259cde5280f" {
				t.Fatalf("mode 10 must bind the loaded bytecode, not a loose Lua source: %+v", row)
			}
		} else if row.RuntimeScript != "" || row.RuntimeHash != "" || row.FosterTemplates != nil {
			t.Fatal("mode 10 runtime leaked to another mode", row.MapID)
		}
		if row.MapID == 9170 && (row.Script != "script/pve/act_zombiedefend_normal.lua" || len(row.ScriptHash) != 64) {
			t.Fatalf("native script binding lost: %+v", row)
		}
		if row.MapID == 9170 {
			p := row.WavePreview
			if p == nil || len(p.Templates) != 5 || len(p.Variants) != 3 {
				t.Fatal("missing verified native wave preview", p)
			}
			for i, variant := range p.Variants {
				if len(variant.Waves) != 25 {
					t.Fatal("wrong wave count")
				}
				for _, check := range []struct{ wave, total int }{{1, 2}, {3, 4}, {4, 4}, {11, 3}, {14, 4}, {21, 2}, {25, 4}} {
					total := uint32(0)
					for id, count := range variant.Waves[check.wave-1].Monsters {
						if id >= uint32(len(p.Templates)) {
							t.Fatal("unknown template")
						}
						total += count
					}
					if total != uint32(check.total*(i+2)) {
						t.Fatalf("variant %d wave %d: %d", i, check.wave, total)
					}
				}
			}
		}
	}
	requirements, _, err := ReadStageRequirements(path)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[uint32]StageRequirement{}
	for _, row := range requirements {
		byID[row.MapID] = row
	}
	for _, row := range maps {
		match, ok := byID[row.MapID]
		if !ok {
			t.Errorf("PVE map %d absent from MapInfo", row.MapID)
		}
		// Same attribute spelling, different native consumers: do not equate
		// these enums or silently normalize MapInfo 110 to a room mode.
		if match.MapType != 110 || (row.MapType != 10 && row.MapType != 21) {
			t.Errorf("current native type mapping changed: %+v / %+v", row, match)
		}
	}
}

func TestStageCatalogueSelection(t *testing.T) {
	record := `<Map LogicType="1" Group="1" SelectDifficulty="1" MapID="8110" MapType="10"/>`
	root, err := parseXML(`<PVEEntryUI>` + record + `</PVEEntryUI>`)
	if err != nil {
		t.Fatal(err)
	}
	maps, err := stageMaps(root)
	if err != nil || len(maps) != 1 || maps[0] != (StageMap{Logic: 1, Group: 1, Difficulty: 1, MapID: 8110, MapType: 10}) {
		t.Fatal(maps, err)
	}
	for _, text := range []string{`<PVEEntryUI/>`, `<Wrong/>`, `<PVEEntryUI>` + record + record + `</PVEEntryUI>`, `<PVEEntryUI><Map MapID="8110"/></PVEEntryUI>`} {
		root, err = parseXML(text)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = stageMaps(root); err == nil {
			t.Fatal("invalid catalogue accepted")
		}
	}
}

func TestStageDisplayRewards(t *testing.T) {
	root, err := parseXML(`<PVEEntryUI><Map LogicType="1" Group="1" SelectDifficulty="1" MapID="8110" MapType="10" DisplayDifficulty="2" RewardItem1="95002500" RewardItem2="60333601" RewardItem3=""/></PVEEntryUI>`)
	if err != nil {
		t.Fatal(err)
	}
	r, err := stageMaps(root)
	if err != nil || r[0].DisplayDifficulty != 2 || r[0].RewardItems != [3]uint32{95002500, 60333601, 0} {
		t.Fatal(r, err)
	}
	for _, value := range []string{"-1", "4294967296", "item"} {
		root.children[0].set("RewardItem1", value)
		if _, err := stageMaps(root); err == nil {
			t.Fatal("invalid reward ID accepted")
		}
	}
}
