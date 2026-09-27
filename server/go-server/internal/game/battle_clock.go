package game

import (
	"kungfu.local/server/internal/protocol"
	"time"
)

// Native 8090 writes scene+0x84. Auto MP (0x9E64E0) runs when this
// counter exceeds entity+0x1B70. It is not a one-time boolean enable.
// One-second cadence is the local compatibility policy pending native traces.
const battleClockInterval = time.Second

func (h *Hub) startBattleClock(r *Room) {
	r.BattleClock = 1
	h.broadcast(r, protocol.Message{ID: protocol.MsgBattleClock, Payload: protocol.Uint32Bytes(r.BattleClock)}, 0)
	serial, started := r.Serial, r.BattleStartedAt
	go func() {
		ticker := time.NewTicker(battleClockInterval)
		defer ticker.Stop()
		for range ticker.C {
			h.lockState()
			h.scopeRoom(r)
			active := h.tickBattleClock(r, serial, started, time.Now())
			h.unlockState()
			if !active {
				return
			}
		}
	}()
}

// Caller holds Hub.Mutex. Old rounds and deleted/reused room IDs never send.
func (h *Hub) tickBattleClock(r *Room, serial uint32, started, now time.Time) bool {
	if h.Rooms[r.ID] != r || r.Stage != "battle" || r.Serial != serial ||
		!r.BattleStartedAt.Equal(started) || len(r.Members) == 0 {
		return false
	}
	if now.Before(started) {
		return true
	}
	elapsed := uint64(now.Sub(started)/battleClockInterval) + 1
	if elapsed > uint64(^uint32(0)) {
		return false
	}
	value := uint32(elapsed)
	if value > r.BattleClock {
		r.BattleClock = value
		h.broadcast(r, protocol.Message{ID: protocol.MsgBattleClock, Payload: protocol.Uint32Bytes(value)}, 0)
	}
	return true
}
