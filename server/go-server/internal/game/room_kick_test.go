package game

import (
	"errors"
	"kungfu.local/server/internal/protocol"
	"testing"
)

func TestRoomKickIgnoresLateReadyRequests(t *testing.T) {
	for _, id := range []uint32{protocol.MsgReady, protocol.MsgCancelReady} {
		h, host, peer, _ := waitingRoomFixture()
		r := host.Room
		r.Members[peer.UID].Ready = true
		roomRequest(t, h, host, protocol.MsgKickRoomPlayer, append(protocol.Uint64Bytes(peer.UID), 0))
		roomOutputs(t, peer, protocol.MsgRoomPlayerKicked)
		roomOutputs(t, host, protocol.MsgRoomPlayerKicked, protocol.MsgPlayerLeftRoom)
		if peer.Room != nil || peer.game().Phase != "lobby" {
			t.Fatal("kicked player did not return to lobby")
		}
		// The request was sent before the client received the kick notification.
		for i := 0; i < 2; i++ {
			if err := h.route(peer, peer.game(), protocol.Message{ID: id}); err != nil {
				t.Fatalf("late request %d disconnected kicked player: %v", id, err)
			}
		}
		roomOutputs(t, peer)
		roomOutputs(t, host)
		if peer.Room != nil || peer.game().Phase != "lobby" || len(r.Members) != 1 || r.Members[peer.UID] != nil || r.Members[host.UID].Ready || r.Stage != "room" {
			t.Fatal("late request changed room or lobby state")
		}
		if err := h.route(peer, peer.game(), protocol.Message{ID: id, Payload: []byte{1}}); !errors.Is(err, protocol.ErrFrame) {
			t.Fatalf("malformed request %d was not rejected: %v", id, err)
		}
	}
}

func TestRoomKickRejectionsPreserveRoomAndConnection(t *testing.T) {
	for _, scenario := range []string{"short", "long", "flag", "self", "missing", "loading", "battle", "stale session"} {
		t.Run(scenario, func(t *testing.T) {
			h, host, peer, _ := waitingRoomFixture()
			r := host.Room
			payload := append(protocol.Uint64Bytes(peer.UID), 0)
			switch scenario {
			case "short":
				payload = payload[:8]
			case "long":
				payload = append(payload, 0)
			case "flag":
				payload[8] = 1
			case "self":
				protocol.WriteUint64(payload, 0, host.UID)
			case "missing":
				protocol.WriteUint64(payload, 0, 99999)
			case "loading", "battle":
				r.Stage = scenario
			case "stale session":
				r.Members[host.UID].Session = &Session{UID: host.UID}
			}
			if err := h.kickRoomPlayer(host, payload); err != nil {
				t.Fatal("rejection disconnected player", err)
			}
			roomOutputs(t, host, 20150)
			roomOutputs(t, peer)
			if len(r.Members) != 2 || peer.Room != r || r.Owner != host.UID {
				t.Fatal("rejection changed room")
			}
		})
	}
}
