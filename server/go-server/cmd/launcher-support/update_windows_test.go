//go:build windows

package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestUpdaterAppliesVerifiedStagedFile(t *testing.T) {
	root := t.TempDir()
	stage := t.TempDir()
	launcher := filepath.Join(root, "launcher.exe")
	exe, err := os.ReadFile(filepath.Join(os.Getenv("WINDIR"), "System32", "whoami.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(launcher, exe, 0700); err != nil {
		t.Fatal(err)
	}
	name := "data/app.so"
	old := []byte("old-logic")
	next := []byte("new-logic")
	dest := filepath.Join(root, filepath.FromSlash(name))
	if err = atomic(dest, old); err != nil {
		t.Fatal(err)
	}
	if err = atomic(filepath.Join(stage, filepath.FromSlash(name)), next); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(next)
	plan := updatePlan{Target: root, Stage: stage, Launcher: launcher, Files: []updateFile{{Name: name, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(next))}}}
	path := filepath.Join(stage, "plan.json")
	raw, _ := json.Marshal(plan)
	os.WriteFile(path, raw, 0600)
	os.WriteFile(filepath.Join(root, ".flutter-update-pending"), []byte(path), 0600)
	if err = applyPlan(path); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dest)
	if string(got) != string(next) {
		t.Fatal("logic file not replaced")
	}
	if _, err = os.Stat(filepath.Join(root, ".flutter-update-pending")); !os.IsNotExist(err) {
		t.Fatal("pending update marker not removed")
	}
	// Reject corrupt staged bytes before any replacement.
	atomic(dest, old)
	atomic(filepath.Join(stage, filepath.FromSlash(name)), []byte("corrupt"))
	if err = applyPlan(path); err == nil {
		t.Fatal("corrupt stage accepted")
	}
	got, _ = os.ReadFile(dest)
	if string(got) != string(old) {
		t.Fatal("live file changed on failed verification")
	}
}

func TestUpdaterDetectsOtherLauncherWithoutChangingFiles(t *testing.T) {
	image, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err = ensureLauncherClosedWithin(image, 10*time.Millisecond); err == nil || !strings.Contains(err.Error(), "PID") {
		t.Fatalf("running launcher was not identified: %v", err)
	}
	if err = ensureLauncherClosed(filepath.Join(t.TempDir(), filepath.Base(image))); err != nil {
		t.Fatalf("unrelated installation blocked: %v", err)
	}
	stage := t.TempDir()
	next := []byte("must-not-install")
	os.WriteFile(filepath.Join(stage, "probe.dat"), next, 0600)
	sum := sha256.Sum256(next)
	plan := updatePlan{Target: filepath.Dir(image), Stage: stage, Launcher: image, Files: []updateFile{{Name: "probe.dat", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(next))}}}
	raw, _ := json.Marshal(plan)
	path := filepath.Join(stage, "plan.json")
	os.WriteFile(path, raw, 0600)
	if err = applyPlan(path); err == nil || !strings.Contains(err.Error(), "仍在运行") {
		t.Fatalf("expected occupancy error: %v", err)
	}
	if _, err = os.Stat(filepath.Join(stage, "journal.json")); !os.IsNotExist(err) {
		t.Fatal("started replacement despite running launcher")
	}
}

func TestUpdaterExitingProcessHelper(t *testing.T) {
	if os.Getenv("OPENKFO_UPDATE_EXIT_TEST") != "1" {
		return
	}
	os.Stdout.WriteString("ready\n")
	time.Sleep(300 * time.Millisecond)
	os.Exit(0)
}

