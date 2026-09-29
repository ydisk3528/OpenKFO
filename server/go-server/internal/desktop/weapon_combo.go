package desktop

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// The client keeps a per-weapon combo state machine in delayacttable.xml:
//
//	<Item WeaponTypeId ="253089" OldState="2011" NewState="2012" KeyInput="1" StartPart="1"/>
//
// Pressing the key named by KeyInput (see <KeyInputList> in the same file:
// 1 = normal attack, 2 = special, 3 = aim, 4 = jump, 5 = forward, 6 = back)
// while the weapon sits in OldState moves it to NewState. itemact.txt only
// declares which animation plays in each state; without a transition row the
// state can never advance, so the weapon cannot chain attacks at all.
//
// action effects are registered separately in acteffect.xml:
//
//	<WeaponEffect ItemID = "253089"> <EffectFile .../> ... </WeaponEffect>
//
// Some shipped weapons were authored by copying another weapon's itemact row
// but never got either registration, which is exactly why an untouched client
// can show a complete 招式 list in the editor yet refuse to combo in game.
var (
	comboRowPattern = regexp.MustCompile(
		`<Item\s+WeaponTypeId\s*=\s*"(\d+)"\s+OldState\s*=\s*"(\d+)"\s+NewState\s*=\s*"(\d+)"\s+KeyInput\s*=\s*"(\d+)"\s+StartPart\s*=\s*"(\d+)"\s*/>`)
	comboListEndPattern = regexp.MustCompile(`</ItemList\s*>`)
	effectBlockPattern  = regexp.MustCompile(
		`(?s)<WeaponEffect\s+ItemID\s*=\s*"(\d+)"\s*>(.*?)</WeaponEffect\s*>`)
	effectRootEndPattern = regexp.MustCompile(`</ActEffect\s*>`)
	keyInputBlockPattern = regexp.MustCompile(
		`(?s)<KeyInput\s+Id\s*=\s*"(\d+)"\s*>(.*?)</KeyInput\s*>`)
	keyInputCommentPattern = regexp.MustCompile(`(?s)<!--(.*?)-->`)
	keyInputValuePattern   = regexp.MustCompile(`<Key\s+value\s*=\s*"(\d+)"\s*/>`)
	keyInputListEndPattern = regexp.MustCompile(`</KeyInputList\s*>`)
)

// KeyInput is one entry of the client's own <KeyInputList>: the id stored in a
// transition row's KeyInput attribute, the label the client's file gives it
// (e.g. "普通攻击" for 1, "C+X" for 13) and the physical key sequence it means.
// The ids are not contiguous — the client uses 1..6 for the basic attacks, 8..13
// for the two-key combinations, 19..24 for the direction combinations and
// 31..33 for the standing/running/jumping skills — and the numbering in the
// banner comment at the top of the file is a *different* code (BATTLEKEY_*),
// so the only trustworthy source is this list itself.
type KeyInput struct {
	ID    string   `json:"id"`
	Label string   `json:"label"`
	Keys  []string `json:"keys"`
}

// keyInputs parses <KeyInputList> so the editor can offer exactly the keys the
// client understands, labelled the way the client's own table labels them.
func keyInputs(a *archive) []KeyInput {
	text, err := a.text("delayacttable.xml")
	if err != nil {
		return nil
	}
	if at := keyInputListEndPattern.FindStringIndex(text); at != nil {
		text = text[:at[0]]
	}
	entries := []KeyInput{}
	for _, match := range keyInputBlockPattern.FindAllStringSubmatch(text, -1) {
		body := match[2]
		label := ""
		if comment := keyInputCommentPattern.FindStringSubmatch(body); comment != nil {
			label = strings.Join(strings.Fields(comment[1]), " ")
		}
		keys := []string{}
		for _, value := range keyInputValuePattern.FindAllStringSubmatch(body, -1) {
			keys = append(keys, value[1])
		}
		if label == "" {
			label = "按键" + match[1]
		}
		entries = append(entries, KeyInput{ID: match[1], Label: label, Keys: keys})
	}
	return entries
}

