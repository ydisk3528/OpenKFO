package tunnel

import (
	"testing"
	"time"
)

func TestUDPDrainRequiresMatchingAck(t *testing.T) {
	d := &UDPDrain{Supported: true}
	done := make(chan struct{})
	sent := make(chan uint32, 1)
	result := make(chan bool, 1)
	go func() { result <- d.Confirm(done, func(n uint32) bool { sent <- n; return true }) }()
	n := <-sent
	d.Ack(n + 1)
	select {
	case <-result:
		t.Fatal("wrong ack accepted")
	case <-time.After(20 * time.Millisecond):
	}
	d.Ack(n)
	select {
	case ok := <-result:
		if !ok {
			t.Fatal("matching ack rejected")
		}
	case <-time.After(time.Second):
		t.Fatal("ack ignored")
	}
}
func TestUDPDrainLegacyAndClosed(t *testing.T) {
	d := &UDPDrain{}
	if d.Confirm(make(chan struct{}), func(uint32) bool { t.Fatal("legacy peer probed"); return true }) {
		t.Fatal("legacy resumed")
	}
	d.Supported = true
	done := make(chan struct{})
	close(done)
	if d.Confirm(done, func(uint32) bool { return true }) {
		t.Fatal("closed resumed")
	}
}

func TestUDPDrainTimeoutCanRecoverAfterBackoff(t *testing.T) {
	d := &UDPDrain{Supported: true}
	done := make(chan struct{})
	if d.Confirm(done, func(uint32) bool { return true }) {
		t.Fatal("missing acknowledgement accepted")
	}
	if d.Disabled || !time.Now().Before(d.retryAfter) {
		t.Fatal("timeout permanently disabled recovery")
	}
	if d.Confirm(done, func(uint32) bool { t.Fatal("retry ignored backoff"); return true }) {
		t.Fatal("backoff bypassed")
	}
	d.retryAfter = time.Now().Add(-time.Second)
	if !d.Confirm(done, func(n uint32) bool { d.Ack(n); return true }) {
		t.Fatal("UDP did not recover after timeout")
	}
}
