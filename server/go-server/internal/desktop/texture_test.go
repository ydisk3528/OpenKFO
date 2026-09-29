package desktop

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstalledShopImages(t *testing.T) {
	client := os.Getenv("OPENKFO_TEST_CLIENT")
	if client == "" {
		t.Skip("set OPENKFO_TEST_CLIENT for installed texture verification")
	}
	for _, fixture := range []struct {
		path          string
		width, height int
	}{
		{"Picture/ItemIcon/253030.png", 80, 80},
		{"Picture/ItemIcon/253912.png", 80, 80},
		{"Picture/ItemIcon/303172.png", 80, 80},
		{"Picture/Shop_Panel1_New.png", 800, 560},
	} {
		b, err := os.ReadFile(filepath.Join(client, "Data/UI", fixture.path))
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := decodeTexture(b)
		if err != nil {
			t.Fatal(err)
		}
		c, err := png.DecodeConfig(bytes.NewReader(decoded))
		if err != nil || c.Width != fixture.width || c.Height != fixture.height {
			t.Fatalf("%s: %v %v", fixture.path, c, err)
		}
		if dir := os.Getenv("OPENKFO_TEST_IMAGE_OUTPUT"); dir != "" {
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, filepath.Base(fixture.path)), decoded, 0644); err != nil {
				t.Fatal(err)
			}
		}
		b[len(b)-1] ^= 1
		if _, err := decodeTexture(b); err == nil {
			t.Fatal("corrupt CRC accepted")
		}
	}
	items := []Item{{Key: "25:253030", Fields: []string{"", "", "", "", "", "", "", "", "", "Picture/ItemIcon/253030.png"}},
		{Key: "escape", Fields: []string{"", "", "", "", "", "", "", "", "", "../../outside.png"}}}
	result, err := shopImages(client, items, []string{"25:253030", "escape"})
	if err != nil || len(result) != 1 || len(result["25:253030"]) == 0 {
		t.Fatalf("images=%d error=%v", len(result), err)
	}
	if _, err := shopImages(client, items, make([]string, 25)); err == nil {
		t.Fatal("unbounded batch accepted")
	}
	all, err := catalog(client, false, false)
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	for _, item := range all {
		if item.Supported {
			keys = append(keys, item.Key)
		}
	}
	count := 0
	for start := 0; start < len(keys); start += 24 {
		end := start + 24
		if end > len(keys) {
			end = len(keys)
		}
		batch, err := shopImages(client, all, keys[start:end])
		if err != nil {
			t.Fatal(err)
		}
		count += len(batch)
	}
	t.Logf("Installed shop icons decoded: %d/%d", count, len(keys))
	reasons := map[string]int{}
	for _, item := range all {
		if !item.Supported {
			continue
		}
		b, err := os.ReadFile(filepath.Join(client, "Data/UI", filepath.FromSlash(strings.ReplaceAll(item.Fields[9], "\\", "/"))))
		if err != nil {
			reasons["missing"]++
			continue
		}
		if _, err = decodeTexture(b); err != nil {
			reasons[err.Error()]++
		}
	}
	t.Logf("Unavailable icons: %v", reasons)
}

func TestCatalogReturnsDecodedInventoryIcons(t *testing.T) {
	client := os.Getenv("OPENKFO_TEST_CLIENT")
	if client == "" {
		t.Skip("installed client required")
	}
	all, err := Catalog(client)
	if err != nil {
		t.Fatal(err)
	}
	wanted := map[uint32]bool{253943: false, 253944: false, 253945: false, 253946: false, 253947: false, 253948: false, 253949: false, 253950: false}
	available := 0
	for _, item := range all {
		if item.Icon == "" {
			source := filepath.Join(client, "Data/UI", filepath.FromSlash(strings.ReplaceAll(item.Fields[9], "\\", "/")))
			if data, readErr := os.ReadFile(source); readErr == nil {
				_, decodeErr := decodeTexture(data)
				t.Errorf("existing icon unavailable %s %s: %v", item.Key, item.Fields[9], decodeErr)
			} else if !os.IsNotExist(readErr) {
				t.Errorf("icon read failed %s: %v", item.Key, readErr)
			}
			if _, needed := wanted[item.ID]; needed {
				t.Errorf("screenshot item %d missing icon", item.ID)
			}
			continue
		}
		data, err := os.ReadFile(item.Icon)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = png.Decode(bytes.NewReader(data)); err != nil {
			t.Fatalf("catalog %s is not decoded PNG: %v", item.Key, err)
		}
		available++
		if _, needed := wanted[item.ID]; needed {
			wanted[item.ID] = true
		}
	}
	for id, found := range wanted {
		if !found {
			t.Errorf("screenshot item %d not verified", id)
		}
	}
	t.Logf("Catalog Image.file paths verified: %d / %d", available, len(all))
}

func TestTextureCacheInvalidatesChangedSource(t *testing.T) {
	// Use a valid in-memory PNG to exercise publication without installed assets.
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	t.Setenv("XDG_CACHE_HOME", dir)
	path := filepath.Join(dir, "source.png")
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	first, err := cachedTexture(path)
	if err != nil {
		t.Fatal(err)
	}
	again, err := cachedTexture(path)
	if err != nil || first != again {
		t.Fatal("cache reuse failed", err)
	}
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	b.Reset()
	png.Encode(&b, img)
	os.WriteFile(path, b.Bytes(), 0600)
	next, err := cachedTexture(path)
	if err != nil || next == first {
		t.Fatal("changed source reused old image", err)
	}
	os.WriteFile(path, []byte("not an image"), 0600)
	if _, err = cachedTexture(path); err == nil {
		t.Fatal("corrupt source accepted")
	}
}
