package desktop

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// acteffect.xml tells the client which effect resources to preload when a
// weapon is equipped: one <WeaponEffect ItemID> block per weapon, each row
// mapping an effect id to its resource file. Actions reference effects by id
// inside their AnmDesc blocks (<Effect>, <HitEffect>, …); a self-made weapon
// whose actions were borrowed from other weapons needs those ids registered
// under its own block or the effects silently never load.

var (
	weaponEffectBlockPattern = regexp.MustCompile(`(?s)<WeaponEffect\b[^>]*>.*?</WeaponEffect\s*>`)
	weaponEffectIdPattern    = regexp.MustCompile(`ItemID\s*=\s*"([^"]*)"`)
	effectFilePattern        = regexp.MustCompile(`<EffectFile\b[^>]*EffectId\s*=\s*"([^"]*)"[^>]*File\s*=\s*"([^"]*)"`)
	effectIdPattern          = regexp.MustCompile(`\beffectid\s*=\s*"([^"]+)"`)
)

// effectPreviews lists the effect ids an action's AnmDesc blocks reference,
// in first-appearance order. Used by the catalogue so the remap template
// picker can show what the client will load for a move before committing.
func effectPreviews(blocks []block) []string {
	seen := map[string]bool{}
	ids := []string{}
	for _, blk := range blocks {
		for _, m := range effectIdPattern.FindAllStringSubmatch(blk.original, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				ids = append(ids, m[1])
			}
		}
	}
	return ids
}

// actionFrames returns the action's total frame count: the last frame its
// <Anm> segments reach, plus one. Hold/loop segments (replaytimes) keep the
// action alive past this, but the timeline itself is this long.
func actionFrames(blocks []block) int {
	last := -1
	for _, blk := range blocks {
		blk.node.walk(func(node *xmlNode) {
			if node.tag != "Anm" {
				return
			}
			if end, err := strconv.Atoi(node.get("endframe")); err == nil && end > last {
				last = end
			}
		})
	}
	if last < 0 {
		return 0
	}
	return last + 1
}

// syncWeaponEffects rewrites the WeaponEffect block of every self-made weapon
// to exactly the set of effects its current itemact row references, minus the
// always-loaded common block (ItemID 0) — matching how the shipped tables are
// written. File names are copied from whichever registration already carries
// the id (122 shipped rows alias *_skill files instead of the raw id); ids
// nobody registers fall back to the id itself. It runs after applyRemaps so
// the block always describes the actions the weapon will actually play.
func syncWeaponEffects(a *archive, created map[string]Blueprint) (*archive, error) {
	if len(created) == 0 {
		return a, nil
	}
	text, err := a.text("acteffect.xml")
	if err != nil {
		return nil, fmt.Errorf("特效登记表缺失：%w", err)
	}
	// Every registered id -> file, plus the common block's ids. First writer
	// wins: shipped owners of one id never disagree on the file name.
	files := map[string]string{}
	common := map[string]bool{}
	for _, block := range weaponEffectBlockPattern.FindAllString(text, -1) {
		id := ""
		if m := weaponEffectIdPattern.FindStringSubmatch(block); m != nil {
			id = m[1]
		}
		for _, row := range effectFilePattern.FindAllStringSubmatch(block, -1) {
			if _, ok := files[row[1]]; !ok {
				files[row[1]] = row[2]
			}
			if id == "0" {
				common[row[1]] = true
			}
		}
	}

	actionText, err := a.text("itemact.txt")
	if err != nil {
		return nil, err
	}
	actions := map[string][]string{}
	for _, line := range strings.Split(strings.ReplaceAll(actionText, "\r\n", "\n"), "\n")[1:] {
		cols := strings.Split(strings.TrimSuffix(line, "\r"), "\t")
		if len(cols) < 3 {
			continue
		}
		if _, ok := created[cols[0]]; !ok {
			continue
		}
		for _, cell := range cols[2:] {
			if cell != "" && cell != "0" {
				actions[cols[0]] = append(actions[cols[0]], cell)
			}
		}
	}

	animations := map[string]string{}
	loadAnimation := func(name string) (string, error) {
		if text, ok := animations[name]; ok {
			return text, nil
		}
		text, err := a.text(name)
		if err != nil {
			return "", err
		}
		animations[name] = text
		return text, nil
	}

	keys := make([]string, 0, len(created))
	for key := range created {
		keys = append(keys, key)
	}
	sort.Strings(keys) // numeric ids share a width, lexical == numeric here
	changed := false
	for _, key := range keys {
		ids := map[string]bool{}
		for _, action := range actions[key] {
			if len(action) <= 4 {
				continue
			}
			file := "animation/" + action[:4] + ".xml"
			animation, err := loadAnimation(file)
			if err != nil {
				// itemact ships dead references (cells pointing at animation
				// files the archive never carried); they render nothing and
				// contribute no effects, so skip them quietly.
				continue
			}
			blockID := action[4:]
			if number, err := strconv.Atoi(blockID); err == nil {
				blockID = strconv.Itoa(number)
			}
			block, ok := currentBlock(animation, blockID)
			if !ok {
				continue // a stray cell the render never plays; skip quietly
			}
			for _, m := range effectIdPattern.FindAllStringSubmatch(block, -1) {
				if !common[m[1]] {
					ids[m[1]] = true
				}
			}
		}
		next, changedBlock, err := replaceWeaponEffectBlock(text, key, ids, files)
		if err != nil {
			return nil, err
		}
		text = next
		changed = changed || changedBlock
	}
	if !changed {
		return a, nil
	}
	encoded, err := encodeText(text)
	if err != nil {
		return nil, err
	}
	data, err := a.replace(map[string][]byte{"acteffect.xml": encoded})
	if err != nil {
		return nil, err
	}
	return parseArchive(data)
}

