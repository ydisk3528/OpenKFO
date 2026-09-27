package persistence

// CompleteTutorial promotes only the native novice title. The title byte is
// the client's persisted introduction gate (8A10B0/924010); never downgrade it.
// Called only after the game hub validates the authenticated tutorial room.
func (s *TitleManager) CompleteTutorial(uid uint64) (byte, error) {
	if uid == 0 {
		return 0, ErrDenied
	}
	tx, txCancel, err := beginTransaction(s.store.DB)
	defer txCancel()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var profile []byte
	if err = tx.QueryRow("SELECT profile FROM accounts WHERE uid=? FOR UPDATE", uid).Scan(&profile); err != nil {
		return 0, err
	}
	if len(profile) != 360 {
		return 0, ErrDenied
	}
	if profile[TitleLevelOffset] < 2 {
		profile[TitleLevelOffset] = 2 // roletitle.xml: 天赋少年Ⅰ; UserGuide.xml graduation.
		if _, err = tx.Exec("UPDATE accounts SET profile=? WHERE uid=?", profile, uid); err != nil {
			return 0, err
		}
	}
	return profile[TitleLevelOffset], tx.Commit()
}
