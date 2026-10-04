package game

import (
	"crypto/sha256"
	"fmt"
	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
)

func (h *Hub) stageSelection(s *Session, payload []byte) error {
	if len(payload) != 0 {
		s.sendGame(notice("关卡查询格式不正确。"))
		return nil
	}
	s.StageViewReady = true
	view, err := storage2_2(h, h.Store.StagePlayerView, s.UID, h.Config.ConfigHash)
	if err != nil {
		s.sendGame(notice("关卡信息读取失败，请稍后重试。"))
		return nil
	}
	if !view.Configured {
		s.sendGame(notice("尚未配置当前客户端的PVE地图目录。"))
		return nil
	}
	return h.sendStageSelection(s, view, true)
}

// All policies come from one DB snapshot. A second policy read could mix an
// old player's projection with a new force-open setting.
func stageSelectionPayload(config Config, view persistence.StagePlayerView) ([]byte, error) {
	supported := map[uint32]bool{}
	// Match resolveWithAllowed's implemented modes/capacities, not arbitrary
	// pool keys. This must not implicitly open unimplemented PVE game logic.
	for _, mode := range []int{0, 1, 2, 3, 5} {
		for _, size := range []int{2, 4, 6, 8} {
			for _, id := range config.Pools[fmt.Sprintf("%d:%d", mode, size)] {
				supported[id] = true
			}
		}
	}
	if config.ConfigHash != "" && view.Access.ClientHash == config.ConfigHash && view.Access.Validate() == nil {
		// PVE admission uses persisted plans, not competitive map pools. Reuse
		// the same validators as room creation for at least one supported size.
		// Validate this immutable snapshot once, rather than once per map/size.
		for _, plan := range view.Access.FosterPlans {
			for players := 1; players <= 8; players++ {
				if _, err := validatedFosterPlan(view.Access, plan.MapID, players); err == nil {
					supported[plan.MapID] = true
					break
				}
			}
		}
		for _, plan := range view.Access.WavePlans {
			for players := 1; players <= 8; players++ {
				if _, err := validatedStagePlan(view.Access, plan.MapID, players); err == nil {
					supported[plan.MapID] = true
					break
				}
			}
		}
	}
	forced := map[uint32]bool{}
	for _, id := range view.ForcedMaps {
		forced[id] = true
	}
	ids := []uint32{}
	for _, id := range view.Maps {
		if supported[id] || forced[id] {
			ids = append(ids, id)
		}
	}
	// Reference trial: 21370 -> 21371, reserved DWORD + NUL-terminated map CSV.
	p, err := (protocol.StageProgress{MapIDs: ids}).Encode()
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (h *Hub) sendStageSelection(s *Session, view persistence.StagePlayerView, explicit bool) error {
	config := h.Config
	var p []byte
	var err error
	if !h.readSessionSnapshot(s, func() { p, err = stageSelectionPayload(config, view) }) || h.Config.ConfigHash != config.ConfigHash {
		return nil
	}
	if err != nil {
		return err
	}
	return sendStageSelectionPayload(s, p, explicit)
}

func sendStageSelectionPayload(s *Session, p []byte, explicit bool) error {
	digest := sha256.Sum256(p)
	if !explicit && s.StageViewRequested && s.StageViewDigest == digest {
		return nil
	}
	s.sendGame(protocol.Message{ID: protocol.MsgStageSelectionReply, Payload: p})
	s.StageViewRequested = true
	s.StageViewDigest = digest
	return nil
}

func (h *Hub) refreshStageSelection(s *Session) error {
	// Before the native lobby UI is initialized, the packet can be discarded
	// or overwritten. Do not mark that early send as a delivered cache view.
	if !s.StageViewReady {
		return nil
	}
	view, err := storage2_2(h, h.Store.StagePlayerView, s.UID, h.Config.ConfigHash)
	if err != nil {
		return err
	}
	// Some native map selectors never request 21370. Initialize their cache
	// proactively; subsequent refreshes are deduplicated by the view digest.
	if !view.Configured && !s.StageViewRequested {
		return nil
	}
	return h.sendStageSelection(s, view, false)
}
