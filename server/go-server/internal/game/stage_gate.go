package game

import "kungfu.local/server/internal/persistence"

// Caller holds Hub.Mutex. Fetch current policy once per room action, and use
// authenticated account titles rather than any client-provided role fields.
func (h *Hub) stageGate(players ...*Session) (func(uint32) bool, error) {
	access, err := h.stageAccess()
	if err != nil {
		return nil, err
	}
	var request []byte
	if len(players) == 1 && players[0] != nil && players[0].Room != nil {
		request = players[0].Room.Request
	}
	return h.stageGateWithAccess(access, request, players...)
}

func (h *Hub) stageGateWithAccess(access persistence.StageAccess, request []byte, players ...*Session) (func(uint32) bool, error) {
	if !access.RequirementsEnabled {
		if (access.ForceOpenAll || len(access.ForceOpenMaps) > 0) && access.ClientHash != h.Config.ConfigHash {
			return nil, persistence.ErrDenied
		}
		return access.Allows, nil
	}
	if len(players) == 0 || access.ClientHash != h.Config.ConfigHash {
		return nil, persistence.ErrDenied
	}
	// Native new characters have title 0. mapmgr's title requirement for
	// training mountain is 1, so applying progression here creates a deadlock.
	// Restrict this exemption to the exact single-player guide request, for
	// both creation and start. Explicit closure and version checks still apply.
	if len(players) == 1 && players[0] != nil && players[0].UID != 0 && tutorialRequest(request) {
		return access.Allows, nil
	}
	minimum := byte(255)
	locked := map[uint32]bool{}
	needsUnlocks := false
	for _, rule := range access.Requirements {
		if rule.NeedsUnlock() {
			needsUnlocks = true
		}
	}
	for _, s := range players {
		if s == nil {
			return nil, persistence.ErrDenied
		}
		title, err := storage2_1(h, h.Store.TitleManager().AccountTitle, s.UID)
		if err != nil {
			return nil, err
		}
		if title < minimum {
			minimum = title
		}
		if needsUnlocks {
			progress, err := storage2_2(h, h.Store.StagePlayerUnlocks, s.UID, access.ClientHash)
			if err != nil {
				return nil, err
			}
			grants := map[uint32]bool{}
			for _, id := range progress.Maps {
				grants[id] = true
			}
			for _, rule := range access.Requirements {
				if !access.AllowsPlayer(rule.MapID, title, h.Config.ConfigHash, grants) {
					locked[rule.MapID] = true
				}
			}
		}
	}
	return func(id uint32) bool { return !locked[id] && access.AllowsTitle(id, minimum, h.Config.ConfigHash) }, nil
}

func roomPlayers(room *Room) []*Session {
	players := make([]*Session, 0, len(room.Members))
	for _, m := range room.Members {
		players = append(players, m.Session)
	}
	return players
}

func (h *Hub) resolveForPlayers(request []byte, players ...*Session) ([]byte, error) {
	access, err := h.stageAccess()
	if err != nil {
		return nil, err
	}
	if err := h.Config.ValidateStageAccess(access); err != nil {
		return nil, err
	}
	allows, err := h.stageGateWithAccess(access, request, players...)
	if err != nil {
		return nil, err
	}
	return h.resolveWithPolicy(request, allows, access)
}
