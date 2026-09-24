package game

import (
	"bytes"
	"fmt"
	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
	"log"
	"sort"
	"strconv"
	"time"
)

type Config struct {
	ExperimentalNeutralNPC bool                             `json:"-"`
	SuitBundles            map[uint32][]uint32              `json:"suit_bundles,omitempty"`
	RandomWeaponTypes      map[uint32]uint32                `json:"random_weapon_types,omitempty"`
	TeamSeriesRounds       uint32                           `json:"team_series_rounds,omitempty"`
	RebornEnabled          bool                             `json:"reborn_enabled,omitempty"`
	SpectatorCapacity      int                              `json:"spectator_capacity,omitempty"`
	LauncherCredentialsKey string                           `json:"launcher_credentials_key,omitempty"`
	StageWaveVariants      map[uint32][]StageWaveVariant    `json:"stage_wave_variants,omitempty"`
	StageWaves             map[uint32][]StageWavePlan       `json:"stage_waves,omitempty"`
	TitleLevels            []byte                           `json:"title_levels,omitempty"` // Verified roletitle.xml levels; empty disables announcements.
	TalismanUses           []TalismanUseRule                `json:"talisman_uses,omitempty"`
	TalismanRepairs        []persistence.TalismanRepairRule `json:"talisman_repairs,omitempty"`
	Honour                 HonourRules                      `json:"honour,omitempty"`
	WeaponUpgradeMode      string                           `json:"weapon_upgrade_mode,omitempty"`
	WeaponLevels           []WeaponLevel                    `json:"weapon_levels,omitempty"`
	LobbyNames             map[uint32]string                `json:"lobby_names,omitempty"`
	LobbyIDs               []uint32                         `json:"lobby_ids,omitempty"`
	CharacterChoices       []persistence.CharacterChoice    `json:"character_choices"`
	Settlement             SettlementRewards                `json:"settlement"`
	ConfigHash             string                           `json:"config_hash"`
	Pools                  map[string][]uint32              `json:"pools"`
	Groups                 map[string][]uint32              `json:"groups"`
}
type Member struct {
	ResultAcknowledged   bool
	Spectator            bool
	WeaponSwitch         weaponSwitchAttempt
	NetworkDelay         uint32
	TalismanEvents       map[uint64]uint32
	BattleLevel          uint16
	Session              *Session
	Slot, Spawn, Team    byte
	Ready, Loaded, Input bool
	BattleEvents         map[battleEventKey]battleSequence
}
type Room struct {
	NeutralNPC            *neutralNPCSession
	Series                *teamSeries
	HealthReceipts        map[[2]uint64]uint32
	PairSelectionVersions map[uint64]uint32
	PairSelections        map[uint64]pairSelection
	Projectiles           map[uint32]*projectileState
	Collectibles          map[[2]uint32]bool
	SpectatorCapacity     int
	PeerProbes            map[[2]uint64]peerProbeObservation
	BattleStartedAt       time.Time
	BattleClock           uint32
	StageElapsedSeconds   uint32
	StageWaves            *stageWaves
	PVEActors             map[uint64]pveActor
	PVEBlocks             map[uint32]pveBlock
	FosterPositions       []byte
	FosterPlan            *protocol.FosterPlan
	FosterSpawned         []int
	FosterTriggered       []bool
	FosterRetired         []int // Per-group removals whose received-event HP was zero.
	FosterFinishReported  bool
	NetworkProbe          *roomNetworkProbe
	CreationPending       bool
	Reliable              map[reliableActor]*reliableExchange
	ReliableSerial        uint32
	Exchange              *seatExchange
	LobbyID               uint32
	LoadTimer             *time.Timer
	Reports               map[uint64][]byte
	DepartedSlots         map[uint64]byte // Current round only; never eligible for rewards.
	ID                    uint16
	Owner                 uint64
	Request               []byte
	Stage                 string
	Serial                uint32
	Members               map[uint64]*Member
}

// The first settlement return sets Stage to room; other clients may still
// display results. Configuration broadcasts require every member back.
func (r *Room) canConfigure(s *Session) bool {
	if r == nil || r.Stage != "room" || s == nil {
		return false
	}
	actor := r.Members[s.UID]
	if actor == nil || actor.Session != s {
		return false
	}
	for uid, member := range r.Members {
		if member == nil || member.Session == nil || member.Session.UID != uid || member.Session.Room != r || member.Session.game() == nil || member.Session.game().Phase != "room" {
			return false
		}
	}
	return true
}

func (hub *Hub) resolve(request []byte) ([]byte, error) {
	access, err := hub.stageAccess()
	if err != nil {
		return nil, err
	}
	return hub.resolveWithAccess(request, access)
}

func (hub *Hub) stageAccess() (persistence.StageAccess, error) {
	if hub.Store == nil {
		return persistence.StageAccess{}, nil
	}
	return hub.Store.StageAccess()
}

func (hub *Hub) resolveWithAccess(request []byte, access persistence.StageAccess) ([]byte, error) {
	return hub.resolveWithPolicy(request, access.Allows, access)
}

