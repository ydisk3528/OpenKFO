package desktop

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Imports are immutable candidates. Only an explicit apply touches the client.
type mergeWorkspace struct {
	Client        string            `json:"client"`
	BaseHash      string            `json:"base_hash"`
	Hash          string            `json:"hash"`
	ActiveProfile string            `json:"active_profile,omitempty"`
	SourceHash    string            `json:"source_hash,omitempty"`
	Assets        []string          `json:"assets"`
	AssetHashes   map[string]string `json:"asset_hashes"`
}

func mergeWorkspaceIdentity(folder, client string) (string, string, error) {
	configFolder := filepath.Join(filepath.Dir(folder), "weapon-config")
	statePath := filepath.Join(configFolder, "settings.json")
	raw, err := os.ReadFile(statePath)
	if err != nil && !os.IsNotExist(err) {
		return "", "", err
	}
	state := weaponState{}
	if err == nil {
		if err = json.Unmarshal(raw, &state); err != nil {
			return "", "", err
		}
	}
	// 外部参照配置可以只有资源目录；没有目标基线或客户端配置时不伪造身份。
	entry := state.baselineFor(client)
	_, baselineErr := os.Stat(entry.path(configFolder))
	if os.IsNotExist(baselineErr) {
		if _, err = os.Stat(configPath(client)); os.IsNotExist(err) {
			return "", "", nil
		} else if err != nil {
			return "", "", err
		}
	} else if baselineErr != nil {
		return "", "", baselineErr
	} else if strings.TrimSpace(entry.SourceHash) == "" {
		source, readErr := loadArchive(entry.path(configFolder))
		if readErr != nil {
			return "", "", readErr
		}
		if err = source.verify(); err != nil {
			return "", "", err
		}
		entry.SourceHash = digest(source.data)
	}
	entry, profile, err := activateWeaponProfile(configFolder, statePath, &state, client, false)
	if err != nil {
		return "", "", err
	}
	return profile, entry.SourceHash, nil
}

func validateMergeWorkspaceIdentity(state mergeWorkspace, currentProfile, currentHash string, request Request) error {
	boundProfile := strings.ToLower(strings.TrimSpace(state.ActiveProfile))
	boundHash := strings.ToLower(strings.TrimSpace(state.SourceHash))
	if boundProfile != "" || boundHash != "" {
		if boundProfile == "" || boundHash == "" {
			return fmt.Errorf("临时配置身份不完整，请重新合并")
		}
		if boundProfile != strings.ToLower(strings.TrimSpace(currentProfile)) || boundHash != strings.ToLower(strings.TrimSpace(currentHash)) {
			return fmt.Errorf("临时配置绑定的客户端配置档已切换，请重新合并")
		}
	}
	if requested := strings.TrimSpace(request.ActiveProfile); requested != "" && strings.ToLower(requested) != boundProfile && (boundProfile != "" || strings.ToLower(requested) != strings.ToLower(strings.TrimSpace(currentProfile))) {
		return fmt.Errorf("请求的客户端配置档与临时配置不一致，请刷新后重试")
	}
	if requested := strings.TrimSpace(request.SourceHash); requested != "" && strings.ToLower(requested) != boundHash && (boundHash != "" || strings.ToLower(requested) != strings.ToLower(strings.TrimSpace(currentHash))) {
		return fmt.Errorf("请求的客户端 SPF2 与临时配置不一致，请刷新后重试")
	}
	return nil
}

