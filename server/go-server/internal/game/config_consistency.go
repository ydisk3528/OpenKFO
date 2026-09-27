package game

import (
	"errors"
	"fmt"
	"kungfu.local/server/internal/persistence"
)

var ErrMapConfigMismatch = errors.New("服务器地图配置未同步，请联系管理员处理，无需重新下载客户端。")

// Empty catalogue is the supported legacy/default configuration.
func (c Config) ValidateStageAccess(a persistence.StageAccess) error {
	if a.ClientHash != "" && a.ClientHash != c.ConfigHash {
		return fmt.Errorf("%w server_config_hash=%s stage_config_hash=%s", ErrMapConfigMismatch, c.ConfigHash, a.ClientHash)
	}
	return nil
}
