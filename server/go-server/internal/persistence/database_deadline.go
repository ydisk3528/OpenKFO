package persistence

import (
	"context"
	"database/sql"
	"time"

	"github.com/go-sql-driver/mysql"
)

const transactionTimeout = 15 * time.Second

// BeginTx's context covers pool acquisition and the entire transaction. The
// caller must cancel after commit/rollback; database/sql rolls back on expiry.
func beginTransaction(db *sql.DB) (*sql.Tx, context.CancelFunc, error) {
	return beginTransactionWithin(db, transactionTimeout)
}

func beginTransactionWithin(db *sql.DB, timeout time.Duration) (*sql.Tx, context.CancelFunc, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	tx, err := db.BeginTx(ctx, nil)
	return tx, cancel, err
}

func boundedDSN(dsn string) (string, error) {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return "", err
	}
	// Keep explicitly shorter operator settings; zero means unbounded.
	for _, limit := range []*time.Duration{&cfg.Timeout, &cfg.ReadTimeout, &cfg.WriteTimeout} {
		if *limit <= 0 || *limit > 5*time.Second {
			*limit = 5 * time.Second
		}
	}
	return cfg.FormatDSN(), nil
}
