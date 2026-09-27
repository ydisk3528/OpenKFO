package persistence

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// TaskRule is explicit server policy; no reward currency is inferred from BaseQuest.
type TaskRule struct {
	ID            uint16   `json:"id"`
	Enabled       bool     `json:"enabled"`
	Next          uint16   `json:"next"`
	Matches       uint32   `json:"matches"`
	MaxCombo      uint32   `json:"max_combo"`
	Counters      []uint32 `json:"counters"`
	Experience    uint32   `json:"experience"`
	Gold          uint32   `json:"gold"`
	RewardCatalog uint32   `json:"reward_catalog,omitempty"`
}
type TaskCatalogueEntry struct {
	ID         uint16 `json:"id"`
	Next       uint16 `json:"next"`
	TitleLevel uint16 `json:"title_level"`
	Enabled    bool   `json:"enabled"`
}
type TaskRules struct {
	Extended   *ExtendedTaskRules   `json:"extended,omitempty"`
	ClientHash string               `json:"client_hash,omitempty"`
	Catalogue  []TaskCatalogueEntry `json:"catalogue,omitempty"`
	Enabled    bool                 `json:"enabled"`
	Tasks      []TaskRule           `json:"tasks"`
}
type TaskSettings struct {
	Revision uint64    `json:"revision"`
	Rules    TaskRules `json:"rules"`
}

func (a TaskSettings) Validate() error {
	if a.Rules.Extended != nil {
		if err := a.Rules.Extended.Validate(); err != nil {
			return err
		}
	}
	if a.Rules.ClientHash != "" || len(a.Rules.Catalogue) > 0 {
		hash, e := hex.DecodeString(a.Rules.ClientHash)
		if e != nil || len(hash) != 32 || a.Rules.ClientHash != strings.ToLower(a.Rules.ClientHash) || len(a.Rules.Catalogue) == 0 || len(a.Rules.Catalogue) > 512 {
			return fmt.Errorf("任务目录必须绑定有效客户端SHA256")
		}
		seen := map[uint16]bool{}
		for _, r := range a.Rules.Catalogue {
			if r.ID == 0 || seen[r.ID] || r.TitleLevel > 255 {
				return fmt.Errorf("任务目录编号或称号等级无效")
			}
			seen[r.ID] = true
		}
		for _, r := range a.Rules.Catalogue {
			if r.Next != 0 && (!seen[r.Next] || r.Next == r.ID) {
				return fmt.Errorf("任务目录后续编号无效")
			}
		}
	}

	if len(a.Rules.Tasks) > 512 || (a.Rules.Enabled && len(a.Rules.Tasks) == 0) {
		return fmt.Errorf("任务配置需要1至512条规则")
	}
	byID := map[uint16]TaskRule{}
	for _, r := range a.Rules.Tasks {
		if _, ok := byID[r.ID]; ok || r.ID == 0 || len(r.Counters) != 29 {
			return fmt.Errorf("任务ID必须非零且唯一，统计条件必须29项")
		}
		if r.Experience > 0x7fffffff || r.Gold > 0x7fffffff || r.Matches > 0x7fffffff || r.MaxCombo > 0x7fffffff {
			return fmt.Errorf("任务数值超过有符号32位范围")
		}
		hasCondition := r.Matches != 0 || r.MaxCombo != 0
		for _, v := range r.Counters {
			if v > 0x7fffffff {
				return fmt.Errorf("任务统计条件超出范围")
			}
			hasCondition = hasCondition || v != 0
		}
		if r.Enabled && !hasCondition {
			return fmt.Errorf("启用的任务必须有完成条件")
		}
		byID[r.ID] = r
	}
	for _, r := range a.Rules.Tasks {
		visited := map[uint16]bool{}
		for id := r.ID; id != 0; {
			next, ok := byID[id]
			if !ok || visited[id] {
				return fmt.Errorf("任务后续链存在循环或缺失ID")
			}
			visited[id] = true
			id = next.Next
		}
	}
	return nil
}

func (s *TaskManager) TaskSettings() (TaskSettings, error) {
	a := TaskSettings{}
	var data []byte
	err := s.store.DB.QueryRow("SELECT revision,rules FROM task_rules WHERE id=1").Scan(&a.Revision, &data)
	if err == sql.ErrNoRows {
		return a, nil
	}
	if err != nil {
		return a, err
	}
	if err = json.Unmarshal(data, &a.Rules); err != nil {
		return a, err
	}
	return a, a.Validate()
}
func (s *TaskManager) SaveTaskSettings(a TaskSettings) (TaskSettings, error) {
	if err := a.Validate(); err != nil {
		return TaskSettings{}, err
	}
	if a.Rules.Tasks == nil {
		a.Rules.Tasks = []TaskRule{}
	}
	data, err := json.Marshal(a.Rules)
	if err != nil {
		return TaskSettings{}, err
	}
	tx, txCancel, err := beginTransaction(s.store.DB)
	defer txCancel()
	if err != nil {
		return TaskSettings{}, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT IGNORE INTO task_rules(id,revision,rules) VALUES(1,0,'{\"enabled\":false,\"tasks\":[]}')"); err != nil {
		return TaskSettings{}, err
	}
	var revision uint64
	var before []byte
	if err = tx.QueryRow("SELECT revision,rules FROM task_rules WHERE id=1 FOR UPDATE").Scan(&revision, &before); err != nil {
		return TaskSettings{}, err
	}
	var old TaskRules
	if err = json.Unmarshal(before, &old); err != nil {
		return TaskSettings{}, err
	}
	if old.ClientHash != "" && a.Rules.ClientHash == "" {
		return TaskSettings{}, fmt.Errorf("任务目录不能由旧管理器清除，请更新后重新读取")
	}
	if old.Extended != nil && a.Rules.Extended == nil {
		return TaskSettings{}, fmt.Errorf("每日/新手配置不能由旧管理器清除，请更新后重新读取")
	}
	if revision != a.Revision {
		return TaskSettings{}, fmt.Errorf("任务配置已被修改，请重新读取后保存")
	}
	if _, err = tx.Exec("UPDATE task_rules SET revision=revision+1,rules=? WHERE id=1", data); err != nil {
		return TaskSettings{}, err
	}
	if _, err = tx.Exec("INSERT INTO task_rules_audit(revision,before_data,after_data) VALUES(?,?,?)", revision+1, before, data); err != nil {
		return TaskSettings{}, err
	}
	if err = tx.Commit(); err != nil {
		return TaskSettings{}, err
	}
	a.Revision++
	return a, nil
}

// 6030 may raise native profile+123 using the local template. Never send it
// unless the matching template exists and cannot exceed the persisted title.
func (r TaskRules) CanNotifyCompletion(hash string, key uint16, title byte) bool {
	if hash == "" || hash != r.ClientHash || (TaskSettings{Rules: r}).Validate() != nil {
		return false
	}
	for _, entry := range r.Catalogue {
		if entry.ID == key {
			return entry.Enabled && entry.TitleLevel <= uint16(title)
		}
	}
	return false
}
