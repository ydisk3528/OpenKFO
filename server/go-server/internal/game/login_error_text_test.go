package game

import (
	"database/sql"
	"kungfu.local/server/internal/loginerrors"
	"kungfu.local/server/internal/persistence"
	"sync"
	"testing"
	"time"
)

func TestLoginErrorTextFallbackAndConcurrency(t *testing.T) {
	db, err := sql.Open("mysql", "")
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	s := &Server{Hub: &Hub{Store: &persistence.Store{DB: db}}}
	want, _ := loginerrors.Find("server_full")
	if got := s.loginErrorText("server_full"); got != want.Default {
		t.Fatal(got)
	}
	messages := map[string]string{"server_full": "自定义提示"}
	s.loginText.Store(&messages)
	s.loginTextAt = time.Time{} // database failure must preserve the last good text
	if got := s.loginErrorText("server_full"); got != "自定义提示" {
		t.Fatal(got)
	}
	s.loginTextMu.Lock()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := s.loginErrorText("server_full"); got != "自定义提示" {
				t.Error(got)
			}
		}()
	}
	wg.Wait()
	s.loginTextMu.Unlock()
}
