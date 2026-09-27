package game

import "kungfu.local/server/internal/protocol"

// Native spectator registry is separate from fighter slots 0..7.
const spectatorSlot byte = 8
const maxSpectators = 8

func (r *Room) observerLimit() int {
	if len(r.Request) != 81 || r.Request[34] == 0 {
		return 0
	}
	if r.SpectatorCapacity < 0 {
		return 0
	}
	if r.SpectatorCapacity > maxSpectators {
		return maxSpectators
	}
	return r.SpectatorCapacity
}
func (r *Room) observerCount() int {
	n := 0
	for _, m := range r.Members {
		if m.Spectator {
			n++
		}
	}
	return n
}
func (r *Room) fighterCount() int { return len(r.Members) - r.observerCount() }
func (r *Room) isObserver(s *Session) bool {
	m := r.Members[s.UID]
	return m != nil && m.Session == s && m.Spectator
}
func (r *Room) firstFighter() *Member {
	var first *Member
	for _, m := range r.Members {
		if !m.Spectator && (first == nil || m.Slot < first.Slot) {
			first = m
		}
	}
	return first
}
func (h *Hub) closeObserverOnlyRoom(r *Room) {
	if r.LoadTimer != nil {
		r.LoadTimer.Stop()
	}
	for uid, m := range r.Members {
		m.Session.Room = nil
		m.Session.game().Phase = "lobby"
		m.Session.sendGame(protocol.Message{ID: protocol.MsgRoomLeft})
		delete(r.Members, uid)
	}
	delete(h.Rooms, r.ID)
}
func (h *Hub) advanceRoomLoading(r *Room) {
	if r.Stage != "loading" {
		return
	}
	for _, m := range r.Members {
		if !m.Loaded {
			return
		}
	}
	r.Stage = "wait_ready"
	h.sendInitialPVEBlocks(r)
	if r.FosterPositions != nil {
		h.broadcast(r, protocol.Message{ID: protocol.MsgBattleEvent, Payload: r.FosterPositions}, 0)
	}
	for _, m := range r.Members {
		m.Session.game().Phase = "wait_ready"
		// Observer loading already initializes its mode. Reinitializing via 4180 breaks it.
		if !m.Spectator {
			m.Session.sendGame(protocol.Message{ID: protocol.MsgAllResourcesReady})
		}
	}
}
func (h *Hub) toggleSpectator(s *Session) error {
	r := s.Room
	replyError := func(code int32) {
		p := append(protocol.Uint64Bytes(s.UID), protocol.Uint32Bytes(uint32(code))...)
		s.sendGame(protocol.Message{ID: protocol.MsgSpectatorChanged, Payload: p})
	}
	if !r.canConfigure(s) {
		replyError(-3)
		return nil
	}
	old := r.Members[s.UID]
	next := *old
	next.Spectator = !old.Spectator
	next.Ready = false
	if next.Spectator {
		if r.observerCount() >= r.observerLimit() || r.fighterCount() <= 1 {
			replyError(-2)
			return nil
		}
		next.Slot, next.Spawn = spectatorSlot, spectatorSlot
	} else {
		if r.fighterCount() >= int(r.Request[protocol.RoomCapacityOffset]) {
			replyError(-1)
			return nil
		}
		for slot := byte(0); slot < spectatorSlot; slot++ {
			used := false
			for _, m := range r.Members {
				if !m.Spectator && m.Slot == slot {
					used = true
				}
			}
			if !used {
				next.Slot = slot
				break
			}
		}
		position, ok := r.freeTeamPosition(next.Team, s.UID)
		if !ok {
			next.Team ^= 1
			position, ok = r.freeTeamPosition(next.Team, s.UID)
		}
		if !ok {
			replyError(-1)
			return nil
		}
		next.Spawn = position
	}
	account, err := storage2_1(h, h.Store.RoleManager().Snapshot, s.UID)
	if err != nil {
		return err
	}
	p := make([]byte, 16)
	protocol.WriteUint64(p, 0, s.UID)
	if next.Spectator {
		p[12] = 1
	}
	p = append(p, fighter(account, &next)...)
	// Membership changes invalidate a pending start, not other fighters' readiness.
	h.cancelNetworkProbe(r)
	h.cancelSeatExchange(r)
	if old.Ready {
		h.broadcast(r, protocol.Message{ID: protocol.MsgPlayerNotReady, Payload: protocol.Uint64Bytes(s.UID)}, 0)
	}
	r.Members[s.UID] = &next
	h.broadcast(r, protocol.Message{ID: protocol.MsgSpectatorChanged, Payload: p}, 0)
	if r.Owner == s.UID && next.Spectator {
		owner := r.firstFighter()
		r.Owner = owner.Session.UID
		if owner.Ready {
			owner.Ready = false
			h.broadcast(r, protocol.Message{ID: protocol.MsgPlayerNotReady, Payload: protocol.Uint64Bytes(r.Owner)}, 0)
		}
		h.broadcast(r, protocol.Message{ID: protocol.MsgRoomOwner, Payload: protocol.Uint64Bytes(r.Owner)}, 0)
	}
	return nil
}

func observerProbe(body []byte, uid uint64) bool {
	if len(body) != 48 {
		return false
	}
	var d protocol.Decoder
	messages, err := d.Feed(body)
	if err != nil || len(d.Buffer) != 0 || len(messages) != 1 || messages[0].ID != peerLatencyProbeID || len(messages[0].Payload) != peerLatencyProbePayloadSize {
		return false
	}
	p := messages[0].Payload
	return protocol.ReadUint64(p, 0) == uid || protocol.ReadUint64(p, 8) == uid
}

// Native 4115 confirms that 4120 was consumed, including by observers. Keep
// 3550/3110 compatibility, but do not wait for a fighter-only action from them.
func (h *Hub) advanceResultAcknowledgements(r *Room) {
	if r.Stage != "settlement" && r.Stage != "room" {
		return
	}
	changed := false
	for _, m := range r.Members {
		phase := m.Session.game().Phase
		if phase == "room" {
			continue
		}
		if phase != "settlement" || !m.ResultAcknowledged {
			return
		}
		changed = true
	}
	if !changed {
		return
	}
	r.Stage = "room"
	for uid, m := range r.Members {
		m.Session.game().Phase = "room"
		m.Ready = false
		if !m.Spectator {
			h.broadcast(r, protocol.Message{ID: protocol.MsgPlayerNotReady, Payload: protocol.Uint64Bytes(uid)}, 0)
		}
	}
}
