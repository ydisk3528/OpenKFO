package game

import (
	"crypto/tls"
	"kungfu.local/server/internal/protocol"
	"kungfu.local/server/internal/tunnel"
	"net"
	"testing"
	"time"
)

func TestUDPBusyRoomDoesNotBlockOtherPeer(t *testing.T) {
	h := NewHub(nil, Config{})
	server := NewServer(h, tls.Certificate{})
	closeUDP, err := server.ListenDatagrams("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer closeUDP()
	room := &Room{ID: 1, Stage: "battle", Members: map[uint64]*Member{}}
	var sockets []*net.UDPConn
	var codecs []*tunnel.DatagramCodec
	for i := 0; i < 2; i++ {
		s := &Session{UID: uint64(i + 1), Room: room, Done: make(chan struct{}), Channels: map[uint32]*Channel{}}
		h.Mutex.Lock()
		h.Sessions[s.UID] = s
		h.Mutex.Unlock()
		defer s.Close()
		grant, peer := server.registerDatagramPeer(s)
		defer server.unregisterDatagramPeer(peer)
		codec, _ := tunnel.NewDatagramCodec(grant, false)
		sock, err := net.DialUDP("udp", nil, server.udp.LocalAddr().(*net.UDPAddr))
		if err != nil {
			t.Fatal(err)
		}
		defer sock.Close()
		sock.SetDeadline(time.Now().Add(2 * time.Second))
		hello, _ := codec.Seal(0, []byte{1})
		sock.Write(hello)
		buf := make([]byte, 1200)
		if _, err := sock.Read(buf); err != nil {
			t.Fatal(err)
		}
		sockets = append(sockets, sock)
		codecs = append(codecs, codec)
	}
	room.combatMutex.Lock()
	defer room.combatMutex.Unlock()
	packet := make([]byte, 25)
	protocol.WriteUint16(packet, 2, 1008)
	encoded, _ := codecs[0].Seal(40001, packet)
	sockets[0].Write(encoded)
	// Let the first packet reach the held room lock before probing peer 2.
	time.Sleep(50 * time.Millisecond)
	hello, _ := codecs[1].Seal(0, []byte{1})
	sockets[1].Write(hello)
	sockets[1].SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 1200)
	n, err := sockets[1].Read(buf)
	if err != nil {
		t.Fatalf("unrelated heartbeat blocked by room: %v", err)
	}
	port, data, err := codecs[1].Open(buf[:n])
	if err != nil || port != 0 || len(data) != 1 || data[0] != 1 {
		t.Fatal("invalid heartbeat", err)
	}
}

func TestUDPInputIsBoundedAndOrdered(t *testing.T) {
	s := &Session{UID: 7, Done: make(chan struct{})}
	p := &serverDatagramPeer{session: s, input: make(chan inboundDatagram, 2), stopped: make(chan struct{})}
	p.enqueueInput(inboundDatagram{port: 1})
	p.enqueueInput(inboundDatagram{port: 2})
	p.enqueueInput(inboundDatagram{port: 3})
	select {
	case <-s.Done:
	default:
		t.Fatal("overflow must close this session")
	}
	if len(p.input) != 2 || (<-p.input).port != 1 || (<-p.input).port != 2 {
		t.Fatal("queue order or bound changed")
	}
}

func TestAuthenticatedUDPRelayAndFallback(t *testing.T) {
	h := NewHub(nil, Config{})
	server := NewServer(h, tls.Certificate{})
	closeUDP, e := server.ListenDatagrams("127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer closeUDP()
	room := &Room{ID: 1, Stage: "battle", Members: map[uint64]*Member{}}
	var sessions []*Session
	var peers []*serverDatagramPeer
	var sockets []*net.UDPConn
	var codecs []*tunnel.DatagramCodec
	for i := 0; i < 2; i++ {
		s := &Session{UID: uint64(i + 1), Account: "udp-test", P2P: uint32(2001 + i), UDPPort: uint16(40001 + i), Bound: true, P2PUntil: time.Now().Add(time.Minute), Room: room, Channels: map[uint32]*Channel{}, Output: make(chan tunnel.Frame, 8), Done: make(chan struct{})}
		h.Mutex.Lock()
		h.Sessions[s.UID] = s
		room.Members[s.UID] = &Member{Session: s}
		h.Mutex.Unlock()
		grant, p := server.registerDatagramPeer(s)
		defer server.unregisterDatagramPeer(p)
		c, _ := tunnel.NewDatagramCodec(grant, false)
		sock, e := net.DialUDP("udp", nil, server.udp.LocalAddr().(*net.UDPAddr))
		if e != nil {
			t.Fatal(e)
		}
		defer sock.Close()
		sock.SetDeadline(time.Now().Add(3 * time.Second))
		if p.send(tunnel.Frame{Op: "udp", Port: s.UDPPort, Data: []byte("before-ready")}) {
			t.Fatal("used unconfirmed UDP")
		}
		hello, _ := c.Seal(0, []byte{1})
		sock.Write(hello)
		buf := make([]byte, 1200)
		n, e := sock.Read(buf)
		if e != nil {
			t.Fatal(e)
		}
		port, data, e := c.Open(buf[:n])
		if e != nil || port != 0 || len(data) != 1 {
			t.Fatal("handshake", e)
		}
		sessions = append(sessions, s)
		peers = append(peers, p)
		sockets = append(sockets, sock)
		codecs = append(codecs, c)
	}
	a, b := sessions[0], sessions[1]
	packet := make([]byte, 29)
	protocol.WriteUint16(packet, 0, 1)
	protocol.WriteUint16(packet, 2, 1008)
	protocol.WriteUint32(packet, 4, a.P2P)
	protocol.WriteUint32(packet, 12, a.P2P)
	packet[23] = 4
	protocol.WriteUint32(packet, 24, b.P2P)
	packet[28] = 99
	encoded, _ := codecs[0].Seal(a.UDPPort, packet)
	sockets[0].Write(encoded)
	select {
	case f := <-b.Output:
		if f.Data[24] != 99 || !peers[1].send(f) {
			t.Fatal("relay failed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("relay timeout")
	}
	buf := make([]byte, 1200)
	n, e := sockets[1].Read(buf)
	if e != nil {
		t.Fatal(e)
	}
	port, data, e := codecs[1].Open(buf[:n])
	if e != nil || port != b.UDPPort || protocol.ReadUint16(data, 2) != 1009 || data[24] != 99 {
		t.Fatal("reply", e)
	}
	sockets[0].Write(encoded) // replay must not reach the room again
	select {
	case <-b.Output:
		t.Fatal("replay forwarded")
	case <-time.After(50 * time.Millisecond):
	}
	peers[1].mu.Lock()
	peers[1].ready = time.Now().Add(-4 * time.Second)
	peers[1].mu.Unlock()
	if peers[1].send(tunnel.Frame{Op: "udp", Port: b.UDPPort, Data: packet}) {
		t.Fatal("stale path should fall back")
	}
}
