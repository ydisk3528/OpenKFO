package bridge

import (
	"encoding/json"
	"fmt"
	"kungfu.local/server/internal/protocol"
	"kungfu.local/server/internal/tunnel"
	"net"
	"testing"
	"time"
)

func TestOrderedTCPFallbackWhenUDPUnavailable(t *testing.T) {
	for _, host := range []string{"", "\x00"} {
		t.Run(fmt.Sprintf("host-%q", host), func(t *testing.T) {
			a, b := net.Pipe()
			defer b.Close()
			s := &remoteSession{connection: a, encoder: json.NewEncoder(a), done: make(chan struct{})}
			defer s.close()
			g, err := tunnel.NewDatagramGrant(19092)
			if err != nil {
				t.Fatal(err)
			}
			g.Order = true
			(&Bridge{}).startDatagrams(s, host, g)
			if s.udpTransport != nil || !s.udpOrdered {
				t.Fatal("failed UDP setup erased negotiated fallback format")
			}
			s.enqueueUDPFallback(tunnel.Frame{Op: "udp", Port: 18001, Data: []byte("native")})
			b.SetReadDeadline(time.Now().Add(time.Second))
			var f tunnel.Frame
			if err := json.NewDecoder(b).Decode(&f); err != nil {
				t.Fatal(err)
			}
			var gate tunnel.UDPOrder
			if err := gate.Receive(f, true, func(got tunnel.Frame) error {
				if got.Value != 1 || string(got.Data) != "native" {
					t.Fatal("wrong fallback frame")
				}
				return nil
			}); err != nil {
				t.Fatal("server rejected fallback", err)
			}
		})
	}
}

func TestOrderedUpstreamRecoversWithoutACKOnRealUDP(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
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
	g, _ := tunnel.NewDatagramGrant(receiver.LocalAddr().(*net.UDPAddr).Port)
	encode, _ := tunnel.NewDatagramCodec(g, false)
	decode, _ := tunnel.NewDatagramCodec(g, true)
	p := &clientDatagramPeer{conn: sender, codec: encode}
	s := &remoteSession{udpOrdered: true, connection: a, encoder: json.NewEncoder(a), done: make(chan struct{}), udpTransport: p}
	defer s.close()
	packet := make([]byte, 24)
	protocol.WriteUint16(packet, 2, 1008)
	s.enqueueUDPFallback(tunnel.Frame{Op: "udp", Port: 18001, Data: packet})
	b.SetReadDeadline(time.Now().Add(time.Second))
	var fallback tunnel.Frame
	if err = json.NewDecoder(b).Decode(&fallback); err != nil {
		t.Fatal(err)
	}
	if fallback.Value != 1 {
		t.Fatal("fallback epoch missing")
	}
	p.confirmHeartbeat(1)
	var gate tunnel.UDPOrder
	got := 0
	deliver := func(tunnel.Frame) error { got++; return nil }
	for i := 0; i < 50; i++ {
		if !s.enqueueUDPFallback(tunnel.Frame{Op: "udp", Port: 18001, Data: packet}) {
			t.Fatal("enqueue")
		}
		receiver.SetReadDeadline(time.Now().Add(time.Second))
		buf := make([]byte, 1200)
		n, err := receiver.Read(buf)
		if err != nil {
			t.Fatal("recovery waited for ACK", err)
		}
		port, data, err := decode.Open(buf[:n])
		if err != nil {
			t.Fatal(err)
		}
		epoch, data, err := tunnel.UnpackUDPEpoch(data)
		if err != nil || epoch != 1 {
			t.Fatal("epoch", err)
		}
		if err = gate.Receive(tunnel.Frame{Op: "udp", Port: port, Value: epoch, Data: data}, false, deliver); err != nil {
			t.Fatal(err)
		}
	}
	if got != 0 {
		t.Fatal("UDP bypassed TCP")
	}
	if err = gate.Receive(fallback, true, deliver); err != nil {
		t.Fatal(err)
	}
	if got != 51 {
		t.Fatal("buffered datagrams not released", got)
	}
}

func TestOrderedDownstreamBridgeDefersEarlyUDP(t *testing.T) {
	socket, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	out, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	b := &Bridge{udp: out}
	s := &remoteSession{udpOrdered: true}
	port := uint16(socket.LocalAddr().(*net.UDPAddr).Port)
	// Bootstrap receipts bypass the combat lane and have no cutover epoch.
	if err = b.deliverDatagram(s, tunnel.Frame{Port: port, PeerReceipt: "receipt", Data: []byte{0}}, true); err != nil {
		t.Fatal(err)
	}
	if err = b.deliverDatagram(s, tunnel.Frame{Port: port, Value: 1, Data: []byte{2}}, false); err != nil {
		t.Fatal(err)
	}
	if err = b.deliverDatagram(s, tunnel.Frame{Port: port, Value: 1, Data: []byte{1}}, true); err != nil {
		t.Fatal(err)
	}
	socket.SetReadDeadline(time.Now().Add(time.Second))
	for _, want := range []byte{0, 1, 2} {
		buf := make([]byte, 10)
		n, e := socket.Read(buf)
		if e != nil || n != 1 || buf[0] != want {
			t.Fatal("native delivery order", n, e, buf)
		}
	}
}