// keyInputLabels indexes keyInputs by id for the flat transition views.
func keyInputLabels(a *archive) map[string]string {
	labels := map[string]string{}
	for _, entry := range keyInputs(a) {
		labels[entry.ID] = entry.Label
	}
	return labels
}

// comboRow is one parsed transition; keeping the fields instead of the raw text
// lets a cloned row be re-emitted with a consistent attribute shape.
type comboRow struct {
	OldState  string
	NewState  string
	KeyInput  string
	StartPart string
}

func (r comboRow) render(weapon string) string {
	return fmt.Sprintf(`<Item WeaponTypeId ="%s" OldState="%s" NewState="%s" KeyInput="%s" StartPart="%s"/>`,
		weapon, r.OldState, r.NewState, r.KeyInput, r.StartPart)
}

// comboRowsOf collects the transitions registered for one weapon id.
func comboRowsOf(text, weapon string) []comboRow {
	rows := []comboRow{}
	for _, match := range comboRowPattern.FindAllStringSubmatch(text, -1) {
		if match[1] != weapon {
			continue
		}
		rows = append(rows, comboRow{OldState: match[2], NewState: match[3], KeyInput: match[4], StartPart: match[5]})
	}
	return rows
}

// comboRowCounts reports how many transitions each weapon has, so the editor
// can flag the ones the client will refuse to chain.
func comboRowCounts(text string) map[string]int {
	counts := map[string]int{}
	for _, match := range comboRowPattern.FindAllStringSubmatch(text, -1) {
		counts[match[1]]++
	}
	return counts
}

// appendComboRows copies a donor's transitions onto another weapon. It is a
// no-op when the target already has rows, so repeated applies stay idempotent.
func appendComboRows(text, donor, target string) (string, int, error) {
	rows := comboRowsOf(text, donor)
	if len(rows) == 0 {
		return text, 0, fmt.Errorf("参考武器 %s 没有连招表，无法借用", donor)
	}
	if len(comboRowsOf(text, target)) > 0 {
		return text, 0, nil
	}
	closing := comboListEndPattern.FindAllStringIndex(text, -1)
	if len(closing) != 1 {
		return text, 0, fmt.Errorf("连招表结构错误")
	}
	rebuilt := make([]string, 0, len(rows))
	for _, row := range rows {
		rebuilt = append(rebuilt, row.render(target))
	}
	block := "\n\t<!--自建/补齐：借用 " + donor + " 的连招-->\n\t" + strings.Join(rebuilt, "\n\t") + "\n"
	at := closing[0][0]
	return text[:at] + block + text[at:], len(rebuilt), nil
}

// effectBlockOf returns the raw <WeaponEffect> body registered for a weapon.
func effectBlockOf(text, weapon string) (string, bool) {
	for _, match := range effectBlockPattern.FindAllStringSubmatch(text, -1) {
		if match[1] == weapon {
			return match[2], true
		}
	}
	return "", false
}

// appendEffectBlock copies a donor's action-effect registration onto another
// weapon. Missing entries only cost visuals, so failures are reported to the
// caller instead of aborting the whole write.
func appendEffectBlock(text, donor, target string) (string, bool) {
	body, ok := effectBlockOf(text, donor)
	if !ok {
		return text, false
	}
	if _, exists := effectBlockOf(text, target); exists {
		return text, false
	}
	closing := effectRootEndPattern.FindAllStringIndex(text, -1)
	if len(closing) != 1 {
		return text, false
	}
	block := "\n<!--自建/补齐：借用 " + donor + " 的动作特效-->\n\t<WeaponEffect ItemID = \"" + target + "\">" +
		body + "</WeaponEffect>\n"
	at := closing[0][0]
	return text[:at] + block + text[at:], true
}