func (hub *Hub) resolveWithAllowed(request []byte, allows func(uint32) bool) ([]byte, error) {
	return hub.resolveWithPolicy(request, allows, persistence.StageAccess{})
}
func (hub *Hub) resolveWithPolicy(request []byte, allows func(uint32) bool, access persistence.StageAccess) ([]byte, error) {
	if len(request) != 81 {
		return nil, protocol.ErrFrame
	}
	if tutorialRequest(request) && allows(1201) {
		return bytes.Clone(request), nil
	}
	mode, capacity := protocol.RoomTypeFromRequest(request), request[protocol.RoomCapacityOffset]
	if mode == protocol.FosterMode {
		chosen := protocol.ReadUint32(request, protocol.RoomMapOffset)
		suggested := protocol.ReadUint32(request, protocol.RoomSuggestedMapOffset)
		known := false
		for _, id := range access.PVEMaps {
			known = known || id == chosen
		}
		for _, plan := range access.WavePlans {
			if plan.MapID == chosen {
				return nil, protocol.ErrFrame
			}
		}
		if capacity < 1 || capacity > 8 || chosen == 0 || !known ||
			hub.Config.ConfigHash == "" || access.ClientHash != hub.Config.ConfigHash ||
			!allows(chosen) || (suggested != 0 && suggested != 0xffffffff && suggested != chosen) {
			return nil, protocol.ErrFrame
		}
		// Room admission uses the known map catalogue. The persisted monster plan
		// is validated separately before battle; missing plans never start combat.
		return bytes.Clone(request), nil
	}
	if mode == protocol.StageAssault {
		chosen := protocol.ReadUint32(request, protocol.RoomMapOffset)
		suggested := protocol.ReadUint32(request, protocol.RoomSuggestedMapOffset)
		if capacity < 1 || capacity > 8 || chosen == 0 || chosen == 0xffffffff ||
			!allows(chosen) || (suggested != 0 && suggested != 0xffffffff && suggested != chosen) {
			return nil, protocol.ErrFrame
		}
		if _, err := hub.Config.persistedStagePlan(access, chosen, int(capacity)); err != nil {
			return nil, err
		}
		resolved := bytes.Clone(request)
		protocol.WriteUint32(resolved, protocol.RoomSuggestedMapOffset, chosen)
		return resolved, nil
	}
	if mode == protocol.RebornMode && (!hub.Config.RebornEnabled || capacity > 6) {
		return nil, protocol.ErrFrame
	}
	if (!mode.IsCompetitive() && mode != protocol.FreePractice && mode != protocol.RebornMode) || (capacity != 2 && capacity != 4 && capacity != 6 && capacity != 8) {
		return nil, protocol.ErrFrame
	}
	chosen, suggested := protocol.ReadUint32(request, 38), protocol.ReadUint32(request, 42)
	// An explicit GM override admits the requested known map even if it was
	// absent from the original pool. Mode/capacity validation remains above.
	if access.ForceOpens(chosen) && allows(chosen) {
		if access.ClientHash != hub.Config.ConfigHash || (suggested != 0 && suggested != 0xffffffff && suggested != chosen) {
			return nil, protocol.ErrFrame
		}
		resolved := bytes.Clone(request)
		protocol.WriteUint32(resolved, protocol.RoomSuggestedMapOffset, chosen)
		return resolved, nil
	}

	pool := hub.Config.Pools[fmt.Sprintf("%d:%d", mode, capacity)]
	group, isGroup := hub.Config.Groups[strconv.FormatUint(uint64(chosen), 10)]
	contains := func(values []uint32, needle uint32) bool {
		for _, value := range values {
			if value == needle {
				return true
			}
		}
		return false
	}
	var target uint32
	if chosen == 0 || chosen == 0xffffffff || isGroup {
		for _, mapID := range pool {
			if !allows(mapID) {
				continue
			}
			if isGroup && !contains(group, mapID) {
				continue
			}
			if target == 0 {
				target = mapID
			}
			if suggested == mapID {
				target = mapID
				break
			}
		}
	} else if allows(chosen) && contains(pool, chosen) && (suggested == 0 || suggested == 0xffffffff || suggested == chosen) {
		target = chosen
	}
	if target == 0 {
		return nil, protocol.ErrFrame
	}
	resolvedRequest := bytes.Clone(request)
	protocol.WriteUint32(resolvedRequest, 38, target)
	protocol.WriteUint32(resolvedRequest, 42, target)
	return resolvedRequest, nil
}
func (hub *Hub) broadcast(room *Room, message protocol.Message, exclude uint64) {
	for uid, member := range room.Members {
		if uid != exclude {
			member.Session.sendGame(message)
		}
	}
}
func (hub *Hub) install(room *Room, session *Session) error {
	return hub.installAs(room, session, false)
}

func (hub *Hub) installAs(room *Room, session *Session, spectator bool) error {
	if room.LobbyID != session.LobbyID || room.Stage != "room" || (!spectator && room.fighterCount() >= int(room.Request[37])) || (spectator && room.observerCount() >= room.observerLimit()) ||
		(!tutorialRoom(room) && (!session.Bound || time.Now().After(session.P2PUntil))) {
		return protocol.ErrFrame
	}
	slot := byte(0)
	for {
		used := false
		for _, member := range room.Members {
			used = used || member.Slot == slot
		}
		if !used {
			break
		}
		slot++
	}
	account, err := hub.Store.RoleManager().Snapshot(session.UID)
	if err != nil {
		return err
	}
	// A seat exchange changes Spawn without changing the roster Slot.
	// New members must use a free position as well as a free roster entry.
	spawn := byte(0)
	for {
		used := false
		for _, member := range room.Members {
			used = used || member.Spawn == spawn
		}
		if !used {
			break
		}
		spawn++
	}
	member := &Member{Session: session, Slot: slot, Spawn: spawn, Team: slot % 2}
	if spectator {
		member.Spectator = true
		member.Slot, member.Spawn = spectatorSlot, spectatorSlot
	} else if room.Type().IsTeam() {
		position, ok := room.freeTeamPosition(member.Team, session.UID)
		if !ok {
			member.Team ^= 1
			position, ok = room.freeTeamPosition(member.Team, session.UID)
		}
		if !ok {
			return protocol.ErrFrame
		}
		member.Spawn = position
	}
	own := fighter(account, member)
	var peers []roomPeer
	for _, member := range room.Members {
		account, err := hub.Store.RoleManager().Snapshot(member.Session.UID)
		if err != nil {
			return err
		}
		peers = append(peers, roomPeer{member, fighter(account, member)})
	}
	hub.completeRoomJoin(room, member, own, peers)
	return nil
}

