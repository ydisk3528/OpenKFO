package game

import (
	"kungfu.local/server/internal/protocol"
	"kungfu.local/server/internal/tunnel"
	"log"
	"net"
	"sync"
	"time"
)

type inboundDatagram struct {
	port uint16
	data []byte
	addr *net.UDPAddr
}

type serverDatagramPeer struct {
	input    chan inboundDatagram
	stopped  chan struct{}
	stopOnce sync.Once
	mu       sync.Mutex
	server   *Server
	session  *Session
	codec    *tunnel.DatagramCodec
	id       [16]byte
	remote   *net.UDPAddr
	ready    time.Time
}

// Open before accepting TLS clients. Failure to bind must not silently advertise UDP.
func (s *Server) ListenDatagrams(address string) (func(), error) {
	addr, e := net.ResolveUDPAddr("udp", address)
	if e != nil {
		return nil, e
	}
	conn, e := net.ListenUDP("udp", addr)
	if e != nil {
		return nil, e
	}
	s.udp = conn
	s.udpPeers = make(map[[16]byte]*serverDatagramPeer)
	go s.readDatagrams()
	return func() { conn.Close() }, nil
}
func (s *Server) registerDatagramPeer(session *Session) (*tunnel.DatagramGrant, *serverDatagramPeer) {
	if s.udp == nil {
		return nil, nil
	}
	g, e := tunnel.NewDatagramGrant(s.udp.LocalAddr().(*net.UDPAddr).Port)
	if e != nil {
		return nil, nil
	}
	c, e := tunnel.NewDatagramCodec(g, true)
	if e != nil {
		return nil, nil
	}
	p := &serverDatagramPeer{server: s, session: session, codec: c, input: make(chan inboundDatagram, 128), stopped: make(chan struct{})}
	copy(p.id[:], g.ID)
	s.udpMutex.Lock()
	s.udpPeers[p.id] = p
	s.udpMutex.Unlock()
	go p.readInput()
	return g, p
}
func (s *Server) unregisterDatagramPeer(p *serverDatagramPeer) {
	if p == nil {
		return
	}
	s.udpMutex.Lock()
	delete(s.udpPeers, p.id)
	p.stopOnce.Do(func() { close(p.stopped) })
	s.udpMutex.Unlock()
}
func (p *serverDatagramPeer) send(f tunnel.Frame) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.remote == nil || time.Since(p.ready) > 3*time.Second {
		return false
	}
	encoded, e := p.codec.Seal(f.Port, f.Data)
	if e != nil {
		return false
	}
	_, e = p.server.udp.WriteToUDP(encoded, p.remote)
	return e == nil
}
func (s *Server) readDatagrams() {
	buffer := make([]byte, tunnel.DatagramLimit+1)
	for {
		n, addr, e := s.udp.ReadFromUDP(buffer)
		if e != nil {
			return
		}
		id, ok := tunnel.DatagramID(buffer[:n])
		if !ok {
			continue
		}
		s.udpMutex.Lock()
		p := s.udpPeers[id]
		s.udpMutex.Unlock()
		if p == nil {
			continue
		}
		port, data, e := p.codec.Open(buffer[:n])
		if e != nil {
			continue
		}
		p.enqueueInput(inboundDatagram{port: port, data: data, addr: addr})
	}
}

// One ordered, bounded lane per authenticated player. The socket reader never
// waits for room locks or database work in another player's handler.
func (p *serverDatagramPeer) enqueueInput(packet inboundDatagram) {
	select {
	case <-p.session.Done:
		return
	case <-p.stopped:
		return
	default:
	}
	select {
	case p.input <- packet:
	default:
		// Match the existing bounded output policy: disconnect only this peer
		// rather than retain stale combat indefinitely or block every room.
		log.Printf("session_queue_overflow uid=%d reason=udp_input_limit", p.session.UID)
		p.session.Close()
	}
}

func (p *serverDatagramPeer) readInput() {
	for {
		select {
		case <-p.session.Done:
			return
		case <-p.stopped:
			return
		case packet := <-p.input:
			p.handleInput(packet)
		}
	}
}

func (p *serverDatagramPeer) handleInput(packet inboundDatagram) {
	s := p.server
	port, data, addr := packet.port, packet.data, packet.addr
	select {
	case <-p.session.Done:
		return
	default:
	}
	if port == 0 {
		if len(data) != 1 || data[0] > 1 {
			return
		}
		s.Hub.Mutex.Lock()
		live := s.Hub.Sessions[p.session.UID] == p.session && !p.session.LoggedOut
		s.Hub.Mutex.Unlock()
		if !live {
			return
		}
		p.mu.Lock()
		p.remote = addr
		if data[0] == 1 {
			if time.Since(p.ready) > 3*time.Second {
				log.Printf("udp_transport_ready uid=%d account=%q", p.session.UID, p.session.Account)
			}
			p.ready = time.Now()
		}
		p.mu.Unlock()
		response, e := p.codec.Seal(0, data)
		if e == nil {
			s.udp.WriteToUDP(response, addr)
		}
		return
	}
	// Only native relay envelopes use this lane; login/lease traffic remains TLS.
	if len(data) < 24 || protocol.ReadUint16(data, 2) != 1008 {
		return
	}
	p.mu.Lock()
	same := p.remote != nil && p.remote.String() == addr.String() && time.Since(p.ready) <= 3*time.Second
	p.mu.Unlock()
	if !same {
		return
	}
	if e := s.Hub.Handle(p.session, tunnel.Frame{Op: "udp", Port: port, Data: data}); e != nil {
		log.Printf("udp_transport_rejected uid=%d", p.session.UID)
	}
}
