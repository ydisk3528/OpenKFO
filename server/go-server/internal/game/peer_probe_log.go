package game

import (
	"encoding/json"
	"kungfu.local/server/internal/protocol"
	"time"
)

// Native 9E9710 echoes two uint64 identities and a uint64 sequence, unchanged.
const peerLatencyProbeID uint32 = 10000
const peerLatencyProbePayloadSize = 24
const peerProbeMaxAge = 10 * time.Second
const peerProbeMaxPairs = 64

type peerProbeObservation struct {
	sequence          uint64
	serial            uint32
	sender, recipient *Session
	started           time.Time
}

// Called under Hub.Mutex, only after authenticated same-room relay checks.
// Diagnostics never reject a packet or alter forwarding. One pending sample per
// directed pair bounds memory and avoids matching duplicates or older replies.
func (h *Hub) observePeerProbe(sender, recipient *Session, body []byte, now time.Time) {
	if h.Trace == nil {
		return
	}
	// Exact native wire size for a 24-byte payload; do not allocate for other traffic.
	if len(body) != 48 {
		return
	}
	var decoder protocol.Decoder
	messages, err := decoder.Feed(body)
	if err != nil || len(decoder.Buffer) != 0 || len(messages) != 1 {
		return
	}
	message := messages[0]
	if message.ID != peerLatencyProbeID || len(message.Payload) != peerLatencyProbePayloadSize {
		return
	}
	origin := protocol.ReadUint64(message.Payload, 0)
	target := protocol.ReadUint64(message.Payload, 8)
	sequence := protocol.ReadUint64(message.Payload, 16)
	room := sender.Room
	if room == nil || recipient.Room != room || sender.UID == recipient.UID {
		return
	}
	request := origin == sender.UID && target == recipient.UID
	response := origin == recipient.UID && target == sender.UID
	if !request && !response {
		return
	}
	key := [2]uint64{origin, target}
	for k, p := range room.PeerProbes {
		if now.Sub(p.started) > peerProbeMaxAge || p.serial != room.Serial {
			delete(room.PeerProbes, k)
		}
	}
	event := "peer_probe_request"
	entry := map[string]any{
		"event": event, "time": now.Format(time.RFC3339Nano), "room": room.ID,
		"battle_serial": room.Serial, "protocol": peerLatencyProbeID, "sequence": sequence,
		"from_uid": sender.UID, "from_account": sender.Account, "from_player": sender.Nickname,
		"to_uid": recipient.UID, "to_account": recipient.Account, "to_player": recipient.Nickname,
		"origin_uid": origin, "target_uid": target,
		"observation": "relay_enqueued_not_socket_write", "client_rtt_available": false,
	}
	if request {
		if p, ok := room.PeerProbes[key]; ok && p.sender == sender && p.recipient == recipient && sequence <= p.sequence {
			return
		}
		if room.PeerProbes == nil {
			room.PeerProbes = make(map[[2]uint64]peerProbeObservation)
		}
		if _, ok := room.PeerProbes[key]; !ok && len(room.PeerProbes) >= peerProbeMaxPairs {
			return
		}
		room.PeerProbes[key] = peerProbeObservation{sequence, room.Serial, sender, recipient, now}
	} else {
		p, ok := room.PeerProbes[key]
		if !ok || p.sequence != sequence || p.sender != recipient || p.recipient != sender || now.Before(p.started) {
			return
		}
		delete(room.PeerProbes, key)
		entry["event"] = "peer_probe_reply"
		entry["request_relay_time"] = p.started.Format(time.RFC3339Nano)
		entry["server_observed_ms"] = float64(now.Sub(p.started).Microseconds()) / 1000
		entry["measurement_scope"] = "server_queue_to_target_and_back_including_target_processing"
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		return
	}
	if h.Trace != nil {
		h.Trace.Print(string(encoded))
	}
}
