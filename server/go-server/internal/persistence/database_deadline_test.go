package persistence

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

func TestDatabaseReadTimeoutLocalDatabase(t *testing.T) {
	dsn := os.Getenv("OPENKFO_DEBUG_DSN")
	if dsn == "" {
		t.Skip("independent debug database required")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil || !strings.HasPrefix(cfg.DBName, "openkfo_debug_") {
		t.Fatal("independent database required")
	}
	cfg.ReadTimeout = 100 * time.Millisecond
	dsn, err = boundedDSN(cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.Ping(); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	var ignored int
	err = db.QueryRow("SELECT SLEEP(1)").Scan(&ignored)
	if err == nil || time.Since(started) > 3*time.Second {
		t.Fatal("database read did not time out", err)
	}
	if err = db.Ping(); err != nil {
		t.Fatal("timed out connection not replaced", err)
	}
}

func TestBoundedDSNPreservesShorterLimits(t *testing.T) {
	for _, input := range []string{"user:password@tcp(localhost:3306)/test", "user:password@tcp(localhost:3306)/test?timeout=1s&readTimeout=1m&writeTimeout=500ms"} {
		out, err := boundedDSN(input)
		if err != nil {
			t.Fatal(err)
		}
		c, err := mysql.ParseDSN(out)
		if err != nil {
			t.Fatal(err)
		}
		if c.Timeout <= 0 || c.Timeout > 5*time.Second || c.ReadTimeout != 5*time.Second || c.WriteTimeout <= 0 || c.WriteTimeout > 5*time.Second {
			t.Fatal("unbounded database I/O")
		}
		if input != "user:password@tcp(localhost:3306)/test" && (c.Timeout != time.Second || c.WriteTimeout != 500*time.Millisecond) {
			t.Fatal("short limit overwritten")
		}
	}
}

type deadlineConnector struct{}

func (deadlineConnector) Connect(context.Context) (driver.Conn, error) { return deadlineConn{}, nil }
func (deadlineConnector) Driver() driver.Driver                        { return deadlineDriver{} }

type deadlineDriver struct{}

func (deadlineDriver) Open(string) (driver.Conn, error) { return deadlineConn{}, nil }

type deadlineConn struct{}

func (deadlineConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unused") }
func (deadlineConn) Close() error                        { return nil }
func (deadlineConn) Begin() (driver.Tx, error)           { return deadlineTx{}, nil }

type deadlineTx struct{}

func (deadlineTx) Commit() error   { return nil }
func (deadlineTx) Rollback() error { return nil }

func TestTransactionDeadlineIncludesPoolWaitAndRollsBack(t *testing.T) {
	db := sql.OpenDB(deadlineConnector{})
	defer db.Close()
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, cancel, err := beginTransactionWithin(db, 20*time.Millisecond)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("pool wait not bounded", err)
	}
	conn.Close()
	tx, cancel, err := beginTransactionWithin(db, 20*time.Millisecond)
	defer cancel()
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	if err = tx.Commit(); err == nil {
		t.Fatal("expired transaction committed")
	}
	// Automatic rollback releases the sole connection for the next request.
	tx, cancel2, err := beginTransactionWithin(db, time.Second)
	defer cancel2()
	if err != nil {
		t.Fatal("expired transaction leaked connection", err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
