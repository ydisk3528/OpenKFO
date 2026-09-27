package game

import "kungfu.local/server/internal/protocol"

type reliableActor struct {
	UID    uint64
	Family uint32
}
type reliableExchange struct {
	Object            uint32
	Pending, Approved bool
	Completed         bool // An accepted phase 2, awaiting the object's 8276 action completion.
	Last              [3]battleSequence
	Seen              [3]bool
}

// The selected native controller arbitrates object availability. This is an
// authenticated relay, not authoritative simulation of client scene objects.
// Keep one exchange per actor/family, matching the entity's current object.
func (h *Hub) reliableBattleEvent(s *Session, m protocol.Message) error {
	r, err := protocol.ParseReliableEvent(m.Payload)
	if err != nil {
		return err
	}
	room := s.Room
	if r.Sender != s.UID || r.Flag51 > 1 {
		return protocol.ErrFrame
	}
	if room.Members[r.Actor] == nil && !room.hasPVEActor(r.Actor) {
		return nil
	}
	if room.stalePVEEvent(s, r.Actor, protocol.ReadUint32(m.Payload, 19)) {
		return nil
	}
	if r.HasContext && r.Context != [2]uint32{uint32(room.ID), room.Serial} {
		return nil
	}
	family := uint32(9000)
	if r.Kind >= 9500 {
		family = 9500
	}
	phase := r.Kind - family
	if (phase == 1 && s.UID != room.Owner) || (phase != 1 && !room.controlsBattleActor(s, r.Actor)) {
		return nil
	}
	if room.ReliableSerial != room.Serial {
		room.Reliable = nil
		room.ReliableSerial = room.Serial
	}
	if room.Reliable == nil {
		room.Reliable = map[reliableActor]*reliableExchange{}
	}
	key := reliableActor{r.Actor, family}
	x := room.Reliable[key]
	if x == nil {
		x = &reliableExchange{}
	}
	seq := protocol.ReadUint32(m.Payload, 19)
	if x.Seen[phase] && int32(seq-x.Last[phase].Sequence) <= 0 {
		return nil
	}
	switch phase {
	case 0:
		if r.Flag51 == 0 {
			if !x.Pending || x.Object != r.ObjectKey {
				return nil
			}
			x.Pending, x.Approved = false, false
		} else {
			if x.Pending {
				return nil
			} // Never replace an outstanding reservation.
			x.Object, x.Pending, x.Approved = r.ObjectKey, true, false
		}
		x.Completed = false
	case 1:
		if !x.Pending || x.Object != r.ObjectKey || x.Approved {
			return nil
		}
		if r.Flag51 == 1 && room.pickupReservedByOther(key, r.ObjectKey) {
			return nil
		}
		x.Approved = r.Flag51 == 1
		if !x.Approved {
			x.Pending = false
		}
	case 2:
		if r.Flag51 != 1 {
			return nil
		}
		// The controller's own native action enters its local queue directly;
		// it need not send a network request before its completion event.
		if s.UID != room.Owner && (!x.Pending || !x.Approved || x.Object != r.ObjectKey) {
			return nil
		}
		if room.pickupReservedByOther(key, r.ObjectKey) {
			return nil
		}
		x.Object, x.Pending, x.Approved = r.ObjectKey, false, false
		x.Completed = true
	}
	x.Seen[phase] = true
	x.Last[phase] = battleSequence{Sequence: seq, Payload: string(m.Payload)}
	room.Reliable[key] = x
	if phase == 0 {
		if owner := room.Members[room.Owner]; owner != nil && owner.Session != s {
			owner.Session.sendGame(m)
		}
	} else {
		h.broadcast(room, m, s.UID)
	}
	return nil
}

// Item and chest namespaces are independent. A controller response must not
// reserve one object for two actors, including its own native fast path.
func (r *Room) pickupReservedByOther(actor reliableActor, object uint32) bool {
	for key, pending := range r.Reliable {
		if key != actor && key.Family == actor.Family && pending.Pending && pending.Approved && pending.Object == object {
			return true
		}
	}
	return false
}
