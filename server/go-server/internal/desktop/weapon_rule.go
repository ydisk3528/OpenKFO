package desktop

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// comborule.xml is the third weapon registration table in the archive. Where
// delayacttable.xml says which key advances a state machine, this one limits
// what the state machine is allowed to reach:
//
//	<ComboRule Weapon="253108">
//	  <!--最多成功命中 3 次，超过后不再命中-->
//	  <MaxComboForSkill Skill="2001899" MaxCombo="3"/>
//	  <!--最后一击改用别的状态/被动-->
//	  <MaxComboForSkill Skill="1380410" MaxCombo="2" ExceedState="3005" ExceedSkillProID=""/>
//	  <BlackListItem PrevSkill="1080120" CurSkill="1080130"/>   <!--两招不能连-->
//	  <WhiteListItem PrevSkill="1080120" CurSkill="1080140"/>   <!--只能接这一招-->
//	</ComboRule>
//
// The file ships its own authoring notes at the bottom (宁超 2011-8-2), quoted
// above. The critical detail is what Skill/PrevSkill/CurSkill actually name:
// they are the skillproid of the *action block* a state plays, not the state
// number and not the weapon id:
//
//	itemact state 2031 -> action 2001133031
//	  -> animation/2001.xml <AnmDesc id="133031">
//	     -> skillproid="1330310"   <-- this is what ComboRule names
//
// A clone weapon keeps its donor's block ids, so its rules must use the donor's
// numbering too; the shipped data does exactly that (253943 -> 942xxxx,
// 253937 -> 126xxxx, 253156 -> 157xxxx).
//
// Note that the trailing authoring note contains a literal <ComboRule> example
// inside an XML comment. Matching naively would treat that example as a real
// rule for 253108 and, worse, rewriting it would corrupt the documentation, so
// every scan below skips comment spans.
var (
	comboRuleBlockPattern = regexp.MustCompile(`(?s)<ComboRule\s+Weapon\s*=\s*"(\d+)"\s*>(.*?)</ComboRule\s*>`)
	comboRuleRootEnd      = regexp.MustCompile(`</ComboRuleList\s*>`)
	xmlCommentPattern     = regexp.MustCompile(`(?s)<!--.*?-->`)

	maxComboPattern = regexp.MustCompile(
		`<MaxComboForSkill\s+Skill\s*=\s*"([^"]*)"\s+MaxCombo\s*=\s*"([^"]*)"([^>]*)/>`)
	blackItemPattern = regexp.MustCompile(
		`<BlackListItem\s+PrevSkill\s*=\s*"([^"]*)"\s+CurSkill\s*=\s*"([^"]*)"\s*/>`)
	whiteItemPattern = regexp.MustCompile(
		`<WhiteListItem\s+PrevSkill\s*=\s*"([^"]*)"\s+CurSkill\s*=\s*"([^"]*)"\s*/>`)
	exceedAttrPattern = regexp.MustCompile(`(ExceedState|ExceedSkillProID)\s*=\s*"([^"]*)"`)
)

// ComboRuleMax is one <MaxComboForSkill>: the skill may land MaxCombo times, and
// the optional Exceed fields redirect the blow that exceeds the limit.
type ComboRuleMax struct {
	Skill            string `json:"skill"`
	MaxCombo         string `json:"max_combo"`
	ExceedState      string `json:"exceed_state,omitempty"`
	ExceedSkillProID string `json:"exceed_skill_pro_id,omitempty"`
}

// ComboRuleLink is one <BlackListItem> or <WhiteListItem>.
type ComboRuleLink struct {
	Prev string `json:"prev"`
	Cur  string `json:"cur"`
}

// ComboRuleSet is everything one weapon may declare in comborule.xml.
type ComboRuleSet struct {
	Max   []ComboRuleMax  `json:"max,omitempty"`
	Black []ComboRuleLink `json:"black,omitempty"`
	White []ComboRuleLink `json:"white,omitempty"`
}

func (s ComboRuleSet) empty() bool {
	return len(s.Max) == 0 && len(s.Black) == 0 && len(s.White) == 0
}

// commentSpans lists the byte ranges covered by XML comments. Both parsing and
// rewriting consult it so the authoring notes at the bottom of comborule.xml are
// never mistaken for data and never rewritten.
func commentSpans(text string) [][2]int {
	spans := [][2]int{}
	for _, at := range xmlCommentPattern.FindAllStringIndex(text, -1) {
		spans = append(spans, [2]int{at[0], at[1]})
	}
	return spans
}