func TestUpdaterWaitsForNewLauncherAfterStalePlan(t *testing.T) {
	// Simulate the reboot recovery path: the current launcher is a new
	// process, unrelated to any PID stored in yesterday's update plan.
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	image := filepath.Join(t.TempDir(), "resume-launcher.exe")
	if err = os.WriteFile(image, b, 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(image, "-test.run=^TestUpdaterExitingProcessHelper$")
	cmd.Env = append(os.Environ(), "OPENKFO_UPDATE_EXIT_TEST=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Wait()
	if line, e := bufio.NewReader(stdout).ReadString('\n'); e != nil || line != "ready\n" {
		t.Fatalf("helper not ready: %q %v", line, e)
	}
	if err = ensureLauncherClosedWithin(image, 2*time.Second); err != nil {
		t.Fatalf("recovering launcher falsely blocked: %v", err)
	}
}

func TestUpdaterRollsBackAndRetriesLockedFile(t *testing.T) {
	root, stage := t.TempDir(), t.TempDir()
	launcher := filepath.Join(root, "launcher.exe")
	exe, err := os.ReadFile(filepath.Join(os.Getenv("WINDIR"), "System32", "whoami.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(launcher, exe, 0700); err != nil {
		t.Fatal(err)
	}
	plan := updatePlan{Target: root, Stage: stage, Launcher: launcher, PID: uint32(os.Getpid()), Created: 1}
	for _, name := range []string{"first.dat", "locked.dat"} {
		if err = os.WriteFile(filepath.Join(root, name), []byte("old"), 0600); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(stage, name), []byte("new"), 0600); err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256([]byte("new"))
		plan.Files = append(plan.Files, updateFile{Name: name, SHA256: hex.EncodeToString(hash[:]), Size: 3})
	}
	p := filepath.Join(stage, "plan.json")
	raw, _ := json.Marshal(plan)
	os.WriteFile(p, raw, 0600)
	os.WriteFile(filepath.Join(root, ".flutter-update-pending"), []byte(p), 0600)
	h, err := syscall.CreateFile(utf(filepath.Join(root, "locked.dat")), syscall.GENERIC_READ, syscall.FILE_SHARE_READ, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	err = applyPlan(p)
	syscall.CloseHandle(h)
	if err == nil {
		t.Fatal("locked destination accepted")
	}
	for _, name := range []string{"first.dat", "locked.dat"} {
		b, e := os.ReadFile(filepath.Join(root, name))
		if e != nil || string(b) != "old" {
			t.Fatal("rollback failed", name, e)
		}
	}
	if _, err = os.Stat(filepath.Join(root, ".flutter-update-pending")); err != nil {
		t.Fatal("lost recovery marker", err)
	}
	if err = applyPlan(p); err != nil {
		t.Fatal("retry failed", err)
	}
	for _, name := range []string{"first.dat", "locked.dat"} {
		b, e := os.ReadFile(filepath.Join(root, name))
		if e != nil || string(b) != "new" {
			t.Fatal("retry incomplete", name, e)
		}
	}
}

func TestPendingRepairPlanRejectsOtherDirectory(t *testing.T) {
	root, stage := t.TempDir(), t.TempDir()
	p := filepath.Join(stage, "plan.json")
	plan := updatePlan{Target: root, Stage: stage}
	raw, _ := json.Marshal(plan)
	os.WriteFile(p, raw, 0600)
	os.WriteFile(filepath.Join(root, ".flutter-update-pending"), []byte(p), 0600)
	if got, err := pendingRepairPlan(root); err != nil || got != p {
		t.Fatal(got, err)
	}
	plan.Target = t.TempDir()
	raw, _ = json.Marshal(plan)
	os.WriteFile(p, raw, 0600)
	if _, err := pendingRepairPlan(root); err == nil {
		t.Fatal("foreign target accepted")
	}
	os.Remove(p)
	if _, err := pendingRepairPlan(root); err == nil {
		t.Fatal("missing stage accepted")
	}
	if _, err := os.Stat(filepath.Join(root, ".flutter-update-pending")); err != nil {
		t.Fatal("removed evidence")
	}
}

func TestCompleteRepairSupersedesMissingStageAndPreservesGame(t *testing.T) {
	root := t.TempDir()
	oldMarker := []byte(filepath.Join(t.TempDir(), "missing-plan.json"))
	os.WriteFile(filepath.Join(root, ".flutter-update-pending"), oldMarker, 0600)
	if err := atomic(filepath.Join(root, "Data", "config.spf2"), []byte("player-game-config")); err != nil {
		t.Fatal(err)
	}
	exe, err := os.ReadFile(filepath.Join(os.Getenv("WINDIR"), "System32", "whoami.exe"))
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for name, data := range map[string][]byte{"启动器.exe": exe, "LauncherSupport.exe": []byte("new-helper"), "data/app.so": []byte("new-ui")} {
		f, e := w.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = f.Write(data); e != nil {
			t.Fatal(e)
		}
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(b.Bytes())
	if _, err = prepareCompleteRepair(root, b.Bytes(), strings.Repeat("0", 64)); err == nil {
		t.Fatal("corrupt payload accepted")
	}
	before, _ := os.ReadFile(filepath.Join(root, ".flutter-update-pending"))
	if !bytes.Equal(before, oldMarker) {
		t.Fatal("failed validation changed pending update")
	}
	p, err := prepareCompleteRepair(root, b.Bytes(), hex.EncodeToString(h[:]))
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(filepath.Dir(p)) // generated temporary staging only
	backups, _ := filepath.Glob(filepath.Join(root, ".flutter-update-pending.before-repair-*"))
	if len(backups) != 1 {
		t.Fatal("old marker not preserved")
	}
	if err = applyPlan(p); err != nil {
		t.Fatal(err)
	}
	game, _ := os.ReadFile(filepath.Join(root, "Data", "config.spf2"))
	if string(game) != "player-game-config" {
		t.Fatal("game files changed")
	}
	ui, _ := os.ReadFile(filepath.Join(root, "data", "app.so"))
	if string(ui) != "new-ui" {
		t.Fatal("new launcher missing")
	}
}
