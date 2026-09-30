package desktop

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func timestampSuffix() string {
	return time.Now().Format("20060102-150405.000000000")
}

// The weapon editor layers a patch set (appended item/itemact rows for
// self-made weapons, combo and action-effect registrations, per-stage clones in
// skillproperty/animation) onto one client at a time. Which client that is
// comes from client-path.json; the GM lets the user switch it.
//
// Every client directory keeps its own baseline snapshot of config.spf2,
// because two things can differ between clients: the user may pick a different
// one, and a client can be refreshed by its own updater behind our back. A
// single shared baseline would silently overwrite those differences.
type clientBaseline struct {
	Directory   string `json:"directory"`
	File        string `json:"file"`
	SourceHash  string `json:"source_hash,omitempty"`
	AppliedHash string `json:"applied_hash,omitempty"`
}

func normalizeDir(dir string) string {
	cleaned := filepath.ToSlash(filepath.Clean(dir))
	return strings.ToLower(cleaned)
}

// baselineFile names the snapshot after the folder so it stays recognisable in
// the weapon-config directory, plus a short digest so two clients that happen
// to share a folder name never collide.
func baselineFile(dir string) string {
	sum := sha256.Sum256([]byte(normalizeDir(dir)))
	name := filepath.Base(filepath.Clean(dir))
	cleaned := make([]rune, 0, len(name))
	for _, r := range name {
		if r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|' || r == ' ' {
			continue
		}
		cleaned = append(cleaned, r)
	}
	label := string(cleaned)
	if label == "" {
		label = "client"
	}
	return "baselines/" + label + "-" + hex.EncodeToString(sum[:4]) + ".spf2"
}

func (b *clientBaseline) path(folder string) string {
	return filepath.Join(folder, filepath.FromSlash(b.File))
}

func configPath(dir string) string {
	return filepath.Join(dir, "Data", "config.spf2")
}

// baselineFor returns the stored snapshot entry for a client directory,
// migrating the pre-existing single baseline on first use.
func (s *weaponState) baselineFor(dir string) *clientBaseline {
	if s.Baselines == nil {
		s.Baselines = map[string]*clientBaseline{}
	}
	key := normalizeDir(dir)
	if existing, ok := s.Baselines[key]; ok && existing != nil {
		existing.Directory = dir
		return existing
	}
	entry := &clientBaseline{Directory: dir, File: baselineFile(dir)}
	// The very first baseline ever captured lived in original.spf2.
	if len(s.Baselines) == 0 {
		entry.File = "original.spf2"
		entry.SourceHash = s.SourceHash
		entry.AppliedHash = s.AppliedHash
	}
	s.Baselines[key] = entry
	return entry
}