func inSpans(spans [][2]int, offset int) bool {
	for _, span := range spans {
		if offset >= span[0] && offset < span[1] {
			return true
		}
	}
	return false
}

// comboRuleBlock is one real <ComboRule> found in the file.
type comboRuleBlock struct {
	Weapon string
	Body   string
	Start  int
	End    int
}

// comboRuleBlocks finds every real <ComboRule> block, skipping the examples the
// authoring comment contains.
func comboRuleBlocks(text string) []comboRuleBlock {
	spans := commentSpans(text)
	blocks := []comboRuleBlock{}
	for _, at := range comboRuleBlockPattern.FindAllStringSubmatchIndex(text, -1) {
		if inSpans(spans, at[0]) {
			continue
		}
		blocks = append(blocks, comboRuleBlock{
			Weapon: text[at[2]:at[3]],
			Body:   text[at[4]:at[5]],
			Start:  at[0],
			End:    at[1],
		})
	}
	return blocks
}

// comboRuleSetOf merges every block registered for one weapon. The shipped file
// has 253043 declared twice, so merging (rather than taking the first) is the
// only reading that loses nothing.
func comboRuleSetOf(text, weapon string) (ComboRuleSet, bool) {
	set := ComboRuleSet{}
	found := false
	for _, block := range comboRuleBlocks(text) {
		if block.Weapon != weapon {
			continue
		}
		found = true
		for _, match := range maxComboPattern.FindAllStringSubmatch(block.Body, -1) {
			entry := ComboRuleMax{Skill: match[1], MaxCombo: match[2]}
			for _, attr := range exceedAttrPattern.FindAllStringSubmatch(match[3], -1) {
				if attr[1] == "ExceedState" {
					entry.ExceedState = attr[2]
				} else {
					entry.ExceedSkillProID = attr[2]
				}
			}
			set.Max = append(set.Max, entry)
		}
		for _, match := range blackItemPattern.FindAllStringSubmatch(block.Body, -1) {
			set.Black = append(set.Black, ComboRuleLink{Prev: match[1], Cur: match[2]})
		}
		for _, match := range whiteItemPattern.FindAllStringSubmatch(block.Body, -1) {
			set.White = append(set.White, ComboRuleLink{Prev: match[1], Cur: match[2]})
		}
	}
	return set, found
}

// comboRuleWeapons lists the weapons the client already constrains, so the
// editor can tell official data from its own additions.
func comboRuleWeapons(text string) map[string]bool {
	weapons := map[string]bool{}
	for _, block := range comboRuleBlocks(text) {
		weapons[block.Weapon] = true
	}
	return weapons
}

func (s ComboRuleSet) render(weapon string) string {
	lines := []string{"\t<ComboRule Weapon=\"" + weapon + "\">"}
	for _, entry := range s.Max {
		attributes := []string{"Skill=\"" + entry.Skill + "\"", "MaxCombo=\"" + entry.MaxCombo + "\""}
		if entry.ExceedState != "" {
			attributes = append(attributes, "ExceedState=\""+entry.ExceedState+"\"")
		}
		if entry.ExceedSkillProID != "" {
			attributes = append(attributes, "ExceedSkillProID=\""+entry.ExceedSkillProID+"\"")
		}
		lines = append(lines, "\t\t<MaxComboForSkill "+strings.Join(attributes, " ")+" />")
	}
	for _, link := range s.Black {
		lines = append(lines, "\t\t<BlackListItem PrevSkill=\""+link.Prev+"\" CurSkill=\""+link.Cur+"\" />")
	}
	for _, link := range s.White {
		lines = append(lines, "\t\t<WhiteListItem PrevSkill=\""+link.Prev+"\" CurSkill=\""+link.Cur+"\" />")
	}
	lines = append(lines, "\t</ComboRule>")
	return strings.Join(lines, "\n") + "\n"
}

// comboRuleMarker names the block the editor added, so a later edit removes its
// own note along with the block instead of stacking a new one on every save.
const comboRuleMarker = "<!--编辑器定制的连招限制-->"

