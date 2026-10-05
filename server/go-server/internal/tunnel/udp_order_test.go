package tunnel

import (
	"fmt"
	"reflect"
	"testing"
)

func TestUDPOrderContinuousTrafficRecoversWithoutACK(t *testing.T) {
	var g UDPOrder
	var got []string
	deliver := func(f Frame) error { got = append(got, string(f.Data)); return nil }
	// UDP recovers while the last reliable frame is still delayed in TCP.
	for i := 0; i < 50; i++ {
		if err := g.Receive(Frame{Value: 1, Data: []byte(fmt.Sprint(i))}, false, deliver); err != nil {
			t.Fatal(err)
		}
	}
	if len(got) != 0 {
		t.Fatal("UDP bypassed fallback")
	}
	if err := g.Receive(Frame{Value: 1, Data: []byte("TCP")}, true, deliver); err != nil {
		t.Fatal(err)
	}
	if len(got) != 51 || got[0] != "TCP" || got[50] != "49" {
		t.Fatal(got)
	}
	if err := g.Receive(Frame{Value: 1, Data: []byte("live")}, false, deliver); err != nil {
		t.Fatal(err)
	}
	if got[51] != "live" {
		t.Fatal("no immediate UDP recovery")
	}
}

func TestUDPOrderMultipleTransitionsAndLatePackets(t *testing.T) {
	var g UDPOrder
	var got []string
	deliver := func(f Frame) error { got = append(got, string(f.Data)); return nil }
	for _, p := range []struct {
		epoch uint32
		tcp   bool
		text  string
	}{
		{0, false, "initial"}, {2, false, "early2"}, {1, false, "early1"},
		{1, true, "tcp1"}, {2, true, "tcp2"}, {1, false, "stale1"}, {0, false, "stale0"}, {2, false, "live2"},
	} {
		if err := g.Receive(Frame{Value: p.epoch, Data: []byte(p.text)}, p.tcp, deliver); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"initial", "tcp1", "early1", "tcp2", "early2", "live2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if g.Receive(Frame{Value: 2}, true, deliver) == nil {
		t.Fatal("duplicate TCP epoch accepted")
	}
	if g.Receive(Frame{Value: 0}, true, deliver) == nil {
		t.Fatal("wrapped epoch accepted")
	}
}

func TestUDPOrderBoundsAndOwnsPendingData(t *testing.T) {
	var g UDPOrder
	data := []byte{7}
	deliver := func(Frame) error { return nil }
	for i := 0; i < 10000; i++ {
		if err := g.Receive(Frame{Value: 1, Data: data}, false, deliver); err != nil {
			t.Fatal(err)
		}
	}
	data[0] = 9
	if len(g.pending) != UDPOrderLimit || g.pending[0].Data[0] != 7 {
		t.Fatal("unbounded or borrowed buffer")
	}
}

func TestUDPEpochIsAuthenticatedAndLegacyPayloadUnchanged(t *testing.T) {
	g, err := NewDatagramGrant(19092)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := NewDatagramCodec(g, false)
	b, _ := NewDatagramCodec(g, true)
	wire, err := a.Seal(18001, PackUDPEpoch(23, []byte("battle")))
	if err != nil {
		t.Fatal(err)
	}
	port, body, err := b.Open(wire)
	if err != nil {
		t.Fatal(err)
	}
	epoch, data, err := UnpackUDPEpoch(body)
	if err != nil || port != 18001 || epoch != 23 || string(data) != "battle" {
		t.Fatal("round trip")
	}
	if _, _, err = UnpackUDPEpoch([]byte{1, 2, 3}); err == nil {
		t.Fatal("truncated epoch")
	}
	if _, _, err = b.Open(wire); err == nil {
		t.Fatal("replay accepted")
	}
	wire, _ = a.Seal(18001, []byte("legacy"))
	_, data, err = b.Open(wire)
	if err != nil || string(data) != "legacy" {
		t.Fatal("legacy codec changed")
	}
}