// ensureBaseline records a client's current config as its baseline, unless one
// was already captured. force re-captures after the client changed underneath
// us (its updater replacing config.spf2 is the common case).
func ensureBaseline(entry *clientBaseline, folder string, force bool) error {
	path := entry.path(folder)
	if !force {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	data, err := os.ReadFile(configPath(entry.Directory))
	if err != nil {
		return fmt.Errorf("读不到 %s（%v）", configPath(entry.Directory), err)
	}
	archive, err := parseArchive(data)
	if err != nil {
		return fmt.Errorf("客户端配置包无法解析：%w", err)
	}
	if err = archive.verify(); err != nil {
		return fmt.Errorf("客户端配置包校验失败：%w", err)
	}
	if force {
		if current, err := os.ReadFile(path); err == nil && len(current) > 0 {
			_ = atomicWrite(path+"."+timestampSuffix()+".bak", current)
		}
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err = atomicWrite(path, data); err != nil {
		return err
	}
	entry.SourceHash = digest(data)
	entry.AppliedHash = ""
	return nil
}

// baselineStatus tells the editor whether the client still matches what we last
// wrote, so it can offer a re-capture when someone else changed it.
func baselineStatus(entry *clientBaseline, folder string) string {
	if _, err := os.Stat(entry.path(folder)); err != nil {
		return "未采集基线"
	}
	data, err := os.ReadFile(configPath(entry.Directory))
	if err != nil {
		return "客户端缺失"
	}
	hash := digest(data)
	if hash == entry.AppliedHash && entry.AppliedHash != "" {
		return "已同步"
	}
	if entry.AppliedHash == "" && hash == entry.SourceHash {
		return "未写入"
	}
	return "有差异"
}

// preparedClient is a fully validated write, staged before any file change so a
// failure cannot leave the client half-updated.
type preparedClient struct {
	Entry   *clientBaseline
	Source  *archive
	Current []byte
	Data    []byte
	Backup  string
}

// prepareClient renders the edit set onto the selected client's own baseline
// and runs every guard, without touching the disk.
func prepareClient(entry *clientBaseline, folder string, state *weaponState, plans map[string][]Rule, info *inspection) (*preparedClient, error) {
	if err := ensureBaseline(entry, folder, false); err != nil {
		return nil, err
	}
	source, err := loadArchive(entry.path(folder))
	if err != nil {
		return nil, fmt.Errorf("基线无法读取：%w", err)
	}
	if err = source.verify(); err != nil {
		return nil, fmt.Errorf("基线校验失败：%w", err)
	}
	if entry.SourceHash != "" && digest(source.data) != entry.SourceHash {
		return nil, fmt.Errorf("基线备份已被改动，已停止写入")
	}
	current, err := os.ReadFile(configPath(entry.Directory))
	if err != nil {
		return nil, fmt.Errorf("读不到 %s", configPath(entry.Directory))
	}
	expected := entry.AppliedHash
	if expected == "" {
		expected = digest(source.data)
	}
	if digest(current) != expected {
		return nil, fmt.Errorf("游戏配置已被其他程序修改，已停止覆盖；如确认无误可「重新采集基线」")
	}
	// Combo rules match the client by skillproid number, but render renumbers
	// the hit properties of every applied stage. Translate what is
	// unambiguous and refuse the rest before anything is written, so a saved
	// black/white list can never silently stop matching.
	if _, err := reconcileComboRules(info, state); err != nil {
		return nil, err
	}
	base, err := buildWeaponBase(source, state)
	if err != nil {
		return nil, err
	}
	itemText, err := base.text("item.txt")
	if err != nil {
		return nil, fmt.Errorf("武器表缺失：%w", err)
	}
	items, err := itemsFromText(entry.Directory, itemText, true, true)
	if err != nil {
		return nil, err
	}
	base, err = applyRemaps(base, state, items)
	if err != nil {
		return nil, fmt.Errorf("状态重映射：%w", err)
	}
	if base, err = applyFrameSwitches(base, state, items); err != nil {
		return nil, fmt.Errorf("帧级连招：%w", err)
	}
	if base, err = applyCounters(base, state, items); err != nil {
		return nil, fmt.Errorf("招架：%w", err)
	}
	if base, err = applyBlockElements(base, state, items); err != nil {
		return nil, fmt.Errorf("防护/自身状态：%w", err)
	}
	if base, err = applyScopes(base, state, items); err != nil {
		return nil, fmt.Errorf("攻击范围：%w", err)
	}
	// 招式特效（<Effect> / <HitEffect>）：共用动作块会先克隆成该武器独占，
	// 原有招式与其它武器不受影响。
	if base, err = applyStageEffects(base, state); err != nil {
		return nil, fmt.Errorf("招式特效：%w", err)
	}
	if len(state.Created) > 0 {
		if base, err = syncWeaponEffects(base, state.Created, state.EffectRows); err != nil {
			return nil, fmt.Errorf("同步特效登记：%w", err)
		}
	}
	data, err := render(base, items, plans, cloneMapOf(state))
	if err != nil {
		return nil, err
	}
	// 这一次渲染内部走过好几次追加式写入（蓝图 / applyRemaps / applyFrameSwitches /
	// render），每写一次就把被改条目的旧副本留在文件里。落盘前紧凑重建一次，
	// 让写出去的文件大小等于真实内容大小。放在校验之前，好让下面所有守卫
	// 都跑在「最终要落盘的字节」上。
	if data, err = compactArchive(data); err != nil {
		return nil, err
	}
	verified, err := parseArchive(data)
	if err != nil {
		return nil, err
	}
	if err = verified.verify(); err != nil {
		return nil, err
	}
	if _, err = verified.xml("skillproperty.xml"); err != nil {
		return nil, err
	}
	if _, err = verified.xml("animation/2001.xml"); err != nil {
		return nil, err
	}
	if err = checkAllowedWrites(source, verified, state, info); err != nil {
		return nil, err
	}
	return &preparedClient{Entry: entry, Source: source, Current: current, Data: data}, nil
}

// commitClient backs up whatever is on disk, writes the rendered archive and
// records the new hashes.
func commitClient(prepared *preparedClient, folder string) error {
	backup := filepath.Join(folder, "before-"+timestampSuffix()+".spf2")
	if err := atomicWrite(backup, prepared.Current); err != nil {
		return err
	}
	if err := atomicWrite(configPath(prepared.Entry.Directory), prepared.Data); err != nil {
		_ = atomicWrite(configPath(prepared.Entry.Directory), prepared.Current)
		return err
	}
	prepared.Backup = backup
	prepared.Entry.SourceHash = digest(prepared.Source.data)
	prepared.Entry.AppliedHash = digest(prepared.Data)
	return nil
}

// buildWeaponBase layers the self-made weapon rows and the combo/effect
// registrations of the current edit set onto a pristine baseline.
func buildWeaponBase(source *archive, state *weaponState) (*archive, error) {
	base := source
	var err error
	if len(state.Created) > 0 {
		if base, err = applyBlueprints(source, state.Created); err != nil {
			return nil, err
		}
	}
	plan := comboPlanOf(state.Created, state.Combos)
	for key := range plan {
		if _, explicit := state.Chains[key]; explicit {
			// An author-authored chain supersedes the borrowed donor table.
			delete(plan, key)
		}
	}
	if len(plan) > 0 {
		if base, _, err = applyComboTables(base, plan); err != nil {
			return nil, err
		}
	}
	if len(state.Chains) > 0 {
		if base, err = applyComboChains(base, state.Chains); err != nil {
			return nil, err
		}
	}
	if len(state.ComboRules) > 0 {
		if base, err = applyComboRules(base, state.ComboRules); err != nil {
			return nil, err
		}
	}
	return base, nil
}

// checkAllowedWrites is the safety net that keeps the editor from touching any
// archive entry it has no business changing.
func checkAllowedWrites(source, verified *archive, state *weaponState, info *inspection) error {
	allowed := map[string]bool{"itemact.txt": true, "skillproperty.xml": true}
	if len(state.Created) > 0 {
		allowed["item.txt"] = true
	}
	if len(state.Created) > 0 || len(state.Combos) > 0 || len(state.Chains) > 0 {
		allowed["delayacttable.xml"] = true
		allowed["acteffect.xml"] = true
	}
	if len(state.ComboRules) > 0 {
		allowed["comborule.xml"] = true
	}
	for _, stages := range state.Remaps {
		for _, remap := range stages {
			if remap != nil && len(remap.Action) >= 4 {
				allowed["animation/"+remap.Action[:4]+".xml"] = true
			}
		}
	}
	for name := range frameSwitchFiles(verified, state) {
		allowed[name] = true
	}
	for name := range counterFiles(verified, state) {
		allowed[name] = true
	}
	for name := range blockElementFiles(verified, state) {
		allowed[name] = true
	}
	for name := range scopeFiles(verified, state) {
		allowed[name] = true
	}
	for name := range stageEffectFiles(verified, state) {
		allowed[name] = true
	}
	if info != nil {
		for _, weapon := range info.weapons {
			for _, rule := range state.Applied[fmt.Sprint(weapon.ID)] {
				for _, stage := range weapon.Stages {
					if stage.Stage == rule.Stage && len(stage.Action) >= 4 {
						allowed["animation/"+stage.Action[:4]+".xml"] = true
					}
				}
			}
		}
	}
	names := make([]string, 0, len(source.entries))
	for name := range source.entries {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		after, err := verified.raw(name)
		if err != nil {
			return err
		}
		before, err := source.raw(name)
		if err != nil {
			return err
		}
		if bytes.Equal(before, after) {
			continue
		}
		if !allowed[name] {
			return fmt.Errorf("无关配置校验失败，未写入：%s", name)
		}
		if strings.HasSuffix(name, ".xml") {
			if _, err = verified.xml(name); err != nil {
				return err
			}
		}
	}
	return nil
}

// describeClient reports the selected client for the UI: where it is, whether
// its config parses, and how it relates to the stored baseline.
func describeClient(entry *clientBaseline, folder string) map[string]any {
	hash := ""
	if data, err := os.ReadFile(configPath(entry.Directory)); err == nil {
		hash = digest(data)
	}
	exists := true
	if _, err := os.Stat(configPath(entry.Directory)); err != nil {
		exists = false
	}
	return map[string]any{
		"directory":      entry.Directory,
		"config_hash":    hash,
		"config_present": exists,
		"state":          baselineStatus(entry, folder),
	}
}

// describeBaselines lists every client we have written to before, so the editor
// can show what switching back would reuse.
func describeBaselines(state *weaponState, folder string) []map[string]any {
	keys := make([]string, 0, len(state.Baselines))
	for key := range state.Baselines {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := []map[string]any{}
	for _, key := range keys {
		entry := state.Baselines[key]
		if entry == nil {
			continue
		}
		result = append(result, describeClient(entry, folder))
	}
	return result
}

// encodeClientPath writes client-path.json, keeping the legacy single-directory
// shape so every other module keeps working unchanged.
func encodeClientPath(directory string) ([]byte, error) {
	payload, err := json.MarshalIndent(map[string]string{"client_directory": directory}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(payload, '\n'), nil
}