// lineIndentStart walks back to the start of the line, but only when everything
// between is indentation. A block that shares its line with other markup (the
// compact fixtures do) must not have its neighbours swallowed.
func lineIndentStart(text string, start int) int {
	at := start
	for at > 0 && text[at-1] != '\n' {
		at--
	}
	for index := at; index < start; index++ {
		if text[index] != ' ' && text[index] != '\t' && text[index] != '\r' {
			return start
		}
	}
	return at
}

// comboRuleOwnedStart is the first byte the editor may rewrite for a block: the
// whole indented line when the block already carries the editor's note, or just
// the block's own line for shipped data (the comments above a block belong to
// the original author and are left alone).
func comboRuleOwnedStart(text string, block comboRuleBlock) int {
	cut := block.Start
	for cut > 0 && (text[cut-1] == ' ' || text[cut-1] == '\t' || text[cut-1] == '\n' || text[cut-1] == '\r') {
		cut--
	}
	if strings.HasSuffix(text[:cut], comboRuleMarker) {
		return lineIndentStart(text, cut-len(comboRuleMarker))
	}
	return lineIndentStart(text, block.Start)
}

// comboRuleOwnedEnd is the byte just past the block plus the line break that
// closes it, so removing a block removes its line instead of leaving a blank one
// behind that would grow on every save.
func comboRuleOwnedEnd(text string, block comboRuleBlock) int {
	end := block.End
	for end < len(text) && (text[end] == ' ' || text[end] == '\t' || text[end] == '\r') {
		end++
	}
	if end < len(text) && text[end] == '\n' {
		end++
	}
	return end
}

// renderBlock is the editor's own output: a marker line naming who wrote it,
// then the rules.
func (s ComboRuleSet) renderBlock(weapon string) string {
	return "\t" + comboRuleMarker + "\n" + s.render(weapon)
}

// setComboRules writes one weapon's rules. The first block the weapon owns is
// replaced where it stands so repeated saves keep the weapon's position in the
// file (and stay byte-identical no matter in which order weapons are saved), any
// duplicate blocks are merged away, and a weapon with no block of its own gets
// one inserted before the list's closing tag. Other weapons' rules and the
// authoring comment are never touched.
func setComboRules(text, weapon string, rules ComboRuleSet) (string, error) {
	if closing := comboRuleRootEnd.FindAllStringIndex(text, -1); len(closing) != 1 {
		return "", fmt.Errorf("连招限制表结构错误")
	}
	owned := []comboRuleBlock{}
	for _, block := range comboRuleBlocks(text) {
		if block.Weapon == weapon {
			owned = append(owned, block)
		}
	}
	if len(owned) == 0 {
		if rules.empty() {
			return text, nil
		}
		at := comboRuleRootEnd.FindStringIndex(text)
		if at == nil {
			return "", fmt.Errorf("连招限制表结构错误")
		}
		return text[:at[0]] + rules.renderBlock(weapon) + text[at[0]:], nil
	}
	var builder strings.Builder
	cursor := 0
	for index, block := range owned {
		builder.WriteString(text[cursor:comboRuleOwnedStart(text, block)])
		cursor = comboRuleOwnedEnd(text, block)
		if index == 0 && !rules.empty() {
			builder.WriteString(rules.renderBlock(weapon))
		}
	}
	builder.WriteString(text[cursor:])
	return builder.String(), nil
}

// applyComboRules writes the author-authored rule sets into comborule.xml. The
// entry already exists in the archive, so this stays inside the writer's
// replace-only contract.
func applyComboRules(a *archive, rules map[string]ComboRuleSet) (*archive, error) {
	if len(rules) == 0 {
		return a, nil
	}
	text, err := a.text("comborule.xml")
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(rules))
	for key := range rules {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	changed := false
	for _, key := range keys {
		set := rules[key]
		before := text
		if text, err = setComboRules(text, key, set); err != nil {
			return nil, err
		}
		if text != before {
			changed = true
		}
	}
	if !changed {
		return a, nil
	}
	encoded, err := encodeText(text)
	if err != nil {
		return nil, err
	}
	data, err := a.replace(map[string][]byte{"comborule.xml": encoded})
	if err != nil {
		return nil, err
	}
	return parseArchive(data)
}