func mergeWeaponDifferences(a *archive, w mergeWeapon) ([]string, error) {
	changes := []string{}
	number := strconv.Itoa(w.ID)
	equal := func(a, b string) bool {
		return strings.Join(strings.Fields(a), " ") == strings.Join(strings.Fields(b), " ")
	}
	for _, row := range []struct {
		file, incoming, label string
		column                int
	}{
		{"item.txt", w.ItemRow, "名称、模型与道具定义", 1}, {"itemact.txt", w.ItemactRow, "招式动作绑定", 0},
	} {
		text, err := a.text(row.file)
		if err != nil {
			return nil, err
		}
		current, _ := tabRowOf(text, row.column, number)
		if strings.TrimRight(current, "\r\n") != strings.TrimRight(row.incoming, "\r\n") {
			changes = append(changes, row.label)
		}
	}
	text, err := a.text("delayacttable.xml")
	if err != nil {
		return nil, err
	}
	rows := []mergeDelayRow{}
	for _, r := range comboRowsOf(text, number) {
		rows = append(rows, mergeDelayRow{Old: r.OldState, New: r.NewState, Key: r.KeyInput, Part: r.StartPart})
	}
	before, _ := json.Marshal(rows)
	after, _ := json.Marshal(append([]mergeDelayRow{}, w.DelayRows...))
	if !bytes.Equal(before, after) {
		changes = append(changes, "连招与按键")
	}
	for _, c := range []struct {
		file, label, incoming string
		extract               func(string, string) string
	}{
		{"acteffect.xml", "攻击特效绑定", w.ActEffectBlock, weaponEffectBlockOf},
		{"comborule.xml", "出招限制", w.ComboRuleInner, comboRuleInnerOf},
	} {
		text, e := a.text(c.file)
		if e != nil {
			return nil, e
		}
		if !equal(c.extract(text, number), c.incoming) {
			changes = append(changes, c.label)
		}
	}
	for prefix, blocks := range w.AnimationBlocks {
		// 组内检索：1002/1006/3001… 这几组没有同名 4 位文件，按 animation/<前缀>.xml
		// 取会整组读不到（会误报「差异」或直接报错）。
		existing := a.groupBlockTexts(prefix)
		for _, block := range blocks {
			if !containsBlockText(existing, block) {
				changes = append(changes, "动作轨道 "+prefix)
				break
			}
		}
	}
	text, err = a.text("skillproperty.xml")
	if err != nil {
		return nil, err
	}
	for _, node := range w.SkillProperties {
		id := propertyIdInPattern.FindStringSubmatch(node)
		if len(id) != 2 {
			return nil, fmt.Errorf("武器 %d 的命中属性编号无效", w.ID)
		}
		current, _ := propertyNodeText(text, id[1])
		if propertyTagOf(current) != propertyTagOf(node) {
			changes = append(changes, "伤害、受击与 BUFF")
			break
		}
	}
	sort.Strings(changes)
	return changes, nil
}

func mergePackageHash(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func mergeAssetPath(root, relative string) (string, error) {
	relative = strings.ReplaceAll(relative, "\\", "/")
	if !strings.HasPrefix(relative, "Data/") || !filepath.IsLocal(filepath.FromSlash(relative)) || strings.Contains(relative, ":") || strings.EqualFold(relative, "Data/config.spf2") {
		return "", fmt.Errorf("合并包资源路径无效：%s", relative)
	}
	for _, part := range strings.Split(relative, "/") {
		if part == ".." {
			return "", fmt.Errorf("资源路径越界")
		}
	}
	path := filepath.Join(root, filepath.FromSlash(relative))
	// Reject links along an existing target path, before any asset can be written.
	for parent := path; parent != root; parent = filepath.Dir(parent) {
		info, e := os.Lstat(parent)
		if e != nil && !os.IsNotExist(e) {
			return "", e
		}
		if e == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("资源路径包含符号链接：%s", relative)
		}
	}
	return path, nil
}
func readMergeWorkspace(folder, id, client string) (mergeWorkspace, string, error) {
	var state mergeWorkspace
	if id == "" || !configID.MatchString(id) {
		return state, "", fmt.Errorf("临时配置编号无效")
	}
	directory := filepath.Join(folder, id)
	raw, e := os.ReadFile(filepath.Join(directory, "workspace.json"))
	if e != nil {
		return state, "", e
	}
	if e = json.Unmarshal(raw, &state); e != nil {
		return state, "", e
	}
	if state.Client != client {
		return state, "", fmt.Errorf("临时配置属于其他客户端，请重新对比")
	}
	a, e := loadArchive(configPath(directory))
	if e != nil {
		return state, "", e
	}
	if e = a.verify(); e != nil {
		return state, "", e
	}
	if state.Hash != digest(a.data) {
		return state, "", fmt.Errorf("临时配置已被修改，请重新导入")
	}
	return state, directory, nil
}

