package game

import (
	"kungfu.local/server/internal/protocol"
	"sort"
)

// 8157 is an observation from the elected native controller, not an instruction
// to grant persistent points or respawn an actor in the server.
func (h *Hub) rebornEvent(s *Session, m protocol.Message) error {
	p, r := m.Payload, s.Room
	if r.Type() != protocol.RebornMode {
		return nil
	}
	if len(p) != 55 || protocol.ReadUint64(p, 4) != s.UID {
		return rejectBattle("reborn event length or sender")
	}
	if s.UID != r.Owner || p[12] != 1 || p[13] != 1 {
		return nil
	}
	target := protocol.ReadUint64(p, 39)
	if actor := r.Members[target]; actor == nil || actor.Spectator {
		return rejectBattle("reborn participant outside fighters")
	}
	code, value := protocol.ReadUint32(p, 47), protocol.ReadUint32(p, 51)
	valid := (code <= 2 && value == 0) || ((code == 3 || code == 6) && value == 3) || (code == 4 && value == 4) || (code == 5 && value >= 5 && value <= 65535) || (code == 7 && value >= 4 && value <= 65535)
	if !valid {
		return nil
	}
	member := r.Members[s.UID]
	key := battleEventKey{Kind: protocol.BattleEventReborn, Actor: target}
	sequence := protocol.ReadUint32(p, 19)
	if previous, ok := member.BattleEvents[key]; ok && int32(sequence-previous.Sequence) <= 0 {
		return nil
	}
	if member.BattleEvents == nil {
		member.BattleEvents = map[battleEventKey]battleSequence{}
	}
	member.BattleEvents[key] = battleSequence{Sequence: sequence, Payload: string(p)}
	h.broadcast(r, m, s.UID)
	return nil
}

func rebornResultRows(r *Room) (map[uint64]protocol.BattleReportRecord, map[uint64]uint32) {
	result := map[uint64]protocol.BattleReportRecord{}
	ranks := map[uint64]uint32{}
	if _, err := validateBattleReport(r, r.Reports[r.Owner]); err != nil {
		return result, ranks
	}
	rows, _ := protocol.ParseBattleReport(r.Reports[r.Owner])
	var ids []uint64
	for _, row := range rows {
		if member := r.Members[row.UID]; member != nil && !member.Spectator {
			result[row.UID] = row
			ids = append(ids, row.UID)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := result[ids[i]], result[ids[j]]
		if a.RebornPoints != b.RebornPoints {
			return a.RebornPoints > b.RebornPoints
		}
		return ids[i] < ids[j]
	})
	for i, uid := range ids {
		ranks[uid] = uint32(i + 1)
	}
	return result, ranks
}