// SkillOption is one skillproid the selected weapon can actually name in a
// ComboRule, together with the state that plays it and the block's own comment.
// Offering these instead of a free-text box is what keeps authors from typing a
// number that no action block ever references — a rule naming an unknown skill
// simply never fires.
type SkillOption struct {
	Skill string `json:"skill"`
	State string `json:"state"`
	Label string `json:"label"`
	Block string `json:"block"`
}

// skillOptions walks the weapon's stages, resolves each stage's action to its
// animation block and collects the skillproid of every hit-property node inside.
// Several skills can share one block (253133's block 133041 declares 1330410,
// 1330411 and 1330412 for 低挑/劈/高挑), so all of them are listed.
func skillOptions(info *inspection, weapon Weapon) []SkillOption {
	seen := map[string]bool{}
	options := []SkillOption{}
	for _, stage := range weapon.Stages {
		if stage.Action == "" || stage.Action == "0" {
			continue
		}
		for _, candidate := range info.blocks[actionKey(stage.Action)] {
			ids := []string{}
			candidate.node.walk(func(node *xmlNode) {
				if ref := node.get("skillproid"); ref != "" && ref != "0" {
					ids = append(ids, ref)
				}
			})
			label := actionDescription(candidate.node)
			if label == "" {
				label = stage.Label
			}
			for _, id := range ids {
				if seen[id] {
					continue
				}
				seen[id] = true
				options = append(options, SkillOption{
					Skill: id,
					State: stage.State,
					Label: label,
					Block: actionKey(stage.Action),
				})
			}
		}
	}
	sort.Slice(options, func(i, j int) bool {
		if options[i].Skill != options[j].Skill {
			return options[i].Skill < options[j].Skill
		}
		return options[i].State < options[j].State
	})
	return options
}

// appliedSkillOptions rewrites the picker's numbers to the ids the client will
// read after the next 应用到游戏. render clones the hit properties of every
// applied stage onto fresh 9000000xx ids, so a rule naming the pre-clone id
// matches nothing once the config is written — the picker has to offer the
// post-clone id or every rule configured through it is dead on arrival. The
// prediction runs on a copy of the persisted clone map: reading a catalogue
// must not pin ids for later runs.
func appliedSkillOptions(info *inspection, state *weaponState, key string, options []SkillOption) []SkillOption {
	if state == nil || len(state.Applied) == 0 || len(options) == 0 {
		return options
	}
	predicted := copyCloneMap(state.PropertyClones)
	if err := assignCloneIDs(info, state.Applied, predicted); err != nil {
		// Broken applied rules surface in render with better context; the raw
		// numbers are the least surprising thing to show meanwhile.
		return options
	}
	applied := map[int]bool{}
	for _, rule := range state.Applied[key] {
		applied[rule.Stage] = true
	}
	for index := range options {
		stage, err := strconv.Atoi(options[index].State)
		if err != nil {
			continue
		}
		number := ruleStageOf(stage)
		if !applied[number] {
			continue
		}
		if id := predicted[key][cloneKey(number, options[index].Skill)]; id != "" {
			options[index].Skill = id
		}
	}
	return options
}

// comboRuleIDIndex lists the skillproid numbers the client will read for one
// weapon after the next apply, plus a reverse index from the numbers a saved
// rule may still carry (pre-clone originals of applied stages, stale clones of
// stages that no longer carry rules) to their current counterpart. A number
// that would translate to two different targets is dropped from the reverse
// index — rewriting those would be a guess.
func comboRuleIDIndex(weapon Weapon, applied []Rule, cloneIDs map[string]string) (map[string]bool, map[string]string) {
	ruled := map[int]bool{}
	for _, rule := range applied {
		ruled[rule.Stage] = true
	}
	effective := map[string]bool{}
	reverse := map[string]string{}
	conflict := map[string]bool{}
	link := func(from, to string) {
		if from == "" || from == to {
			return
		}
		if previous, seen := reverse[from]; seen && previous != to {
			conflict[from] = true
			return
		}
		reverse[from] = to
	}
	for _, stage := range weapon.Stages {
		for _, oldID := range stage.PropertyIDs {
			clone := cloneIDs[cloneKey(stage.Stage, oldID)]
			if ruled[stage.Stage] && clone != "" {
				effective[clone] = true
				link(oldID, clone)
				continue
			}
			effective[oldID] = true
			if clone != "" {
				link(clone, oldID)
			}
		}
	}
	for from := range conflict {
		delete(reverse, from)
	}
	return effective, reverse
}

