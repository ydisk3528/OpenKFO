package game

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
	"kungfu.local/server/internal/tunnel"
	"sync"
	"testing"
	"time"
)

type slowListConnector struct{ entered, release chan struct{} }
type slowListConn struct {
	snapshotConn
	gate slowListConnector
}

func (c slowListConnector) Connect(context.Context) (driver.Conn, error) {
	return slowListConn{gate: c}, nil
}
func (c slowListConnector) Driver() driver.Driver { return snapshotDriver{} }
func (c slowListConn) QueryContext(ctx context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
	close(c.gate.entered)
	select {
	case <-c.gate.release:
		return &snapshotRows{columns: []string{"id", "data", "state"}}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func TestListReadsReleaseStateAndKeepNewMailPending(t *testing.T) {
	for _, kind := range []string{"friends", "mail", "new-mail"} {
		t.Run(kind, func(t *testing.T) {
			h, other, _, s := combatFixture()
			s.game().Phase = "lobby"
			gate := slowListConnector{make(chan struct{}), make(chan struct{})}
			db := sql.OpenDB(gate)
			defer db.Close()
			h.Store = &persistence.Store{DB: db}
			var once sync.Once
			release := func() { once.Do(func() { close(gate.release) }) }
			defer release()
			s.MailDirty = true
			done := make(chan error, 1)
			go func() {
				h.lockState()
				h.scopeSession(s)
				defer h.unlockState()
				if kind == "friends" {
					done <- h.friendSnapshot(s)
				} else {
					done <- h.mail(s, protocol.Message{ID: 1300})
				}
			}()
			select {
			case <-gate.entered:
			case <-time.After(time.Second):
				t.Fatal("no query")
			}
			active := make(chan error, 1)
			go func() { active <- h.Handle(other, tunnel.Frame{Op: "ping"}) }()
			select {
			case err := <-active:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("list read blocked other player")
			}
			if kind == "new-mail" {
				h.lockState()
				s.mailRevision++
				s.MailDirty = true
				h.unlockState()
			}
			release()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "friends":
				roomOutputs(t, s, 8020)
			case "mail":
				roomOutputs(t, s, 1310)
				if s.MailDirty {
					t.Fatal("delivered list not marked clean")
				}
			case "new-mail":
				roomOutputs(t, s)
				if !s.MailDirty {
					t.Fatal("concurrent delivery lost pending refresh")
				}
			}
		})
	}
}
