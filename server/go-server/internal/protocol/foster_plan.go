package protocol

import (
	"fmt"
	"math"
)

type FosterSpawn struct {
	BornBox   *[6]float32 `json:"born_box,omitempty"`
	Template  uint32      `json:"template"`
	Position  [3]float32  `json:"position"`
	Direction uint32      `json:"direction"`
}

// Each verified group currently contains one ordered sub-list. Groups are
// concurrent, not StageAssault waves. Global capacity includes corpses;
// sub-list capacity counts living monsters. Native group batch admission also
// checks global corpses, but its batch budget counts group living monsters only.
type FosterGroup struct {
	Family        uint32        `json:"family,omitempty"`         // Shared EVENT_GROUP capacity; zero is an independent legacy group.
	PreviousBatch uint32        `json:"previous_batch,omitempty"` // One-based preceding batch, zero for first.
	EndAfter      *float32      `json:"end_after,omitempty"`
	TriggerBoxes  [][6]float32  `json:"trigger_boxes,omitempty"`
	Spawns        []FosterSpawn `json:"spawns"`
	SubLimit      uint32        `json:"sub_limit"`
	GroupLimit    uint32        `json:"group_limit"`
	TriggerBox    [6]float32    `json:"trigger_box"`
	Block         uint32        `json:"block,omitempty"`
}

type FosterPlan struct {
	InitialHP   []float32     `json:"initial_hp,omitempty"` // Native template index, not display units.
	Groups      []FosterGroup `json:"groups"`
	GlobalLimit uint32        `json:"global_limit"`
	PlayerLimit uint32        `json:"player_limit"`
}

// These bounds protect configuration and the native six-player/100-identity
// layout. Validation is not evidence that an arbitrary Lua script is supported.
func (p FosterPlan) Validate(templateCount int) error {
	if templateCount < 1 || templateCount > 1024 || p.PlayerLimit < 1 || p.PlayerLimit > 6 || p.GlobalLimit < 1 || p.GlobalLimit > 100 || len(p.Groups) == 0 || len(p.Groups) > 256 {
		return fmt.Errorf("模式10计划人数、怪物容量或事件组无效")
	}
	finite := func(v float32) bool { return !math.IsNaN(float64(v)) && !math.IsInf(float64(v), 0) }
	// Old saved plans remain readable; battle preparation requires the new data.
	if len(p.InitialHP) != 0 && len(p.InitialHP) != templateCount {
		return fmt.Errorf("模式10血量目录与模板数量不一致")
	}
	for _, hp := range p.InitialHP {
		if !finite(hp) || hp <= 0 {
			return fmt.Errorf("模式10怪物初始血量必须是有限正数")
		}
	}
	total := 0
	for index, group := range p.Groups {
		if group.PreviousBatch > uint32(index) || (group.PreviousBatch > 0 && (group.Family == 0 || p.Groups[group.PreviousBatch-1].Family != group.Family)) {
			return fmt.Errorf("模式10批次依赖无效")
		}
		if group.EndAfter != nil && (!finite(*group.EndAfter) || *group.EndAfter < 0 || *group.EndAfter > 3600) {
			return fmt.Errorf("模式10批次延时无效")
		}
		checkBox := func(b [6]float32) bool {
			for i := 0; i < 3; i++ {
				if !finite(b[i]) || !finite(b[i+3]) || b[i] > b[i+3] {
					return false
				}
			}
			return true
		}
		for _, b := range group.TriggerBoxes {
			if !checkBox(b) {
				return fmt.Errorf("模式10触发区域无效")
			}
		}
		for _, sp := range group.Spawns {
			if sp.BornBox != nil && !checkBox(*sp.BornBox) {
				return fmt.Errorf("模式10随机出生范围无效")
			}
		}
		total += len(group.Spawns)
		if len(group.Spawns) == 0 || total > 10000 || group.SubLimit < 1 || group.SubLimit > group.GroupLimit || group.GroupLimit > p.GlobalLimit {
			return fmt.Errorf("模式10怪物数量或生成上限无效")
		}

		for i := 0; i < 3; i++ {
			if !finite(group.TriggerBox[i]) || !finite(group.TriggerBox[i+3]) || group.TriggerBox[i] > group.TriggerBox[i+3] {
				return fmt.Errorf("模式10触发区域无效")
			}
		}
		for _, spawn := range group.Spawns {
			if spawn.Template >= uint32(templateCount) {
				return fmt.Errorf("模式10怪物模板索引越界")
			}
			for _, v := range spawn.Position {
				if !finite(v) {
					return fmt.Errorf("模式10怪物位置无效")
				}
			}
		}
	}
	return nil
}