// comboPlan maps a weapon that needs registrations to the donor it borrows from.
type comboPlan map[string]string

// comboPlanOf merges the donors implied by self-made blueprints with the
// explicit completion requests stored in the editor state.
func comboPlanOf(created map[string]Blueprint, combos map[string]int) comboPlan {
	plan := comboPlan{}
	for key, blueprint := range created {
		if blueprint.Donor > 0 {
			plan[key] = strconv.Itoa(blueprint.Donor)
		}
	}
	for target, donor := range combos {
		if donor > 0 {
			plan[target] = strconv.Itoa(donor)
		}
	}
	return plan
}

// comboResult summarises what one completion pass actually wrote.
type comboResult struct {
	Weapons int
	Rows    int
	Effects int
}

// applyComboTables returns a configuration in which every weapon in the plan
// owns a combo state machine (and, when the donor has one, an action-effect
// block). Both target files already exist in the archive, so this stays inside
// the writer's replace-only contract.
func applyComboTables(a *archive, plan comboPlan) (*archive, comboResult, error) {
	summary := comboResult{}
	if len(plan) == 0 {
		return a, summary, nil
	}
	targets := make([]string, 0, len(plan))
	for target := range plan {
		targets = append(targets, target)
	}
	sort.Strings(targets)

	replacements := map[string][]byte{}
	for _, file := range []string{"delayacttable.xml", "acteffect.xml"} {
		text, err := a.text(file)
		if err != nil {
			continue
		}
		changed := false
		for _, target := range targets {
			donor := plan[target]
			if file == "delayacttable.xml" {
				next, rows, err := appendComboRows(text, donor, target)
				if err != nil {
					return nil, summary, err
				}
				if rows > 0 {
					text, changed = next, true
					summary.Rows += rows
					summary.Weapons++
				}
				continue
			}
			if next, ok := appendEffectBlock(text, donor, target); ok {
				text, changed = next, true
				summary.Effects++
			}
		}
		if changed {
			encoded, err := encodeText(text)
			if err != nil {
				return nil, summary, err
			}
			replacements[file] = encoded
		}
	}
	if len(replacements) == 0 {
		return a, summary, nil
	}
	data, err := a.replace(replacements)
	if err != nil {
		return nil, summary, err
	}
	result, err := parseArchive(data)
	if err != nil {
		return nil, summary, err
	}
	return result, summary, nil
}

// annotateComboState fills in the combo/effect columns of the catalogue so the
// editor can warn about weapons the client will refuse to chain. Both files are
// optional: a client that ships without them simply has nothing to report.
func annotateComboState(info *inspection, a *archive) error {
	counts := map[string]int{}
	if text, err := a.text("delayacttable.xml"); err == nil {
		counts = comboRowCounts(text)
	}
	effects := map[string]bool{}
	if text, err := a.text("acteffect.xml"); err == nil {
		for _, match := range effectBlockPattern.FindAllStringSubmatch(text, -1) {
			effects[match[1]] = true
		}
	}
	actionLines := [][]string{}
	if text, err := a.text("itemact.txt"); err == nil {
		for _, line := range splitRows(text) {
			actionLines = append(actionLines, line)
		}
	}
	suggestions := comboSuggestions(actionLines, counts)
	for i := range info.weapons {
		weapon := &info.weapons[i]
		key := strconv.Itoa(weapon.ID)
		weapon.ComboRows = counts[key]
		weapon.Effects = effects[key]
		if weapon.ComboRows == 0 {
			weapon.ComboSuggestion = suggestions[weapon.ID]
		}
	}
	return nil
}

