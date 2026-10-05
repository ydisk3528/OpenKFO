package tunnel

import (
	"net"
	"testing"
	"time"
)

// A loopback link intentionally loses one datagram, reorders two and duplicates
// one. This verifies codec recovery, not retransmission or in-game simulation.
func TestLoopbackDatagramLossReorderAndDuplicate(t *testing.T) {
	receiver, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()
	sender, err := net.DialUDP("udp4", nil, receiver.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	grant, _ := NewDatagramGrant(receiver.LocalAddr().(*net.UDPAddr).Port)
	enc, _ := NewDatagramCodec(grant, false)
	dec, _ := NewDatagramCodec(grant, true)
	packets := make([][]byte, 4)
	for i := range packets {
		packets[i], err = enc.Seal(38181, []byte{byte(i)})
		if err != nil {
			t.Fatal(err)
		}
	}
	// Packet 1 is lost in transit; 2 overtakes 0; 2 is delivered twice.
	for _, i := range []int{2, 0, 2, 3} {
		if _, err = sender.Write(packets[i]); err != nil {
			t.Fatal(err)
		}
	}
	receiver.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, DatagramLimit+1)
	var accepted []byte
	duplicates := 0
	for i := 0; i < 4; i++ {
		n, _, e := receiver.ReadFromUDP(buf)
		if e != nil {
			t.Fatal(e)
		}
		port, data, e := dec.Open(buf[:n])
		if e != nil {
			duplicates++
			continue
		}
		if port != 38181 || len(data) != 1 {
			t.Fatal("invalid payload")
		}
		accepted = append(accepted, data[0])
	}
	if string(accepted) != string([]byte{2, 0, 3}) || duplicates != 1 {
		t.Fatalf("received=%v rejected=%d", accepted, duplicates)
	}
}
