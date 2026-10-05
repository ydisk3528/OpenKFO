package game

import (
	"kungfu.local/server/internal/tunnel"
	"sync/atomic"
	"testing"
	"time"
)

func TestOrderedDownstreamRecoversDuringContinuousTraffic(t *testing.T) {
	s := &Session{udpOrdered: true, Output: make(chan tunnel.Frame, 128), datagramOutput: make(chan tunnel.Frame, 128), Done: make(chan struct{})}
	defer s.Close()
	s.datagramEnabled.Store(true)
	var ready atomic.Bool
	delivered := make(chan tunnel.Frame, 128)
	go s.writeDatagrams(func(f tunnel.Frame) bool { delivered <- f; return true }, ready.Load)
	s.emit(tunnel.Frame{Op: "udp", Data: []byte{0}})
	var tcp tunnel.Frame
	select {
	case tcp = <-s.Output:
	case <-time.After(time.Second):
		t.Fatal("fallback missing")
	}
	if tcp.Value != 1 {
		t.Fatal("missing epoch")
	}
	ready.Store(true)
	var receiver tunnel.UDPOrder
	var got []byte
	apply := func(f tunnel.Frame) error { got = append(got, f.Data[0]); return nil }
	for i := 1; i <= 50; i++ {
		s.emit(tunnel.Frame{Op: "udp", Data: []byte{byte(i)}})
		select {
		case f := <-delivered:
			if err := receiver.Receive(f, false, apply); err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("recovered UDP stuck behind ACK")
		}
	}
	if len(got) != 0 {
		t.Fatal("new UDP overtook delayed TCP")
	}
	if err := receiver.Receive(tcp, true, apply); err != nil {
		t.Fatal(err)
	}
	if len(got) != 51 {
		t.Fatal(got)
	}
	for i, v := range got {
		if int(v) != i {
			t.Fatal("reordered", got)
		}
	}
	if len(s.Output) != 0 {
		t.Fatal("unexpected ACK fences")
	}
}

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
	// More traffic must continue over TCP without waiting for this ACK.
	s.emit(tunnel.Frame{Op: "udp", Data: []byte{9}})
	select {
	case f := <-s.Output:
		if len(f.Data) != 1 || f.Data[0] != 9 {
			t.Fatal("fallback did not continue")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("sender blocked waiting for ACK")
	}
	s.udpDrain.Ack(fence.Value)
	s.emit(tunnel.Frame{Op: "udp", Data: []byte{10}})
	select {
	case f := <-s.Output:
		if len(f.Data) != 1 || f.Data[0] != 10 {
			t.Fatal("old ACK covered new traffic")
		}
	case <-time.After(time.Second):
		t.Fatal("fallback missing")
	}
	select {
	case fence = <-s.Output:
		if fence.Kind != "udp-down-drain" {
			t.Fatal("new fence missing")
		}
	case <-time.After(time.Second):
		t.Fatal("new fence missing")
	}
	s.udpDrain.Ack(fence.Value)
	s.emit(tunnel.Frame{Op: "udp", Data: []byte{2}})
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

// Exercise a sustained stream, not just one packet waiting behind a fence.
func TestContinuousFallbackDoesNotPauseOrResumeOutOfOrder(t *testing.T) {
	s := &Session{Output: make(chan tunnel.Frame, 128), datagramOutput: make(chan tunnel.Frame, 128), Done: make(chan struct{})}
	defer s.Close()
	s.datagramEnabled.Store(true)
	s.udpDrain.Supported = true
	var ready atomic.Bool
	delivered := make(chan tunnel.Frame, 128)
	go s.writeDatagrams(func(f tunnel.Frame) bool { delivered <- f; return true }, ready.Load)
	s.emit(tunnel.Frame{Op: "udp", Data: []byte{0}})
	take := func() tunnel.Frame {
		t.Helper()
		select {
		case f := <-s.Output:
			s.queuedBytes.Add(-int64(len(f.Data) + 128))
			return f
		case <-time.After(500 * time.Millisecond):
			t.Fatal("fallback stalled awaiting ACK")
			return tunnel.Frame{}
		}
	}
	if f := take(); f.Op != "udp" {
		t.Fatal("first fallback missing")
	}
	fence := take()
	if fence.Kind != "udp-down-drain" {
		t.Fatal("fence missing")
	}
	ready.Store(true)
	for n := 1; n <= 50; n++ {
		if !s.emit(tunnel.Frame{Op: "udp", Data: []byte{byte(n)}}) {
			t.Fatal("enqueue")
		}
		if f := take(); f.Op != "udp" || f.Data[0] != byte(n) {
			t.Fatal("fallback reordered", n, f)
		}
	}
	select {
	case <-delivered:
		t.Fatal("UDP bypassed unacknowledged TCP stream")
	default:
	}
	// An ACK for the first fence cannot cover the 50 later fallback packets.
	s.udpDrain.Ack(fence.Value)
	s.emit(tunnel.Frame{Op: "udp", Data: []byte{51}})
	if f := take(); f.Op != "udp" || f.Data[0] != 51 {
		t.Fatal("old ACK released newer traffic")
	}
	fence = take()
	if fence.Kind != "udp-down-drain" {
		t.Fatal("replacement fence missing")
	}
	s.udpDrain.Ack(fence.Value)
	s.emit(tunnel.Frame{Op: "udp", Data: []byte{52}})
	select {
	case f := <-delivered:
		if f.Data[0] != 52 {
			t.Fatal("wrong recovery frame")
		}
	case <-time.After(time.Second):
		t.Fatal("safe UDP recovery failed")
	}
}
