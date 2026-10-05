package game

import (
	"bytes"
	"fmt"
	"math"
	"testing"
	"time"

	"kungfu.local/server/internal/protocol"
	"kungfu.local/server/internal/tunnel"
)

// Exercise both native relay and reliable battle messages in a full 4v4 room.
// A client which stops consuming output must not stall the other seven clients,
// including when that client is the controller. This does not simulate the
// native controller's game logic or claim that logic can run without its host.
func TestPVP4V4SlowReceiverIsolation(t *testing.T) {
	for _, mode := range []protocol.RoomType{protocol.TeamSurvival, protocol.TeamDeathmatch} {
		for _, slowSlot := range []int{0, 7} {
			t.Run(fmt.Sprintf("%s/slow-slot-%d", mode, slowSlot), func(t *testing.T) {
				h := NewHub(nil, Config{})
				r := &Room{ID: 1, Serial: 7, Stage: "battle", Request: make([]byte, 81), Members: map[uint64]*Member{}}
				r.Request[protocol.RoomTypeOffset] = byte(mode)
				h.Rooms[r.ID] = r
				players := make([]*Session, 8)
				for i := range players {
					s := &Session{UID: uint64(1003 + i), P2P: uint32(2001 + i), UDPPort: uint16(40001 + i), Bound: true, P2PUntil: time.Now().Add(time.Minute), Room: r, GameChannel: 1, Channels: map[uint32]*Channel{1: {ID: 1, Kind: "game", Phase: "battle"}}, Output: make(chan tunnel.Frame, 128), datagramOutput: make(chan tunnel.Frame, 128), Done: make(chan struct{})}
					s.datagramEnabled.Store(true)
					players[i] = s
					h.Sessions[s.UID] = s
					r.Members[s.UID] = &Member{Session: s, Slot: byte(i), Team: byte(i / 4)}
					defer s.Close()
				}
				r.Owner = players[0].UID
				sender := players[1]
				check := func(queue chan tunnel.Frame, s *Session, want []byte, udp bool) {
					t.Helper()
					select {
					case f := <-queue:
						s.queuedBytes.Add(-int64(len(f.Data) + 128))
						data := f.Data
						if udp {
							if len(data) < 24 || protocol.ReadUint16(data, 2) != 1009 || protocol.ReadUint32(data, 4) != s.P2P {
								t.Fatal("invalid relay envelope")
							}
							data = data[24:]
						}
						if !bytes.Equal(data, want) {
							t.Fatal("battle payload altered or reordered")
						}
					default:
						t.Fatal("healthy recipient missed frame")
					}
				}
				for n := uint32(1); n <= 160; n++ {
					movement := combatPacket(protocol.BattleEventMovement, 108, sender.UID, sender.UID, 0)
					protocol.WriteUint32(movement.Payload, 15, n)
					raw, _ := protocol.Encode(movement)
					packet := make([]byte, 24)
					protocol.WriteUint16(packet, 0, 1)
					protocol.WriteUint16(packet, 2, 1008)
					protocol.WriteUint32(packet, 4, sender.P2P)
					protocol.WriteUint32(packet, 12, sender.P2P)
					packet[23] = 28
					for _, s := range players {
						if s != sender {
							b := make([]byte, 4)
							protocol.WriteUint32(b, 0, s.P2P)
							packet = append(packet, b...)
						}
					}
					packet = append(packet, raw...)
					if err := h.Handle(sender, tunnel.Frame{Op: "udp", Port: sender.UDPPort, Data: packet}); err != nil {
						t.Fatal(err)
					}
					for i, s := range players {
						if s != sender && i != slowSlot {
							check(s.datagramOutput, s, raw, true)
						}
					}
					for _, id := range []uint32{protocol.BattleEventAction, protocol.BattleEventHealth} {
						m := combatPacket(id, 103, sender.UID, sender.UID, 95)
						if id == protocol.BattleEventHealth {
							m = combatPacket(id, 94, sender.UID, players[5].UID, 86)
							protocol.WriteUint64(m.Payload, 47, sender.UID)
							protocol.WriteUint32(m.Payload, 67, math.Float32bits(3))
						}
						protocol.WriteUint32(m.Payload, 19, n)
						encoded, _ := protocol.Encode(m)
						if err := h.Handle(sender, tunnel.Frame{Op: "data", Channel: 1, Data: encoded}); err != nil {
							t.Fatal(err)
						}
						for i, s := range players {
							if s != sender && i != slowSlot {
								check(s.Output, s, encoded, false)
							}
						}
					}
				}
				for i, s := range players {
					select {
					case <-s.Done:
						if i != slowSlot {
							t.Fatal("slow recipient disconnected healthy player")
						}
					default:
						if i == slowSlot {
							t.Fatal("unbounded stalled recipient")
						}
					}
				}
				if r.Stage != "battle" {
					t.Fatal("output congestion ended match")
				}
			})
		}
	}
}
