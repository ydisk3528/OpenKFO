package desktop

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigInspectionReadOnly(t *testing.T) {
	source := os.Getenv("OPENKFO_CLIENT_ARCHIVE")
	if source == "" {
		t.Skip("archive required")
	}
	before, e := os.ReadFile(source)
	if e != nil {
		t.Fatal(e)
	}
	admin := &Admin{Root: t.TempDir()}
	r := clientConfigRequest{Base: source}
	result, e := admin.inspectConfig(r, false)
	if e != nil {
		t.Fatal(e)
	}
	m := result.(map[string]any)
	r.Revision = m["revision"].(string)
	if len(m["files"].([]map[string]any)) == 0 {
		t.Fatal("empty catalog")
	}
	out, e := admin.inspectConfig(r, true)
	if e != nil {
		t.Fatal(e)
	}
	folder := out.(map[string]any)["folder"].(string)
	a, e := loadArchive(source)
	if e != nil {
		t.Fatal(e)
	}
	for name := range a.entries {
		raw, e := a.raw(name)
		if e != nil {
			t.Fatal(e)
		}
		exported, e := os.ReadFile(filepath.Join(folder, "原始文件", filepath.FromSlash(name)))
		if e != nil || !bytes.Equal(raw, exported) {
			t.Fatal(name, e)
		}
	}
	after, e := os.ReadFile(source)
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("source changed", e)
	}
	r.Revision = "stale"
	if _, e = admin.inspectConfig(r, true); e == nil {
		t.Fatal("stale source accepted")
	}
}
