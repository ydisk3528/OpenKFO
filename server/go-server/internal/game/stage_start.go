package game

import (
	"fmt"
	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
)

// Take a private copy of the current GM plan before allocating a battle. Later
// GM edits apply to the next battle, never to an already active wave counter.
func (c Config) persistedStagePlan(access persistence.StageAccess, mapID uint32, players int) (*stageWaves, error) {
	if c.ConfigHash == "" || access.ClientHash != c.ConfigHash {
		return nil, fmt.Errorf("关卡波次与当前客户端版本不一致，请重新导入地图配置")
	}
	if err := access.Validate(); err != nil {
		return nil, err
	}
	return validatedStagePlan(access, mapID, players)
}

// Caller must validate the complete access snapshot and its client hash first.
func validatedStagePlan(access persistence.StageAccess, mapID uint32, players int) (*stageWaves, error) {
	if !access.Allows(mapID) {
		return nil, fmt.Errorf("当前关卡已关闭")
	}
	for _, plan := range access.WavePlans {
		if plan.MapID == mapID {
			// Use the existing variant selector and deep copy. Do not fall back
			// to startup JSON after the GM explicitly deletes a persisted plan.
			return (Config{StageWaveVariants: map[uint32][]StageWaveVariant{mapID: plan.Variants}}).stagePlan(mapID, players)
		}
	}
	return nil, fmt.Errorf("当前关卡缺少已保存的波次配置，请在GM中导入并保存")
}

func (h *Hub) prepareStageBattle(r *Room) (*stageWaves, error) {
	access, err := h.stageAccess()
	if err != nil {
		return nil, err
	}
	mapID := protocol.ReadUint32(r.Request, protocol.RoomMapOffset)
	waves, err := h.Config.persistedStagePlan(access, mapID, r.fighterCount())
	if err != nil {
		return nil, err
	}
	if err := h.validateStageRewards(mapID); err != nil {
		return nil, err
	}
	return waves, nil
}

func (h *Hub) validateStageRewards(mapID uint32) error {
	settings, err := storage2_1(h, h.Store.RewardManager().BattleRewards, h.Config.Settlement)
	if err != nil {
		return err
	}
	for _, outcome := range []string{persistence.StageOutcomeClear, persistence.StageOutcomeFailed} {
		if _, ok := settings.Rules.StageReward(mapID, outcome); !ok {
			return fmt.Errorf("当前关卡缺少通关/失败奖励配置，请在GM中保存关卡奖励")
		}
	}
	return nil
}
