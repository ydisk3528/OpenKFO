package tunnel

import (
	"encoding/binary"
	"sync"
)

// UDPOrder is negotiated independently of the legacy drain ACK. Each TCP
// fallback advances an epoch; subsequent UDP carries that epoch. The receiver
// buffers early UDP until that TCP frame is delivered, without an ACK round trip.
// This orders transport transitions, not unreliable datagrams within an epoch.
type UDPOrder struct {
	mu      sync.Mutex
	epoch   uint32
	pending []Frame
}

const UDPOrderLimit = 64

func PackUDPEpoch(epoch uint32, data []byte) []byte {
	b := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(b, epoch)
	copy(b[4:], data)
	return b
}

func UnpackUDPEpoch(data []byte) (uint32, []byte, error) {
	if len(data) < 4 {
		return 0, nil, ErrDatagram
	}
	return binary.BigEndian.Uint32(data), data[4:], nil
}

// deliver must not call this gate recursively. It is serialized only with the
// other transport of this session, never with another player or a heartbeat.
func (g *UDPOrder) Receive(f Frame, reliable bool, deliver func(Frame) error) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if reliable {
		if f.Value == 0 || f.Value <= g.epoch {
			return ErrDatagram
		}
		if err := deliver(f); err != nil {
			return err
		}
		g.epoch = f.Value
		pending := g.pending
		g.pending = nil
		for _, p := range pending {
			if p.Value == g.epoch {
				if err := deliver(p); err != nil {
					return err
				}
			} else if p.Value > g.epoch {
				g.pending = append(g.pending, p)
			}
		}
		return nil
	}
	if f.Value < g.epoch {
		return nil
	} // Late UDP must not rewind a TCP transition.
	if f.Value == g.epoch {
		return deliver(f)
	}
	if len(g.pending) == UDPOrderLimit {
		// Native UDP is best effort; keep recent packets with bounded memory.
		copy(g.pending, g.pending[1:])
		g.pending = g.pending[:UDPOrderLimit-1]
	}
	f.Data = append([]byte(nil), f.Data...)
	g.pending = append(g.pending, f)
	return nil
}
