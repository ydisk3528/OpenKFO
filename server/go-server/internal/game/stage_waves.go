package game

import (
	"fmt"
	"kungfu.local/server/internal/protocol"
)

// Counts must come from the matching map's script/template catalogue. No
// default wave count is inferred from another map (such as zombie defence).
type StageWavePlan = protocol.StageWavePlan

// Map scripts may select different spawn groups for the current player count.
// Ranges come from that script; no universal 2/4-player thresholds are assumed.
type StageWaveVariant = protocol.StageWaveVariant

func (c Config) stagePlan(mapID uint32, players int) (*stageWaves, error) {
	if players < 1 || players > 8 {
		return nil, fmt.Errorf("PVE player count must be 1–8")
	}
	variants, configured := c.StageWaveVariants[mapID]
	if !configured {
		return newStageWaves(c.StageWaves[mapID])
	}
	var selected *stageWaves
	var occupied [9]bool
	for _, variant := range variants {
		if variant.MinPlayers < 1 || variant.MaxPlayers > 8 || variant.MinPlayers > variant.MaxPlayers {
			return nil, fmt.Errorf("invalid PVE player range")
		}
		for n := variant.MinPlayers; n <= variant.MaxPlayers; n++ {
			if occupied[n] {
				return nil, fmt.Errorf("overlapping PVE player ranges")
			}
			occupied[n] = true
		}
		plan, err := newStageWaves(variant.Waves)
		if err != nil {
			return nil, err
		}
		if players >= variant.MinPlayers && players <= variant.MaxPlayers {
			selected = plan
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("PVE map has no verified plan for %d players", players)
	}
	return selected, nil
}

type stageWaves struct {
	plans    []StageWavePlan
	index    int
	spawned  map[uint32]uint32
	finished bool
}

func newStageWaves(plans []StageWavePlan) (*stageWaves, error) {
	if len(plans) == 0 {
		return nil, fmt.Errorf("PVE map has no verified wave plan")
	}
	w := &stageWaves{spawned: map[uint32]uint32{}}
	for _, plan := range plans {
		if len(plan.Monsters) == 0 {
			return nil, fmt.Errorf("PVE wave has no monsters")
		}
		copyPlan := StageWavePlan{Monsters: map[uint32]uint32{}}
		for template, count := range plan.Monsters {
			if count == 0 {
				return nil, fmt.Errorf("PVE monster count must be positive")
			}
			copyPlan.Monsters[template] = count
		}
		w.plans = append(w.plans, copyPlan)
	}
	return w, nil
}

func (w *stageWaves) canSpawn(template uint32) bool {
	return !w.finished && w.spawned[template] < w.plans[w.index].Monsters[template]
}

// A departing controller dissolves the stage; ordinary peers keep playing.
// Reuse ordinary leave cleanup, without DB reads or PvP re-entry.
func (h *Hub) abortStageRoom(r *Room) {
	if r.LoadTimer != nil {
		r.LoadTimer.Stop()
		r.LoadTimer = nil
	}
	r.Stage, r.Reports = "room", nil
	r.PVEActors, r.StageWaves, r.Reliable = nil, nil, nil
	r.PVEBlocks = nil
	r.FosterPositions = nil
	r.FosterActivated = nil
	r.FosterBatchEnded = nil
	r.FosterPlan = nil
	r.FosterSpawned = nil
	r.FosterTriggered = nil
	r.FosterRetired = nil
	r.FosterFinishReported = false
	for _, member := range r.Members {
		s := member.Session
		h.leave(s, true)
		s.sendGame(notice("房主已退出，闯关房间已解散。"))
	}
}

func (h *Hub) stageWaveReport(s *Session, ch *Channel, payload []byte) error {
	r := s.Room
	if r == nil || r.Type() != protocol.StageAssault || r.Stage != "battle" || ch.Phase != "battle" || r.Owner != s.UID {
		return nil
	}
	member := r.Members[s.UID]
	if member == nil || member.Session != s {
		return protocol.ErrFrame
	}
	report, err := protocol.ParseStageWaveReport(payload)
	if err != nil {
		return err
	}
	w := r.StageWaves
	if w == nil || w.finished || report.Wave != int32(w.index+1) || report.ReportValue != 1 ||
		report.ContextValue != uint64(r.ID)|(uint64(r.Serial)<<32) {
		return nil
	}
	for _, actor := range r.PVEActors {
		if actor.active {
			return nil
		}
	}
	for template, count := range w.plans[w.index].Monsters {
		if w.spawned[template] != count {
			return nil
		}
	}
	// First wave starts locally (93B6B0). Only the next wave is announced;
	// repeated old reports must not resend a command that respawns monsters.
	w.index++
	next := uint32(w.index + 1)
	if w.index == len(w.plans) {
		w.finished = true
		next = ^uint32(0) // Native 82A820's -1 end-scene branch; not a reward receipt.
	}
	w.spawned = map[uint32]uint32{}
	p := make([]byte, 40)
	protocol.WriteUint32(p, 8, next)
	h.broadcast(r, protocol.Message{ID: protocol.MsgStageWaveControl, Payload: p}, 0)
	return nil
}
