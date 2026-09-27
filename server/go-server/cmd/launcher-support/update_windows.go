//go:build windows

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

type updateFile struct {
	Name, SHA256 string
	Size         int64
}
type updatePlan struct {
	Target, Stage, Launcher string
	PID                     uint32
	Created                 uint64
	Files                   []updateFile
}
type backupFile struct {
	Path, Copy string
	Existed    bool
}

func safeName(n string) bool {
	return n != "" && !strings.Contains(n, "..") && !strings.ContainsAny(n, "\\:") && !strings.HasPrefix(n, "/") && filepath.Clean(n) != "."
}
func noLinks(path string) error {
	for p := path; ; p = filepath.Dir(p) {
		st, err := os.Lstat(p)
		if err == nil && st.Mode()&os.ModeSymlink != 0 {
			return errors.New("更新路径不能使用链接")
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return nil
}

// Check the full image path: other installations and game processes are unrelated.
func ensureLauncherClosed(image string) error {
	return ensureLauncherClosedWithin(image, 15*time.Second)
}

func ensureLauncherClosedWithin(image string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	snapshot, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return fmt.Errorf("检查启动器占用失败：%w", err)
	}
	defer syscall.CloseHandle(snapshot)
	entry := syscall.ProcessEntry32{Size: uint32(unsafe.Sizeof(syscall.ProcessEntry32{}))}
	for err = syscall.Process32First(snapshot, &entry); err == nil; err = syscall.Process32Next(snapshot, &entry) {
		if !strings.EqualFold(syscall.UTF16ToString(entry.ExeFile[:]), filepath.Base(image)) {
			continue
		}
		handle, e := syscall.OpenProcess(processQuery|syscall.SYNCHRONIZE, false, entry.ProcessID)
		if e != nil {
			if e == syscall.ERROR_ACCESS_DENIED {
				return fmt.Errorf("无法确认启动器是否已退出（PID %d），请关闭所有启动器窗口后重试", entry.ProcessID)
			}
			continue // The process may have exited after the snapshot.
		}
		var buffer [32768]uint16
		length := uint32(len(buffer))
		ok, _, queryErr := kernel.NewProc("QueryFullProcessImageNameW").Call(uintptr(handle), 0, uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&length)))
		if ok == 0 {
			syscall.CloseHandle(handle)
			return fmt.Errorf("读取启动器路径失败（PID %d）：%w", entry.ProcessID, queryErr)
		}
		if strings.EqualFold(filepath.Clean(syscall.UTF16ToString(buffer[:length])), filepath.Clean(image)) {
			// A pending update may have been created before a reboot. The new
			// launcher starts this helper before exiting, so the plan's old PID
			// cannot be used to wait for the current launcher. Wait on the actual
			// matching process handle; never terminate it or trust a stale PID.
			remaining := time.Until(deadline).Milliseconds()
			if remaining < 0 {
				remaining = 0
			}
			result, waitErr := syscall.WaitForSingleObject(handle, uint32(remaining))
			syscall.CloseHandle(handle)
			if waitErr != nil {
				return fmt.Errorf("等待启动器退出失败（PID %d）：%w", entry.ProcessID, waitErr)
			}
			if result != syscall.WAIT_OBJECT_0 {
				return fmt.Errorf("更新已等待启动器退出，但该进程仍在运行（PID %d）。请关闭此目录的启动器后重试；游戏无需关闭。文件：%s", entry.ProcessID, image)
			}
		} else {
			syscall.CloseHandle(handle)
		}
	}
	if err != syscall.ERROR_NO_MORE_FILES {
		return fmt.Errorf("检查启动器进程失败：%w", err)
	}
	return nil
}