// Snapshot every account before committing membership. Persistence failures in
// install must never leave a partially joined room or send partial rosters.
type roomPeer struct {
	member *Member
	raw    []byte
}

func (hub *Hub) completeRoomJoin(room *Room, member *Member, own []byte, peers []roomPeer) {
	session := member.Session
	room.Members[session.UID] = member
	session.Room = room
	session.game().Phase = "room"
	if tutorialRoom(room) || (room.Owner == session.UID && (room.Type() == protocol.FosterMode || room.Type() == protocol.StageAssault)) {
		// 8253A0 initializes the native mode/map from the 83B creation reply;
		// the client then sends 3550 and 3070 before receiving its 3100 roster.
		room.CreationPending = true
		p := make([]byte, 83)
		protocol.WriteUint16(p, 0, room.ID)
		copy(p[2:], room.Request)
		session.sendGame(protocol.Message{ID: protocol.MsgRoomCreated, Payload: p})
		return
	}
	session.sendGame(protocol.Message{ID: protocol.MsgRoomEntered, Payload: roomEntryForMember(room, member, own)})
	session.sendGame(protocol.Message{ID: protocol.MsgRoomOwner, Payload: protocol.Uint64Bytes(room.Owner)})
	// 3105 consumes consecutive variable-length records, without a count prefix.
	// Never send an empty roster: the native consumer reads the first record.
	sort.Slice(peers, func(i, j int) bool { return peers[i].member.Slot < peers[j].member.Slot })
	var roster []byte
	for _, peer := range peers {
		roster = append(roster, peer.raw...)
	}
	if len(roster) != 0 {
		session.sendGame(protocol.Message{ID: 3105, Payload: roster})
	}
	for _, peer := range peers {
		peer.member.Session.sendGame(protocol.Message{ID: 3090, Payload: own})
	}
	// Membership changes invalidate pending operations, not the peers' consent.
	hub.cancelNetworkProbe(room)
	hub.cancelSeatExchange(room)
	// 3090 invokes the native new-member callback. Reassert ready states after
	// that notification for both existing clients and the newly joined client.
	for _, peer := range peers {
		if peer.member.Ready && !peer.member.Spectator {
			hub.broadcast(room, protocol.Message{ID: protocol.MsgPlayerReady, Payload: protocol.Uint64Bytes(peer.member.Session.UID)}, 0)
		}
	}
}

func (hub *Hub) clearRoomReady(room *Room) {
	hub.cancelNetworkProbe(room)
	hub.cancelSeatExchange(room)
	for uid, member := range room.Members {
		if member.Ready {
			member.Ready = false
			hub.broadcast(room, protocol.Message{ID: protocol.MsgPlayerNotReady, Payload: protocol.Uint64Bytes(uid)}, 0)
		}
	}
}

func (hub *Hub) leave(session *Session, acknowledge bool) {
	hub.leaveWithNotice(session, acknowledge, protocol.MsgPlayerLeftRoom)
}

