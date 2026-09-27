package game

import "kungfu.local/server/internal/tunnel"

// Keep native UDP independent of a slow reliable TCP write. If the UDP lease
// is unavailable, preserve the existing reliable fallback and bounded queues.
// No room/state locks are held while either transport performs network I/O.
func (s *Session) writeDatagrams(send func(tunnel.Frame) bool) {
	for {
		select {
		case <-s.Done:
			return
		case f := <-s.datagramOutput:
			s.queuedBytes.Add(-int64(len(f.Data) + 128))
			if !send(f) && !s.enqueue(f, s.Output) {
				return
			}
		}
	}
}
