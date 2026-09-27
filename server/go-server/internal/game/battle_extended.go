package game

import (
	"kungfu.local/server/internal/protocol"
	"math"
	"time"
)

const pairSelectionLease = 15 * time.Second // Native 8143 selection timeout is milliseconds.
const maxBattleObjects = 65536              // Per-round bound; tombstones prevent key resurrection.
type pairSelection struct {
	target  uint64
	expires time.Time
}
type projectileState struct {
	owner     uint64
	alive     bool
	witnesses map[uint64]bool
}

func (r *Room) recordPairSelection(s *Session, p []byte) {
	sequence := protocol.ReadUint32(p, 19)
	if previous, ok := r.PairSelectionVersions[s.UID]; ok && int32(sequence-previous) <= 0 {
		return
	}
	if r.PairSelectionVersions == nil {
		r.PairSelectionVersions = map[uint64]uint32{}
	}
	r.PairSelectionVersions[s.UID] = sequence
	if r.PairSelections == nil {
		r.PairSelections = map[uint64]pairSelection{}
	}
	target := protocol.ReadUint64(p, 47)
	if p[12] == 1 && p[13] == 1 && protocol.ReadUint64(p, 39) == s.UID && target != s.UID && r.Members[target] != nil && !r.Members[target].Spectator && protocol.ReadUint32(p, 55) == uint32(pairSelectionLease/time.Millisecond) {
		r.PairSelections[s.UID] = pairSelection{target, time.Now().Add(pairSelectionLease)}
	} else {
		delete(r.PairSelections, s.UID)
	}
}

