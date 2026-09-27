package game

import (
	"bytes"
	"kungfu.local/server/internal/protocol"
	"math"
	"time"
)

// The controller emits 20405 immediately before its 4160. Hold the snapshot
// until every native mode object is initialized; never relay arbitrary later
// reposition requests. Admission for mode 10 remains a separate policy.
func (h *Hub) fosterPositions(s *Session, ch *Channel, payload []byte) error {
	r := s.Room
	if r == nil || r.Type() != protocol.FosterMode || r.Stage != "loading" || ch.Phase != "loading" || r.Owner != s.UID {
		return nil
	}
	m := r.Members[s.UID]
	if m == nil || m.Session != s {
		return protocol.ErrFrame
	}
	if m.Loaded {
		return nil
	}
	sender, rows, err := protocol.ParseFosterPositions(payload)
	if err != nil {
		return err
	}
	if sender != s.UID {
		return protocol.ErrFrame
	}
	count := 0
	for _, row := range rows {
		if row.UID == 0 {
			continue
		}
		if r.Members[row.UID] == nil {
			if _, departed := r.DepartedSlots[row.UID]; !departed {
				return protocol.ErrFrame
			}
			continue // A queued initial snapshot may still contain a departed fighter.
		}
		count++
	}
	if count != len(r.Members) {
		return protocol.ErrFrame
	}
	if r.FosterPositions != nil {
		if !bytes.Equal(r.FosterPositions, payload) {
			return protocol.ErrFrame
		}
		return nil
	}
	r.FosterPositions = bytes.Clone(payload)
	for _, row := range rows {
		if row.UID != 0 {
			r.triggerFosterGroups(row.Position)
		}
	}
	return nil
}

// EVENT_BOXS latches activation when any player enters the inclusive AABB.
// This follows reported coordinates, not independent server-side movement.
func (r *Room) triggerFosterGroups(position [3]float32) {
	if r.Type() != protocol.FosterMode || r.FosterPlan == nil || len(r.FosterTriggered) != len(r.FosterPlan.Groups) {
		return
	}
	for _, v := range position {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return
		}
	}
	for i, group := range r.FosterPlan.Groups {
		boxes := group.TriggerBoxes
		if len(boxes) == 0 {
			boxes = [][6]float32{group.TriggerBox}
		}
		for _, box := range boxes {
			inside := true
			for axis, v := range position {
				inside = inside && v >= box[axis] && v <= box[axis+3]
			}
			if inside && !r.FosterTriggered[i] {
				r.FosterTriggered[i] = true
				if r.FosterActivated == nil {
					r.FosterActivated = map[int]time.Time{}
				}
				r.FosterActivated[i] = time.Now()
			}
		}
	}
}
