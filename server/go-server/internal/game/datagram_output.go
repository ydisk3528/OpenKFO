package game

import "kungfu.local/server/internal/tunnel"

// Keep native UDP independent of a slow reliable TCP write. If the UDP lease
// is unavailable, preserve the existing reliable fallback and bounded queues.
// No room/state locks are held while either transport performs network I/O.
func (s *Session) writeDatagrams(send func(tunnel.Frame) bool, ready ...func() bool) {
	fallback := false
	for {
		select {
		case <-s.Done:
			return
		case f := <-s.datagramOutput:
			s.queuedBytes.Add(-int64(len(f.Data) + 128))
			useUDP := true
			if len(ready) > 0 {
				useUDP = ready[0]()
			}
			if useUDP && fallback {
				useUDP = s.udpDrain.Confirm(s.Done, func(n uint32) bool {
					return s.enqueue(tunnel.Frame{Op: "pong", Kind: "udp-down-drain", Value: n}, s.Output)
				})
			}
			if !useUDP || !send(f) {
				if !s.enqueue(f, s.Output) {
					return
				}
				fallback = true
			} else {
				fallback = false
			}
		}
	}
}
