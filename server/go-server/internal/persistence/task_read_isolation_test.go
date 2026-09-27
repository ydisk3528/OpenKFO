package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/go-sql-driver/mysql"
	"os"
	"strings"
	"testing"
	"time"
)

// Requires a fresh disposable database, never modifies the real game schema.
func TestTaskListDoesNotWaitForWriterLocalDatabase(t *testing.T) {
	dsn := os.Getenv("OPENKFO_TASK_READ_DSN")
	if dsn == "" {
		t.Skip("fresh debug database required")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil || !strings.HasPrefix(cfg.DBName, "openkfo_debug_task_reads_") {
		t.Fatal("unsafe database")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := db.Exec(q, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec("CREATE TABLE accounts(uid BIGINT PRIMARY KEY) ENGINE=InnoDB")
	exec("CREATE TABLE task_rules(id INT PRIMARY KEY,revision BIGINT,rules MEDIUMBLOB) ENGINE=InnoDB")
	exec("CREATE TABLE extended_task_progress(uid BIGINT,task_key SMALLINT,cycle VARCHAR(10),state TINYINT,rule_revision BIGINT,rule_data MEDIUMBLOB,counts BINARY(12),PRIMARY KEY(uid,task_key,cycle)) ENGINE=InnoDB")
	exec("INSERT INTO accounts VALUES(1)")
	data, err := json.Marshal(TaskRules{Extended: extendedTaskFixture()})
	if err != nil {
		t.Fatal(err)
	}
	exec("INSERT INTO task_rules VALUES(1,1,?)", data)
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var uid uint64
	if err = tx.QueryRow("SELECT uid FROM accounts WHERE uid=1 FOR UPDATE").Scan(&uid); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("UPDATE task_rules SET rules='uncommitted-invalid-config' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	store := &Store{DB: db}
	done := make(chan error, 1)
	go func() {
		states, e := store.TaskManager().ExtendedTasks(1, strings.Repeat("a", 64))
		if e == nil && len(states) != 1 {
			e = ErrDenied
		}
		done <- e
	}()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		tx.Rollback()
		<-done
		t.Fatal("display query waited for writer")
	}
	// The mutation helper must still lock, unlike the display snapshot.
	writer, e := db.BeginTx(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer writer.Rollback()
	mutation := make(chan error, 1)
	go func() { _, e := extendedTasksTx(writer, 1, strings.Repeat("a", 64)); mutation <- e }()
	select {
	case e := <-mutation:
		t.Fatalf("mutation bypassed held account lock: %v", e)
	case <-time.After(100 * time.Millisecond):
	}
	if e = tx.Rollback(); e != nil {
		t.Fatal(e)
	}
	select {
	case e := <-mutation:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("mutation did not resume after writer rollback")
	}
}