// reconcileComboRules keeps the saved combo rules effective against the config
// the client will read. render renumbers the hit properties of every applied
// stage, so a rule naming the previous number (or a stale clone of a stage
// that no longer carries a rule) matches nothing and the client silently
// ignores it — measured 2026-09-27: a whole black/white list was inert that
// way. Each reference that maps to exactly one current number is rewritten in
// place; anything still unknown fails the write with the numbers named, so the
// author re-picks instead of shipping a rule that does nothing. The clone ids
// the translation assumes are pinned into state, so the render that follows
// cannot hand out different ones. Reports whether the stored rules changed, so
// the caller can re-bake the base.
func reconcileComboRules(info *inspection, state *weaponState) (bool, error) {
	if state == nil || len(state.ComboRules) == 0 {
		return false, nil
	}
	predicted := cloneMapOf(state)
	if err := assignCloneIDs(info, state.Applied, predicted); err != nil {
		// render reports this with the full context; do not double up here.
		return false, nil
	}
	changed := false
	for key, set := range state.ComboRules {
		weapon, ok := weaponByID(info, key)
		if !ok {
			continue
		}
		effective, reverse := comboRuleIDIndex(weapon, state.Applied[key], predicted[key])
		dead := map[string]bool{}
		rewrite := func(value string) string {
			if value == "" || effective[value] {
				return value
			}
			if target, unique := reverse[value]; unique && effective[target] {
				changed = true
				return target
			}
			dead[value] = true
			return value
		}
		for index := range set.Max {
			set.Max[index].Skill = rewrite(set.Max[index].Skill)
			set.Max[index].ExceedSkillProID = rewrite(set.Max[index].ExceedSkillProID)
		}
		for index := range set.Black {
			set.Black[index].Prev = rewrite(set.Black[index].Prev)
			set.Black[index].Cur = rewrite(set.Black[index].Cur)
		}
		for index := range set.White {
			set.White[index].Prev = rewrite(set.White[index].Prev)
			set.White[index].Cur = rewrite(set.White[index].Cur)
		}
		if len(dead) > 0 {
			numbers := make([]string, 0, len(dead))
			for number := range dead {
				numbers = append(numbers, number)
			}
			sort.Strings(numbers)
			return false, fmt.Errorf("武器 %s（%s）的连招限制引用了客户端读不到的被动编号：%s。这些招式应用后编号会变化，请打开「连招限制」卡片重新选择后保存", weapon.Name, key, strings.Join(numbers, "、"))
		}
		state.ComboRules[key] = set
	}
	return changed, nil
}

// weaponByID finds one inspected weapon by its id.
func weaponByID(info *inspection, key string) (Weapon, bool) {
	for _, weapon := range info.weapons {
		if strconv.Itoa(weapon.ID) == key {
			return weapon, true
		}
	}
	return Weapon{}, false
}

// comboRuleEditable decides whether the editor may write this weapon's rules.
// Self-made weapons are always fair game, and so is any weapon the client does
// not constrain yet — adding a limit where none exists cannot damage shipped
// data. Rewriting a block that ships in the client is refused, which keeps the
// original rule set recoverable from the baseline alone.
func comboRuleEditable(state *weaponState, info *inspection, key string, official, overridden bool) bool {
	if overridden {
		return true
	}
	if isEditableWeapon(state, info, key) {
		return true
	}
	return !official
}

// comboRuleView is the read side of the editor: the effective rule set, the
// rules that ship with the client, whether the weapon may be edited at all, and
// every skillproid its action blocks actually declare. shipped must be the
// pristine baseline — base already carries this editor's own overrides, so
// reading the "official" rules from it would report the override as shipped.
func comboRuleView(shipped *archive, info *inspection, state *weaponState, key, revision string) map[string]any {
	stored, overridden := state.ComboRules[key]
	official := false
	officialRules := ComboRuleSet{}
	if text, err := shipped.text("comborule.xml"); err == nil {
		if parsed, found := comboRuleSetOf(text, key); found {
			officialRules, official = parsed, true
		}
	}
	effective := stored
	if !overridden {
		effective = officialRules
	}
	weapon, _ := weaponByID(info, key)
	// 下拉必须给「应用之后客户端会读到的编号」，否则选出来的规则写进去就永不匹配。
	options := appliedSkillOptions(info, state, key, skillOptions(info, weapon))
	editable := comboRuleEditable(state, info, key, official, overridden)
	reason := ""
	if !editable {
		reason = "本客户端已内置该武器的连招限制，属官方数据，编辑器只改自建武器与未登记限制的武器"
	}
	return map[string]any{
		"weapon":         key,
		"rules":          effective,
		"shipped":        officialRules,
		"official":       official,
		"overridden":     overridden,
		"editable":       editable,
		"reason":         reason,
		"skills":         options,
		"unknown_skills": unknownComboSkills(effective, options),
		"revision":       revision,
	}
}

