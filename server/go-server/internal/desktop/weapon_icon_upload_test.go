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

func TestSafeIconName(t *testing.T) {
	good := []string{"253013.png", "DizzyHammer.PNG", "custom_ab12cd34ef.png", "steelsword.png", "a-b_c.png"}
	bad := []string{
		"王八拳拳谱图标 (1).png", // CJK + space + parens, the real-world failure
		"with space.png",
		"paren(1).png",
		".png",
		"icon.jpg",
		"icon",
	}
	for _, name := range good {
		if !safeIconName(name) {
			t.Fatalf("expected safe: %q", name)
		}
	}
	for _, name := range bad {
		if safeIconName(name) {
			t.Fatalf("expected unsafe: %q", name)
		}
	}
}

func encodePNG(t *testing.T, w, h int, fill color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, fill)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestUploadWeaponIconNormalizesAndRenames(t *testing.T) {
	client := t.TempDir()
	source := filepath.Join(t.TempDir(), "王八拳拳谱图标 (1).png")
	if err := os.WriteFile(source, encodePNG(t, 1536, 1536, color.RGBA{R: 200, G: 40, B: 40, A: 255}), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := uploadWeaponIcon(client, source)
	if err != nil {
		t.Fatal(err)
	}
	icon := result["icon"].(string)
	if strings.ContainsAny(icon, " ()") || strings.ContainsFunc(icon, func(r rune) bool { return r > 127 }) {
		t.Fatalf("unsafe icon path returned: %q", icon)
	}
	if !strings.HasPrefix(icon, "Picture\\ItemIcon\\custom_") {
		t.Fatalf("expected hashed custom name, got %q", icon)
	}
	dest := filepath.Join(client, "Data", "UI", "Picture", "ItemIcon", filepath.Base(icon))
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("stored icon is not a decodable PNG: %v", err)
	}
	if got := img.Bounds().Dx(); got != iconSize || img.Bounds().Dy() != iconSize {
		t.Fatalf("icon not normalized to %d: %dx%d", iconSize, got, img.Bounds().Dy())
	}
	// The solid source must survive the box filter (alpha-weighted average).
	r, g, b, a := img.At(40, 40).RGBA()
	if r>>8 != 200 || g>>8 != 40 || b>>8 != 40 || a>>8 != 255 {
		t.Fatalf("colour lost in resize: %v %v %v %v", r>>8, g>>8, b>>8, a>>8)
	}
	// Idempotent: uploading the same picture again yields the same name.
	again, err := uploadWeaponIcon(client, source)
	if err != nil || again["icon"] != icon {
		t.Fatalf("re-upload not idempotent: %q vs %q (%v)", again["icon"], icon, err)
	}
}

func TestUploadWeaponIconKeepsSafeNames(t *testing.T) {
	client := t.TempDir()
	source := filepath.Join(t.TempDir(), "my_icon.png")
	if err := os.WriteFile(source, encodePNG(t, 80, 80, color.RGBA{A: 255}), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := uploadWeaponIcon(client, source)
	if err != nil {
		t.Fatal(err)
	}
	if got := result["icon"].(string); got != `Picture\ItemIcon\my_icon.png` {
		t.Fatalf("safe name should be preserved, got %q", got)
	}
	bad := filepath.Join(t.TempDir(), "x.jpg")
	if err := os.WriteFile(bad, []byte("not a png"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := uploadWeaponIcon(client, bad); err == nil {
		t.Fatal("non-png should be rejected")
	}
}