func (hub *Hub) leaveWithNotice(session *Session, acknowledge bool, departure uint32) {
	hub.clearInvitations(session)
	room := session.Room
	if room == nil {
		if acknowledge {
			session.sendGame(protocol.Message{ID: protocol.MsgRoomLeft})
		}
		return
	}
	hub.cancelNetworkProbe(room)
	hub.cancelSeatExchange(room)
	observerLeft := room.isObserver(session)
	teamBattle := !observerLeft && room.Type().IsTeam() && (room.Stage == "battle" || room.Stage == "finishing" || room.Stage == "settlement")
	if teamBattle {
		if member := room.Members[session.UID]; member != nil {
			if room.DepartedSlots == nil {
				room.DepartedSlots = map[uint64]byte{}
			}
			room.DepartedSlots[session.UID] = member.Slot
		}
		delete(room.Reports, session.UID)
	}
	room.retireBattleObjects(session.UID)
	delete(room.Members, session.UID)
	delete(room.Reliable, reliableActor{session.UID, 9000})
	delete(room.Reliable, reliableActor{session.UID, 9500})
	session.Room = nil
	session.ConsumeIntents = nil
	session.TalismanPending = nil
	if channel := session.game(); channel != nil {
		channel.Phase = "lobby"
	}
	if acknowledge {
		session.sendGame(protocol.Message{ID: protocol.MsgRoomLeft})
	}
	if len(room.Members) == 0 {
		if room.LoadTimer != nil {
			room.LoadTimer.Stop()
		}
		delete(hub.Rooms, room.ID)
		return
	}
	pveStage := room.Type() == protocol.StageAssault || room.Type() == protocol.FosterMode
	stageSettled := pveStage && room.Stage == "settlement"
	if pveStage && room.Stage != "room" && !stageSettled {
		// The native map script and monster pool belong to the controller.
		// Do not hand an in-progress script to another player's empty state.
		hub.abortStageRoom(room)
		return
	}
	// A settled stage has already committed rewards. A departing controller
	// must not turn it into an aborted/no-reward match for the remaining peers.
	interrupted := room.Stage != "room" && !stageSettled
	hub.broadcast(room, protocol.Message{ID: departure, Payload: protocol.Uint64Bytes(session.UID)}, 0)
	if room.Owner == session.UID {
		var first *Member
		for _, member := range room.Members {
			if !member.Spectator && (first == nil || member.Slot < first.Slot) {
				first = member
			}
		}
		if first == nil {
			hub.closeObserverOnlyRoom(room)
			return
		}
		room.Owner = first.Session.UID
		hub.broadcast(room, protocol.Message{ID: protocol.MsgRoomOwner, Payload: protocol.Uint64Bytes(room.Owner)}, 0)
	}
	// Leaving the waiting room changes membership, not the remaining players'
	// readiness. Pending start/seat negotiations were already cancelled above.
	if room.Stage == "room" {
		// A host must be able to start again after membership changes. Keep
		// other fighters ready, but clear the current (possibly new) host.
		if owner := room.Members[room.Owner]; owner != nil && owner.Ready {
			owner.Ready = false
			hub.broadcast(room, protocol.Message{ID: protocol.MsgPlayerNotReady, Payload: protocol.Uint64Bytes(room.Owner)}, 0)
		}
		return
	}
	if observerLeft {
		hub.advanceRoomLoading(room)
		hub.advanceResultAcknowledgements(room)
		if room.Stage == "room" {
			hub.clearRoomReady(room)
		}
		return
	}
	if room.Series != nil && room.Stage != "settlement" {
		returnErr := hub.recoverRoom(room, "多回合成员变更，本场中止且不发放奖励。")
		if returnErr != nil {
			log.Printf("series_recovery_failed room=%d error=%v", room.ID, returnErr)
		}
		return
	}
	if teamBattle {
		hub.teamBattleMemberLeft(room, session.UID)
		return
	}
	if interrupted {
		if err := hub.recoverRoom(room, "有玩家离开，本局中止，已返回房间。本次中止不发放奖励。"); err != nil {
			log.Printf("room_recovery_failed room=%d error=%v", room.ID, err)
		}
		return
	}
	hub.clearRoomReady(room)
}
func (hub *Hub) equipmentChanged(session *Session) {
	if session.Room == nil {
		return
	}
	account, err := hub.Store.RoleManager().Snapshot(session.UID)
	if err != nil {
		return
	}
	hub.broadcastEquipment(session, account)
}

