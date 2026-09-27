package game

import (
	"time"

	"kungfu.local/server/internal/protocol"
)

func (r *Room) cooperativePVE() bool {
	return r != nil && (r.Type() == protocol.FosterMode || r.Type() == protocol.StageAssault)
}

func (r *Room) initialTeam(slot byte) byte {
	if r.cooperativePVE() {
		return 0
	}
	return slot % 2
}

// NPCs are assigned an opposing team by the client. Cooperative players must
// therefore share a team, including on the AI controller's client.
func (h *Hub) normalizePVETeams(r *Room) {
	if !r.cooperativePVE() {
		return
	}
	for _, m := range r.Members {
		if !m.Spectator && m.Team != 0 {
			m.Team = 0
			h.broadcast(r, protocol.Message{ID: 3250, Payload: roomTeam(m)}, 0)
		}
	}
}

// Reason 3 alone is not trusted as a timeout. Only the server's battle clock
// and this room's configured time limit authorize a failed PVE settlement.
func pveDeadlineReached(r *Room, now time.Time) bool {
	if !r.cooperativePVE() || len(r.Request) < 49 || r.BattleStartedAt.IsZero() {
		return false
	}
	limit := protocol.ReadUint16(r.Request, 47)
	return limit > 0 && !now.Before(r.BattleStartedAt.Add(time.Duration(limit)*time.Second))
}
