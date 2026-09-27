package persistence

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"math"
	"strings"

	"github.com/go-sql-driver/mysql"
	"golang.org/x/crypto/scrypt"
)

// AuthenticateOrRegister only creates a missing account. A wrong password on
// an existing account must never reset credentials or create another identity.
func (store *Store) AuthenticateOrRegister(name, legacy string) (Account, error) {
	if !accountPattern.MatchString(name) || !legacyPattern.MatchString(legacy) {
		return Account{}, ErrDenied
	}
	name = strings.ToLower(name)
	var uid uint64
	err := store.DB.QueryRow("SELECT uid FROM accounts WHERE account=?", name).Scan(&uid)
	if err == nil {
		return store.Authenticate(name, legacy)
	}
	if err != sql.ErrNoRows {
		return Account{}, err
	}

	account := Account{Account: name, Profile: make([]byte, 360), Salt: make([]byte, 16), Digest: make([]byte, 32), LegacySalt: make([]byte, 16)}
	// The native client transmits a password hash, not plaintext. Store its
	// salted verifier; do not pretend it is a plaintext password or log it.
	// The unused plaintext verifier stays unpredictable until an explicit reset.
	for _, field := range [][]byte{account.Salt, account.Digest, account.LegacySalt} {
		if _, err = rand.Read(field); err != nil {
			return Account{}, err
		}
	}
	account.LegacyDigest, err = scrypt.Key([]byte(strings.ToLower(legacy)), account.LegacySalt, 32768, 8, 3, 32)
	if err != nil {
		return Account{}, err
	}
	tx, txCancel, err := beginTransaction(store.DB)
	defer txCancel()
	if err != nil {
		return Account{}, err
	}
	defer tx.Rollback()
	// A no-op upsert takes an exclusive lock. INSERT IGNORE would leave
	// concurrent callers upgrading shared locks and could deadlock here.
	if _, err = tx.Exec("INSERT INTO counters(name,value) VALUES('account',0) ON DUPLICATE KEY UPDATE value=value"); err != nil {
		return Account{}, err
	}
	var counter, maximum uint64
	if err = tx.QueryRow("SELECT value FROM counters WHERE name='account' FOR UPDATE").Scan(&counter); err != nil {
		return Account{}, err
	}
	// Recheck after the allocation lock: concurrent first logins must converge
	// on one account, including requests with different passwords/casing.
	err = tx.QueryRow("SELECT uid FROM accounts WHERE account=? FOR UPDATE", name).Scan(&uid)
	if err == nil {
		tx.Rollback()
		return store.Authenticate(name, legacy)
	}
	if err != sql.ErrNoRows {
		return Account{}, err
	}
	if err = tx.QueryRow("SELECT COALESCE(MAX(uid),0) FROM accounts").Scan(&maximum); err != nil {
		return Account{}, err
	}
	if counter < maximum {
		counter = maximum
	}
	if counter == math.MaxUint64 {
		return Account{}, ErrDenied
	}
	account.UID = counter + 1
	if _, err = tx.Exec("UPDATE counters SET value=? WHERE name='account'", account.UID); err != nil {
		return Account{}, err
	}
	if err = insertAccount(tx, account); err != nil {
		tx.Rollback()
		var duplicate *mysql.MySQLError
		if errors.As(err, &duplicate) && duplicate.Number == 1062 {
			return store.Authenticate(name, legacy)
		}
		return Account{}, err
	}
	if err = tx.Commit(); err != nil {
		return Account{}, err
	}
	return store.RoleManager().Snapshot(account.UID)
}
