package bridge

import (
	"bufio"
	"encoding/json"
	"net"
	"testing"
	"time"

	"kungfu.local/server/internal/tunnel"
)

func TestReloginQueuesOnlyRequestingSessionAndClosesOldSockets(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	native, game := net.Pipe()
	defer game.Close()
	s := &remoteSession{connection: client, reader: bufio.NewReader(client), done: make(chan struct{}), channels: map[uint32]*nativeChannel{1: {Conn: native, stopped: make(chan struct{})}}}
	b := &Bridge{active: s, relogin: make(chan *remoteSession, 1)}
	go b.receive(s)
	server.SetWriteDeadline(time.Now().Add(time.Second))
	if err := json.NewEncoder(server).Encode(tunnel.Frame{Op: "relogin"}); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-b.relogin:
		if got != s {
			t.Fatal("wrong client selected")
		}
	case <-time.After(time.Second):
		t.Fatal("restart request lost")
	}
	game.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := game.Read(make([]byte, 1)); err == nil {
		t.Fatal("old native connection still open")
	} else if e, ok := err.(net.Error); ok && e.Timeout() {
		t.Fatal("old native connection was not closed")
	}
}

func TestSharedClientRoutesByProcessLifetime(t *testing.T) {
	first := &remoteSession{identity: Identity{PID: 101, Created: 10, Image: "same-client"}, done: make(chan struct{})}
	second := &remoteSession{identity: Identity{PID: 102, Created: 20, Image: "same-client"}, done: make(chan struct{})}
	b := &Bridge{Config: Config{SharedClient: true}, sessions: map[uint32]*remoteSession{101: first, 102: second}}
	if b.current(first.identity) != first || b.current(second.identity) != second {
		t.Fatal("windows share a session")
	}
	stale := first.identity
	stale.Created++
	if b.current(stale) != nil {
		t.Fatal("reused PID accepted")
	}
	close(first.done)
	if b.current(first.identity) != nil || b.current(second.identity) != second {
		t.Fatal("closing one window affected another")
	}
}
