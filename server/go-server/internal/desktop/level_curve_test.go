package desktop

import (
	"bytes"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestLevelCurvePreservesOtherEntries(t *testing.T) {
	path := os.Getenv("OPENKFO_CLIENT_ARCHIVE")
	if path == "" {
		t.Skip("archive required")
	}
	before, e := loadArchive(path)
	if e != nil {
		t.Fatal(e)
	}
	baseline := os.Getenv("OPENKFO_LEVEL_BASELINE")
	if baseline == "" {
		t.Skip("baseline required")
	}
	base, e := loadArchive(baseline)
	if e != nil {
		t.Fatal(e)
	}
	original, e := base.raw("levelup.txt")
	if e != nil {
		t.Fatal(e)
	}
	costs := make([]uint32, 200)
	for _, line := range strings.Split(string(original), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		n, _ := strconv.Atoi(f[0])
		v, _ := strconv.ParseUint(f[1], 10, 32)
		if n > 0 {
			costs[n-1] = uint32(v)
		}
	}
	for i := 150; i < 199; i++ {
		costs[i] = uint32((uint64(costs[i-1])*103 + 99) / 100)
	}
	out, e := BuildLevelCurve(path, baseline, costs)
	if e != nil {
		t.Fatal(e)
	}
	after, e := parseArchive(out)
	if e != nil {
		t.Fatal(e)
	}
	if e = after.verify(); e != nil {
		t.Fatal(e)
	}
	for name := range before.entries {
		a, e := before.raw(name)
		if e != nil {
			t.Fatal(e)
		}
		b, e := after.raw(name)
		if e != nil {
			t.Fatal(e)
		}
		if name != "levelup.txt" && name != "loadingtipscfg.xml" && !bytes.Equal(a, b) {
			t.Fatal("unrelated entry changed", name)
		}
	}
	loading, e := after.raw("loadingtipscfg.xml")
	if e != nil || bytes.Count(loading, []byte(`LvUpperBound = "200"`)) != 3 {
		t.Fatal("loading backgrounds and subtitles do not cover level 200", e)
	}
	raw, e := after.raw("levelup.txt")
	if e != nil || !bytes.HasPrefix(raw, original) {
		t.Fatal("original levels changed", e)
	}
	text, e := after.text("levelup.txt")
	if e != nil || len(strings.Fields(text)) != 201*7 {
		t.Fatal("missing level rows", e)
	}
	costs[199] = 1
	if _, e = BuildLevelCurve(path, baseline, costs); e == nil {
		t.Fatal("nonzero max cost accepted")
	}
}
