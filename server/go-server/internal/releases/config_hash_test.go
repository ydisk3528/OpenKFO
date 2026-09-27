package releases

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestActiveConfigHashPrecedence(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config.json")
	base := strings.Repeat("a", 64)
	if err := os.WriteFile(config, []byte(`{"config_hash":"`+base+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if hash, err := ActiveConfigHash(config, dir); err != nil || hash != base {
		t.Fatal(hash, err)
	}
	for _, kind := range []string{"weapons", "client"} {
		hash := Hash([]byte(kind))
		m := Manifest{Kind: kind, Version: "test", Notes: "test", SHA256: hash, Package: hash + ".zip", Size: 1, ConfigHash: hash}
		raw, _ := json.Marshal(m)
		if err := os.WriteFile(filepath.Join(dir, kind+".json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
		if got, err := ActiveConfigHash(config, dir); err != nil || got != hash {
			t.Fatal(got, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "client.json"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ActiveConfigHash(config, dir); err == nil {
		t.Fatal("invalid active release silently fell back")
	}
	if err := os.WriteFile(config, []byte(`{"config_hash":"`+base+`","release_version_url":"https://example.com/version/version.json"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := ActiveConfigHash(config, dir); err != nil || got != base {
		t.Fatal("legacy feed overrode OSS-managed configuration", got, err)
	}
}
