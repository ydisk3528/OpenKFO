package game

import (
	"errors"
	"log"
	"math"
	"time"

	"kungfu.local/server/internal/protocol"
)

type battleSequence struct {
	Sequence uint32
	Payload  string
}

type battleEventKey struct {
	Kind  uint32
	Actor uint64
}

// Layouts verified against gfld.dat's native dispatch handlers. In particular,
// 8121 carries damage/healing (82A9D0); 8122 carries an integer state 0..7
// consumed by 82A8B0 -> 9F2DC0, not an absolute HP float.
// Do not infer that an unknown packet is safe to broadcast from its size alone.
func (hub *Hub) battleMessage(session *Session, channel *Channel, message protocol.Message) (err error) {
	// Parse/field validation errors in delegated battle handlers also drop
	// just this decoded packet. Other errors retain their original semantics.
	defer func() {
		if errors.Is(err, protocol.ErrFrame) {
			err = rejectBattle("battle packet validation: %v", err)
		}
	}()
	room, payload := session.Room, message.Payload
	if room != nil && room.isObserver(session) {
		return nil
	}
	var ownershipNotice error
	if len(payload) >= 4 && protocol.ReadUint32(payload, 0) == protocol.BattleEventFosterPositions {
		return hub.fosterPositions(session, channel, payload)
	}
	if len(payload) >= 4 && (protocol.ReadUint32(payload, 0) == protocol.BattleEventPVEBlockCreate || protocol.ReadUint32(payload, 0) == protocol.BattleEventPVEBlockRemove) {
		return hub.pveBlockMessage(session, channel, message)
	}
	if room == nil || room.Stage != "battle" || channel.Phase != "battle" {
		return nil
	}
	member := room.Members[session.UID]
	if member == nil || member.Session != session || len(payload) < 39 {
		return rejectBattle("battle packet fields invalid")
	}
	id := protocol.ReadUint32(payload, 0)
	if id == neutralNPCRequest {
		return hub.neutralNPCRequest(session, message)
	}
	if id == neutralNPCReady {
		return hub.neutralNPCReady(session, message)
	}
	if id >= seriesInterval && id <= seriesFinish {
		return hub.seriesEvent(session, message, nil)
	}
	if id == protocol.BattleEventStageWaveEnd {
		return hub.pveFinishEvent(session, message)
	}
	if id == protocol.BattleEventPVEActorCreate || id == protocol.BattleEventPVEActorRemove {
		return hub.pveActorMessage(session, message)
	}
	if (id >= 9000 && id <= 9002) || (id >= 9500 && id <= 9502) {
		return hub.reliableBattleEvent(session, message)
	}
	if id == protocol.BattleEventReborn {
		return hub.rebornEvent(session, message)
	}
	if id == protocol.BattleEventDeathCountdown || id == protocol.BattleEventDeathTerminal {
		return hub.deathNotice(session, message)
	}
	if handled, err := hub.extendedBattleEvent(session, message); handled {
		return err
	}
	length, contextOffset := 0, 0
	actorOffset := 39
	var floats []int
	switch id {
	case protocol.BattleEventActionArgument:
		length = 53
	case protocol.BattleEventTargetSelection:
		length = 59
	case protocol.BattleEventTargetAction:
		length = 55
	case protocol.BattleEventWeaponOperation:
		length, actorOffset, contextOffset = 75, 59, 67
	case protocol.BattleEventActorValue:
		length = 51
	case protocol.BattleEventMovement:
		length, floats = 108, []int{51, 55, 59, 63, 67, 71, 87, 91}
	case protocol.BattleEventHealth:
		length, contextOffset, floats = 94, 86, []int{67, 72, 76, 80}
	case protocol.BattleEventState:
		length = 51
	case protocol.BattleEventMana:
		// AddMP (9DA2F0) sends delta +47 and resulting MP +51. Native
		// 827D00 checks the +55 context and applies +51 via 9E4470.
		length, contextOffset, floats = 63, 55, []int{47, 51}
	case protocol.BattleEventSkillEffect:
		// 82B8D0 resolves source at 47 and target at 55 before applying
		// the skill's effect list. There is no room trailer in this packet.
		length, actorOffset = 71, 55
	case protocol.BattleEventAction:
		length, contextOffset, floats = 103, 95, []int{63, 67, 71}
	case protocol.BattleEventBuff:
		length, contextOffset = 87, 79 // +67..74 are opaque, not verified floating-point values.
	case protocol.BattleEventScoreboard:
		// Server authority policy: only the room owner may publish the whole
		// scoreboard. Native local control flag is not an authentication grant.
		if room.Owner != session.UID {
			return nil
		}
		length = 334
	case 8440:
		length = 71
	case 8441, 8451:
		length = 47
	case 8450:
		length, contextOffset = 59, 51
	default:
		if time.Since(session.LastBattleNotice) > time.Second {
			log.Printf("battle_unhandled uid=%d room=%d id=%d bytes=%d", session.UID, room.ID, id, len(payload))
			session.LastBattleNotice = time.Now()
		}
		return nil
	}
	if len(payload) != length {
		return rejectBattle("battle envelope id=%d uid=%d bytes=%d", id, session.UID, len(payload))
	}
	sender := protocol.ReadUint64(payload, 4)
	if id == protocol.BattleEventMovement {
		// 82B230 resolves the moving entity from +4, not +39. The room
		// controller can send movement for a registered monster only.
		if actor, known := room.PVEActors[sender]; (room.Type() == protocol.StageAssault || room.Type() == protocol.FosterMode) && known && !actor.active {
			return nil
		}
		if !room.controlsBattleActor(session, sender) {
			return rejectBattle("battle movement ownership uid=%d actor=%d", session.UID, sender)
		}
	} else if sender != session.UID {
		return rejectBattle("battle sender id=%d uid=%d actor=%d", id, session.UID, sender)
	}
	if id == protocol.BattleEventScoreboard {
		if room.Type() == protocol.RebornMode && (payload[12] != 1 || payload[13] != 1) {
			return nil
		}
		var slots [8]uint64
		for uid, m := range room.Members {
			if m.Spectator {
				continue
			}
			if m.Slot >= 8 || slots[m.Slot] != 0 {
				return rejectBattle("battle packet fields invalid")
			}
			slots[m.Slot] = uid
		}
		for slot, uid := range slots {
			record := payload[46+slot*36 : 46+(slot+1)*36]
			if protocol.ReadUint64(record, 0) != uid {
				return rejectBattle("battle scoreboard roster slot=%d", slot)
			}
			// The native producer zeroes empty slots. Do not forward stale
			// counters for a vacant slot into the recipient's local score array.
			if uid == 0 {
				for _, value := range record {
					if value != 0 {
						return rejectBattle("battle packet fields invalid")
					}
				}
			}
		}
	}
	if contextOffset != 0 && (protocol.ReadUint32(payload, contextOffset) != uint32(room.ID) || protocol.ReadUint32(payload, contextOffset+4) != room.Serial) {
		return rejectBattle("battle context id=%d uid=%d", id, session.UID)
	}
	for _, offset := range floats {
		value := float64(math.Float32frombits(protocol.ReadUint32(payload, offset)))
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return rejectBattle("battle packet fields invalid")
		}
	}
	if id == protocol.BattleEventTargetSelection || id == protocol.BattleEventTargetAction {
		target := protocol.ReadUint64(payload, 47)
		if target != 0 && (room.Members[target] == nil || room.Members[target].Spectator) {
			return rejectBattle("battle target not a fighter id=%d target=%d", id, target)
		}
	}
	if id == protocol.BattleEventWeaponOperation {
		op := protocol.ReadUint32(payload, 39)
		if op < 1 || op > 3 {
			return nil
		}
	}
	if id == protocol.BattleEventBuff {
		if protocol.ReadUint32(payload, 75) > 1 {
			return rejectBattle("unsupported buff operation")
		}
	}
	if id == 8122 && protocol.ReadUint32(payload, 47) > 7 {
		return rejectBattle("battle packet fields invalid")
	}
	if id != 8120 && id != 8155 {
		actor := protocol.ReadUint64(payload, actorOffset)
		if room.stalePVEEvent(session, actor, protocol.ReadUint32(payload, 19)) {
			return nil
		}
		// Keep unknown local/tutorial entities isolated. The known practice
		// dummy is shared: its controller's effects must reach the other clients.
		if room.Members[actor] == nil && !room.hasPracticeDummy(actor) && (tutorialRoom(room) || (len(room.Request) > 46 && room.Type() == protocol.FreePractice)) {
			return nil
		}
		if room.Members[actor] != nil && room.Members[actor].Spectator {
			return nil
		}
		if room.Members[actor] == nil && !room.hasPVEActor(actor) && !room.hasPracticeDummy(actor) {
			return rejectBattle("battle unknown actor id=%d actor=%d", id, actor)
		}
		// The controller reports damage to the dummy even when another player
		// caused it. Accepting a second replica's report would double the damage.
		if room.hasPracticeDummy(actor) && session.UID != room.Owner {
			return nil
		}
		if id == protocol.BattleEventHealth || id == protocol.BattleEventSkillEffect || id == protocol.BattleEventBuff {
			attacker := protocol.ReadUint64(payload, 47)
			if attacker != 0 && room.stalePVEEvent(session, attacker, protocol.ReadUint32(payload, 19)) {
				return nil
			}
			// Native replicas also report expiration of a remote actor's buff.
			// Cleanup clears the source and parameters; it is not a new attack.
			cleanup := id == protocol.BattleEventBuff && attacker == 0 && protocol.ReadUint32(payload, 55) != 0
			if cleanup {
				for _, offset := range []int{59, 63, 75} {
					cleanup = cleanup && protocol.ReadUint32(payload, offset) == 0
				}
			}
			if attacker != 0 && room.Members[attacker] == nil && !room.hasPracticeDummy(attacker) && (tutorialRoom(room) || (len(room.Request) > 46 && room.Type() == protocol.FreePractice)) {
				return nil
			}
			if attacker != 0 && room.Members[attacker] == nil && !room.hasPVEActor(attacker) && !room.hasPracticeDummy(attacker) {
				return rejectBattle("battle unknown effect source id=%d uid=%d target=%d source=%d", id, session.UID, actor, attacker)
			}
			if !cleanup && !room.controlsBattleActor(session, actor) && !room.controlsBattleActor(session, attacker) {
				notice := rejectBattle("battle effect ownership id=%d uid=%d target=%d source=%d", id, session.UID, actor, attacker)
				// Explicit compatibility policy: authenticated replicas may
				// report another known room actor's BUFF. Audit and relay it.
				// Damage/skill execution authority and unknown actors remain checked.
				if id != protocol.BattleEventBuff || attacker == 0 {
					return notice
				}
				ownershipNotice = notice
			}
		} else if id == protocol.BattleEventAction && protocol.ReadUint32(payload, 47) == 10 && (protocol.ReadUint32(payload, 51) == 3 || protocol.ReadUint32(payload, 51) == 4) {
			// Native 98AFF0 is gated by the scene controller (44B3F0), then
			// scans all eight players. 98B103/98B20D publish object interactions
			// for the affected player at +39, not necessarily the sender.
			// Only the room controller may report these cross-player actions.
			if session.UID != room.Owner || room.Members[actor] == nil {
				return nil
			}
		} else if !room.controlsBattleActor(session, actor) {
			return rejectBattle("battle actor id=%d actor=%d expected=%d", id, actor, session.UID)
		}
	}
	// A controller can report the same event kind for several PVE entities.
	// Track targets independently so reordering one entity cannot drop another.
	key := battleEventKey{Kind: id}
	if id == protocol.BattleEventMovement {
		key.Actor = sender
	} else if id != protocol.BattleEventScoreboard {
		key.Actor = protocol.ReadUint64(payload, actorOffset)
	}
	sequence := protocol.ReadUint32(payload, 19)
	if id == protocol.BattleEventMovement {
		// 7D1520 uses its motion counter at +15 (7D0EF0/7D0EE0),
		// not A3FBB0's event counter at +19. These domains cannot be compared.
		sequence = protocol.ReadUint32(payload, 15)
	}
	if member.BattleEvents == nil {
		member.BattleEvents = make(map[battleEventKey]battleSequence)
	}
	previous, seen := member.BattleEvents[key]
	// A3FBB0 -> 7D1730 assigns a new event counter to every native 8121.
	// A changed amount/effect at the same sequence is not another health or
	// BUFF/skill event. Native generic effects advance +19 for each event.
	if seen && ((sequence == previous.Sequence && (id == protocol.BattleEventHealth || id == protocol.BattleEventBuff || id == protocol.BattleEventSkillEffect || string(payload) == previous.Payload)) || int32(sequence-previous.Sequence) < 0) {
		return nil
	}
	member.BattleEvents[key] = battleSequence{sequence, string(payload)}
	if id == protocol.BattleEventTargetSelection {
		room.recordPairSelection(session, payload)
	}
	if id == protocol.BattleEventHealth {
		room.trackFosterHealth(payload)
	}
	if id == protocol.BattleEventMovement && room.Members[sender] != nil {
		// 7D16BA copies actor position (9E42E0) to +51; the consumer
		// passes absolute=true to 9E9D10. +63 is a different vector.
		room.triggerFosterGroups([3]float32{
			math.Float32frombits(protocol.ReadUint32(payload, 51)),
			math.Float32frombits(protocol.ReadUint32(payload, 55)),
			math.Float32frombits(protocol.ReadUint32(payload, 59)),
		})
	}
	if ownershipNotice != nil {
		hub.recordSecurityAudit(session, channel, message, ownershipNotice, true)
	}
	hub.broadcast(room, message, session.UID)
	if id == 8121 || id == 8122 {
		if time.Since(session.LastBattleNotice) > time.Second {
			log.Printf("battle_health_relay uid=%d room=%d id=%d actor=%d recipients=%d", session.UID, room.ID, id, protocol.ReadUint64(payload, 39), len(room.Members)-1)
			session.LastBattleNotice = time.Now()
		}
	}
	return nil
}
