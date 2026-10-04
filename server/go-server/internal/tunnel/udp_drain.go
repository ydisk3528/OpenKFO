package tunnel

import (
	"sync"
	"sync/atomic"
	"time"
)

// One sending worker owns next/Disabled; the receive loop only calls Ack.
// Acknowledgement is sent after all earlier fallback frames were dispatched.
type UDPDrain struct {
	Supported  bool
	Disabled   bool
	next       uint32
	retryAfter time.Time
	ack        atomic.Uint32
	wakeOnce   sync.Once
	wake       chan struct{}
}

func (d *UDPDrain) initWake() { d.wakeOnce.Do(func() { d.wake = make(chan struct{}, 1) }) }
func (d *UDPDrain) Ack(n uint32) {
	d.initWake()
	d.ack.Store(n)
	select {
	case d.wake <- struct{}{}:
	default:
	}
}
func (d *UDPDrain) Confirm(done <-chan struct{}, send func(uint32) bool) bool {
	if !d.Supported || d.Disabled || time.Now().Before(d.retryAfter) {
		return false
	}
	select {
	case <-done:
		return false
	default:
	}
	d.initWake()
	d.next++
	if d.next == 0 {
		d.Disabled = true
		return false
	}
	if !send(d.next) {
		d.Disabled = true
		return false
	}
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		if d.ack.Load() == d.next {
			return true
		}
		select {
		case <-done:
			return false
		case <-timer.C:
			// A slow acknowledgement is not a permanent UDP failure.
			// Keep TCP ordering and retry a fresh fence after a bounded backoff.
			d.retryAfter = time.Now().Add(5 * time.Second)
			return false
		case <-d.wake:
		}
	}
}
