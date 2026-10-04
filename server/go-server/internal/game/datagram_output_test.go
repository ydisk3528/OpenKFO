package game

import (
	"kungfu.local/server/internal/tunnel"
	"sync/atomic"
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

func TestDownstreamUDPRecoveryWaitsForClientDrain(t *testing.T) {
	s := &Session{Output: make(chan tunnel.Frame, 8), datagramOutput: make(chan tunnel.Frame, 8), Done: make(chan struct{})}
	defer s.Close()
	s.datagramEnabled.Store(true)
	s.udpDrain.Supported = true
	var ready atomic.Bool
	delivered := make(chan tunnel.Frame, 2)
	go s.writeDatagrams(func(f tunnel.Frame) bool { delivered <- f; return true }, ready.Load)
	s.emit(tunnel.Frame{Op: "udp", Data: []byte{1}})
	select {
	case f := <-s.Output:
		if len(f.Data) != 1 || f.Data[0] != 1 {
			t.Fatal("first packet missing")
		}
	case <-time.After(time.Second):
		t.Fatal("fallback lost")
	}
	ready.Store(true)
	s.emit(tunnel.Frame{Op: "udp", Data: []byte{2}})
	var fence tunnel.Frame
	select {
	case fence = <-s.Output:
	case <-time.After(time.Second):
		t.Fatal("fence missing")
	}
	if fence.Op != "pong" || fence.Kind != "udp-down-drain" || fence.Value == 0 {
		t.Fatal("invalid fence")
	}
	select {
	case <-delivered:
		t.Fatal("UDP resumed before client drain")
	case <-time.After(30 * time.Millisecond):
	}
	s.udpDrain.Ack(fence.Value)
	select {
	case f := <-delivered:
		if f.Data[0] != 2 {
			t.Fatal("wrong packet")
		}
	case <-time.After(time.Second):
		t.Fatal("UDP did not recover")
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
