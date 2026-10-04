package bridge

import (
	"bufio"
	"encoding/json"
	"io"
	"kungfu.local/server/internal/tunnel"
	"net"
	"testing"
	"time"
)

func TestNativeSlowChannelDoesNotBlockReceiveAndCloseDrains(t *testing.T) {
	client, server := net.Pipe()
	a, slow := net.Pipe()
	b, fast := net.Pipe()
	defer server.Close()
	defer slow.Close()
	defer fast.Close()
	s := &remoteSession{connection: client, reader: bufio.NewReader(client), done: make(chan struct{}), channels: map[uint32]*nativeChannel{}}
	s.channels[1] = newNativeChannel(s, 1, a)
	s.channels[2] = newNativeChannel(s, 2, b)
	defer s.close()
	for _, c := range s.channels {
		go c.writeLoop()
	}
	go (&Bridge{}).receive(s)
	server.SetWriteDeadline(time.Now().Add(time.Second))
	e := json.NewEncoder(server)
	for _, f := range []tunnel.Frame{{Op: "data", Channel: 1, Data: []byte("blocked")}, {Op: "data", Channel: 2, Data: []byte("one")}, {Op: "data", Channel: 2, Data: []byte("two")}, {Op: "close", Channel: 2}} {
		if err := e.Encode(f); err != nil {
			t.Fatal(err)
		}
	}
	fast.SetReadDeadline(time.Now().Add(time.Second))
	data, err := io.ReadAll(fast)
	if err != nil || string(data) != "onetwo" {
		t.Fatalf("order/drain/isolated receive: %q %v", data, err)
	}
	select {
	case <-s.done:
		t.Fatal("slow channel closed session early")
	default:
	}
	// Bound queued bytes even when the game never reads its socket.
	c := s.channels[1]
	if c.enqueue(nativeDelivery{data: make([]byte, 2*1024*1024), readAt: time.Now()}) {
		t.Fatal("unbounded queue")
	}
}
