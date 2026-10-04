package bridge

import (
	"encoding/json"
	"kungfu.local/server/internal/protocol"
	"kungfu.local/server/internal/tunnel"
	"net"
	"os"
	"syscall"
	"testing"
	"time"
)

type observedUDPWrite struct {
	net.Conn
	started chan struct{}
}

func TestUDPProbeDoesNotEnableNativeTraffic(t *testing.T) {
	p := &clientDatagramPeer{}
	if p.confirmHeartbeat(0) || p.ready() {
		t.Fatal("probe enabled UDP before server confirmation")
	}
	if !p.confirmHeartbeat(1) || !p.ready() {
		t.Fatal("confirmed path not enabled")
	}
	p.mu.Lock()
	p.ack = time.Now().Add(-4 * time.Second)
	p.mu.Unlock()
	if p.confirmHeartbeat(0) || p.ready() {
		t.Fatal("recovery probe bypassed confirmation")
	}
	if !p.confirmHeartbeat(1) || !p.ready() {
		t.Fatal("confirmation did not restore path")
	}
}

func (c *observedUDPWrite) Write(b []byte) (int, error) {
	select {
	case c.started <- struct{}{}:
	default:
	}
	return c.Conn.Write(b)
}

func TestUDPRecoveryDoesNotBypassPendingFallback(t *testing.T) {
	client, reliable := net.Pipe()
	defer reliable.Close()
	observed := &observedUDPWrite{Conn: client, started: make(chan struct{}, 1)}
	receiver, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()
	sender, err := net.DialUDP("udp4", nil, receiver.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	grant, _ := tunnel.NewDatagramGrant(receiver.LocalAddr().(*net.UDPAddr).Port)
	codec, _ := tunnel.NewDatagramCodec(grant, false)
	decode, _ := tunnel.NewDatagramCodec(grant, true)
	peer := &clientDatagramPeer{conn: sender, codec: codec}
	s := &remoteSession{connection: observed, encoder: json.NewEncoder(observed), done: make(chan struct{}), udpTransport: peer}
	s.udpDrain.Supported = true
	defer s.close()
	packet := make([]byte, 24)
	protocol.WriteUint16(packet, 2, 1008)
	if !s.enqueueUDPFallback(tunnel.Frame{Op: "udp", Port: 100, Data: packet}) {
		t.Fatal("first enqueue")
	}
	select {
	case <-observed.started:
	case <-time.After(time.Second):
		t.Fatal("fallback not started")
	}
	peer.mu.Lock()
	peer.ack = time.Now()
	peer.mu.Unlock()
	packet[23] = 1
	if !s.enqueueUDPFallback(tunnel.Frame{Op: "udp", Port: 100, Data: packet}) {
		t.Fatal("second enqueue")
	}
	receiver.SetReadDeadline(time.Now().Add(80 * time.Millisecond))
	buf := make([]byte, 1200)
	if _, err := receiver.Read(buf); err == nil {
		t.Fatal("new UDP bypassed pending TCP write")
	} else if e, ok := err.(net.Error); !ok || !e.Timeout() {
		t.Fatal(err)
	}
	reliable.SetReadDeadline(time.Now().Add(time.Second))
	var first tunnel.Frame
	decoder := json.NewDecoder(reliable)
	if err := decoder.Decode(&first); err != nil {
		t.Fatal(err)
	}
	if first.Data[23] != 0 {
		t.Fatal("first packet overwritten")
	}
	var fence tunnel.Frame
	if err := decoder.Decode(&fence); err != nil {
		t.Fatal(err)
	}
	if fence.Kind != "udp-drain" || fence.Value == 0 {
		t.Fatal("missing drain fence")
	}
	receiver.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	if _, err := receiver.Read(buf); err == nil {
		t.Fatal("resumed before peer acknowledgement")
	}
	s.udpDrain.Ack(fence.Value)
	receiver.SetReadDeadline(time.Now().Add(time.Second))
	n, err := receiver.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	port, data, err := decode.Open(buf[:n])
	if err != nil || port != 100 || data[23] != 1 {
		t.Fatal("recovered UDP not delivered", err)
	}
}

func TestUDPQueueFullKeepsSessionAndNewestInput(t *testing.T) {
	s := &remoteSession{done: make(chan struct{}), udpFallback: make(chan tunnel.Frame, 2)}
	s.udpFallbackOnce.Do(func() {}) // Hold the writer to simulate backpressure.
	for _, b := range []byte{1, 2, 3} {
		if !s.enqueueUDPFallback(tunnel.Frame{Op: "udp", Data: []byte{b}}) {
			t.Fatal("enqueue failed")
		}
	}
	select {
	case <-s.done:
		t.Fatal("queue overflow disconnected player")
	default:
	}
	if (<-s.udpFallback).Data[0] != 2 || (<-s.udpFallback).Data[0] != 3 {
		t.Fatal("did not retain newest UDP in order")
	}
}

func TestUDPReadErrorsAreClassified(t *testing.T) {
	for _, code := range []syscall.Errno{10054, 10061, 10040, 10055} {
		if !retryUDPRead(&net.OpError{Op: "read", Net: "udp", Err: code}) {
			t.Fatal("recoverable error stopped receiver", code)
		}
	}
	if retryUDPRead(net.ErrClosed) || retryUDPRead(syscall.EINVAL) {
		t.Fatal("fatal/closed socket would spin")
	}
}

func TestUDPFallbackDoesNotBlockAndCopiesReadBuffer(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	s := &remoteSession{connection: client, encoder: json.NewEncoder(client), done: make(chan struct{})}
	defer s.close()
	data := []byte{1, 2, 3}
	done := make(chan bool, 1)
	go func() { done <- s.enqueueUDPFallback(tunnel.Frame{Op: "udp", Port: 100, Data: data}) }()
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("enqueue failed")
		}
	case <-time.After(time.Second):
		t.Fatal("enqueue waited for TCP receiver")
	}
	data[0] = 9
	server.SetReadDeadline(time.Now().Add(time.Second))
	var f tunnel.Frame
	if err := json.NewDecoder(server).Decode(&f); err != nil {
		t.Fatal(err)
	}
	if len(f.Data) != 3 || f.Data[0] != 1 || f.Port != 100 {
		t.Fatalf("buffer reused: %+v", f)
	}
}

func TestUDPIdentityCacheRevalidatesExpiredPort(t *testing.T) {
	image, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	sock, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer sock.Close()
	addr := sock.LocalAddr().(*net.UDPAddr)
	cache := newUDPIdentityCache()
	defer cache.close()
	id, err := cache.resolve(addr, image)
	if err != nil {
		t.Fatal(err)
	}
	if id.PID != uint32(os.Getpid()) {
		t.Fatal("wrong owner")
	}
	handle := cache.entries[addr.Port].handle
	if _, err = cache.resolve(addr, image); err != nil || cache.entries[addr.Port].handle != handle {
		t.Fatal("cache not reused", err)
	}
	sock.Close()
	entry := cache.entries[addr.Port]
	entry.expires = time.Now().Add(-time.Second)
	cache.entries[addr.Port] = entry
	if _, err = cache.resolve(addr, image); err == nil {
		t.Fatal("expired socket accepted")
	}
	if len(cache.entries) != 0 {
		t.Fatal("stale handle retained")
	}
}
