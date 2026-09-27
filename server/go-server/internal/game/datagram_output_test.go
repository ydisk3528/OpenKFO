package game

import (
	"kungfu.local/server/internal/tunnel"
	"testing"
	"time"
)

func TestDatagramOutputDoesNotWaitForReliableWriter(t *testing.T) {
	s := &Session{Output: make(chan tunnel.Frame, 8), datagramOutput: make(chan tunnel.Frame, 8), Done: make(chan struct{})}
	defer s.Close()
	s.datagramEnabled.Store(true)
	delivered := make(chan tunnel.Frame, 1)
	go s.writeDatagrams(func(f tunnel.Frame) bool { delivered <- f; return true })
	s.emit(tunnel.Frame{Op: "data", Data: []byte("blocked reliable writer")})
	s.emit(tunnel.Frame{Op: "udp", Data: []byte("battle")})
	select {
	case f := <-delivered:
		if string(f.Data) != "battle" {
			t.Fatal(f)
		}
	case <-time.After(time.Second):
		t.Fatal("UDP waited for reliable queue")
	}
	if len(s.Output) != 1 {
		t.Fatal("reliable message lost")
	}
}

func TestDatagramFallbackPreservesFrameAndQueueAccounting(t *testing.T) {
	s := &Session{Output: make(chan tunnel.Frame, 8), datagramOutput: make(chan tunnel.Frame, 8), Done: make(chan struct{})}
	defer s.Close()
	s.datagramEnabled.Store(true)
	go s.writeDatagrams(func(tunnel.Frame) bool { return false })
	s.emit(tunnel.Frame{Op: "udp", Port: 123, Data: []byte("battle")})
	select {
	case f := <-s.Output:
		if f.Op != "udp" || f.Port != 123 || string(f.Data) != "battle" {
			t.Fatal(f)
		}
		s.queuedBytes.Add(-int64(len(f.Data) + 128))
		if n := s.queuedBytes.Load(); n != 0 {
			t.Fatal("double accounting", n)
		}
	case <-time.After(time.Second):
		t.Fatal("fallback lost")
	}
}