func (hub *Hub) broadcastEquipment(session *Session, account persistence.Account) {
	if session.Room == nil || session.Room.Stage != "room" {
		return
	}
	member := session.Room.Members[session.UID]
	if member == nil {
		return
	}
	// Inventory acknowledgements do not refresh the owner's room actor.
	// 3105 updates existing actors, including self via native 81F202/9F3540.
	// Unlike the new-member notification, it does not rejoin or clear readiness.
	roster := fighter(account, member)
	hub.broadcast(session.Room, protocol.Message{ID: protocol.MsgRoomRoster, Payload: roster}, 0)
	// 3105 replaces the appearance but its self branch does not rebuild item
	// effects. Native 3350 does (81F422 -> 9FAC40 -> 9EDF20), including wings.
	// Include unequipped talismans in this check so removing the last one also
	// clears its effects. Ordinary clothing/weapon-only inventories stay unchanged.
	for _, item := range account.Inventory {
		if len(item) == protocol.InventoryRecordSize && item[protocol.InventoryKindOffset] == protocol.ItemTalisman {
			hub.broadcast(session.Room, protocol.Message{ID: protocol.MsgRoomEquipmentEffects, Payload: roomEquipmentEffects(roster)}, 0)
			break
		}
	}
}
func (hub *Hub) roomMessage(session *Session, channel *Channel, message protocol.Message) (bool, error) {
	payload := message.Payload
	room := session.Room
	uid := session.UID
	switch message.ID {
	case protocol.MsgRoomInvite, protocol.MsgRoomInviteAccept, protocol.MsgRoomInviteDecline:
		return true, hub.roomInvitation(session, channel, message)
	case protocol.MsgRoomWaitingTimeout:
		if len(payload) != 0 {
			return true, protocol.ErrFrame
		}
		if room != nil && room.Stage == "room" && channel == session.game() && channel.Phase == "room" && room.Members[uid] != nil && room.Members[uid].Session == session {
			session.sendGame(protocol.Message{ID: protocol.MsgRoomWaitingExpired, Payload: protocol.Uint64Bytes(uid)})
			hub.leaveWithNotice(session, false, protocol.MsgRoomWaitingExpired)
		}
	case protocol.MsgRoomDetailRequest:
		id, err := protocol.ParseRoomDetailRequest(payload)
		if err != nil {
			return true, err
		}
		if channel.Phase != "lobby" || room != nil {
			return true, nil
		}
		target := hub.Rooms[uint16(id)]
		available := id != 0 && target != nil && target.LobbyID == session.LobbyID && target.Stage == "room" && !tutorialRoom(target)
		// This opens a password dialog, not a room. 3070 still checks password,
		// capacity, map policy and current membership when the player confirms.
		session.send(channel.ID, protocol.Message{ID: protocol.MsgRoomDetail, Payload: protocol.EncodeRoomDetail(id, available)})
	case protocol.MsgRoomListRequest:
		// A lobby refresh can be sent while 3070 is still awaiting 3100.
		// Joining must not invalidate that read-only request once it is dequeued.
		if (channel.Phase != "lobby" && channel.Phase != "room") || len(payload) != 3 || payload[1] > 1 {
			return true, protocol.ErrFrame
		}
		if !session.StageViewReady && hub.Store != nil {
			session.StageViewReady = true
			if err := hub.refreshStageSelection(session); err != nil {
				session.StageViewReady = false
				log.Printf("stage_refresh_failed uid=%d", uid)
			}
		}
		// Native 92DD40 sends page, refresh option, mode (0x88 = all).
		// The old adapter mistook the page for a mode and hid mode-0 rooms.
		ids := []int{}
		for id, room := range hub.Rooms {
			if !tutorialRoom(room) && room.LobbyID == session.LobbyID && (payload[1] == 1 || room.Stage == "room") && (payload[2] == 0x88 || protocol.RoomType(payload[2]) == room.Type()) {
				ids = append(ids, int(id))
			}
		}
		sort.Ints(ids)
		page := int(payload[0])
		if page < 1 {
			page = 1
		}
		const roomsPerPage = 9
		pages := (len(ids) + roomsPerPage - 1) / roomsPerPage
		if pages < 1 {
			pages = 1
		}
		start := (page - 1) * roomsPerPage
		response := make([]byte, 8)
		// 824280 derives record count from payload length; header words are
		// page counters (82432C -> +15C, 82433D -> +158), not record counts.
		protocol.WriteUint32(response, 0, uint32(page))
		protocol.WriteUint32(response, 4, uint32(pages))
		count := 0
		for index := start; index < len(ids) && index < start+roomsPerPage; index++ {
			response = append(response, roomList(hub.Rooms[uint16(ids[index])])...)
			count++
		}
		session.send(channel.ID, protocol.Message{ID: protocol.MsgRoomList, Payload: response})
		log.Printf("room_directory uid=%d page=%d option=%d mode=%d rooms=%d returned=%d", uid, page, payload[1], payload[2], len(hub.Rooms), count)
	case protocol.MsgCreateRoom:
		if len(payload) == protocol.RoomRequestSize {
			if err := hub.checkRoomName(payload); err != nil {
				session.sendGame(protocol.Message{ID: protocol.MsgRoomCreateError, Payload: []byte{44, 0}})
				session.sendGame(notice(moderationNotice(err)))
				return true, nil
			}
		}
		if room != nil && channel.Phase == "room" && room.Owner == uid {
			resolved, err := hub.resolve(payload)
			if err == nil && bytes.Equal(resolved, room.Request) {
				return true, nil
			}
		}
		if channel.Phase != "lobby" || room != nil || (!session.Bound && !tutorialRequest(payload)) {
			return true, protocol.ErrFrame
		}
		if tutorialRequest(payload) {
			title, err := hub.Store.TitleManager().AccountTitle(uid)
			if err != nil {
				return true, err
			}
			if title >= 2 {
				// A delayed native request may already be queued at completion.
				// Repair its local gate, cancel the attempted guide transition,
				// and never allocate another room or grant another reward.
				announced, err := hub.announceTutorialReward(session)
				if err != nil {
					return true, err
				}
				if !announced {
					syncTutorialTitle(session, title)
				}
				session.sendGame(protocol.Message{ID: protocol.MsgRoomLeft})
				session.sendGame(notice("新手引导已经完成，无需重复训练。"))
				return true, nil
			}
		}
		resolvedRequest, err := hub.resolveForPlayers(payload, session)
		if err != nil {
			session.send(channel.ID, protocol.Message{ID: protocol.MsgRoomCreateError, Payload: []byte{44, 0}})
			return true, nil
		}
		id := uint16(1)
		for hub.Rooms[id] != nil && id < 256 {
			id++
		}
		if id >= 256 {
			return true, protocol.ErrFrame
		}
		if hub.Config.TeamSeriesRounds != 0 && protocol.RoomType(resolvedRequest[protocol.RoomTypeOffset]) == protocol.TeamSurvival && resolvedRequest[protocol.RoomCapacityOffset] > seriesMaxFighters {
			session.sendGame(notice("团队多回合最多六名参战者。"))
			return true, nil
		}
		newRoom := &Room{SpectatorCapacity: hub.Config.SpectatorCapacity, LobbyID: session.LobbyID, ID: id, Owner: uid, Request: resolvedRequest, Stage: "room", Members: map[uint64]*Member{}}
		if newRoom.Type() == protocol.TeamSurvival && hub.Config.TeamSeriesRounds != 0 {
			newRoom.Series = newTeamSeries(hub.Config.TeamSeriesRounds)
		}
		if err = hub.install(newRoom, session); err != nil {
			return true, err
		}
		hub.Rooms[id] = newRoom
		log.Printf("room_created uid=%d room=%d mode=%d rooms=%d", uid, id, resolvedRequest[protocol.RoomTypeOffset], len(hub.Rooms))
	case protocol.MsgJoinRoom:
		join, err := protocol.ParseRoomJoinRequest(payload)
		if err != nil {
			return true, protocol.ErrFrame
		}
		id := join.RoomID
		if room != nil && room.ID == id && channel.Phase == "room" {
			if room.Owner == uid && room.CreationPending {
				return true, hub.acknowledgeCreatedRoomJoin(session, room)
			}
			return true, nil
		}
		if channel.Phase != "lobby" || room != nil {
			return true, protocol.ErrFrame
		}
		target := hub.Rooms[id]
		code := uint32(0)
		switch {
		case join.Mode != protocol.JoinAsPlayer && join.Mode != protocol.JoinAsSpectator:
			code = 130
		case target == nil || target.LobbyID != session.LobbyID:
			code = 29
		case target.Stage != "room":
			code = 30
		case join.Mode == protocol.JoinAsSpectator && target.observerCount() >= target.observerLimit():
			code = 32
		case join.Mode == protocol.JoinAsPlayer && target.fighterCount() >= int(target.Request[37]):
			code = 32
		case !bytes.Equal(bytes.SplitN(join.Password[:], []byte{0}, 2)[0], bytes.SplitN(target.Request[21:32], []byte{0}, 2)[0]):
			code = 31
		}
		if code != 0 {
			session.send(channel.ID, protocol.Message{ID: 3080, Payload: append(bytes.Clone(payload), protocol.Uint32Bytes(code)...)})
			return true, nil
		}
		// A valid lobby request can arrive before native 1156 binding. This is
		// an admission failure, not a corrupt frame: keep the login alive.
		if !tutorialRoom(target) && (!session.Bound || time.Now().After(session.P2PUntil)) {
			log.Printf("room_join_transport_not_ready uid=%d account=%q room=%d bound=%t peer=%d udp_port=%d lease_valid=%t", uid, session.Account, id, session.Bound, session.P2P, session.UDPPort, time.Now().Before(session.P2PUntil))
			session.send(channel.ID, protocol.Message{ID: 3080, Payload: append(bytes.Clone(payload), protocol.Uint32Bytes(130)...)})
			session.sendGame(notice("无法进入房间：游戏网络绑定尚未完成，请稍后重试。账号仍保持登录。"))
			return true, nil
		}
		allows, err := hub.stageGate(session)
		if err != nil || !allows(protocol.ReadUint32(target.Request, 38)) {
			session.send(channel.ID, protocol.Message{ID: 3080, Payload: append(bytes.Clone(payload), protocol.Uint32Bytes(130)...)})
			session.sendGame(notice("无法加入：地图已关闭、称号条件未满足或目录版本不匹配。"))
			return true, nil
		}
		return true, hub.installAs(target, session, join.Mode == protocol.JoinAsSpectator)
	case protocol.MsgToggleSpectator:
		if len(payload) != 0 {
			return true, protocol.ErrFrame
		}
		return true, hub.toggleSpectator(session)
	case 3075:
		if len(payload) != 1 || channel.Phase != "lobby" || room != nil {
			return true, protocol.ErrFrame
		}
		allows, err := hub.stageGate(session)
		if err != nil {
			session.sendGame(notice("无法查询可加入地图，请核对关卡条件和客户端版本。"))
			return true, nil
		}
		ids := []int{}
		for id := range hub.Rooms {
			ids = append(ids, int(id))
		}
		sort.Ints(ids)
		for _, id := range ids {
			target := hub.Rooms[uint16(id)]
			if target.LobbyID == session.LobbyID && target.Stage == "room" && target.Request[21] == 0 && target.fighterCount() < int(target.Request[37]) && allows(protocol.ReadUint32(target.Request, 38)) && (payload[0] == 0x88 || protocol.RoomType(payload[0]) == target.Type()) {
				return true, hub.install(target, session)
			}
		}
		session.sendGame(notice("暂无可加入的房间，请创建房间或稍后重试。"))
	case protocol.MsgTutorialComplete:
		return true, hub.completeTutorial(session, channel, payload)
	case protocol.MsgLeaveRoom:
		if len(payload) != 0 {
			return true, protocol.ErrFrame
		}
		if room != nil && (room.Stage == "settlement" || channel.Phase == "settlement") {
			return true, hub.returnFromSettlement(session)
		}
		hub.leave(session, true)
	case seriesReportMessage:
		return true, hub.seriesResult(session, payload)
	case 4110:
		return true, hub.settleReport(session, payload)
	case 4115:
		if len(payload) != 4 {
			return true, protocol.ErrFrame
		}
		if room != nil && channel.Phase == "settlement" && (room.Stage == "settlement" || room.Stage == "room") {
			room.Members[uid].ResultAcknowledged = true
			hub.advanceResultAcknowledgements(room)
		}
	case protocol.MsgRoomAcknowledgement:
		if len(payload) != 12 || protocol.ReadUint64(payload, 0) != uid {
			return true, protocol.ErrFrame
		}
		if room != nil && room.Members[uid] != nil {
			state := protocol.ReadUint32(payload, 8)
			if state == 0 && (room.Stage == "settlement" || room.Stage == "room") {
				// Native result confirmation returns in place via 3550(0),
				// without 3110. Clear the icon for every peer and unlock ready.
				var returned *persistence.Account
				if channel.Phase == "settlement" {
					a, err := hub.Store.RoleManager().Snapshot(uid)
					if err != nil {
						return true, err
					}
					returned = &a
				}
				channel.Phase = "room"
				room.Stage = "room"
				hub.broadcast(room, message, 0)
				if returned != nil && session.syncUnequippedInventory(returned.Inventory) {
					hub.refreshExpiredEquipment(session, *returned)
				}
				if returned != nil {
					if err := hub.extendedTaskLists(session); err != nil {
						session.sendGame(notice("任务进度刷新失败，请稍后打开任务列表。"))
					}
				}
			} else if state == 3 && channel.Phase == "settlement" {
				hub.broadcast(room, message, 0)
			}
		}
	case 3260, 3262, 3263:
		return true, hub.exchangeSeat(session, message)
	case 3230:
		if len(payload) != 1 || payload[0] > 1 {
			return true, protocol.ErrFrame
		}
		if !room.canConfigure(session) {
			session.sendGame(notice("请等待所有玩家返回房间后再换队。"))
			return true, nil
		}
		member := room.Members[uid]
		if member.Spectator {
			return true, nil
		}
		if member.Team == payload[0] {
			session.sendGame(protocol.Message{ID: 3250, Payload: roomTeam(member)})
			return true, nil
		}
		if room.Type().IsTeam() {
			position, ok := room.freeTeamPosition(payload[0], uid)
			if !ok {
				session.sendGame(notice("目标队伍没有空位。"))
				return true, nil
			}
			member.Spawn = position
		}
		member.Team = payload[0]
		hub.broadcast(room, protocol.Message{ID: 3250, Payload: roomTeam(member)}, 0)
		hub.clearRoomReady(room)
	case protocol.MsgKickRoomPlayer:
		return true, hub.kickRoomPlayer(session, payload)
	case protocol.MsgChangeRoomOwner:
		return true, hub.changeRoomOwner(session, payload)
	case 3200:
		if len(payload) != 48 {
			return true, protocol.ErrFrame
		}
		if room == nil || room.Stage != "room" || room.Owner != uid {
			return true, nil
		}
		if !room.canConfigure(session) {
			session.sendGame(notice("请等待所有玩家返回房间后再修改设置。"))
			return true, nil
		}
		candidate, err := applyRoomSettings(room.Request, payload)
		if err != nil {
			return true, nil
		}
		if hub.rejectRoomName(session, candidate) {
			return true, nil
		}
		resolved, err := hub.resolveForPlayers(candidate, roomPlayers(room)...)
		if err != nil {
			session.sendGame(notice("房间设置或地图不可用。"))
			return true, nil
		}
		if bytes.Equal(room.Request, resolved) {
			session.sendGame(protocol.Message{ID: 3220, Payload: roomSettings(resolved)})
			return true, nil
		}
		if room.observerCount() > 0 && (resolved[34] == 0 || protocol.RoomTypeFromRequest(resolved) != protocol.TeamSurvival) {
			session.sendGame(notice("请先让观战者离开或切换为参战者，再关闭观战。"))
			return true, nil
		}
		room.Request = resolved
		hub.broadcast(room, protocol.Message{ID: 3220, Payload: roomSettings(resolved)}, 0)
		hub.clearRoomReady(room)
	case protocol.MsgReady, protocol.MsgCancelReady:
		if len(payload) != 0 || room == nil {
			return true, protocol.ErrFrame
		}
		if !room.canConfigure(session) {
			return true, nil
		}
		member := room.Members[uid]
		if member.Spectator {
			return true, nil
		}
		if room.CreationPending {
			return true, nil
		}
		if !tutorialRoom(room) && time.Now().After(session.P2PUntil) {
			return true, protocol.ErrFrame
		}
		if message.ID == protocol.MsgCancelReady {
			hub.cancelNetworkProbe(room)
			hub.cancelSeatExchange(room)
			member.Ready = false
			hub.broadcast(room, protocol.Message{ID: protocol.MsgPlayerNotReady, Payload: protocol.Uint64Bytes(uid)}, 0)
			return true, nil
		}
		// Both PVE modes accept a solo party. Their persisted plan, map access
		// and reward configuration are still validated before startBattle.
		if uid == room.Owner && room.Type() != protocol.FreePractice && room.Type() != protocol.FosterMode && room.Type() != protocol.StageAssault && !tutorialRoom(room) {
			if room.fighterCount() < 2 {
				return true, nil
			}
			if room.Type() == protocol.TeamSurvival || room.Type() == protocol.TeamDeathmatch {
				teams := map[byte]bool{}
				for _, member := range room.Members {
					if !member.Spectator {
						teams[member.Team] = true
					}
				}
				if len(teams) < 2 {
					return true, nil
				}
			}
		}
		if uid == room.Owner {
			allows, err := hub.stageGate(roomPlayers(room)...)
			if err != nil || !allows(protocol.ReadUint32(room.Request, 38)) {
				session.sendGame(notice("当前地图不可开战，请检查地图开关、全员称号条件和客户端版本。"))
				return true, nil
			}
			for other, member := range room.Members {
				if !member.Spectator && other != uid && !member.Ready {
					return true, nil
				}
			}
		}
		hub.cancelSeatExchange(room)
		member.Ready = true
		hub.broadcast(room, protocol.Message{ID: protocol.MsgPlayerReady, Payload: protocol.Uint64Bytes(uid)}, 0)
		if uid == room.Owner {
			for _, member := range room.Members {
				if (!member.Spectator && !member.Ready) || (!tutorialRoom(room) && time.Now().After(member.Session.P2PUntil)) {
					return true, nil
				}
			}
			if tutorialRoom(room) {
				return true, hub.startBattle(room)
			}
			hub.beginNetworkProbe(room)
		}
	case protocol.MsgNetworkDelayReply:
		return true, hub.networkDelayReply(session, payload)
	case protocol.MsgResourceReady:
		if len(payload) != 0 {
			return true, protocol.ErrFrame
		}
		if room == nil {
			return true, nil
		}
		if room.Members[uid].Loaded {
			return true, nil
		}
		if room.Stage != "loading" {
			return true, nil
		}
		room.Members[uid].Loaded = true
		hub.broadcast(room, protocol.Message{ID: protocol.MsgPlayerResourceReady, Payload: protocol.Uint64Bytes(uid)}, 0)
		hub.advanceRoomLoading(room)
	case protocol.MsgBattleInputReady:
		ready, err := protocol.ParseBattleInputReady(payload)
		if err != nil || ready.UID != uid {
			return true, protocol.ErrFrame
		}
		if room == nil || ready.RoomID != room.ID {
			return true, nil
		}
		if room.Members[uid].Spectator || room.Members[uid].Input {
			return true, nil
		}
		if room.Stage != "wait_ready" {
			return true, nil
		}
		room.Members[uid].Input = true
		for _, member := range room.Members {
			if !member.Spectator && !member.Input {
				return true, nil
			}
		}
		room.Stage = "battle"
		room.BattleStartedAt = time.Now()
		room.StageElapsedSeconds = 0
		if room.LoadTimer != nil {
			room.LoadTimer.Stop()
			room.LoadTimer = nil
		}
		for _, member := range room.Members {
			member.Session.game().Phase = "battle"
		}
		response := append(protocol.Uint32Bytes(uint32(room.ID)), protocol.Uint32Bytes(uint32(room.ID))...)
		response = append(response, protocol.Uint32Bytes(room.Serial)...)
		hub.broadcast(room, protocol.Message{ID: protocol.MsgBattleStarted, Payload: response}, 0)
		hub.startBattleClock(room)
	case protocol.MsgBattleEvent:
		return true, hub.battleMessage(session, channel, message)
	case protocol.MsgStageWaveReport:
		return true, hub.stageWaveReport(session, channel, payload)
	default:
		return false, nil
	}
	return true, nil
}

