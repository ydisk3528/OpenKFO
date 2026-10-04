package game

import (
	"fmt"
	"slices"

	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
)

func (c Config) persistedFosterPlan(access persistence.StageAccess, mapID uint32, players int) (*protocol.FosterPlan, error) {
	if c.ConfigHash == "" || access.ClientHash != c.ConfigHash {
		return nil, fmt.Errorf("关卡事件计划与当前客户端版本不一致，请重新导入地图配置")
	}
	if err := access.Validate(); err != nil {
		return nil, err
	}
	return validatedFosterPlan(access, mapID, players)
}

// Caller must validate the complete access snapshot and its client hash first.
func validatedFosterPlan(access persistence.StageAccess, mapID uint32, players int) (*protocol.FosterPlan, error) {
	if !access.Allows(mapID) {
		return nil, fmt.Errorf("当前关卡已关闭")
	}
	for _, config := range access.FosterPlans {
		if config.MapID != mapID {
			continue
		}
		if players < 1 || players > int(config.Plan.PlayerLimit) {
			return nil, fmt.Errorf("当前人数超过关卡事件计划限制")
		}
		plan := config.Plan
		if len(plan.InitialHP) != len(config.Templates) {
			return nil, fmt.Errorf("关卡事件计划缺少怪物初始血量，请在GM中重新读取客户端地图条件并保存")
		}
		plan.InitialHP = slices.Clone(plan.InitialHP)
		plan.Groups = slices.Clone(plan.Groups)
		for i := range plan.Groups {
			plan.Groups[i].Spawns = slices.Clone(plan.Groups[i].Spawns)
			plan.Groups[i].TriggerBoxes = slices.Clone(plan.Groups[i].TriggerBoxes)
			if delay := plan.Groups[i].EndAfter; delay != nil {
				value := *delay
				plan.Groups[i].EndAfter = &value
			}
			for j := range plan.Groups[i].Spawns {
				if box := plan.Groups[i].Spawns[j].BornBox; box != nil {
					value := *box
					plan.Groups[i].Spawns[j].BornBox = &value
				}
			}
		}
		return &plan, nil
	}
	return nil, fmt.Errorf("当前关卡缺少已保存的事件计划，请在GM中导入并保存")
}

func (h *Hub) prepareFosterBattle(r *Room) (*protocol.FosterPlan, error) {
	access, err := h.stageAccess()
	if err != nil {
		return nil, err
	}
	mapID := protocol.ReadUint32(r.Request, protocol.RoomMapOffset)
	plan, err := h.Config.persistedFosterPlan(access, mapID, r.fighterCount())
	if err != nil {
		return nil, err
	}
	if err := h.validateStageRewards(mapID); err != nil {
		return nil, err
	}
	return plan, nil
}
