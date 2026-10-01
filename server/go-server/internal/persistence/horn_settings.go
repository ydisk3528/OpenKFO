package persistence

import "errors"

var ErrHornDisabled = errors.New("此类喇叭已暂停发送")

type HornSettings struct {
	Revision       uint64 `json:"revision"`
	ChannelEnabled bool   `json:"channel_enabled"`
	RealmEnabled   bool   `json:"realm_enabled"`
	MoodEnabled    bool   `json:"mood_enabled"`
}

func (r HornSettings) Allows(kind uint32) bool {
	switch kind {
	case 2480:
		return r.ChannelEnabled
	case 2486:
		return r.RealmEnabled
	case 2481:
		return r.MoodEnabled
	}
	return false
}
func (s *Store) HornSettings() (HornSettings, error) {
	var r HornSettings
	err := s.DB.QueryRow(`SELECT revision,channel_enabled,realm_enabled,mood_enabled FROM horn_settings WHERE id=1`).Scan(&r.Revision, &r.ChannelEnabled, &r.RealmEnabled, &r.MoodEnabled)
	return r, err
}
func (s *Store) SaveHornSettings(r HornSettings) (HornSettings, error) {
	result, err := s.DB.Exec(`UPDATE horn_settings SET revision=revision+1,channel_enabled=?,realm_enabled=?,mood_enabled=? WHERE id=1 AND revision=?`, r.ChannelEnabled, r.RealmEnabled, r.MoodEnabled, r.Revision)
	if err != nil {
		return r, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return r, err
	}
	if n != 1 {
		return r, errors.New("喇叭配置已变化，请重新读取后保存")
	}
	r.Revision++
	return r, nil
}
