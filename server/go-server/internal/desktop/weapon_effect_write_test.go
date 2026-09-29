package desktop

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestWriteActEffectToLocalClient performs the one-off data fix for the local
// test client: register 253300's effects in acteffect.xml without touching
// any other archive entry. Guarded by an environment variable so normal test
// runs never write to the client.
func TestWriteActEffectToLocalClient(t *testing.T) {
	if os.Getenv("OPENKFO_WRITE_ACTEFFECT") == "" {
		t.Skip("set OPENKFO_WRITE_ACTEFFECT=1 to write the registration into the local client")
	}
	client := os.Getenv("OPENKFO_WEAPON_TEST_CLIENT")
	raw, err := os.ReadFile(filepath.Join(client, "Data", "config.spf2"))
	if err != nil {
		t.Fatal(err)
	}
	a, err := parseArchive(raw)
	if err != nil {
		t.Fatal(err)
	}
	created := map[string]Blueprint{"253300": {ID: 253300, Name: "王八拳", Donor: 253013}}
	synced, err := syncWeaponEffects(a, created)
	if err != nil {
		t.Fatal(err)
	}
	// Safety net: only acteffect.xml may differ from the input.
	for name := range a.entries {
		before, _ := a.raw(name)
		after, _ := synced.raw(name)
		if name != "acteffect.xml" && !bytes.Equal(before, after) {
			t.Fatalf("越界改动：%s", name)
		}
	}
	before, _ := a.raw("acteffect.xml")
	after, _ := synced.raw("acteffect.xml")
	if bytes.Equal(before, after) {
		t.Fatal("acteffect.xml 未变化")
	}
	if err = os.WriteFile(filepath.Join(client, "Data", "config.spf2"), synced.data, 0644); err != nil {
		t.Fatal(err)
	}
	// Round-trip check on the file we just wrote.
	check, err := parseArchive(synced.data)
	if err != nil {
		t.Fatal(err)
	}
	if err = check.verify(); err != nil {
		t.Fatal(err)
	}
	t.Log("acteffect.xml 已写回，253300 特效登记完成")
}
