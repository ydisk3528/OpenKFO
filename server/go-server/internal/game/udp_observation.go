package game

import (
	"kungfu.local/server/internal/protocol"
	"log"
	"math"
)

// Only observe complete small native frames after routing to at least one
// authenticated room peer. Do not decode fragments or forward a second copy.
func (h *Hub) observeRelayedBattle(s *Session, body []byte, recipients map[uint64]bool) {
	r := s.Room
	if r == nil || len(recipients) == 0 || len(body) > 1024 || (r.Stage != "loading" && r.Stage != "battle") || r.isObserver(s) || s.game() == nil {
		return
	}
	member := r.Members[s.UID]
	if member == nil || member.Session != s {
		return
	}
	var decoder protocol.Decoder
	messages, err := decoder.Feed(body)
	if err != nil || len(decoder.Buffer) != 0 || len(messages) > 8 {
		return
	}
	for _, message := range messages {
		p := message.Payload
		if message.ID != protocol.MsgBattleEvent || len(p) < 39 {
			continue
		}
		id := protocol.ReadUint32(p, 0)
		if protocol.ReadUint64(p, 4) != s.UID || p[12] != 1 || p[13] != 1 {
			continue
		}
		s.tracePacket("C->S", s.game().ID, "udp-observed", message.ID, p, false)
		var err error
		switch id {
		case neutralNPCRequest:
			if r.Stage == "battle" && s.game().Phase == "battle" {
				err = h.neutralNPCRequest(s, message)
			}
		case neutralNPCReady:
			if r.Stage == "battle" && s.game().Phase == "battle" {
				err = h.neutralNPCReady(s, message)
			}
		case seriesInterval, seriesContinue, seriesReady, seriesFinish:
			err = h.seriesEvent(s, message, recipients)
		case 8291, 8292:
			err = h.useTalismanObserved(s, s.game(), message, recipients)
		case protocol.BattleEventPVEActorCreate, protocol.BattleEventPVEActorRemove:
			if r.Stage == "battle" && s.game().Phase == "battle" {
				err = h.applyPVEActor(s, message, true)
			}
		case protocol.BattleEventPVEBlockCreate, protocol.BattleEventPVEBlockRemove:
			err = h.applyPVEBlock(s, s.game(), message, true)
		case protocol.BattleEventFosterPositions:
			err = h.fosterPositions(s, s.game(), p)
		case protocol.BattleEventStageWaveEnd:
			if r.Stage == "battle" && s.game().Phase == "battle" {
				err = h.applyPVEFinish(s, message, true)
			}
		case protocol.BattleEventHealth:
			event, parseErr := protocol.ParseBattleHealth(p)
			if parseErr == nil && r.Stage == "battle" && s.game().Phase == "battle" && (r.controlsBattleActor(s, event.Target) || r.controlsBattleActor(s, event.Source)) && r.hasPVEActor(event.Target) && event.Context == uint64(r.ID)|uint64(r.Serial)<<32 && !r.stalePVEEvent(s, event.Target, protocol.ReadUint32(p, 19)) && (event.Source == 0 || r.Members[event.Source] != nil || r.hasPVEActor(event.Source)) {
				r.trackFosterHealth(p)
			}
		}
		if err != nil {
			log.Printf("udp_observation_rejected account=%q uid=%d room=%d event=%d error=%v", s.Account, s.UID, r.ID, id, err)
		}
	}
	// Movement has a separate native header/counter from generic events.
	for _, message := range messages {
		p := message.Payload
		if message.ID != protocol.MsgBattleEvent || len(p) != 108 || protocol.ReadUint32(p, 0) != protocol.BattleEventMovement || protocol.ReadUint64(p, 4) != s.UID || p[12] != 0 || p[13] != 0 || r.Stage != "battle" {
			continue
		}
		r.triggerFosterGroups([3]float32{math.Float32frombits(protocol.ReadUint32(p, 51)), math.Float32frombits(protocol.ReadUint32(p, 55)), math.Float32frombits(protocol.ReadUint32(p, 59))})
	}
}