// replaceWeaponEffectBlock swaps (or inserts, or removes) the block of one
// weapon inside the acteffect table text. The replacement keeps the shipped
// layout: tab-indented tags, LF line endings, a short comment line above a
// freshly inserted block.
func replaceWeaponEffectBlock(text, weapon string, ids map[string]bool, files map[string]string) (string, bool, error) {
	var replacement string
	if len(ids) > 0 {
		sorted := make([]string, 0, len(ids))
		for id := range ids {
			sorted = append(sorted, id)
		}
		sort.Strings(sorted)
		var b strings.Builder
		// No leading indent: the block pattern matches from "<" onward, so the
		// tab already sitting before the shipped block stays in place and the
		// swap stays byte-stable across runs.
		b.WriteString("<WeaponEffect ItemID = \"" + weapon + "\">")
		for _, id := range sorted {
			file := files[id]
			if file == "" {
				file = id
			}
			b.WriteString("\n\t<EffectFile EffectId = \"" + id + "\" File = \"" + file + "\" />")
		}
		b.WriteString("\n\t</WeaponEffect>")
		replacement = b.String()
	}
	for _, block := range weaponEffectBlockPattern.FindAllString(text, -1) {
		m := weaponEffectIdPattern.FindStringSubmatch(block)
		if m == nil || m[1] != weapon {
			continue
		}
		if block == replacement {
			return text, false, nil
		}
		if replacement == "" {
			// No effects left: drop the block together with the blank line
			// and comment line directly above it, if any.
			start := strings.Index(text, block)
			head := text[:start]
			if idx := strings.LastIndex(head, "\n\n"); idx >= 0 {
				rest := strings.TrimLeft(head[idx+2:], "\t")
				if strings.HasPrefix(rest, "<!--") {
					head = head[:idx+1]
				}
			}
			return head + strings.TrimLeft(text[start+len(block):], "\n"), true, nil
		}
		return strings.Replace(text, block, replacement, 1), true, nil
	}
	if replacement == "" {
		return text, false, nil
	}
	// New weapon: append before the closing tag, following the blank line +
	// comment convention of the shipped blocks.
	insertion := "\n\n\t<!-- 自建武器 " + weapon + " 需要加载的特效 -->\n\t" + replacement + "\n"
	idx := strings.LastIndex(text, "</ActEffect>")
	if idx < 0 {
		return "", false, fmt.Errorf("特效登记表缺少 ActEffect 结束标签")
	}
	return text[:idx] + insertion + text[idx:], true, nil
}
