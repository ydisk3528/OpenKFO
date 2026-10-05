package bridge

import "kungfu.local/server/internal/tunnel"

// Receiving must never wait for an upstream TLS write or its mutex. Only small
// control replies use this bounded lane; game-channel writes keep their order.
func (s *remoteSession) enqueueControl(f tunnel.Frame) bool {
	s.controlOnce.Do(func() {
		s.controlOutput = make(chan tunnel.Frame, 8)
		go func() {
			for {
				select {
				case <-s.done:
					return
				case frame := <-s.controlOutput:
					if err := s.send(frame); err != nil {
						s.close()
						return
					}
				}
			}
		}()
	})
	select {
	case <-s.done:
		return false
	default:
	}
	select {
	case s.controlOutput <- f:
		return true
	default:
		return false
	}
}
