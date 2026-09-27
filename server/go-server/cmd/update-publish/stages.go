package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"kungfu.local/server/internal/desktop"
	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/releases"
)

func releaseConfig(raw []byte, path string) error {
	z, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return err
	}
	for _, f := range z.File {
		if strings.EqualFold(strings.ReplaceAll(f.Name, "\\", "/"), "Data/config.spf2") {
			r, err := f.Open()
			if err != nil {
				return err
			}
			defer r.Close()
			data, err := io.ReadAll(io.LimitReader(r, releases.MaxPackage+1))
			if err != nil {
				return err
			}
			if len(data) > releases.MaxPackage {
				return fmt.Errorf("config exceeds size limit")
			}
			return os.WriteFile(path, data, 0600)
		}
	}
	return fmt.Errorf("发布包缺少 Data/config.spf2，不能核对地图版本")
}

// sudo does not preserve the service's database environment. Read it from
// the running service, never duplicate credentials in a public manifest.
func serviceDSN() (string, error) {
	if dsn := os.Getenv("KK_MYSQL_DSN"); dsn != "" {
		return dsn, nil
	}
	pid, err := exec.Command("systemctl", "show", "kungfu-go", "--property=MainPID", "--value").Output()
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(filepath.Join("/proc", strings.TrimSpace(string(pid)), "environ"))
	if err != nil {
		return "", fmt.Errorf("无法读取运行服务的数据库环境，发布已取消")
	}
	for _, value := range strings.Split(string(raw), "\x00") {
		if strings.HasPrefix(value, "KK_MYSQL_DSN=") {
			return strings.TrimPrefix(value, "KK_MYSQL_DSN="), nil
		}
	}
	return "", fmt.Errorf("服务未设置数据库环境，发布已取消")
}

func prepareStages(dir string, m releases.Manifest, raw []byte) (*persistence.Store, *persistence.StageRebindTransaction, error) {
	previous, err := releases.Load(dir, "client")
	if os.IsNotExist(err) {
		previous, err = releases.Load(dir, "weapons")
	}
	if err != nil {
		return nil, nil, fmt.Errorf("无法核对原客户端版本，发布已取消：%w", err)
	}
	if previous.ConfigHash == m.ConfigHash {
		return nil, nil, nil
	}
	oldRaw, err := os.ReadFile(filepath.Join(dir, previous.Package))
	if err != nil {
		return nil, nil, err
	}
	if err = releases.ValidatePackage(previous, oldRaw); err != nil {
		return nil, nil, err
	}
	tmp, err := os.MkdirTemp("", "stage-publish-")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(tmp)
	oldPath, newPath := filepath.Join(tmp, "old.spf2"), filepath.Join(tmp, "new.spf2")
	if err = releaseConfig(oldRaw, oldPath); err != nil {
		return nil, nil, err
	}
	if err = releaseConfig(raw, newPath); err != nil {
		return nil, nil, err
	}
	if err = desktop.VerifyStageCompatibility(oldPath, newPath); err != nil {
		return nil, nil, err
	}
	dsn, err := serviceDSN()
	if err != nil {
		return nil, nil, err
	}
	store, err := persistence.OpenExisting(dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("无法连接配置数据库，发布已取消")
	}
	tx, err := store.BeginStageRebind(previous.ConfigHash, m.ConfigHash)
	if err != nil {
		store.DB.Close()
		return nil, nil, err
	}
	return store, tx, nil
}
