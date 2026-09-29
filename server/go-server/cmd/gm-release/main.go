// Build a public GM runtime ZIP from the allowlisted files only, then publish via SSH.
package main

import (
	"archive/zip"
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"kungfu.local/server/internal/desktop"
	"kungfu.local/server/internal/releases"
)

func main() {
	kind := flag.String("kind", "gm", "release kind: gm, client or launcher")
	root := flag.String("root", ".", "repository root")
	dir := flag.String("directory", "", "complete GM release directory")
	notes := flag.String("notes", "", "release notes")
	flag.Parse()
	if err := run(*root, *dir, *notes, *kind); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(root, dir, notes, kind string) error {
	if dir == "" || strings.TrimSpace(notes) == "" {
		return fmt.Errorf("directory and notes are required")
	}
	if kind != "gm" && kind != "client" && kind != "launcher" {
		return fmt.Errorf("unsupported release kind")
	}
	var configHash, executableHash string
	var buffer bytes.Buffer
	z := zip.NewWriter(&buffer)
	err := filepath.WalkDir(dir, func(path string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		rel = filepath.ToSlash(rel)
		allowed := rel == "OpenKFO.Updater.exe" || rel == "GM管理器.exe" || rel == "kungfu-desktop-admin.exe" || strings.HasSuffix(strings.ToLower(rel), ".dll") && !strings.Contains(rel, "/") || strings.HasPrefix(rel, "data/")
		if kind == "client" {
			allowed = releases.AllowedClientFile(rel)
			if !allowed {
				return fmt.Errorf("unsupported client file %s", rel)
			}
		}
		if kind == "launcher" {
			allowed = rel == "launcher.exe"
			if !allowed {
				return fmt.Errorf("launcher package only accepts launcher.exe")
			}
		}
		if !allowed {
			return nil
		}
		if e.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink rejected")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if rel == "Data/config.spf2" {
			configHash = releases.Hash(data)
		}
		if rel == "launcher.exe" {
			executableHash = releases.Hash(data)
		}
		f, err := z.Create(rel)
		if err == nil {
			_, err = f.Write(data)
		}
		return err
	})
	if err != nil {
		return err
	}
	if err = z.Close(); err != nil {
		return err
	}
	raw := buffer.Bytes()
	m := releases.Manifest{Kind: kind, ConfigHash: configHash, ExecutableHash: executableHash, Version: time.Now().UTC().Format("20060102T150405Z"), Notes: notes, SHA256: releases.Hash(raw), Size: int64(len(raw))}
	m.Package = m.SHA256 + ".zip"
	result, err := desktop.New(root).Publish(m, raw)
	if err == nil {
		fmt.Println(result)
	}
	return err
}
