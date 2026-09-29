package persistence

import (
	"database/sql"
	"github.com/go-sql-driver/mysql"
	"os"
	"strings"
	"testing"
)

func TestLoginErrorValidation(t *testing.T) {
	for _, m := range []map[string]string{{"invented": "test"}, {"logout_failed": "test"}, {"server_full": strings.Repeat("字", 301)}, {"server_full": "bad\x00"}} {
		if (LoginErrorSettings{Messages: m}).Validate() == nil {
			t.Fatal("invalid override accepted")
		}
	}
	if (LoginErrorSettings{Messages: map[string]string{"server_full": "稍后再来"}}).Validate() != nil {
		t.Fatal("valid override rejected")
	}
}
func TestLoginErrorSettingsDatabase(t *testing.T) {
	dsn := os.Getenv("OPENKFO_DEBUG_DSN")
	if dsn == "" {
		t.Skip("isolated DB required")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil || !strings.HasPrefix(cfg.DBName, "openkfo_debug_") {
		t.Fatal("unsafe database")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	for _, q := range []string{"CREATE TEMPORARY TABLE login_error_rules(id INT PRIMARY KEY,revision BIGINT,rules MEDIUMBLOB) ENGINE=InnoDB", "CREATE TEMPORARY TABLE login_error_rules_audit(revision BIGINT PRIMARY KEY,before_data MEDIUMBLOB,after_data MEDIUMBLOB) ENGINE=InnoDB"} {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	s := &Store{DB: db}
	a, err := s.LoginErrorSettings()
	if err != nil || len(a.Catalog) < 12 {
		t.Fatal(a, err)
	}
	a.Messages["server_full"] = "满员，请稍后重试"
	b, err := s.SaveLoginErrorSettings(a)
	if err != nil || b.Revision != 1 {
		t.Fatal(b, err)
	}
	if _, err = s.SaveLoginErrorSettings(a); err == nil {
		t.Fatal("lost update accepted")
	}
	// Another Store simulates a running server reading a GM process's commit.
	c, err := (&Store{DB: db}).LoginErrorSettings()
	if err != nil || c.Messages["server_full"] != a.Messages["server_full"] {
		t.Fatal(c, err)
	}
	c.Messages["server_full"] = "  "
	if _, err = s.SaveLoginErrorSettings(c); err != nil {
		t.Fatal(err)
	}
	c, err = s.LoginErrorSettings()
	if err != nil || len(c.Messages) != 0 || c.Revision != 2 {
		t.Fatal(c, err)
	}
	var count int
	if err = db.QueryRow("SELECT COUNT(*) FROM login_error_rules_audit").Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
}