// The legacy one-step RPC reuses the staged transaction, but requires the
// preview identity and revision. The low-level importer remains usable by stage.
func (admin *Admin) weaponMergeDirect(r Request, client string) (any, error) {
	if strings.TrimSpace(r.ActiveProfile) == "" || strings.TrimSpace(r.SourceHash) == "" {
		return nil, fmt.Errorf("请求缺少客户端配置档身份，请重新对比合并包")
	}
	if strings.TrimSpace(r.Revision) == "" {
		return nil, fmt.Errorf("请求缺少合并预览版本，请重新对比合并包")
	}
	if r.MergeWorkspace != "" || (r.ClientConfig != nil && (r.ClientConfig.Base != "" || r.ClientConfig.ResourceRoot != "")) {
		return nil, fmt.Errorf("外部参照或临时配置请使用临时合并应用流程")
	}
	reader, _, manifest, err := openMergeZip(r.SourcePath)
	if err != nil {
		return nil, err
	}
	reader.Close()
	// Reject stale requests before compare can even create a workspace folder.
	identityFolder := filepath.Join(admin.Root, "runtime-local", "weapon-merge-workspaces")
	currentProfile, currentHash, err := mergeWorkspaceIdentity(identityFolder, client)
	if err != nil {
		return nil, err
	}
	if err = validateWeaponProfileRequest(r, currentProfile, currentHash, true); err != nil {
		return nil, err
	}
	packageHash, err := mergePackageHash(r.SourcePath)
	if err != nil {
		return nil, err
	}
	current, err := os.ReadFile(configPath(client))
	if err != nil {
		return nil, err
	}
	if r.Revision != digest([]byte(packageHash+digest(current))) {
		return nil, fmt.Errorf("武器包或参照配置已变化，请重新对比")
	}
	ids := append([]int(nil), r.MergeWeapons...)
	if len(ids) == 0 {
		for _, weapon := range manifest.Weapons {
			ids = append(ids, weapon.ID)
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("合并包里没有可导入的武器")
	}
	compareRequest := Request{
		Operation:    "weapon_merge_compare",
		SourcePath:   r.SourcePath,
		ClientConfig: r.ClientConfig,
	}
	compared, err := admin.weaponMergeWorkspace(compareRequest, client)
	if err != nil {
		return nil, err
	}
	preview, ok := compared.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("合并预览返回格式无效")
	}
	profile, _ := preview["active_profile"].(string)
	sourceHash, _ := preview["source_hash"].(string)
	proof, _ := preview["revision"].(string)
	if err = validateWeaponProfileRequest(r, profile, sourceHash, true); err != nil {
		return nil, err
	}
	if r.Revision != proof {
		return nil, fmt.Errorf("武器包或参照配置已变化，请重新对比")
	}
	stageRequest := Request{
		Operation:     "weapon_merge_stage",
		SourcePath:    r.SourcePath,
		MergeWeapons:  ids,
		Revision:      proof,
		ActiveProfile: profile,
		SourceHash:    sourceHash,
	}
	staged, err := admin.weaponMergeWorkspace(stageRequest, client)
	if err != nil {
		return nil, err
	}
	stage, ok := staged.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("合并暂存返回格式无效")
	}
	workspace, ok := stage["workspace"].(string)
	if !ok || strings.TrimSpace(workspace) == "" {
		return nil, fmt.Errorf("合并暂存缺少 workspace")
	}
	applyRequest := Request{
		Operation:      "weapon_merge_apply",
		MergeWorkspace: workspace,
		ActiveProfile:  profile,
		SourceHash:     sourceHash,
	}
	applied, err := admin.weaponMergeWorkspace(applyRequest, client)
	if err != nil {
		return nil, err
	}
	if result, ok := applied.(map[string]any); ok {
		result["imported"] = ids
		result["message"] = fmt.Sprintf("已直接合并 %d 把武器到当前客户端", len(ids))
	}
	return applied, nil
}

