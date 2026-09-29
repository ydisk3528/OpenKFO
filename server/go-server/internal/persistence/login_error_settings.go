package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"kungfu.local/server/internal/loginerrors"
	"strings"
)

type LoginErrorSettings struct {
	Revision uint64              `json:"revision"`
	Messages map[string]string   `json:"messages"`
	Catalog  []loginerrors.Entry `json:"catalog,omitempty"`
}

func (a LoginErrorSettings) Validate() error {
	for code, msg := range a.Messages {
		e, ok := loginerrors.Find(code)
		if !ok || !e.Editable {
			return fmt.Errorf("不支持配置错误码：%s", code)
		}
		if !loginerrors.ValidMessage(msg) {
			return fmt.Errorf("%s 提示最多300字，不允许控制字符", code)
		}
	}
	return nil
}
func (s *Store) LoginErrorSettings() (LoginErrorSettings, error) {
	return s.LoginErrorSettingsContext(context.Background())
}
func (s *Store) LoginErrorSettingsContext(ctx context.Context) (LoginErrorSettings, error) {
	a := LoginErrorSettings{Messages: map[string]string{}, Catalog: loginerrors.Catalog()}
	var raw []byte
	err := s.DB.QueryRowContext(ctx, "SELECT revision,rules FROM login_error_rules WHERE id=1").Scan(&a.Revision, &raw)
	if err == sql.ErrNoRows {
		return a, nil
	}
	if err != nil {
		return a, err
	}
	if err = json.Unmarshal(raw, &a.Messages); err != nil {
		return a, err
	}
	return a, a.Validate()
}
func (s *Store) SaveLoginErrorSettings(a LoginErrorSettings) (LoginErrorSettings, error) {
	if err := a.Validate(); err != nil {
		return a, err
	}
	a.Catalog = loginerrors.Catalog()
	if a.Messages == nil {
		a.Messages = map[string]string{}
	}
	for code, msg := range a.Messages {
		if strings.TrimSpace(msg) == "" {
			delete(a.Messages, code)
		}
	}
	raw, err := json.Marshal(a.Messages)
	if err != nil {
		return a, err
	}
	tx, cancel, err := beginTransaction(s.DB)
	defer cancel()
	if err != nil {
		return a, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT IGNORE INTO login_error_rules(id,revision,rules) VALUES(1,0,'{}')"); err != nil {
		return a, err
	}
	var revision uint64
	var before []byte
	if err = tx.QueryRow("SELECT revision,rules FROM login_error_rules WHERE id=1 FOR UPDATE").Scan(&revision, &before); err != nil {
		return a, err
	}
	if revision != a.Revision {
		return a, fmt.Errorf("登录提示配置已被修改，请重新读取后保存")
	}
	if _, err = tx.Exec("UPDATE login_error_rules SET revision=revision+1,rules=? WHERE id=1", raw); err != nil {
		return a, err
	}
	if _, err = tx.Exec("INSERT INTO login_error_rules_audit(revision,before_data,after_data) VALUES(?,?,?)", revision+1, before, raw); err != nil {
		return a, err
	}
	if err = tx.Commit(); err != nil {
		return a, err
	}
	a.Revision++
	return a, nil
}