// validateComboRuleSet keeps the editor from writing a rule the client cannot
// use. Skill identifiers are free-form on purpose: a clone weapon legitimately
// names its donor's block ids, which live outside its own 2xxx stages, so an
// unknown number is reported as a warning rather than refused.
func validateComboRuleSet(set ComboRuleSet) error {
	check := func(field, value string) error {
		if value == "" {
			return fmt.Errorf("%s 不能为空", field)
		}
		// 九位：编辑器自己发的编号就是这个量级 —— 命中属性克隆号
		// 900000000+ 起、新增属性节点 800000001+ 起。以前卡在 99999999（八位），
		// 结果「应用到游戏」后块里真正生效的号，保存时反而被自己拒掉。
		number, err := strconv.Atoi(value)
		if err != nil || number < 1000 || number > 999999999 {
			return fmt.Errorf("%s 必须是动作块被动编号（如 1330310）", field)
		}
		return nil
	}
	for _, entry := range set.Max {
		if err := check("Skill", entry.Skill); err != nil {
			return err
		}
		limit, err := strconv.Atoi(entry.MaxCombo)
		if err != nil || limit < 1 || limit > 999 {
			return fmt.Errorf("最大命中次数必须是 1..999 之间的整数")
		}
		if entry.ExceedState != "" {
			if number, err := strconv.Atoi(entry.ExceedState); err != nil || number < 1000 || number > 9999 {
				return fmt.Errorf("ExceedState 必须是四位状态号（如 3005）")
			}
		}
		if entry.ExceedSkillProID != "" {
			if err := check("ExceedSkillProID", entry.ExceedSkillProID); err != nil {
				return err
			}
		}
	}
	// 校验只挡"写进去游戏读不懂"的数据，不挡官方自己就在用的写法：
	//
	// 1. Prev == Cur 就是"同一个技能不能连续放"这条规则本身。官方 comborule.xml
	//    的 373 条黑名单里有 85 条这样的自环（253013 的 80819 → 80819 就是一例，
	//    253164 一口气 6 条），以前当非法拒掉，官方数据既改不动也补不回来。
	// 2. 白名单的 CurSkill 允许为空，含义是"这个前招之后什么都不许接"
	//    （官方 253147 就靠它实现 ZC/ZX 只能接爆气：CurSkill=""）。
	//    黑名单官方没有空值用例，两端都必须是编号。
	for _, link := range set.Black {
		if err := check("PrevSkill", link.Prev); err != nil {
			return err
		}
		if err := check("CurSkill", link.Cur); err != nil {
			return err
		}
	}
	for _, link := range set.White {
		if err := check("PrevSkill", link.Prev); err != nil {
			return err
		}
		if link.Cur == "" {
			continue
		}
		if err := check("CurSkill", link.Cur); err != nil {
			return err
		}
	}
	return nil
}

// unknownComboSkills reports which numbers in a rule set no action block of the
// weapon declares. The editor shows them as a warning; the write still happens
// because donor-numbered clones are legitimate.
func unknownComboSkills(set ComboRuleSet, options []SkillOption) []string {
	known := map[string]bool{}
	for _, option := range options {
		known[option.Skill] = true
	}
	unknown := map[string]bool{}
	add := func(value string) {
		if value != "" && !known[value] {
			unknown[value] = true
		}
	}
	for _, entry := range set.Max {
		add(entry.Skill)
		add(entry.ExceedSkillProID)
	}
	for _, link := range append(append([]ComboRuleLink{}, set.Black...), set.White...) {
		add(link.Prev)
		add(link.Cur)
	}
	result := make([]string, 0, len(unknown))
	for value := range unknown {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
