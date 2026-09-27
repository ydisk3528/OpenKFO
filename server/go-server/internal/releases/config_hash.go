package releases

import (
	"encoding/json"
	"os"
)

// ActiveConfigHash uses the same release precedence as the game server.
func ActiveConfigHash(configPath, updatesDir string) (string, error) {
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return "", err
	}
	var c struct {
		Hash              string `json:"config_hash"`
		ReleaseVersionURL string `json:"release_version_url"`
	}
	if err = json.Unmarshal(raw, &c); err != nil {
		return "", err
	}
	// An OSS-managed server uses its synchronized configuration; legacy
	// download manifests must not override the database's configuration hash.
	if c.ReleaseVersionURL != "" {
		return c.Hash, nil
	}
	if updatesDir != "" {
		m, err := Load(updatesDir, "client")
		if os.IsNotExist(err) {
			m, err = Load(updatesDir, "weapons")
		}
		if err == nil {
			return m.ConfigHash, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
	}
	return c.Hash, nil
}
