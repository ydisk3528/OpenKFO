//go:build windows

package bridge

import (
	"errors"
	"kungfu.local/server/internal/tunnel"
	"log"
	"net"
	"strconv"
	"sync"
	"syscall"
	"time"
)

type clientDatagramPeer struct {
	mu    sync.Mutex
	conn  *net.UDPConn
	codec *tunnel.DatagramCodec
	ack   time.Time
}

func (p *clientDatagramPeer) ready() bool {
	p.mu.Lock()
	ready := time.Since(p.ack) <= 3*time.Second
	p.mu.Unlock()
	return ready
}

// A probe reply proves only the return path. Wait for the server to acknowledge
// the confirmation before sending native packets on its separately queued lane.
func (p *clientDatagramPeer) confirmHeartbeat(value byte) bool {
	if value != 1 {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	wasReady := time.Since(p.ack) <= 3*time.Second
	p.ack = time.Now()
	return !wasReady
}
func (p *clientDatagramPeer) send(port uint16, data []byte) bool {
	if !p.ready() {
		return false
	}
	encoded, e := p.codec.Seal(port, data)
	if e != nil {
		return false
	}
	_, e = p.conn.Write(encoded)
	return e == nil
}
func (b *Bridge) startDatagrams(s *remoteSession, host string, g *tunnel.DatagramGrant) {
	codec, e := tunnel.NewDatagramCodec(g, false)
	if e != nil {
		return
	}
	addr, e := net.ResolveUDPAddr("udp", net.JoinHostPort(host, strconv.Itoa(g.Port)))
	if e != nil {
		return
	}
	conn, e := net.DialUDP("udp", nil, addr)
	if e != nil {
		return
	}
	p := &clientDatagramPeer{conn: conn, codec: codec}
	s.udpTransport = p
	s.udpDrain.Supported = g.Drain
	go func() {
		defer conn.Close()
		var lastReadError time.Time
		buffer := make([]byte, tunnel.DatagramLimit+1)
		for {
			n, e := conn.Read(buffer)
			if e != nil {
				if retryUDPRead(e) {
					if time.Since(lastReadError) >= 5*time.Second {
						log.Printf("udp_read_retry uid=%d", s.uid)
						lastReadError = time.Now()
					}
					select {
					case <-s.done:
						return
					case <-time.After(20 * time.Millisecond):
						continue
					}
				}
				p.mu.Lock()
				p.ack = time.Time{}
				p.mu.Unlock()
				return
			}
			port, data, e := codec.Open(buffer[:n])
			if e != nil {
				continue
			}
			if port == 0 {
				if len(data) != 1 || data[0] > 1 {
					continue
				}
				if p.confirmHeartbeat(data[0]) {
					log.Printf("udp_transport_ready account=%q uid=%d", s.account, s.uid)
				}
				// Confirm the server-to-client path before any native packet uses it.
				if data[0] == 0 {
					encoded, e := codec.Seal(0, []byte{1})
					if e == nil {
						conn.Write(encoded)
					}
				}
				continue
			}
			s.mutex.Lock()
			allowed := s.udpPorts[int(port)]
			s.mutex.Unlock()
			if !allowed {
				continue
			}
			b.udp.WriteToUDP(data, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: int(port)})
		}
	}()
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		previous := false
		for {
			p.mu.Lock()
			ready := time.Since(p.ack) <= 3*time.Second
			p.mu.Unlock()
			if previous && !ready {
				log.Printf("udp_transport_fallback account=%q uid=%d reason=heartbeat_timeout", s.account, s.uid)
			}
			previous = ready
			value := byte(0)
			if ready {
				value = 1
			}
			encoded, e := codec.Seal(0, []byte{value})
			if e == nil {
				conn.Write(encoded)
			}
			select {
			case <-s.done:
				return
			case <-ticker.C:
			}
		}
	}()
}

func retryUDPRead(err error) bool {
	if errors.Is(err, net.ErrClosed) {
		return false
	}
	// Windows reports asynchronous ICMP errors on connected UDP sockets.
	for _, code := range []syscall.Errno{10054, 10061, 10051, 10065, 10040, 10055} {
		if errors.Is(err, code) {
			return true
		}
	}
	var temporary net.Error
	return errors.As(err, &temporary) && (temporary.Timeout() || temporary.Temporary())
}
