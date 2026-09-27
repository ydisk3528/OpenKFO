package game

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/tunnel"
	"strings"
	"testing"
	"time"
)

type delayedExpiryDriver struct{}
type delayedExpiryConn struct{ snapshotConn }

var expiryEntered, expiryRelease chan struct{}

func (delayedExpiryDriver) Open(string) (driver.Conn, error) { return delayedExpiryConn{}, nil }
func (c delayedExpiryConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	if strings.HasPrefix(q, "SELECT instance FROM inventory_expirations") {
		close(expiryEntered)
		<-expiryRelease
		return &snapshotRows{columns: []string{"instance"}}, nil
	}
	return c.snapshotConn.QueryContext(ctx, q, args)
}
func init() { sql.Register("delayed-expiry-refresh", delayedExpiryDriver{}) }

func TestBackgroundInventoryQueryDoesNotBlockOtherPlayers(t *testing.T) {
	h, s, peer, _ := waitingRoomFixture()
	db, e := sql.Open("delayed-expiry-refresh", "")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	h.Store = &persistence.Store{DB: db}
	s.StageViewReady = false
	s.MailDirty = false
	expiryEntered, expiryRelease = make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- h.RefreshExpiredInventory(s) }()
	select {
	case <-expiryEntered:
	case <-time.After(time.Second):
		t.Fatal("query not entered")
	}
	handled := make(chan error, 1)
	go func() { handled <- h.Handle(peer, tunnel.Frame{Op: "ping"}) }()
	select {
	case e := <-handled:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		close(expiryRelease)
		<-done
		t.Fatal("slow DB blocked gameplay")
	}
	h.Mutex.Lock()
	s.Room.Stage = "battle"
	h.Mutex.Unlock()
	close(expiryRelease)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if len(s.Output) != 0 || s.Room.Stage != "battle" {
		t.Fatal("stale refresh touched battle")
	}
}