func (admin *Admin) weaponMergeWorkspace(r Request, client string) (any, error) {
	if r.ClientConfig != nil && r.ClientConfig.ResourceRoot != "" {
		client = r.ClientConfig.ResourceRoot
	}
	client, e := filepath.Abs(client)
	if e != nil {
		return nil, e
	}
	folder := filepath.Join(admin.Root, "runtime-local", "weapon-merge-workspaces")
	if e = os.MkdirAll(folder, 0700); e != nil {
		return nil, e
	}
	release, e := acquireWeaponLock(folder)
	if e != nil {
		return nil, e
	}
	defer release()
	target := client
	state := mergeWorkspace{Client: client}
	if r.MergeWorkspace != "" {
		state, target, e = readMergeWorkspace(folder, r.MergeWorkspace, client)
		if e != nil {
			return nil, e
		}
	}
	profile, sourceHash := state.ActiveProfile, state.SourceHash
	if r.Operation == "weapon_merge_compare" || r.Operation == "weapon_merge_stage" {
		profile, sourceHash, e = mergeWorkspaceIdentity(folder, client)
		if e != nil {
			return nil, e
		}
	}
	if r.Operation != "weapon_merge_apply" {
		if e = validateMergeWorkspaceIdentity(state, profile, sourceHash, r); e != nil {
			return nil, e
		}
	}
	if r.Operation == "weapon_merge_compare" || r.Operation == "weapon_merge_stage" {
		packageHash, e := mergePackageHash(r.SourcePath)
		if e != nil {
			return nil, e
		}
		basePath := configPath(target)
		if r.MergeWorkspace == "" && r.ClientConfig != nil && r.ClientConfig.Base != "" {
			basePath = r.ClientConfig.Base
		}
		current, e := loadArchive(basePath)
		if e != nil {
			return nil, e
		}
		proof := digest([]byte(packageHash + digest(current.data)))
		if r.Operation == "weapon_merge_compare" {
			value, e := weaponMergePreview(r, target)
			if e != nil {
				return nil, e
			}
			raw, _ := json.Marshal(value)
			result := map[string]any{}
			_ = json.Unmarshal(raw, &result)
			result["revision"] = proof
			if r.MergeWorkspace != "" {
				result["active_profile"] = state.ActiveProfile
				result["source_hash"] = state.SourceHash
			} else {
				result["active_profile"] = profile
				result["source_hash"] = sourceHash
			}
			return result, nil
		}
		if r.Revision != proof {
			return nil, fmt.Errorf("武器包或参照配置已变化，请重新对比")
		}
		if len(r.MergeWeapons) == 0 {
			return nil, fmt.Errorf("请勾选要合并的武器")
		}
		reader, assets, manifest, e := openMergeZip(r.SourcePath)
		if e != nil {
			return nil, e
		}
		defer reader.Close()
		for _, rel := range manifest.Assets {
			if _, e = mergeAssetPath(target, rel); e != nil {
				return nil, e
			}
			f, ok := assets[rel]
			if !ok {
				return nil, fmt.Errorf("武器包缺少资源：%s", rel)
			}
			if f.UncompressedSize64 > 64<<20 {
				return nil, fmt.Errorf("资源过大：%s", rel)
			}
		}
		candidate, e := os.MkdirTemp(folder, "merge-")
		if e != nil {
			return nil, e
		}
		keep := false
		defer func() {
			if !keep {
				os.RemoveAll(candidate)
			}
		}()
		if e = os.MkdirAll(filepath.Join(candidate, "Data"), 0700); e != nil {
			return nil, e
		}
		if e = atomicWrite(configPath(candidate), current.data); e != nil {
			return nil, e
		}
		for _, rel := range state.Assets {
			from, e := mergeAssetPath(target, rel)
			if e != nil {
				return nil, e
			}
			to, e := mergeAssetPath(candidate, rel)
			if e != nil {
				return nil, e
			}
			raw, e := os.ReadFile(from)
			if e != nil {
				return nil, e
			}
			if state.AssetHashes[rel] != digest(raw) {
				return nil, fmt.Errorf("临时资源已变化：%s", rel)
			}
			if e = os.MkdirAll(filepath.Dir(to), 0700); e != nil {
				return nil, e
			}
			if e = atomicWrite(to, raw); e != nil {
				return nil, e
			}
		}
		if state.BaseHash == "" {
			state.BaseHash = digest(current.data)
		}
		if r.MergeWorkspace == "" && state.ActiveProfile == "" && profile != "" {
			state.ActiveProfile = profile
			state.SourceHash = sourceHash
		}
		value, e := weaponMergeImport(r, candidate, candidate)
		if e != nil {
			return nil, e
		}
		latestHash, e := mergePackageHash(r.SourcePath)
		if e != nil || latestHash != packageHash {
			return nil, fmt.Errorf("合并过程中 ZIP 已变化，请重新对比")
		}
		mergedArchive, e := loadArchive(configPath(candidate))
		if e != nil {
			return nil, e
		}
		items, e := catalog(candidate, false, false)
		if e != nil {
			return nil, e
		}
		include := map[string]bool{}
		for _, section := range packageSections {
			include[section] = true
		}
		required, _ := collectWeaponAssets(candidate, mergedArchive, items, r.MergeWeapons, include)
		incoming := []string{}
		for _, file := range required {
			incoming = append(incoming, file.Path)
		}
		all := map[string]bool{}
		for _, rel := range append(state.Assets, incoming...) {
			all[rel] = true
		}
		state.Assets = nil
		for rel := range all {
			state.Assets = append(state.Assets, rel)
		}
		sort.Strings(state.Assets)
		state.AssetHashes = map[string]string{}
		for _, rel := range state.Assets {
			path, e := mergeAssetPath(candidate, rel)
			if e != nil {
				return nil, e
			}
			raw, e := os.ReadFile(path)
			if e != nil {
				return nil, e
			}
			state.AssetHashes[rel] = digest(raw)
		}
		merged, e := os.ReadFile(configPath(candidate))
		if e != nil {
			return nil, e
		}
		state.Hash = digest(merged)
		raw, _ := json.Marshal(state)
		if e = atomicWrite(filepath.Join(candidate, "workspace.json"), raw); e != nil {
			return nil, e
		}
		raw, _ = json.Marshal(value)
		result := map[string]any{}
		_ = json.Unmarshal(raw, &result)
		result["workspace"] = filepath.Base(candidate)
		result["assets"] = len(incoming)
		result["path"] = configPath(candidate)
		result["active_profile"] = state.ActiveProfile
		result["source_hash"] = state.SourceHash
		result["message"] = "已合并到临时配置，游戏文件未修改"
		keep = true
		return result, nil
	}
	if r.MergeWorkspace == "" {
		return nil, fmt.Errorf("没有待保存的临时配置")
	}
	config, e := os.ReadFile(configPath(target))
	if e != nil {
		return nil, e
	}
	payload := map[string][]byte{"Data/config.spf2": config}
	for _, rel := range state.Assets {
		path, e := mergeAssetPath(target, rel)
		if e != nil {
			return nil, e
		}
		raw, e := os.ReadFile(path)
		if e != nil {
			return nil, e
		}
		if state.AssetHashes[rel] != digest(raw) {
			return nil, fmt.Errorf("临时资源已变化：%s，请重新合并", rel)
		}
		payload[rel] = raw
	}
	if r.Operation == "weapon_merge_save" {
		// 前端不传 path 时（GM 部署下原生保存对话框不可用），落到固定快照目录。
		path := r.Path
		if strings.TrimSpace(path) == "" {
			dir := filepath.Join(admin.Root, "dist", "config-snapshots")
			if e = os.MkdirAll(dir, 0700); e != nil {
				return nil, e
			}
			path = filepath.Join(dir, fmt.Sprintf("config-merge-%s.zip", time.Now().Format("20060102-150405")))
		}
		if !strings.EqualFold(filepath.Ext(path), ".zip") {
			return nil, fmt.Errorf("请保存为 ZIP 文件")
		}
		if _, e = os.Stat(path); !os.IsNotExist(e) {
			return nil, fmt.Errorf("保存目标已存在，请使用新文件名")
		}
		var buffer bytes.Buffer
		writer := zip.NewWriter(&buffer)
		names := []string{}
		for name := range payload {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			w, e := writer.Create(name)
			if e != nil {
				return nil, e
			}
			if _, e = w.Write(payload[name]); e != nil {
				return nil, e
			}
		}
		if e = writer.Close(); e != nil {
			return nil, e
		}
		f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return nil, e
		}
		_, e = f.Write(buffer.Bytes())
		if e == nil {
			e = f.Sync()
		}
		closeErr := f.Close()
		if e == nil {
			e = closeErr
		}
		if e != nil {
			os.Remove(path)
			return nil, e
		}
		return map[string]any{"path": path, "message": "临时配置和新增资源已保存为 ZIP，未修改游戏"}, nil
	}
	if r.Operation != "weapon_merge_apply" {
		return nil, fmt.Errorf("未知临时配置操作")
	}
	edits := filepath.Join(admin.Root, "runtime-local", "weapon-config")
	if e = os.MkdirAll(edits, 0700); e != nil {
		return nil, e
	}
	unlock, e := acquireWeaponLock(edits)
	if e != nil {
		return nil, e
	}
	defer unlock()
	if state.ActiveProfile != "" || state.SourceHash != "" || strings.TrimSpace(r.ActiveProfile) != "" || strings.TrimSpace(r.SourceHash) != "" {
		currentProfile, currentHash, identityErr := mergeWorkspaceIdentity(folder, client)
		if identityErr != nil {
			return nil, identityErr
		}
		if e = validateMergeWorkspaceIdentity(state, currentProfile, currentHash, r); e != nil {
			return nil, e
		}
	}
	before, e := os.ReadFile(configPath(client))
	if e != nil {
		return nil, e
	}
	if digest(before) != state.BaseHash {
		return nil, fmt.Errorf("所选基础与游戏当前配置不一致，或游戏配置已变化，不能覆盖；请保存临时 ZIP，再基于游戏当前配置重新合并")
	}
	backup, e := os.MkdirTemp(folder, "before-apply-")
	if e != nil {
		return nil, e
	}
	if e = os.MkdirAll(filepath.Join(backup, "Data"), 0700); e != nil {
		return nil, e
	}
	if e = atomicWrite(configPath(backup), before); e != nil {
		return nil, e
	}
	// Back up every existing asset before writing any, then roll back on error.
	originals := map[string][]byte{}
	paths := map[string]string{}
	for rel := range payload {
		if rel == "Data/config.spf2" {
			paths[rel] = configPath(client)
			originals[rel] = before
			continue
		}
		path, e := mergeAssetPath(client, rel)
		if e != nil {
			return nil, e
		}
		paths[rel] = path
		raw, e := os.ReadFile(path)
		if e != nil && !os.IsNotExist(e) {
			return nil, e
		}
		if e == nil {
			originals[rel] = raw
			bp := filepath.Join(backup, filepath.FromSlash(rel))
			if e = os.MkdirAll(filepath.Dir(bp), 0700); e != nil {
				return nil, e
			}
			if e = atomicWrite(bp, raw); e != nil {
				return nil, e
			}
		}
	}
	written := []string{}
	rollback := func(cause error) (any, error) {
		failures := []string{}
		for i := len(written) - 1; i >= 0; i-- {
			rel := written[i]
			var err error
			if raw, ok := originals[rel]; ok {
				err = atomicWrite(paths[rel], raw)
			} else {
				err = os.Remove(paths[rel])
			}
			if err != nil {
				failures = append(failures, rel+": "+err.Error())
			}
		}
		return nil, fmt.Errorf("应用失败：%v；备份：%s；回滚错误：%v", cause, backup, failures)
	}
	names := append([]string{}, state.Assets...)
	names = append(names, "Data/config.spf2")
	for _, rel := range names {
		if rel == "Data/config.spf2" {
			now, e := os.ReadFile(configPath(client))
			if e != nil {
				return rollback(e)
			}
			if digest(now) != state.BaseHash {
				return rollback(fmt.Errorf("写入前游戏配置已变化"))
			}
		}
		if e = os.MkdirAll(filepath.Dir(paths[rel]), 0700); e != nil {
			return rollback(e)
		}
		if e = atomicWrite(paths[rel], payload[rel]); e != nil {
			return rollback(e)
		}
		written = append(written, rel)
	}
	// 合并导入直接换掉了客户端的 config.spf2。武器编辑器的基线若还停在旧文件上，
	// 之后每次「应用到游戏」都会撞上 prepareClient 的守卫（客户端配置与基线
	// 不一致）而被永久拒绝——编辑集里的 buff/招式挂载再也写不回去。所以这里
	// 顺手把该客户端的新配置采为基线，并把编辑集里已应用的编辑重新渲染进去，
	// 让基线 = 「客户端当前真实状态 + 已应用编辑」，守卫下一轮才能通过。
	if e = syncBaselineAfterImport(edits, client, backup); e != nil {
		return rollback(e)
	}
	newProfile, newHash, e := mergeWorkspaceIdentity(folder, client)
	if e != nil {
		return rollback(e)
	}
	return map[string]any{
		"path":           configPath(client),
		"backup":         backup,
		"active_profile": newProfile,
		"source_hash":    newHash,
		"message":        "临时配置已应用到游戏，请重新打开游戏",
	}, nil
}

