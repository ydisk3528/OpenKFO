package game

import (
	"bytes"
	"encoding/json"
	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
	"log"
	"sort"
	"time"
)

// A native report is not proof of clearance. Match its identities/context to
// this room and require the server's complete wave progression for reason 1.
func validateStageFinish(r *Room, payload []byte) (string, error) {
	if r != nil && r.Type() == protocol.FosterMode {
		return validateFosterFinish(r, payload)
	}
	if r == nil || r.Type() != protocol.StageAssault || r.StageWaves == nil {
		return "", nil
	}
	_, reason, err := protocol.ParsePVEFinishReport(r.Type(), payload)
	if err != nil {
		return "", err
	}
	health, err := validateBattleReport(r, payload)
	if err != nil {
		return "", err
	}
	w := r.StageWaves
	switch reason {
	case protocol.StageFinishWaves:
		if !w.finished || w.index != len(w.plans) || len(w.plans) == 0 {
			return "", nil
		}
		for _, actor := range r.PVEActors {
			if actor.active {
				return "", nil
			}
		}
		return persistence.StageOutcomeClear, nil
	case protocol.StageFinishActorFlags:
		// The native flag itself has unresolved semantics. Only the subset
		// where the controller also reports every player dead is actionable.
		if w.finished {
			return "", nil
		}
		for _, hp := range health {
			if hp != 0 {
				return "", nil
			}
		}
		return persistence.StageOutcomeFailed, nil
	case protocol.StageFinishCounterZero:
		if pveDeadlineReached(r, time.Now()) {
			return persistence.StageOutcomeFailed, nil
		}
		return "", nil
	default:
		return "", nil
	}
}

// 93C890 / 942FE0 call 987E40 only for the controller. Do not send 4100 to solicit
// peers: its report producer does not preserve this mode's natural reason.
// This collects a validated finish for the separate PVE payout/result flow.
func (h *Hub) stageFinishReport(s *Session, payload []byte) error {
	accepted, err := recordStageFinish(s, payload)
	if err != nil {
		return err
	}
	if !accepted {
		h.deferStageFinish(s, payload)
		return nil
	}
	s.Room.pendingStageFinish = nil
	if err = h.settleStage(s.Room); err != nil {
		log.Printf("stage_settlement_pending room=%d serial=%d error=%v", s.Room.ID, s.Room.Serial, err)
		s.sendGame(notice("关卡结果已接收，奖励结算暂未完成，服务器将自动重试。"))
		h.retryStageSettlement(s.Room)
	}
	return nil
}

func (h *Hub) retryStageSettlement(r *Room) {
	if r.LoadTimer != nil {
		r.LoadTimer.Stop()
	}
	serial := r.Serial
	r.LoadTimer = time.AfterFunc(10*time.Second, func() {
		h.lockState()
		h.scopeRoom(r)
		defer h.unlockState()
		if h.Rooms[r.ID] != r || r.Serial != serial || r.Stage != "finishing" {
			return
		}
		if err := h.settleStage(r); err != nil {
			log.Printf("stage_settlement_retry room=%d serial=%d error=%v", r.ID, serial, err)
			h.retryStageSettlement(r)
		}
	})
}

func recordStageFinish(s *Session, payload []byte) (bool, error) {
	r := s.Room
	if r == nil || (r.StageWaves == nil && r.FosterPlan == nil) || r.Owner != s.UID || (r.Stage != "battle" && r.Stage != "finishing") {
		return false, nil
	}
	m := r.Members[s.UID]
	if m == nil || m.Session != s {
		return false, protocol.ErrFrame
	}
	outcome, err := validateStageFinish(r, payload)
	if err != nil {
		return false, err
	}
	if outcome == "" {
		return false, nil
	}
	if previous := r.Reports[s.UID]; previous != nil {
		// First accepted report is immutable; retries cannot rewrite outcome.
		return bytes.Equal(previous, payload), nil
	}
	r.Reports = map[uint64][]byte{s.UID: bytes.Clone(payload)}
	r.Stage = "finishing"
	if !r.BattleStartedAt.IsZero() {
		elapsed := time.Since(r.BattleStartedAt) / time.Second
		if elapsed > 0 && elapsed <= 2147483647 {
			r.StageElapsedSeconds = uint32(elapsed)
		}
	}
	return true, nil
}

