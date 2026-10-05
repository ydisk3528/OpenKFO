//go:build windows

package bridge

import (
	"encoding/base64"
	"errors"
	"os"
	"strings"
)

func (config Config) certificateBytes(name string) ([]byte, error) {
	if !config.EmbeddedCertificates {
		return os.ReadFile(name)
	}
	name = strings.TrimPrefix(strings.ReplaceAll(name, "\\", "/"), "launcher-files/")
	value, ok := embeddedCertificates[name]
	if !ok {
		return nil, errors.New("embedded certificate unavailable; use matching launcher components")
	}
	return base64.StdEncoding.DecodeString(value)
}