// syncBaselineAfterImport 在合并导入换掉客户端的 config.spf2 之后，重新采集
// 该客户端的基线，并把目标 profile 中已应用的编辑重新渲染回去。
func syncBaselineAfterImport(folder, client, backup string) error {
	statePath := filepath.Join(folder, "settings.json")
	raw, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var state weaponState
	if err = json.Unmarshal(raw, &state); err != nil {
		return err
	}
	if _, err = os.Stat(configPath(client)); err != nil {
		return nil
	}

	// 保留当前全局 profile 后，再以只读方式切到导入目标的 profile。
	// activateWeaponProfile(..., false) 会把目标作者态装入 state，但不会把
	// 目标切换过程提前持久化，也不会把其他客户端的编辑混入本次渲染。
	globalProfile := state.ActiveProfile
	if err = archiveWeaponProfile(folder, globalProfile, &state); err != nil {
		return err
	}
	entry, targetProfile, err := activateWeaponProfile(folder, statePath, &state, client, false)
	if err != nil {
		return err
	}
	// ensureBaseline(..., true) mutates the shared baseline entry in state. Archive
	// a deep copy first so the old profile keeps its original SourceHash/Baselines.
	archivedRaw, err := json.Marshal(&state)
	if err != nil {
		return err
	}
	var archivedState weaponState
	if err = json.Unmarshal(archivedRaw, &archivedState); err != nil {
		return err
	}
	if err = archiveWeaponProfile(folder, targetProfile, &archivedState); err != nil {
		return err
	}
	if err = ensureBaseline(entry, folder, true); err != nil {
		return err
	}
	state.Baselines[normalizeDir(client)] = entry

	source, err := loadArchive(entry.path(folder))
	if err != nil {
		return fmt.Errorf("重新采集基线后无法读取：%w", err)
	}
	if err = source.verify(); err != nil {
		return fmt.Errorf("重新采集基线后校验失败：%w", err)
	}
	if hasWeaponAuthorState(&state) {
		base, err := buildWeaponBase(source, &state)
		if err != nil {
			return err
		}
		itemText, err := base.text("item.txt")
		if err != nil {
			return err
		}
		items, err := itemsFromText(entry.Directory, itemText, true, true)
		if err != nil {
			return err
		}
		info, err := inspect(base, items)
		if err != nil {
			return err
		}
		// 合并导入不碰招式编号，连招校验与本次写入无关，跳过以免旧黑名单拦住。
		plan, err := prepareClient(entry, folder, &state, state.Applied, info, withoutComboReconcile())
		if err != nil {
			return err
		}
		if err = commitClient(plan, folder); err != nil {
			return err
		}
	}

	// 新 baseline 的 hash 是新的 profile 身份。旧 profile 已在 force 更新前
	// 归档，不能再用本次的新 state 覆盖它；只迁移到新的 profile。
	newProfile := profileKeyForBaseline(entry, folder)
	if err = migrateMergeImportWorkspaces(folder, targetProfile, newProfile); err != nil {
		return err
	}
	state.ActiveProfile = newProfile
	state.SourceHash = entry.SourceHash
	state.AppliedHash = entry.AppliedHash
	if !strings.EqualFold(strings.TrimSpace(targetProfile), strings.TrimSpace(newProfile)) {
		if err = archiveWeaponProfile(folder, newProfile, &state); err != nil {
			return err
		}
	}
	encoded, err := json.MarshalIndent(&state, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(statePath, encoded)
}

func migrateMergeImportWorkspaces(folder, oldProfile, newProfile string) error {
	if strings.EqualFold(strings.TrimSpace(oldProfile), strings.TrimSpace(newProfile)) {
		return nil
	}
	oldDir := weaponWorkspaceFolder(folder, oldProfile)
	entries, err := os.ReadDir(oldDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	newDir := weaponWorkspaceFolder(folder, newProfile)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		from := filepath.Join(oldDir, entry.Name())
		to := filepath.Join(newDir, entry.Name())
		data, readErr := os.ReadFile(from)
		if readErr != nil {
			return readErr
		}
		existing, readErr := os.ReadFile(to)
		if readErr == nil {
			if !bytes.Equal(existing, data) {
				return fmt.Errorf("新 profile workspace 已存在不同内容：%s", entry.Name())
			}
			continue
		}
		if !os.IsNotExist(readErr) {
			return readErr
		}
		if err = os.MkdirAll(newDir, 0700); err != nil {
			return err
		}
		if err = atomicWrite(to, data); err != nil {
			return err
		}
	}
	return nil
}