// comboSuggestions picks, for every weapon with no transitions, the shipped
// weapon whose itemact row matches it best. A weapon distilled from a donor
// (like 混沌宇宙 from 龙拳) matches on every shared action column, so the
// suggestion is normally exactly the original template.
func comboSuggestions(actionLines [][]string, counts map[string]int) map[int]int {
	suggestions := map[int]int{}
	rows := map[string][]string{}
	for _, row := range actionLines {
		if len(row) >= 2 {
			rows[row[0]] = row
		}
	}
	candidates := []string{}
	for id, count := range counts {
		if count > 0 {
			candidates = append(candidates, id)
		}
	}
	sort.Strings(candidates)
	for id, row := range rows {
		if counts[id] > 0 {
			continue
		}
		target, err := strconv.Atoi(id)
		if err != nil {
			continue
		}
		best, bestScore := 0, 0
		for _, candidate := range candidates {
			other := rows[candidate]
			score := 0
			for i := 2; i < len(row) && i < len(other); i++ {
				if row[i] != "" && row[i] == other[i] {
					score++
				}
			}
			if score <= bestScore {
				continue
			}
			if number, err := strconv.Atoi(candidate); err == nil {
				best, bestScore = number, score
			}
		}
		if best > 0 {
			suggestions[target] = best
		}
	}
	return suggestions
}

// keyInputNames is the fallback label table for clients whose
// delayacttable.xml has no readable <KeyInputList>; the parsed table always
// wins. The values below mirror the comments in the shipped file (1..6 are the
// basic inputs, 8..13 the two-key combinations, 19..24 the direction
// combinations, 31..33 the standing/running/jumping skills).
var keyInputNames = map[string]string{
	"1":  "普通攻击",
	"2":  "特殊攻击",
	"3":  "瞄准",
	"4":  "跳跃",
	"5":  "前",
	"6":  "后",
	"8":  "Z+Z",
	"9":  "C+C",
	"10": "X+X",
	"11": "X+C",
	"12": "Z+X+C",
	"13": "C+X",
	"19": "前前普通",
	"20": "前前特殊",
	"21": "前特殊",
	"22": "前普通",
	"23": "后特殊",
	"24": "后普通",
	"31": "站技Z+X+C",
	"32": "跑技前前C+X",
	"33": "跳技跳C+X",
}

func keyInputLabel(key string) string {
	if name, ok := keyInputNames[key]; ok {
		return name
	}
	return "按键" + key
}

// comboChain returns the delayacttable transitions for one weapon, annotated
// with the human labels of both states, so the editor can draw the chain
// instead of a flat stage list.
func comboChain(a *archive, info *inspection, weaponID string) []map[string]string {
	text, err := a.text("delayacttable.xml")
	if err != nil {
		return nil
	}
	rows := comboRowsOf(text, weaponID)
	if len(rows) == 0 {
		return nil
	}
	labels := map[string]string{}
	for _, weapon := range info.weapons {
		if strconv.Itoa(weapon.ID) != weaponID {
			continue
		}
		for _, stage := range weapon.Stages {
			labels[stage.State] = stage.Label
		}
		break
	}
	label := func(state string) string {
		if text, ok := labels[state]; ok && text != "" {
			return state + " · " + text
		}
		return state
	}
	// Prefer the client's own labels: the ids are not contiguous and several of
	// them (8 = Z+Z, 11 = X+C, 13 = C+X) are easy to get backwards.
	keyLabels := keyInputLabels(a)
	keyLabel := func(key string) string {
		if text, ok := keyLabels[key]; ok && text != "" {
			return text
		}
		return keyInputLabel(key)
	}
	result := make([]map[string]string, 0, len(rows))
	for _, row := range rows {
		result = append(result, map[string]string{
			"old":       row.OldState,
			"new":       row.NewState,
			"key":       row.KeyInput,
			"key_label": keyLabel(row.KeyInput),
			"old_label": label(row.OldState),
			"new_label": label(row.NewState),
		})
	}
	return result
}

