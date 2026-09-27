//go:build windows

package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Set only for the standalone repair distribution. The sidecar is the complete
// tested launcher bundle, not an arbitrary ZIP supplied by a player.
var repairPayloadHash string

// The repair tool lives next to the launcher, but runs from a fresh temporary
// location so it cannot lock a file being replaced by the original update.
func pendingRepairPlan(root string) (string, error) {
	b, err := os.ReadFile(filepath.Join(root, ".flutter-update-pending"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("此目录没有待恢复的启动器更新。请把修复工具放到报错启动器的同一目录再运行。")
		}
		return "", err
	}
	path := strings.TrimSpace(string(b))
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("更新恢复记录路径无效，原文件已保留")
	}
	b, err = os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("原更新文件已丢失，请重新解压最新版完整启动器包：%w", err)
	}
	var plan updatePlan
	if err = json.Unmarshal(b, &plan); err != nil {
		return "", err
	}
	if !strings.EqualFold(filepath.Clean(plan.Target), filepath.Clean(root)) || !strings.EqualFold(filepath.Dir(path), filepath.Clean(plan.Stage)) {
		return "", fmt.Errorf("更新记录不属于此启动器目录，未执行修复")
	}
	return path, nil
}

func repairPendingUpdate(root string) error {
	var plan string
	var err error
	if repairPayloadHash != "" {
		b, e := os.ReadFile(filepath.Join(root, "repair-payload.zip"))
		if e != nil {
			return fmt.Errorf("请将修复包完整解压到启动器目录，缺少 repair-payload.zip：%w", e)
		}
		plan, err = prepareCompleteRepair(root, b, repairPayloadHash)
	} else {
		plan, err = pendingRepairPlan(root)
	}
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	b, err := os.ReadFile(exe)
	if err != nil {
		return err
	}
	stage, err := os.MkdirTemp("", "OpenKFO-update-repair-")
	if err != nil {
		return err
	}
	helper := filepath.Join(stage, "UpdateHelper.exe")
	if err = os.WriteFile(helper, b, 0700); err != nil {
		return err
	}
	cmd := exec.Command(helper, "--apply", plan)
	cmd.Dir = stage
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err = cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func prepareCompleteRepair(root string, payload []byte, expected string) (string, error) {
	hash := sha256.Sum256(payload)
	if hex.EncodeToString(hash[:]) != expected {
		return "", fmt.Errorf("修复包校验失败，请重新下载完整修复包；原文件未替换")
	}
	z, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		return "", err
	}
	if err = noLinks(root); err != nil {
		return "", err
	}
	stage, err := os.MkdirTemp("", "OpenKFO-complete-repair-")
	if err != nil {
		return "", err
	}
	plan := updatePlan{Target: root, Stage: stage, Launcher: filepath.Join(root, "启动器.exe")}
	seen := map[string]bool{}
	var total uint64
	for _, f := range z.File {
		if f.FileInfo().IsDir() {
			continue
		}
		key := strings.ToLower(f.Name)
		if !safeName(f.Name) || seen[key] || f.Mode()&os.ModeSymlink != 0 || f.UncompressedSize64 > 256<<20 {
			return "", fmt.Errorf("修复包文件路径或大小无效")
		}
		seen[key] = true
		total += f.UncompressedSize64
		if total > 512<<20 {
			return "", fmt.Errorf("修复包过大")
		}
		r, e := f.Open()
		if e != nil {
			return "", e
		}
		b, e := io.ReadAll(io.LimitReader(r, int64(f.UncompressedSize64)+1))
		r.Close()
		if e != nil || uint64(len(b)) != f.UncompressedSize64 {
			return "", fmt.Errorf("修复包解压失败：%s", f.Name)
		}
		if e = atomic(filepath.Join(stage, filepath.FromSlash(f.Name)), b); e != nil {
			return "", e
		}
		h := sha256.Sum256(b)
		plan.Files = append(plan.Files, updateFile{Name: f.Name, Size: int64(len(b)), SHA256: hex.EncodeToString(h[:])})
	}
	if !seen["启动器.exe"] || !seen["launchersupport.exe"] || !seen["data/app.so"] {
		return "", fmt.Errorf("修复包不是完整启动器")
	}
	gate, err := syscall.CreateFile(utf(filepath.Join(root, ".flutter-update.lock")), syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return "", fmt.Errorf("另一个更新正在执行，请待其结束后再修复")
	}
	defer syscall.CloseHandle(gate)
	if err = ensureLauncherClosed(plan.Launcher); err != nil {
		return "", err
	}
	marker := filepath.Join(root, ".flutter-update-pending")
	if old, e := os.ReadFile(marker); e == nil {
		// Preserve the old plan and rollback files for diagnosis. A verified
		// complete bundle supersedes it, including missing/corrupt old stages.
		if err = atomic(marker+".before-repair-"+fmt.Sprint(time.Now().UnixNano()), old); err != nil {
			return "", err
		}
	} else if !os.IsNotExist(e) {
		return "", e
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		return "", err
	}
	path := filepath.Join(stage, "plan.json")
	if err = atomic(path, raw); err != nil {
		return "", err
	}
	if err = atomic(marker, []byte(path)); err != nil {
		return "", err
	}
	return path, nil
}
