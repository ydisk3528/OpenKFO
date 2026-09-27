package desktop

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
)

const fosterConfigHash = "142515f07e84aaab3ba8ea4d4c2b2ba76a14b70d35abc40fc09996e04061d914"

const iceChapterConfigHash = "a878fdb58625df9c711bb03ddc6e7b1e7cd0108a6713e63cf03673041876a6ca"
const iceChapterScriptHash = "8c20680435c5d6eaab04c99cc1bc023c9b16f8db71ccc3490cee9aba246e1742"

type FosterTemplateCatalogue struct {
	ConfigHash string    `json:"config_hash"`
	Names      []string  `json:"names"`      // Index is the native 20400 template DWORD.
	InitialHP  []float32 `json:"initial_hp"` // Same index; native float, without display scaling.
}

// A62D60 builds the global name vector from each definition's first field;
// A63407 sorts it before A62AE0 looks up the zero-based index. A changed Lua
// configuration is not parsed heuristically as an equivalent ruleset.
func fosterTemplates(raw []byte) (*FosterTemplateCatalogue, error) {
	if digest(raw) != fosterConfigHash && digest(raw) != iceChapterConfigHash {
		return nil, nil
	}
	// A62D60 reads field 12 into definition+74. 943465..94347C copies
	// that float directly to maximum/current HP (+2C/+34), then 94350A
	// commits the attributes. Keep each value paired with its raw GBK name.
	matches := regexp.MustCompile(`(?m)^\["[^"\r\n]+"\]\s*=\s*\{"([^"\r\n]+)"\s*,(?:\s*(?:"[^"\r\n]*"|[^,\r\n{}]+)\s*,){10}\s*([0-9]+(?:\.[0-9]+)?)\s*,`).FindAllStringSubmatch(string(raw), -1)
	expected := 262
	if digest(raw) == iceChapterConfigHash {
		expected++
	}
	if len(matches) != expected {
		return nil, fmt.Errorf("unexpected verified Foster template count")
	}
	// Preserve leading spaces and sort GBK bytes, not decoded Unicode names.
	sort.Slice(matches, func(i, j int) bool { return matches[i][1] < matches[j][1] })
	result := &FosterTemplateCatalogue{ConfigHash: digest(raw)}
	for i, match := range matches {
		name := match[1]
		if i > 0 && name == matches[i-1][1] {
			return nil, fmt.Errorf("duplicate Foster template name")
		}
		decoded, err := decodeText([]byte(name))
		if err != nil {
			return nil, err
		}
		result.Names = append(result.Names, decoded)
		hp, err := strconv.ParseFloat(match[2], 32)
		if err != nil || hp <= 0 {
			return nil, fmt.Errorf("invalid verified Foster template HP at index %d", i)
		}
		result.InitialHP = append(result.InitialHP, float32(hp))
	}
	return result, nil
}