// frameSwitch is one frame-level key switch inside an action block: while the
// action plays, pressing keycode inside the switch window advances to
// nextstate. This is the *second* combo channel (CustomStateSwitch), separate
// from delayacttable.xml. Attrs carries the raw attribute list so the editor can
// round-trip attributes it does not model when adding or deleting switches.
type frameSwitch struct {
	State    string            `json:"state"`     // source itemact column
	Next     string            `json:"next"`      // nextstate
	KeyCode  string            `json:"keycode"`   // raw底层按键码 (e.g. "7", "7,8", "-7")
	KeyLabel string            `json:"key_label"` // human label (e.g. "X", "X C", "松开X")
	Window   string            `json:"window"`    // switch frame window, e.g. "20-25"
	Attrs    []FrameSwitchAttr `json:"attrs"`
}

// comboFrameSwitches lists the CustomStateSwitch nodes every action block of
// one weapon declares, keyed by the itemact state that plays that block.
func comboFrameSwitches(a *archive, info *inspection, weaponID string) []frameSwitch {
	result := []frameSwitch{}
	for _, weapon := range info.weapons {
		if strconv.Itoa(weapon.ID) != weaponID {
			continue
		}
		for _, stage := range weapon.Stages {
			if stage.Action == "" || stage.Action == "0" {
				continue
			}
			blocks := info.blocks[actionKey(stage.Action)]
			if len(blocks) == 0 {
				continue
			}
			for _, blk := range blocks {
				blk.node.walk(func(node *xmlNode) {
					if node.tag != "CustomStateSwitch" {
						return
					}
					keycode := node.get("keycode")
					attrs := make([]FrameSwitchAttr, 0, len(node.attrs))
					for _, attr := range node.attrs {
						attrs = append(attrs, FrameSwitchAttr{Key: attr.Name.Local, Value: attr.Value})
					}
					result = append(result, frameSwitch{
						State:    stage.State,
						Next:     node.get("nextstate"),
						KeyCode:  keycode,
						KeyLabel: frameKeyLabel(keycode),
						Window:   frameSwitchWindow(node),
						Attrs:    attrs,
					})
				})
			}
		}
		break
	}
	return result
}

// frameKeyLabel renders a CustomStateSwitch keycode as a readable sequence.
// The底层码 is not the same numbering as delayacttable's KeyInput: here
// 7=X 8=C 9=Z 5=跳 20=前 21=后, a comma joins a rapid sequence, and a leading
// minus means release.
func frameKeyLabel(keycode string) string {
	if strings.TrimSpace(keycode) == "" {
		return "—"
	}
	parts := strings.Split(keycode, ",")
	labels := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		release := strings.HasPrefix(part, "-")
		code := strings.TrimPrefix(part, "-")
		name := code
		switch code {
		case "7":
			name = "X"
		case "8":
			name = "C"
		case "9":
			name = "Z"
		case "5":
			name = "跳"
		case "20":
			name = "前"
		case "21":
			name = "后"
		default:
			name = "键" + code
		}
		if release {
			name = "松开" + name
		}
		labels = append(labels, name)
	}
	return strings.Join(labels, " ")
}

// frameSwitchWindow returns the frame range in which the key switch fires.
func frameSwitchWindow(node *xmlNode) string {
	start := node.get("switchstartframe")
	end := node.get("switchendframe")
	if start == "" || end == "" {
		return ""
	}
	return start + "-" + end
}

// ComboTransition is one author-authored edge of a weapon's combo state
// machine: from OldState, pressing KeyInput (see keyInputNames) advances to
// NewState.
type ComboTransition struct {
	OldState  string `json:"old"`
	NewState  string `json:"new"`
	KeyInput  string `json:"key"`
	StartPart string `json:"part,omitempty"`
}

func (t ComboTransition) row() comboRow {
	part := t.StartPart
	if part == "" {
		part = "1"
	}
	return comboRow{OldState: t.OldState, NewState: t.NewState, KeyInput: t.KeyInput, StartPart: part}
}