func (h *Hub) settleStage(r *Room) error {
	outcome, err := validateStageFinish(r, r.Reports[r.Owner])
	if err != nil {
		return err
	}
	if outcome == "" {
		return nil
	}
	settings, err := storage2_1(h, h.Store.RewardManager().BattleRewards, h.Config.Settlement)
	if err != nil {
		return err
	}
	var awards []persistence.BattleReward
	for uid, m := range r.Members {
		if m.Spectator {
			continue
		}
		awards = append(awards, persistence.BattleReward{UID: uid, Outcome: outcome, StartLevel: m.BattleLevel})
	}
	reports, err := json.Marshal(r.Reports)
	if err != nil {
		return err
	}
	awards, err = storage2_5(h, h.Store.BattleManager().SettleStage, r.Serial, protocol.ReadUint32(r.Request, protocol.RoomMapOffset), reports, awards, settings.Rules)
	if err != nil {
		return err
	}
	// Construct every result before changing phases or sending any notification.
	packets := map[uint64]protocol.Message{}
	for uid := range r.Members {
		p, err := stageResultPacket(r, awards, uid)
		if err != nil {
			return err
		}
		packets[uid] = p
	}
	r.Stage = "settlement"
	if r.LoadTimer != nil {
		r.LoadTimer.Stop()
		r.LoadTimer = nil
	}
	for _, m := range r.Members {
		m.Session.game().Phase = "settlement"
		m.Ready, m.Loaded, m.Input = false, false, false
		m.Session.ConsumeIntents = nil
		if m.Spectator {
			m.Session.sendGame(packets[m.Session.UID])
		}
	}
	for _, a := range awards {
		s := r.Members[a.UID].Session
		s.sendGame(protocol.Message{ID: 4300, Payload: bytes.Clone(a.Profile[persistence.ExperienceOffset : persistence.ExperienceOffset+8])})
		s.sendGame(protocol.Message{ID: 1240, Payload: protocol.Uint32Bytes(a.GoldBalance)})
		s.sendGame(protocol.Message{ID: 1230, Payload: protocol.Uint32Bytes(a.TicketBalance)})
		if s.Inventory == nil {
			s.Inventory = map[uint32][]byte{}
		}
		for _, item := range a.Items {
			s.sendGame(protocol.Message{ID: protocol.MsgItemAdded, Payload: bytes.Clone(item)})
			s.Inventory[protocol.ReadUint32(item, 0)] = bytes.Clone(item)
		}
		s.sendGame(packets[a.UID])
		log.Printf("stage_settled room=%d serial=%d map=%d uid=%d outcome=%s tickets=%d result_message=4120 result_bytes=%d", r.ID, r.Serial, protocol.ReadUint32(r.Request, protocol.RoomMapOffset), a.UID, a.Outcome, a.TicketBalance, len(packets[a.UID].Payload))
	}
	return nil
}

func stageResultPacket(r *Room, awards []persistence.BattleReward, recipient uint64) (protocol.Message, error) {
	if r.Type() == protocol.FosterMode {
		return fosterResultPacket(r, awards, recipient)
	}
	if r.Type() != protocol.StageAssault || r.StageWaves == nil {
		return protocol.Message{}, protocol.ErrFrame
	}
	ordered := append([]persistence.BattleReward(nil), awards...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].UID == recipient {
			return false
		}
		if ordered[j].UID == recipient {
			return true
		}
		return ordered[i].UID < ordered[j].UID
	})
	rows := make([]protocol.StageResult, len(ordered))
	for i, a := range ordered {
		if len(a.Profile) != protocol.RoleProfileSize {
			return protocol.Message{}, protocol.ErrFrame
		}
		row := &rows[i]
		row.UID, row.Experience, row.Gold = a.UID, a.Experience, a.Gold
		// Emulator uses the common success/loss result values. The PVE row
		// renderer 936900 does not read its fourth (result) argument.
		if a.Outcome == persistence.StageOutcomeClear {
			row.ResultValue = 1
		} else {
			row.ResultValue = 2
		}
		row.Waves, row.ElapsedSeconds = uint32(r.StageWaves.index), r.StageElapsedSeconds
		// No invented scoring curve: grade remains the native default image.
		protocol.WriteUint32(row.Raw[:], 87, ^uint32(0)) // Hide unconfirmed extra score.
		protocol.WriteUint16(row.Raw[:], 14, protocol.ReadUint16(a.Profile, persistence.LevelOffset))
		protocol.WriteUint32(row.Raw[:], 16, protocol.ReadUint32(a.Profile, persistence.ExperienceOffset))
		copy(row.Raw[140:], a.Profile)
		if len(a.Items) > 0 {
			row.ItemID = protocol.ReadUint32(a.Items[0], 5)
		}
	}
	p, err := protocol.EncodeStageResults(rows)
	return protocol.Message{ID: 4120, Payload: p}, err
}

// 82C970 routes mode 10 to 80F560 / Result.sui, not result21.sui.
// Reuse its ordinary 140-byte header + complete profile serializer. Only the
// UI result enum is mapped here; persisted PVE outcomes/reward policy stay PVE.
func fosterResultPacket(r *Room, awards []persistence.BattleReward, recipient uint64) (protocol.Message, error) {
	if len(awards) == 0 || len(awards) > 6 || len(awards) != r.fighterCount() || r.Members[recipient] == nil {
		return protocol.Message{}, protocol.ErrFrame
	}
	rows := append([]persistence.BattleReward(nil), awards...)
	seen := map[uint64]bool{}
	for i := range rows {
		row := &rows[i]
		if row.UID == 0 || seen[row.UID] || r.Members[row.UID] == nil || r.Members[row.UID].Spectator || len(row.Profile) != protocol.RoleProfileSize {
			return protocol.Message{}, protocol.ErrFrame
		}
		seen[row.UID] = true
		switch row.Outcome {
		case persistence.StageOutcomeClear:
			row.Outcome = "win"
		case persistence.StageOutcomeFailed:
			row.Outcome = "loss"
		default:
			return protocol.Message{}, protocol.ErrFrame
		}
	}
	return settlementPacket(r, rows, recipient), nil
}
