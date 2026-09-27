package game

import (
	"bytes"
	"kungfu.local/server/internal/protocol"
	"log"
	"sort"
	"time"
)

// The timer covers both resource loading and the final input barrier.
func (hub *Hub) watchLoading(room *Room) {
	if room.LoadTimer != nil {
		room.LoadTimer.Stop()
	}
	serial := room.Serial
	room.LoadTimer = time.AfterFunc(90*time.Second, func() {
		hub.lockState()
		hub.scopeRoom(room)
		defer hub.unlockState()
		hub.expireLoading(room, serial)
	})
}

func (hub *Hub) expireLoading(room *Room, serial uint32) {
	if hub.Rooms[room.ID] != room || room.Serial != serial || (room.Stage != "loading" && room.Stage != "wait_ready") {
		return
	}
	log.Printf("loading_timeout room=%d serial=%d stage=%s", room.ID, serial, room.Stage)
	if err := hub.recoverRoom(room, "等待玩家加载超时，已返回房间，请重新准备。"); err != nil {
		log.Printf("loading_recovery_failed room=%d error=%v", room.ID, err)
	}
}

func (hub *Hub) recoverRoom(room *Room, reason string) error {
	peers := make([]roomPeer, 0, len(room.Members))
	for uid, member := range room.Members {
		account, err := storage2_1(hub, hub.Store.RoleManager().Snapshot, uid)
		if err != nil {
			return err
		}
		peers = append(peers, roomPeer{member, fighter(account, member)})
	}
	hub.restoreRoom(room, peers, reason)
	return nil
}

// Reuse the verified leave/enter sequence to clear the native battle world.
// An interrupted match has no trusted final report and awards nothing.
func (hub *Hub) restoreRoom(room *Room, peers []roomPeer, reason string) {
	if room.LoadTimer != nil {
		room.LoadTimer.Stop()
		room.LoadTimer = nil
	}
	room.Stage, room.Reports = "room", nil
	room.PairSelections, room.PairSelectionVersions = nil, nil
	room.Projectiles, room.Collectibles = nil, nil
	room.PVEActors, room.StageWaves = nil, nil
	room.PVEBlocks = nil
	room.HealthReceipts = nil
	if room.Series != nil {
		room.Series = newTeamSeries(room.Series.limit)
	}
	room.FosterPositions = nil
	room.FosterActivated = nil
	room.FosterBatchEnded = nil
	room.FosterPlan = nil
	room.FosterSpawned = nil
	room.FosterTriggered = nil
	room.FosterRetired = nil
	room.FosterFinishReported = false
	room.Reliable = nil
	sort.Slice(peers, func(i, j int) bool { return peers[i].member.Slot < peers[j].member.Slot })
	for _, peer := range peers {
		m := peer.member
		m.Ready, m.Loaded, m.Input = false, false, false
		m.BattleEvents = nil
		m.TalismanEvents = nil
		m.Session.ConsumeIntents = nil
		m.Session.TalismanPending = nil
		m.Session.sendGame(protocol.Message{ID: protocol.MsgRoomLeft})
	}
	for _, peer := range peers {
		own := bytes.Clone(peer.raw)
		own[53] = 0
		s := peer.member.Session
		s.game().Phase = "room"
		s.sendGame(protocol.Message{ID: protocol.MsgRoomEntered, Payload: roomEntryForMember(room, peer.member, own)})
		s.sendGame(protocol.Message{ID: protocol.MsgRoomOwner, Payload: protocol.Uint64Bytes(room.Owner)})
		var roster []byte
		for _, other := range peers {
			if other.member != peer.member {
				raw := bytes.Clone(other.raw)
				raw[53] = 0
				roster = append(roster, raw...)
			}
		}
		if len(roster) > 0 {
			s.sendGame(protocol.Message{ID: 3105, Payload: roster})
		}
		peer.member.Session.sendGame(notice(reason))
	}
}