// setComboRows replaces every transition a weapon owns with the given set,
// leaving the rest of delayacttable.xml intact.
func setComboRows(text, weapon string, rows []comboRow) (string, error) {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "<Item") {
			if match := comboRowPattern.FindStringSubmatch(trimmed); match != nil && match[1] == weapon {
				// 顺手删掉紧贴在这条转移上方的编辑器标记，保证重复调用
				// 字节稳定（合并导入会重复执行同一把武器的改写）。
				if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "<!--编辑器定制的连招-->" {
					out = out[:len(out)-1]
				}
				continue
			}
		}
		out = append(out, line)
	}
	text = strings.Join(out, "\n")
	if len(rows) == 0 {
		// 没有转移：删掉已有行就够了，别在表里留一个空的编辑器标记。
		return text, nil
	}
	rebuilt := make([]string, 0, len(rows))
	for _, row := range rows {
		rebuilt = append(rebuilt, "\t"+row.render(weapon))
	}
	closing := comboListEndPattern.FindAllStringIndex(text, -1)
	if len(closing) != 1 {
		return "", fmt.Errorf("连招表结构错误")
	}
	block := "\t<!--编辑器定制的连招-->\n" + strings.Join(rebuilt, "\n") + "\n"
	at := closing[0][0]
	return text[:at] + block + text[at:], nil
}

// applyComboChains writes the author-authored state machines into
// delayacttable.xml, replacing the existing rows (and any borrowed donor rows)
// for each weapon that has one.
func applyComboChains(a *archive, chains map[string][]ComboTransition) (*archive, error) {
	if len(chains) == 0 {
		return a, nil
	}
	text, err := a.text("delayacttable.xml")
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(chains))
	for key := range chains {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		rows := make([]comboRow, 0, len(chains[key]))
		for _, transition := range chains[key] {
			rows = append(rows, transition.row())
		}
		if text, err = setComboRows(text, key, rows); err != nil {
			return nil, err
		}
	}
	encoded, err := encodeText(text)
	if err != nil {
		return nil, err
	}
	data, err := a.replace(map[string][]byte{"delayacttable.xml": encoded})
	if err != nil {
		return nil, err
	}
	return parseArchive(data)
}

// comboDeadEnds lists the states a weapon can enter through a combo transition
// but can never leave (no outgoing row): once the chain reaches one of these,
// pressing any key can no longer advance it. In other words these are the
// "cannot chain further" points the flat chain view does not call out.
func comboDeadEnds(a *archive, info *inspection, weaponID string) []map[string]string {
	text, err := a.text("delayacttable.xml")
	if err != nil {
		return nil
	}
	rows := comboRowsOf(text, weaponID)
	if len(rows) == 0 {
		return nil
	}
	olds := map[string]bool{}
	news := map[string]bool{}
	for _, row := range rows {
		olds[row.OldState] = true
		news[row.NewState] = true
	}
	labels := map[string]string{}
	active := map[string]bool{}
	for _, weapon := range info.weapons {
		if strconv.Itoa(weapon.ID) != weaponID {
			continue
		}
		for _, stage := range weapon.Stages {
			if stage.Action != "" && stage.Action != "0" {
				active[stage.State] = true
			}
			labels[stage.State] = stage.Label
		}
		break
	}
	states := make([]string, 0, len(news))
	for state := range news {
		if !active[state] || olds[state] {
			continue
		}
		states = append(states, state)
	}
	sort.Slice(states, func(i, j int) bool {
		a, _ := strconv.Atoi(states[i])
		b, _ := strconv.Atoi(states[j])
		return a < b
	})
	result := make([]map[string]string, 0, len(states))
	for _, state := range states {
		label := labels[state]
		if label == "" {
			label = state
		} else {
			label = state + " · " + label
		}
		result = append(result, map[string]string{"state": state, "label": label})
	}
	return result
}