func applyPlan(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var plan updatePlan
	if err = json.Unmarshal(b, &plan); err != nil {
		return err
	}
	if !filepath.IsAbs(plan.Target) || !filepath.IsAbs(plan.Stage) || !strings.EqualFold(filepath.Dir(plan.Launcher), plan.Target) || len(plan.Files) == 0 {
		return errors.New("更新计划无效")
	}
	// Hash every staged file before touching the installation.
	gate, err := syscall.CreateFile(utf(filepath.Join(plan.Target, ".flutter-update.lock")), syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return errors.New("另一个更新正在运行")
	}
	defer syscall.CloseHandle(gate)
	data := map[string][]byte{}
	var total int64
	for _, f := range plan.Files {
		if !safeName(f.Name) || f.Size < 0 || f.Size > 256<<20 {
			return errors.New("更新文件路径或长度无效")
		}
		dst := filepath.Join(plan.Target, filepath.FromSlash(f.Name))
		if err = noLinks(dst); err != nil {
			return err
		}
		v, e := os.ReadFile(filepath.Join(plan.Stage, filepath.FromSlash(f.Name)))
		if e != nil {
			return e
		}
		sum := sha256.Sum256(v)
		total += int64(len(v))
		if int64(len(v)) != f.Size || hex.EncodeToString(sum[:]) != f.SHA256 || total > 512<<20 {
			return errors.New("更新文件校验失败")
		}
		if _, ok := data[dst]; ok {
			return errors.New("重复更新路径")
		}
		data[dst] = v
	}
	if h, e := identity(request{PID: plan.PID, Created: plan.Created, Image: plan.Launcher}); e == nil {
		defer syscall.CloseHandle(h)
		r, e := syscall.WaitForSingleObject(h, 15000)
		if e != nil || r != syscall.WAIT_OBJECT_0 {
			return errors.New("请关闭启动器后重试更新，文件尚未替换")
		}
	}
	if err = ensureLauncherClosed(plan.Launcher); err != nil {
		return err
	}
	journalPath := filepath.Join(plan.Stage, "journal.json")
	if previous, e := os.ReadFile(journalPath); e == nil {
		var entries []backupFile
		if e = json.Unmarshal(previous, &entries); e != nil {
			return e
		}
		for _, entry := range entries {
			if _, ok := data[entry.Path]; !ok {
				return errors.New("更新恢复记录路径无效")
			}
			expected := filepath.Join(plan.Stage, "rollback", strings.TrimPrefix(entry.Path, plan.Target+string(os.PathSeparator)))
			if !strings.EqualFold(expected, entry.Copy) {
				return errors.New("更新备份路径无效")
			}
			if entry.Existed {
				old, e := os.ReadFile(entry.Copy)
				if e != nil {
					return e
				}
				if e = atomic(entry.Path, old); e != nil {
					return e
				}
			} else if e = os.Remove(entry.Path); e != nil && !os.IsNotExist(e) {
				return e
			}
		}
		if e = os.Remove(journalPath); e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	backup := filepath.Join(plan.Stage, "rollback")
	if err = os.MkdirAll(backup, 0700); err != nil {
		return err
	}
	var journal []backupFile
	for _, f := range plan.Files {
		dst := filepath.Join(plan.Target, filepath.FromSlash(f.Name))
		copy := filepath.Join(backup, filepath.FromSlash(f.Name))
		old, e := os.ReadFile(dst)
		exists := e == nil
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		if exists {
			if e = atomic(copy, old); e != nil {
				return e
			}
		}
		journal = append(journal, backupFile{dst, copy, exists})
	}
	raw, _ := json.Marshal(journal)
	if err = atomic(filepath.Join(plan.Stage, "journal.json"), raw); err != nil {
		return err
	}
	for written, j := range journal {
		if err = atomic(j.Path, data[j.Path]); err != nil {
			for _, undo := range journal[:written] {
				if undo.Existed {
					v, e := os.ReadFile(undo.Copy)
					if e == nil {
						e = atomic(undo.Path, v)
					}
					if e != nil {
						return errors.New("回滚未完成，请保留更新目录并联系发布者")
					}
				} else {
					os.Remove(undo.Path)
				}
			}
			return fmt.Errorf("替换更新文件失败：%s。请检查文件占用或写入权限：%w", j.Path, err)
		}
	}
	os.Remove(filepath.Join(plan.Stage, "journal.json"))
	os.Remove(filepath.Join(plan.Target, ".flutter-update-pending"))
	cmd := exec.Command(plan.Launcher)
	cmd.Dir = plan.Target
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err = cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
