package game

import (
	"kungfu.local/server/internal/protocol"
	"testing"
	"time"
)

func TestFosterBatchesAndRandomBounds(t *testing.T) {
	_, s, _, _ := combatFixture()
	r := s.Room
	r.Request[46] = byte(protocol.FosterMode)
	box := [6]float32{-10, 0, -5, 10, 5, 5}
	delay := float32(5)
	spawn := protocol.FosterSpawn{Template: 0, BornBox: &box, Direction: 2}
	r.FosterPlan = &protocol.FosterPlan{GlobalLimit: 32, Groups: []protocol.FosterGroup{
		{Family: 1, SubLimit: 1, GroupLimit: 1, Spawns: []protocol.FosterSpawn{spawn}},
		{Family: 1, PreviousBatch: 1, EndAfter: &delay, SubLimit: 1, GroupLimit: 1, Spawns: []protocol.FosterSpawn{spawn}},
	}}
	r.FosterSpawned = []int{0, 0}
	r.FosterTriggered = []bool{true, true}
	event := protocol.PVEActorCreate{TemplateValue: 0, Position: [3]float32{5, 2, 0}, DirectionValue: 2}
	if r.fosterSpawnGroup(event) != 0 {
		t.Fatal("first batch")
	}
	event.Position[0] = 11
	if r.fosterSpawnGroup(event) != -1 {
		t.Fatal("out of bounds accepted")
	}
	event.Position[0] = 5
	r.FosterSpawned[0] = 1
	r.PVEActors = map[uint64]pveActor{42: {active: true, fosterGroup: 0, maximumHP: 8, reportedHP: 8}}
	if r.fosterSpawnGroup(event) != -1 {
		t.Fatal("shared group limit bypass")
	}
	a := r.PVEActors[42]
	a.reportedHP = 0
	r.PVEActors[42] = a
	r.refreshFosterBatchEnds()
	if r.fosterSpawnGroup(event) != -1 {
		t.Fatal("delay bypass")
	}
	r.FosterBatchEnded[0] = time.Now().Add(-6 * time.Second)
	if r.fosterSpawnGroup(event) != 1 {
		t.Fatal("second batch did not open")
	}
}

func TestFosterRemovedAliveDoesNotEndBatch(t *testing.T) {
	r := &Room{FosterPlan: &protocol.FosterPlan{Groups: []protocol.FosterGroup{{Spawns: []protocol.FosterSpawn{{}}}}}, FosterSpawned: []int{1}, FosterRetired: []int{0}, PVEActors: map[uint64]pveActor{42: {active: false, fosterGroup: 0}}}
	r.refreshFosterBatchEnds()
	if !r.FosterBatchEnded[0].IsZero() {
		t.Fatal("unverified removal ended batch")
	}
	r.FosterRetired[0] = 1
	r.refreshFosterBatchEnds()
	if r.FosterBatchEnded[0].IsZero() {
		t.Fatal("verified dead actor did not complete batch")
	}
}
