package game

import (
	"bytes"
	"fmt"
	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
	"kungfu.local/server/internal/tunnel"
	"testing"
	"time"
)

func hornInput(kind uint32) []byte {
	n, lo, to := 215, 12, 13
	if kind == 2481 {
		n, lo, to = 257, 50, 56
	}
	p := make([]byte, n)
	text := persistence.GBK("喇叭测试101")
	p[lo] = byte(len(text) + 1)
	copy(p[to:], text)
	return p
}
func TestHornWire(t *testing.T) {
	for k, want := range map[uint32]uint32{2480: 2485, 2486: 2487, 2481: 2482} {
		p := hornInput(k)
		msg, text, e := parseHorn(k, p, 42, "真实玩家")
		if e != nil || msg.ID != want || text != "喇叭测试101" || protocol.ReadUint64(msg.Payload, 0) != 42 || !bytes.Equal(msg.Payload[8:16], persistence.GBK("真实玩家")) {
			t.Fatal(k, msg, text, e)
		}
		for _, bad := range [][]byte{nil, p[:len(p)-1], append(bytes.Clone(p), 0)} {
			if _, _, e := parseHorn(k, bad, 42, "x"); e == nil {
				t.Fatal("bad length")
			}
		}
		p[len(p)-1] = 1
		if _, _, e := parseHorn(k, p, 42, "x"); e == nil {
			t.Fatal("nonzero padding")
		}
	}
	p := hornInput(2481)
	p[51] = 255
	if _, _, e := parseHorn(2481, p, 1, "x"); e == nil {
		t.Fatal("invalid mood style")
	}
}
func hornFixture(n int) (*Hub, *HornManager) {
	h := NewHub(nil, Config{})
	m := h.hornManager()
	for i := 1; i <= n; i++ {
		phase := []string{"lobby", "room", "battle"}[i%3]
		s := &Session{UID: uint64(i), LobbyID: uint32(1 + i%2), GameChannel: 3, Channels: map[uint32]*Channel{3: {ID: 3, Phase: phase}}, Output: make(chan tunnel.Frame, 8), Done: make(chan struct{})}
		h.Sessions[s.UID] = s
		m.join(s)
	}
	return h, m
}
func TestHornOnlineScopeAndBackpressure(t *testing.T) {
	h, m := hornFixture(10)
	msg, txt, _ := parseHorn(2480, hornInput(2480), 1, "sender")
	job := hornJob{sender: hornTarget{h.Sessions[1], 3, 2}, message: msg, text: txt, kind: 2480}
	n, skip := m.broadcast(job)
	if n != 5 || skip != 0 {
		t.Fatal(n, skip)
	}
	for _, s := range h.Sessions {
		if (s.LobbyID == 2) != (len(s.Output) == 1) {
			t.Fatal("wrong channel/room recipient")
		}
		for len(s.Output) > 0 {
			f := <-s.Output
			s.queuedBytes.Add(-int64(len(f.Data) + 128))
		}
	}
	h.removeHornSession(h.Sessions[2])
	h.Sessions[3].Close()
	h.Sessions[4].LoggedOut = true
	h.Sessions[5].GameChannel = 4 // stale login must not receive
	slow := h.Sessions[6]
	for i := 0; i < 4; i++ {
		slow.Output <- tunnel.Frame{}
	}
	job.kind = 2486
	n, skip = m.broadcast(job)
	if n != 5 || skip != 4 {
		t.Fatal(n, skip)
	}
	select {
	case <-slow.Done:
		t.Fatal("horn kicked slow player")
	default:
	}
	if len(slow.Output) != 4 {
		t.Fatal("filled battle reserve")
	}
	m.running = true
	m.jobs = make([]hornJob, hornQueueLimit)
	if m.submit(job) {
		t.Fatal("unbounded queue")
	}
}
func TestHornTenThousand(t *testing.T) {
	h, m := hornFixture(10000)
	msg, _, _ := parseHorn(2486, hornInput(2486), 1, "sender")
	start := time.Now()
	n, skip := m.broadcast(hornJob{sender: hornTarget{h.Sessions[1], 3, 2}, message: msg, kind: 2486})
	if n != 10000 || skip != 0 {
		t.Fatal(n, skip)
	}
	var shared *byte
	for _, s := range h.Sessions {
		f := <-s.Output
		if shared == nil {
			shared = &f.Data[0]
		} else if shared != &f.Data[0] {
			t.Fatal("per-recipient encoding")
		}
	}
	t.Logf("10000 in-memory recipients queued in %s (not a network load test)", time.Since(start))
}
func BenchmarkHornTenThousand(b *testing.B) {
	h, m := hornFixture(10000)
	msg, _, _ := parseHorn(2486, hornInput(2486), 1, "sender")
	job := hornJob{sender: hornTarget{h.Sessions[1], 3, 2}, message: msg, kind: 2486}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		n, _ := m.broadcast(job)
		if n != 10000 {
			b.Fatal(fmt.Sprint(n))
		}
		for _, s := range h.Sessions {
			f := <-s.Output
			s.queuedBytes.Add(-int64(len(f.Data) + 128))
		}
	}
}

func TestHornConcurrentMembership(t *testing.T) {
	h, m := hornFixture(1000)
	msg, _, _ := parseHorn(2486, hornInput(2486), 1, "sender")
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 30; i++ {
			m.broadcast(hornJob{message: msg, kind: 2486})
		}
	}()
	for i := 0; i < 1000; i++ {
		h.Mutex.Lock()
		s := h.Sessions[uint64(1+i%1000)]
		h.removeHornSession(s)
		s.LobbyID = 1 + s.LobbyID%2
		m.join(s)
		h.Mutex.Unlock()
	}
	<-done
}
