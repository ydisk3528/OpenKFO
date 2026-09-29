package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"kungfu.local/server/internal/desktop"
	"kungfu.local/server/internal/gmversion"
	"os"
	"path/filepath"
)

func run() (any, error) {
	root := flag.String("root", ".", "repository directory")
	localSettings := flag.String("local-settings", "", "local debug server private settings path")
	gmSettings := flag.String("gm-settings", "", "GM launcher settings path (gm-settings.json)")
	flag.Parse()
	absolute, err := filepath.Abs(*root)
	if err != nil {
		return nil, err
	}
	input, err := io.ReadAll(io.LimitReader(os.Stdin, (4<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(input) > 4<<20 {
		return nil, fmt.Errorf("请求过大")
	}
	var request desktop.Request
	if err = json.Unmarshal(input, &request); err != nil {
		return nil, err
	}
	if request.Operation != "gm_version" {
		if err := gmversion.Check(request.GMVersion); err != nil {
			return nil, err
		}
	}
	admin := desktop.New(absolute)
	admin.LocalSettings = *localSettings
	if *gmSettings != "" {
		if settingsPath, err := filepath.Abs(*gmSettings); err == nil {
			admin.GMSettings = settingsPath
		} else {
			admin.GMSettings = *gmSettings
		}
	}
	return admin.Call(request)
}
func main() {
	result, err := run()
	response := map[string]any{"ok": err == nil}
	if err != nil {
		response["error"] = err.Error()
	} else {
		response["result"] = result
	}
	if err = json.NewEncoder(os.Stdout).Encode(response); err != nil {
		os.Exit(1)
	}
}
