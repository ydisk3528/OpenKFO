package persistence

// The progress row's (uid, task_key, cycle) primary key is the entitlement.
// State 3 is its durable receipt; account, inventory and state commit together.
// Request pointers, claimed state and trailing native bytes are never inputs.
func (s *TaskManager) ClaimExtendedTask(uid uint64, hash string, action uint32, key uint16, growth RewardRules) (r TaskAwards, err error) {
	kind := "daily"
	if action == 6312 {
		kind = "newbie"
	} else if action != 6311 {
		return r, ErrDenied
	}
	if uid == 0 || key == 0 || hash == "" {
		return r, ErrDenied
	}
	if err = growth.Validate(); err != nil {
		return r, err
	}
	tx, txCancel, err := beginTransaction(s.store.DB)
	defer txCancel()
	if err != nil {
		return r, err
	}
	defer tx.Rollback()
	states, err := extendedTasksTx(tx, uid, hash)
	if err != nil {
		return r, err
	}
	var task *ExtendedTaskState
	for i := range states {
		if states[i].Key == key && states[i].Snapshot.Rule.Kind == kind {
			task = &states[i]
			break
		}
	}
	if task != nil && task.State == 3 {
		// extendedTasksTx validated owner, client hash, kind and current cycle.
		// Never recreate deleted rewards or rerun growth on an acknowledged claim.
		r.AlreadyClaimed = true
		r.Keys = []uint16{task.Key}
		return r, tx.Commit()
	}
	if task == nil || task.State != 4 || !extendedTaskConditionsMet(*task) {
		return r, ErrDenied
	}
	if err = tx.QueryRow("SELECT profile,gold FROM accounts WHERE uid=?", uid).Scan(&r.Profile, &r.GoldBalance); err != nil {
		return r, err
	}
	r.Experience, r.Gold = task.Snapshot.Rule.Experience, task.Snapshot.Rule.Gold
	r.GoldBalance, r.Items, err = (RewardManager{}).GrantProgressItems(tx, uid, r.Profile, uint64(r.GoldBalance), r.Experience, r.Gold, growth)
	if err != nil {
		return TaskAwards{}, err
	}
	if task.Snapshot.Rule.RewardCatalog != 0 {
		item, e := (RewardManager{}).GrantItem(tx, uid, task.Snapshot.Rule.RewardCatalog)
		if e != nil {
			return TaskAwards{}, e
		}
		r.Items = append(r.Items, item)
	}
	if _, err = tx.Exec("UPDATE accounts SET profile=?,gold=? WHERE uid=?", r.Profile, r.GoldBalance, uid); err != nil {
		return TaskAwards{}, err
	}
	if _, err = tx.Exec("UPDATE extended_task_progress SET state=3 WHERE uid=? AND task_key=? AND cycle=?", uid, task.Key, task.Cycle); err != nil {
		return TaskAwards{}, err
	}
	r.Keys = []uint16{task.Key}
	if err = tx.Commit(); err != nil {
		return TaskAwards{}, err
	}
	return r, nil
}

func extendedTaskConditionsMet(s ExtendedTaskState) bool {
	if len(s.Snapshot.Template.Conditions) != 3 {
		return false
	}
	hasCondition := false
	for i, c := range s.Snapshot.Template.Conditions {
		if c.Required == 0 {
			continue
		}
		hasCondition = true
		if c.Event == "" || !validExtendedTaskEvent(c.Event) || s.Counts[i] < uint32(c.Required) {
			return false
		}
	}
	return hasCondition
}
