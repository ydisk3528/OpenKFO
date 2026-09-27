package persistence

import "database/sql"

func (m *WalletManager) AdjustTickets(uid uint64, mode string, amount uint32, operationID string) (uint32, uint32, error) {
	if (mode != "gift" && mode != "set") || amount > 2147483647 || len(operationID) < 1 || len(operationID) > 128 {
		return 0, 0, ErrDenied
	}
	transaction, transactionCancel, err := beginTransaction(m.store.DB)
	defer transactionCancel()
	if err != nil {
		return 0, 0, err
	}
	defer transaction.Rollback()
	var before uint32
	if err = transaction.QueryRow(`SELECT tickets FROM accounts WHERE uid=? FOR UPDATE`, uid).Scan(&before); err != nil {
		return 0, 0, err
	}
	var oldUID uint64
	var oldMode string
	var oldAmount, oldBefore, oldAfter uint32
	err = transaction.QueryRow(`SELECT uid,mode,amount,before_balance,after_balance FROM wallet_operations WHERE operation_id=?`, operationID).Scan(&oldUID, &oldMode, &oldAmount, &oldBefore, &oldAfter)
	if err == nil {
		if oldUID != uid || mode != oldMode || amount != oldAmount {
			return 0, 0, ErrDenied
		}
		return oldBefore, oldAfter, transaction.Commit()
	}
	if err != sql.ErrNoRows {
		return 0, 0, err
	}
	after := uint64(amount)
	if mode == "gift" {
		after += uint64(before)
	}
	if after > 2147483647 {
		return 0, 0, ErrDenied
	}
	if _, err = transaction.Exec(`UPDATE accounts SET tickets=? WHERE uid=?`, after, uid); err != nil {
		return 0, 0, err
	}
	if _, err = transaction.Exec(`INSERT INTO wallet_operations(operation_id,uid,mode,amount,before_balance,after_balance) VALUES(?,?,?,?,?,?)`, operationID, uid, mode, amount, before, after); err != nil {
		return 0, 0, err
	}
	return before, uint32(after), transaction.Commit()
}
