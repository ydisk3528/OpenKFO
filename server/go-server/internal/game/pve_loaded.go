package game

import (
	"log"

	"kungfu.local/server/internal/protocol"
)

// Native 827FC0 resolves the sender's slot and passes the actor key at +39
// to mode vtable+120. Foster 940040 sets that slot's loaded bit; 943B60
// waits for every remote fighter before registering the host's NPC.
// This acknowledgement comes from peers, unlike host-only spawn events.
func (h *Hub) pveActorLoaded(s *Session, message protocol.Message) error {
	r, p := s.Room, message.Payload
	if r.Type() != protocol.FosterMode && r.Type() != protocol.StageAssault {
		return nil
	}
	if len(p) != 47 || protocol.ReadUint64(p, 4) != s.UID || p[12] != 1 || p[13] != 1 {
		return rejectBattle("PVE loaded acknowledgement fields")
	}
	m := r.Members[s.UID]
	owner := r.Members[r.Owner]
	actor := protocol.ReadUint64(p, 39)
	if s.UID == r.Owner || owner == nil || m.Slot >= 8 || !r.hasPVEActor(actor) {
		return nil
	}
	key := battleEventKey{Kind: 20406, Actor: actor}
	if _, seen := m.BattleEvents[key]; seen {
		return nil
	}
	if m.BattleEvents == nil {
		m.BattleEvents = map[battleEventKey]battleSequence{}
	}
	// Actor creation clears this key when an identity is reused. Peer event
	// counters are independent of the controller's spawn event counter.
	m.BattleEvents[key] = battleSequence{Sequence: protocol.ReadUint32(p, 19)}
	owner.Session.sendGame(message)
	log.Printf("pve_actor_loaded room=%d serial=%d uid=%d actor=%d controller=%d", r.ID, r.Serial, s.UID, actor, r.Owner)
	return nil
}
