package bridge

import (
	"bufio"
	"encoding/json"
	"kungfu.local/server/internal/tunnel"
	"net"
	"testing"
	"time"
)

func TestReceiveContinuesWhileControlWriteBlocked(t *testing.T) {
	local, remote := net.Pipe()
	defer remote.Close()
	s := &remoteSession{connection: local, reader: bufio.NewReader(local), encoder: json.NewEncoder(local), done: make(chan struct{}), channels: map[uint32]*nativeChannel{}, traces: map[uint32]*packetTrace{}}
	game, gamePeer := net.Pipe()
	defer gamePeer.Close()
	ch := newNativeChannel(s, 1, game)
	s.channels[1] = ch
	defer s.close()
	finished := make(chan struct{})
	go func() { (&Bridge{}).receive(s); close(finished) }()
	writer := json.NewEncoder(remote)
	remote.SetWriteDeadline(time.Now().Add(time.Second))
	if err := writer.Encode(tunnel.Frame{Op: "pong", Kind: "udp-down-drain", Value: 7}); err != nil {
		t.Fatal(err)
	}
	// Hold the ACK write while a stream of 32 downstream messages arrives.
	for n := 0; n < 32; n++ {
		if err := writer.Encode(tunnel.Frame{Op: "data", Channel: 1, Data: []byte{byte(n)}}); err != nil {
			t.Fatal("TLS receive blocked by control reply", err)
		}
		select {
		case d := <-ch.output:
			if len(d.data) != 1 || d.data[0] != byte(n) {
				t.Fatal("delivery reordered")
			}
		case <-time.After(250 * time.Millisecond):
			t.Fatal("battle data stalled behind ACK")
		}
	}
	s.close()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("receiver failed to close")
	}
}
func TestControlQueueBounded(t *testing.T) {
	s := &remoteSession{done: make(chan struct{}), controlOutput: make(chan tunnel.Frame, 1)}
	s.controlOnce.Do(func() {})
	if !s.enqueueControl(tunnel.Frame{Value: 1}) || s.enqueueControl(tunnel.Frame{Value: 2}) {
		t.Fatal("queue bound")
	}
	close(s.done)
	if s.enqueueControl(tunnel.Frame{}) {
		t.Fatal("closed accepted")
	}
}