// Only called for an authenticated fighter in the current battle. These events
// have object/pair ownership rather than the ordinary actor-at-39 layout.
func (h *Hub) extendedBattleEvent(s *Session, msg protocol.Message) (bool, error) {
	r, p := s.Room, msg.Payload
	id := protocol.ReadUint32(p, 0)
	size, context, matrix := 0, 0, 0
	var floats []int
	switch id {
	case protocol.BattleEventPairTransform:
		size = 115
		floats = []int{55, 59, 63, 67, 71, 75, 83, 87, 91, 95, 99, 103}
	case protocol.BattleEventSlip:
		size = 87
		floats = []int{55, 59, 63, 67, 71, 75, 79, 83}
	case protocol.BattleEventCollectibleSpawn:
		size = 63
		floats = []int{51, 55, 59}
	case protocol.BattleEventProjectileCreate:
		size, context, matrix = 131, 123, 55
	case protocol.BattleEventProjectileResult:
		size, context, matrix = 123, 115, 47
	case protocol.BattleEventProjectileHit:
		size, context, matrix = 123, 115, 51
	case protocol.BattleEventProjectileUpdate:
		size, context, matrix = 119, 111, 47
	case protocol.BattleEventProjectileRemove, protocol.BattleEventItemComplete:
		size, context = 51, 43
	default:
		return false, nil
	}
	bad := func(reason string) (bool, error) {
		return true, rejectBattle("battle extended id=%d uid=%d: %s", id, s.UID, reason)
	}
	if len(p) != size || protocol.ReadUint64(p, 4) != s.UID {
		return bad("length or sender")
	}
	if p[12] != 1 || p[13] != 1 {
		return true, nil
	}
	if context != 0 && (protocol.ReadUint32(p, context) != uint32(r.ID) || protocol.ReadUint32(p, context+4) != r.Serial) {
		return bad("stale context")
	}
	if matrix != 0 {
		for o := matrix; o < matrix+64; o += 4 {
			floats = append(floats, o)
		}
	}
	for _, o := range floats {
		v := float64(math.Float32frombits(protocol.ReadUint32(p, o)))
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return bad("nonfinite transform")
		}
	}
	key := battleEventKey{Kind: id, Actor: s.UID}
	sequence := protocol.ReadUint32(p, 19)
	m := r.Members[s.UID]
	if previous, ok := m.BattleEvents[key]; ok && int32(sequence-previous.Sequence) <= 0 {
		return true, nil
	}
	var commit func()
	switch id {
	case protocol.BattleEventItemComplete:
		if s.UID != r.Owner {
			return bad("item completion requires controller")
		}
		if r.ReliableSerial != r.Serial {
			return true, nil
		}
		object := protocol.ReadUint32(p, 39)
		for actor, exchange := range r.Reliable {
			member := r.Members[actor.UID]
			if actor.Family == 9000 && exchange.Completed && exchange.Object == object && member != nil && !member.Spectator {
				commit = func() { exchange.Completed = false }
				break
			}
		}
		if commit == nil {
			return true, nil
		}
	case protocol.BattleEventPairTransform:
		first, second := protocol.ReadUint64(p, 39), protocol.ReadUint64(p, 47)
		selection, ok := r.PairSelections[first]
		if first == second || second != s.UID || r.Members[first] == nil || r.Members[first].Spectator || !ok || selection.target != second || !time.Now().Before(selection.expires) {
			return true, nil
		}
	case protocol.BattleEventSlip:
		first, second := protocol.ReadUint64(p, 39), protocol.ReadUint64(p, 47)
		if !((first == s.UID && second == 0) || (first == 0 && second == s.UID) || (first == 0 && second == 0)) {
			return true, nil
		}
		for _, part := range []struct {
			uid     uint64
			offsets []int
		}{{first, []int{55, 59, 63, 79}}, {second, []int{67, 71, 75, 83}}} {
			if part.uid == 0 {
				for _, o := range part.offsets {
					if math.Float32frombits(protocol.ReadUint32(p, o)) != 0 {
						return bad("vector without actor")
					}
				}
			}
		}
	case protocol.BattleEventCollectibleSpawn:
		kind, object := protocol.ReadUint32(p, 39), protocol.ReadUint32(p, 43)
		if s.UID != r.Owner {
			return bad("spawn requires controller")
		}
		if kind < 1 || kind > 3 {
			return true, nil
		}
		family := kind
		if kind == 2 {
			family = 1
		}
		objectKey := [2]uint32{family, object}
		if r.Collectibles[objectKey] {
			return true, nil
		}
		if len(r.Collectibles) >= maxBattleObjects {
			return bad("collectible capacity")
		}
		commit = func() {
			if r.Collectibles == nil {
				r.Collectibles = map[[2]uint32]bool{}
			}
			r.Collectibles[objectKey] = true
		}
	case protocol.BattleEventProjectileCreate:
		source, object := protocol.ReadUint64(p, 39), protocol.ReadUint32(p, 47)
		if source != s.UID && !(s.UID == r.Owner && (source == 0 || r.Members[source] != nil && !r.Members[source].Spectator)) {
			return bad("projectile source")
		}
		if prior := r.Projectiles[object]; prior != nil {
			if prior.owner != s.UID {
				return bad("projectile key owner")
			}
			return true, nil
		}
		if len(r.Projectiles) >= maxBattleObjects {
			return bad("projectile capacity")
		}
		commit = func() {
			if r.Projectiles == nil {
				r.Projectiles = map[uint32]*projectileState{}
			}
			r.Projectiles[object] = &projectileState{owner: s.UID, alive: true, witnesses: map[uint64]bool{}}
		}
	default:
		object := protocol.ReadUint32(p, 39)
		prior := r.Projectiles[object]
		if prior == nil || !prior.alive {
			return true, nil
		}
		switch id {
		case protocol.BattleEventProjectileHit:
			if protocol.ReadUint64(p, 43) != s.UID {
				return bad("hit target")
			}
			commit = func() { prior.witnesses[s.UID] = true }
		case protocol.BattleEventProjectileResult:
			state := protocol.ReadUint32(p, 43)
			if state != 2 && state != 3 && state != 4 && state != 6 {
				return true, nil
			}
			if prior.owner != s.UID && !(state == 2 && prior.witnesses[s.UID]) {
				return bad("result without witness")
			}
			if prior.owner != s.UID {
				commit = func() { delete(prior.witnesses, s.UID) }
			}
		default:
			if prior.owner != s.UID {
				return bad("projectile update owner")
			}
			if id == protocol.BattleEventProjectileRemove {
				commit = func() { prior.alive = false }
			}
		}
	}
	if m.BattleEvents == nil {
		m.BattleEvents = map[battleEventKey]battleSequence{}
	}
	m.BattleEvents[key] = battleSequence{sequence, string(p)}
	if commit != nil {
		commit()
	}
	h.broadcast(r, msg, s.UID)
	return true, nil
}

// Called only after a P2P packet was actually queued to its target. A selection
// observed on UDP authorizes the same paired transform as a TCP selection.
func (r *Room) observeRelayedPairSelection(s, recipient *Session, body []byte) {
	if r.Stage != "battle" || r.isObserver(s) || r.isObserver(recipient) {
		return
	}
	var d protocol.Decoder
	messages, err := d.Feed(body)
	if err != nil || len(d.Buffer) != 0 || len(messages) != 1 {
		return
	}
	msg := messages[0]
	p := msg.Payload
	if msg.ID != protocol.MsgBattleEvent || len(p) != 59 || protocol.ReadUint32(p, 0) != protocol.BattleEventTargetSelection || protocol.ReadUint64(p, 4) != s.UID || protocol.ReadUint64(p, 39) != s.UID || p[12] != 1 || p[13] != 1 {
		return
	}
	target := protocol.ReadUint64(p, 47)
	if target != 0 && target != recipient.UID {
		return
	}
	r.recordPairSelection(s, p)
}
func (r *Room) retireBattleObjects(uid uint64) {
	delete(r.PairSelections, uid)
	for source, p := range r.PairSelections {
		if p.target == uid {
			delete(r.PairSelections, source)
		}
	}
	for _, p := range r.Projectiles {
		if p.owner == uid {
			p.alive = false
		}
		delete(p.witnesses, uid)
	}
}
