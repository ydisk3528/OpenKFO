package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConfigEntryRejectsAmbiguousAndExternalResources(t *testing.T) {
	good := file{Path: "Data/config.spf2", URL: base + "releases/test/client/Data/config.spf2", Size: 123, SHA256: strings.Repeat("a", 64)}
	for _, tc := range []struct {
		name  string
		rows  []file
		valid bool
	}{
		{"valid", []file{good}, true}, {"missing", nil, false}, {"duplicate", []file{good, good}, false},
		{"external", []file{{Path: good.Path, URL: "https://example.com/config.spf2", Size: 123, SHA256: good.SHA256}}, false},
		{"invalid hash", []file{{Path: good.Path, URL: good.URL, Size: 123, SHA256: "bad"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(manifest{Version: "test", Target: "client", Files: tc.rows})
			_, err := configEntry(raw, "test")
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
	raw, _ := json.Marshal(manifest{Version: "test", Target: "client", Files: []file{good}})
	if _, err := configEntry(raw, "other"); err == nil {
		t.Fatal("cross-version accepted")
	}
}
