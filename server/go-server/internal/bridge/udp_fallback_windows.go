package bridge

import (
	"bytes"
	"kungfu.local/server/internal/protocol"
	"kungfu.local/server/internal/tunnel"
	"log"
	"time"
)

// All native UDP uses one lane per session. New packets cannot bypass a pending
// fallback write when UDP recovers. The shared native reader never waits for TCP.
func (s *remoteSession) enqueueUDPFallback(f tunnel.Frame) bool {
	s.udpFallbackOnce.Do(func() {
		s.udpFallback = make(chan tunnel.Frame, 64)
		go s.writeUDPFallback()
	})
	select {
	case <-s.done:
		return false
	default:
	}
	f.Data = bytes.Clone(f.Data) // The socket reader reuses its buffer.
	f.QueuedAt = time.Now()
	select {
	case s.udpFallback <- f:
		return true
	default:
		// These are native UDP datagrams, not reliable game-channel messages.
		// Bound memory and prefer current input over accumulated stale input.
		select {
		case <-s.udpFallback:
			s.udpDropped++
		default:
		}
		if time.Since(s.udpDropLog) >= 5*time.Second {
			log.Printf("udp_queue_drop uid=%d dropped=%d", s.uid, s.udpDropped)
			s.udpDropLog = time.Now()
			s.udpDropped = 0
		}
		select {
		case s.udpFallback <- f:
			return true
		default:
			return false
		}
	}
}

func (s *remoteSession) writeUDPFallback() {
	var lastSlow time.Time
	fallback := false
	for {
		select {
		case <-s.done:
			return
		case f := <-s.udpFallback:
			started := time.Now()
			var err error
			useUDP := s.udpTransport != nil && len(f.Data) >= 24 && protocol.ReadUint16(f.Data, 2) == 1008 && s.udpTransport.ready()
			if useUDP && fallback {
				useUDP = s.udpDrain.Confirm(s.done, func(n uint32) bool { return s.send(tunnel.Frame{Op: "ping", Kind: "udp-drain", Value: n}) == nil })
			}
			if !useUDP || !s.udpTransport.send(f.Port, f.Data) {
				err = s.send(f)
				fallback = true
			} else {
				fallback = false
			}
			finished := time.Now()
			if finished.Sub(f.QueuedAt) > 100*time.Millisecond && finished.Sub(lastSlow) > 5*time.Second {
				lastSlow = finished
				log.Printf("udp_fallback_slow uid=%d queue_ms=%d send_ms=%d remaining=%d", s.uid, started.Sub(f.QueuedAt).Milliseconds(), finished.Sub(started).Milliseconds(), len(s.udpFallback))
			}
			if err != nil {
				s.close()
				return
			}
		}
	}
}
