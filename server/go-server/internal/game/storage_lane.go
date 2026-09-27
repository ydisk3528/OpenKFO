package game

import (
	"bytes"
	"kungfu.local/server/internal/protocol"
	"kungfu.local/server/internal/tunnel"
	"sync"
	"time"
)

// Non-combat state changes remain serialized across storage waits. Only checked,
// storage-free combat messages in OTHER rooms may run during that interval.
// The current operation and its room are frozen, preserving transaction/reply order.
type storageScope struct {
	session   *Session
	room      *Room
	exclusive bool
	ioTime    time.Duration
}

func (h *Hub) storageCond() *sync.Cond {
	if h.ioCond == nil {
		h.ioCond = sync.NewCond(&h.Mutex)
	}
	return h.ioCond
}
func (h *Hub) lockState() {
	h.Mutex.Lock()
	for h.ioPaused {
		h.storageCond().Wait()
	}
	h.ioScope = &storageScope{exclusive: true}
}
func (h *Hub) unlockState() { h.ioScope = nil; h.Mutex.Unlock() }
func (h *Hub) scopeRoom(r *Room) {
	if h.ioScope != nil {
		h.ioScope.room = r
		h.ioScope.exclusive = false
	}
}
func (h *Hub) scopeSession(s *Session) {
	if h.ioScope != nil {
		h.ioScope.session = s
		h.ioScope.exclusive = false
		if s != nil {
			h.ioScope.room = s.Room
		}
	}
}

func (h *Hub) lockFrame(s *Session, f tunnel.Frame) bool {
	h.Mutex.Lock()
	for h.ioPaused && !h.storageFreeFrame(s, f) {
		h.storageCond().Wait()
	}
	owner := !h.ioPaused
	if owner {
		h.ioScope = &storageScope{session: s, room: s.Room}
	}
	return owner
}
func (h *Hub) yieldStorage() func() {
	// Direct helper tests have no dispatcher scope and keep synchronous behavior.
	if h.ioScope == nil {
		return func() {}
	}
	if h.ioPaused {
		panic("storage call reached combat-only lane")
	}
	scope := h.ioScope
	started := time.Now()
	h.ioPaused = true
	h.Mutex.Unlock()
	return func() {
		h.Mutex.Lock()
		scope.ioTime += time.Since(started)
		h.ioPaused = false
		h.storageCond().Broadcast()
	}
}
func (h *Hub) storageFreeFrame(s *Session, f tunnel.Frame) bool {
	scope := h.ioScope
	if scope == nil || scope.exclusive || s == scope.session || h.Sessions[s.UID] != s || s.LoggedOut || s.Room == nil || s.Room == scope.room || (scope.session != nil && s.Room == scope.session.Room) || s.Room.Stage != "battle" {
		return false
	}
	return roomCombatFrame(s, f)
}

// Fast combat is serialized per room. The shared registry read guard freezes
// membership/channel lifetime, but does not exclude combat in another room.
// Never wait for a busy room while holding the registry guard: it would delay
// unrelated rooms behind a queued membership writer.
func (h *Hub) lockRoomFrame(s *Session, f tunnel.Frame) func() {
	if f.Op != "data" && f.Op != "udp" && f.Op != "ping" {
		return nil
	}
	h.Mutex.RLock()
	r := s.Room
	h.Mutex.RUnlock()
	if r == nil {
		return nil
	}
	r.combatMutex.Lock()
	h.Mutex.RLock()
	valid := h.Sessions[s.UID] == s && !s.LoggedOut && s.Room == r && r.Stage == "battle"
	if valid && h.ioPaused {
		valid = h.storageFreeFrame(s, f)
	} else if valid {
		valid = roomCombatFrame(s, f)
	}
	if !valid {
		h.Mutex.RUnlock()
		r.combatMutex.Unlock()
		return nil
	}
	return func() { h.Mutex.RUnlock(); r.combatMutex.Unlock() }
}

func roomCombatFrame(s *Session, f tunnel.Frame) bool {
	if f.Op == "ping" {
		return true
	}
	var d protocol.Decoder
	body := f.Data
	udp := f.Op == "udp"
	if udp {
		if len(body) < 24 {
			return false
		}
		id := protocol.ReadUint16(body, 2)
		if id == 1013 {
			return true
		}
		if id != 1008 {
			return false
		}
		offset := 24 + int(body[23])
		if offset >= len(body) {
			return false
		}
		body = body[offset:]
	} else {
		if f.Op != "data" {
			return false
		}
		c := s.Channels[f.Channel]
		if c == nil || c.Kind != "game" || c.Phase != "battle" {
			return false
		}
		d.Buffer = bytes.Clone(c.Decoder.Buffer)
	}
	messages, err := d.Feed(body)
	// Invalid/fragmented UDP payloads cannot enter observeRelayedBattle.
	if udp && (err != nil || len(d.Buffer) > 0 || len(body) > 1024 || len(messages) > 8) {
		return true
	}
	if err != nil || len(d.Buffer) > 0 || len(messages) == 0 {
		return false
	}
	for _, m := range messages {
		if m.ID == 0 || (udp && m.ID == 10000) {
			continue
		}
		if m.ID != protocol.MsgBattleEvent || len(m.Payload) < 4 {
			return false
		}
		switch protocol.ReadUint32(m.Payload, 0) {
		case protocol.BattleEventMovement, protocol.BattleEventHealth, protocol.BattleEventState,
			protocol.BattleEventSkillEffect, protocol.BattleEventMana, protocol.BattleEventAction,
			protocol.BattleEventBuff, protocol.BattleEventActionArgument, protocol.BattleEventTargetSelection,
			protocol.BattleEventTargetAction, protocol.BattleEventActorValue, protocol.BattleEventPairTransform,
			protocol.BattleEventSlip, protocol.BattleEventCollectibleSpawn, protocol.BattleEventProjectileCreate,
			protocol.BattleEventProjectileResult, protocol.BattleEventProjectileHit, protocol.BattleEventProjectileUpdate,
			protocol.BattleEventProjectileRemove, protocol.BattleEventPVEActorCreate, protocol.BattleEventPVEActorRemove,
			protocol.BattleEventPVEBlockCreate, protocol.BattleEventPVEBlockRemove,
			protocol.BattleEventDeathCountdown, protocol.BattleEventDeathTerminal, protocol.BattleEventReborn,
			9000, 9001, 9002, 9500, 9501, 9502, 20406:
		default:
			return false
		}
	}
	return true
}
