package persistence

import (
	"fmt"
	"strings"
	"time"
)

func validateNotice(text string) error {
	if strings.TrimSpace(text) == "" || strings.ContainsRune(text, 0) || len(GBK(text)) > 199 {
		return fmt.Errorf("通知不能为空，最多99个汉字或199个英文字节")
	}
	return nil
}
func (s *Store) AdminNotice(r AdminRequest) (any, error) {
	if len(r.ID) < 1 || len(r.ID) > 100 {
		return nil, fmt.Errorf("通知编号无效")
	}
	if r.Operation == "notice_send" {
		if err := validateNotice(r.Reason); err != nil {
			return nil, err
		}
		_, err := s.DB.Exec("INSERT INTO gm_notices(id,content,state,created) VALUES(?,?,'pending',?) ON DUPLICATE KEY UPDATE id=id", r.ID, r.Reason, time.Now().Unix())
		if err != nil {
			return nil, err
		}
	}
	var text, state string
	var count int
	err := s.DB.QueryRow("SELECT content,state,recipients FROM gm_notices WHERE id=?", r.ID).Scan(&text, &state, &count)
	if err != nil {
		return nil, err
	}
	if r.Operation == "notice_send" && text != r.Reason {
		return nil, fmt.Errorf("本次编号已用于其他通知，请重新发送")
	}
	return map[string]any{"id": r.ID, "state": state, "recipients": count}, nil
}
