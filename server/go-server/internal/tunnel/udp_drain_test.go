package tunnel

import "testing"

func TestUDPDrainNonblockingAndOrdered(t *testing.T) {
	d := &UDPDrain{Supported: true}
	done := make(chan struct{})
	var fences []uint32
	send := func(n uint32) bool { fences = append(fences, n); return true }
	d.FallbackSent(done, send)
	if d.Ready(done) || len(fences) != 1 {
		t.Fatal("unacknowledged fence")
	}
	for i := 0; i < 1000; i++ {
		d.FallbackSent(done, send)
	}
	if len(fences) != 1 {
		t.Fatal("fence flood")
	}
	d.Ack(fences[0])
	if d.Ready(done) {
		t.Fatal("old ACK bypassed newer TCP traffic")
	}
	d.FallbackSent(done, send)
	if len(fences) != 2 {
		t.Fatal("missing fresh fence")
	}
	d.Ack(fences[0])
	if d.Ready(done) {
		t.Fatal("stale ACK")
	}
	d.Ack(fences[1])
	if !d.Ready(done) {
		t.Fatal("safe recovery denied")
	}
	close(done)
	if d.Ready(done) {
		t.Fatal("closed recovered")
	}
}
func TestUDPDrainLegacyFailureAndWrap(t *testing.T) {
	done := make(chan struct{})
	d := &UDPDrain{}
	d.FallbackSent(done, func(uint32) bool { t.Fatal("legacy fence"); return true })
	if d.Ready(done) {
		t.Fatal("legacy recovered")
	}
	d.Supported = true
	d.FallbackSent(done, func(uint32) bool { return false })
	if !d.Disabled || d.Ready(done) {
		t.Fatal("send failure")
	}
	d = &UDPDrain{Supported: true, next: ^uint32(0)}
	d.Ack(d.next)
	d.FallbackSent(done, func(uint32) bool { t.Fatal("wrapped fence"); return true })
	if !d.Disabled {
		t.Fatal("wrap allowed")
	}
}
