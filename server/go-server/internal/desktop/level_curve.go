package desktop

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// BuildLevelCurve restores the original 0-150 rows verbatim, appending only
// new levels. Every unrelated entry comes from the current archive.
func BuildLevelCurve(path, baseline string, costs []uint32) ([]byte, error) {
	if len(costs) != 200 || costs[199] != 0 {
		return nil, fmt.Errorf("expected 200 levels with zero final cost")
	}
	a, err := loadArchive(path)
	if err != nil {
		return nil, err
	}
	base, err := loadArchive(baseline)
	if err != nil {
		return nil, err
	}
	raw, err := base.raw("levelup.txt")
	if err != nil {
		return nil, err
	}
	seen := map[int]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 7 {
			return nil, fmt.Errorf("invalid base level row")
		}
		n, e := strconv.Atoi(fields[0])
		if e != nil || n < 0 || n > 150 || seen[n] {
			return nil, fmt.Errorf("invalid base level index")
		}
		seen[n] = true
		cost, e := strconv.ParseUint(fields[1], 10, 31)
		if e != nil {
			return nil, e
		}
		if n > 0 && uint32(cost) != costs[n-1] {
			return nil, fmt.Errorf("refusing change to base level %d", n)
		}
	}
	if len(seen) != 151 {
		return nil, fmt.Errorf("incomplete base curve")
	}
	out := append([]byte(nil), raw...)
	if len(out) == 0 || out[len(out)-1] != '\n' {
		out = append(out, '\r', '\n')
	}
	for level := 151; level <= 200; level++ {
		cost := costs[level-1]
		if level < 200 && (cost <= costs[level-2] || cost > 2147483647) {
			return nil, fmt.Errorf("invalid extended experience")
		}
		out = append(out, []byte(fmt.Sprintf("%d\t%d\t0\t0\t0\t0\t0\r\n", level, cost))...)
	}
	loading, err := a.raw("loadingtipscfg.xml")
	if err != nil {
		return nil, err
	}
	// Backgrounds, overlays and subtitles must also cover the extended levels.
	upper := regexp.MustCompile(`LvUpperBound\s*=\s*"(?:150|200)"`)
	if len(upper.FindAll(loading, -1)) != 3 {
		return nil, fmt.Errorf("unexpected loading level ranges")
	}
	loading = upper.ReplaceAll(loading, []byte(`LvUpperBound = "200"`))
	return a.replace(map[string][]byte{"levelup.txt": out, "loadingtipscfg.xml": loading})
}