func (hub *Hub) startBattle(room *Room) error {
	if err := room.validateSeriesTeams(); err != nil {
		return err
	}
	var waves *stageWaves
	var foster *protocol.FosterPlan
	if room.Type() == protocol.FosterMode {
		var err error
		foster, err = hub.prepareFosterBattle(room)
		if err != nil {
			return err
		}
	}
	if room.Type() == protocol.StageAssault {
		var err error
		waves, err = hub.prepareStageBattle(room)
		if err != nil {
			return err
		}
	}
	for _, member := range room.Members {
		if randomWeaponsEnabled && !member.Spectator && member.Session.RandomWeaponMode != protocol.RandomWeaponOff {
			if err := hub.selectRandomWeapon(member.Session, member.Session.RandomWeaponMode, true); err != nil {
				return err
			}
		}
	}
	for uid, member := range room.Members {
		account, err := hub.Store.RoleManager().Snapshot(uid)
		if err != nil {
			return err
		}
		member.BattleLevel = persistence.ProfileLevel(account.Profile)
	}
	serial, err := hub.Store.BattleManager().NextBattle()
	if err != nil {
		return err
	}
	room.Serial = serial
	if room.Series != nil {
		room.Series = newTeamSeries(room.Series.limit)
	}
	room.StageWaves = waves
	room.FosterPlan = foster
	room.FosterSpawned = nil
	room.FosterTriggered = nil
	room.FosterRetired = nil
	room.FosterFinishReported = false
	if foster != nil {
		room.FosterSpawned = make([]int, len(foster.Groups))
		room.FosterTriggered = make([]bool, len(foster.Groups))
		room.FosterRetired = make([]int, len(foster.Groups))
	}
	room.PVEActors = nil
	room.NeutralNPC = nil
	room.HealthReceipts = nil
	room.PVEBlocks = nil
	room.FosterPositions = nil
	room.Reliable = nil
	room.ReliableSerial = serial
	room.PairSelectionVersions = nil
	room.PairSelections = nil
	room.Projectiles = nil
	room.Collectibles = nil
	room.Reports = nil
	room.DepartedSlots = nil
	room.Stage = "loading"
	hub.watchLoading(room)
	for _, member := range room.Members {
		member.ResultAcknowledged = false
		member.Loaded, member.Input = false, false
		member.BattleEvents = nil
		member.TalismanEvents = nil
		member.Session.ConsumeIntents = nil
		member.Session.TalismanPending = nil
		response := battleStartPayload(room)
		member.Session.game().Phase = "loading"
		member.Session.sendGame(protocol.Message{ID: protocol.MsgBattleLoading, Payload: response})
	}
	return nil
}

// Native 81E1A0 passes +11 to 818910, which enables entity+1B74 only
// for that slot. Every recipient must select the same battle controller.
// The server chooses the room owner; +11 is not the recipient's own slot.
func battleStartPayload(room *Room) []byte {
	response := make([]byte, 53)
	protocol.WriteUint32(response, 0, uint32(room.ID))
	protocol.WriteUint32(response, 5, room.Serial)
	protocol.WriteUint16(response, 11, uint16(room.Members[room.Owner].Slot))
	// +13 holds eight GetNetDelay values (81E1A0 -> 5495A0 -> 5564A0),
	// not peer IDs. Values are server-observed application round trips.
	// Empty slots and the guide (which skips P2P probing) retain zero.
	for _, member := range room.Members {
		if member.Slot < 8 {
			protocol.WriteUint32(response, 13+int(member.Slot)*4, member.NetworkDelay)
		}
	}
	protocol.WriteUint32(response, 45, uint32(room.ID))
	protocol.WriteUint32(response, 49, room.Serial)
	return response
}
