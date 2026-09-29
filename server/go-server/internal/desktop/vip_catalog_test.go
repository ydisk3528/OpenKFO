package desktop

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVIPClientCatalogue(t *testing.T) {
	path := os.Getenv("OPENKFO_CLIENT_ARCHIVE")
	if path == "" {
		t.Skip("read-only client archive required")
	}
	items, err := catalog(filepath.Dir(filepath.Dir(path)), false, false)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[uint32]bool{}
	for _, i := range items {
		if i.ID >= 730001 && i.ID <= 730003 {
			if i.Kind != 73 {
				t.Fatalf("unexpected VIP kind %d", i.Kind)
			}
			seen[i.ID] = true
		}
	}
	if len(seen) != 3 {
		t.Fatalf("missing VIP card records: %v", seen)
	}
}
