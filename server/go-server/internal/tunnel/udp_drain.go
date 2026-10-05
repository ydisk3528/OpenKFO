package tunnel

import "sync/atomic"

// The sender owns next/dirty; the receiver only acknowledges fences.
// Fallback traffic continues over TCP while confirmation is outstanding.
// UDP resumes only when the fence covers EVERY preceding fallback frame.
type UDPDrain struct {
	Supported bool
	Disabled  bool
	next      uint32
	dirty     bool
	ack       atomic.Uint32
}

func (d *UDPDrain) Ack(n uint32) { d.ack.Store(n) }
func (d *UDPDrain) Ready(done <-chan struct{}) bool {
	select {
	case <-done:
		return false
	default:
	}
	return d.Supported && !d.Disabled && d.next != 0 && !d.dirty && d.ack.Load() == d.next
}

// Called after the fallback frame has entered the ordered TCP stream/queue.
// At most one fence is outstanding. An old ACK never covers newer frames.
func (d *UDPDrain) FallbackSent(done <-chan struct{}, send func(uint32) bool) {
	d.dirty = true
	if !d.Supported || d.Disabled {
		return
	}
	select {
	case <-done:
		return
	default:
	}
	if d.next != 0 && d.ack.Load() != d.next {
		return
	}
	d.next++
	if d.next == 0 {
		d.Disabled = true
		return
	}
	if !send(d.next) {
		d.Disabled = true
		return
	}
	d.dirty = false
}
