package game

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
	"kungfu.local/server/internal/tunnel"
)

// Hub serializes room transitions. Network writes run outside this lock so a
// slow player cannot block the other players. Split by room if scale requires it.
type Hub struct {
	ioCond               *sync.Cond
	ioPaused             bool
	ioScope              *storageScope
	lastSlowHandleLog    time.Time
	slowLogMutex         sync.Mutex
	auditWriteMutex      sync.Mutex
	releaseVersion       atomic.Value
	Invites              map[uint64]roomInvitation
	SecurityLogDirectory string
	// Configured before serving. Audit jobs must execute serially.
	SubmitSecurityAudit     func(func()) bool
	securityAuditRetryAfter time.Time
	securityAuditSkipped    uint64
	Trace                   *log.Logger
	Mutex                   sync.RWMutex
	Store                   *persistence.Store
	Config                  Config
	Rooms                   map[uint16]*Room
	Sessions                map[uint64]*Session
	NextPlayer              uint32
	PeerKey                 []byte
}

type Channel struct {
	ID          uint32
	Kind        string
	Phase       string
	Decoder     protocol.Decoder
	LoginBuffer []byte
	Sequence    uint64
}

type Session struct {
	ClientRelease        string
	UpdateNoticeVersion  string
	RandomWeaponMode     uint32
	StageViewRequested   bool
	StageViewReady       bool
	StageViewDigest      [32]byte
	TitleOffer           byte              // Server-announced title; retained after claim to bind retries.
	ExtendedTaskNotified map[uint16]string // Client hash + cycle, scoped to this login.
	TalismanPending      map[uint32]pendingTalisman
	LobbyID              uint32 // Native 2010 selected lobby, distinct from tunnel channel ID.
	Trace                *log.Logger
	UID                  uint64
	Account              string
	Nickname             string
	Namespace            string
	Channels             map[uint32]*Channel
	Output               chan tunnel.Frame
	Done                 chan struct{}
	closeOnce            sync.Once
	queuedBytes          atomic.Int64
	GameChannel          uint32
	BootstrapChannel     uint32
	HandoffUntil         time.Time
	GrantUntil           time.Time
	TablesReady          bool
	Room                 *Room
	P2P                  uint32
	P2PUntil             time.Time
	Bound                bool
	Port                 uint16
	LastChat             time.Time
	LastFriendRequest    time.Time
	ConsumeIntents       map[uint32]bool
	LastBattleNotice     time.Time
	UDPPort              uint16
	LastUDPNotice        time.Time
	UDPRelayed           uint64
	Inventory            map[uint32][]byte
	VIPKind              uint32
	VIPShopPercent       uint32
	WeaponRevision       uint64
	TalismanQuote        *talismanQuote
	RenewalQuote         *renewalQuote
	LoggedOut            bool
	MailPreview          uint32
	MailAttachment       uint32
	MailClaimFailed      bool
	MailDirty            bool
	refreshBusy          bool
	refreshRevision      uint64
	mailRevision         uint64
}

var errAccountOnline = errors.New("account already online")
var errServerFull = errors.New("server session capacity reached")
var errClientPort = errors.New("invalid client port")

func NewHub(store *persistence.Store, config Config) *Hub {
	return &Hub{Store: store, Config: config, Rooms: map[uint16]*Room{}, Sessions: map[uint64]*Session{}, NextPlayer: 1001}
}

func (hub *Hub) Attach(account persistence.Account, port uint16, peerReceipt ...string) (*Session, error) {
	hub.lockState()
	defer hub.unlockState()
	if previous := hub.Sessions[account.UID]; previous != nil {
		select {
		case <-previous.Done:
			hub.scopeSession(previous)
			hub.leave(previous, false)
			delete(hub.Sessions, account.UID)
		default:
		}
	}
	if hub.Sessions[account.UID] != nil {
		return nil, errAccountOnline
	}
	if port == 0 {
		return nil, errClientPort
	}
	limit := hub.Config.sessionLimit()
	if len(hub.Sessions) >= limit {
		log.Printf("login_capacity_reached sessions=%d limit=%d", len(hub.Sessions), limit)
		return nil, errServerFull
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	session := &Session{Trace: hub.Trace, UID: account.UID, Account: account.Account, Nickname: account.Nickname,
		Namespace: hex.EncodeToString(nonce), Channels: map[uint32]*Channel{}, Output: make(chan tunnel.Frame, 128),
		Done: make(chan struct{}), Port: port, GrantUntil: time.Now().Add(2 * time.Minute)}
	if err := hub.initPeerKey(); err != nil {
		return nil, err
	}
	if len(peerReceipt) > 0 {
		if err := hub.resumePeer(session, peerReceipt[0]); err != nil {
			return nil, err
		}
	}
	hub.Sessions[session.UID] = session
	return session, nil
}

func (session *Session) Close() { session.closeOnce.Do(func() { close(session.Done) }) }
func (session *Session) emit(frame tunnel.Frame) bool {
	frame.QueuedAt = time.Now()
	select {
	case <-session.Done:
		return false
	default:
	}
	if session.queuedBytes.Add(int64(len(frame.Data)+128)) > 2*1024*1024 {
		session.Close()
		return false
	}
	select {
	case session.Output <- frame:
		session.traceFrame("S->C queued", frame)
		return true
	default:
		session.Close()
	}
	return false
}
func (session *Session) game() *Channel { return session.Channels[session.GameChannel] }
func (session *Session) send(channelID uint32, message protocol.Message) {
	encoded, err := protocol.Encode(message)
	if err != nil {
		session.Close()
		return
	}
	session.emit(tunnel.Frame{Op: "data", Channel: channelID, Data: encoded})
}
func (session *Session) sendGame(message protocol.Message) {
	if session.game() != nil {
		session.send(session.GameChannel, message)
	}
}
func (hub *Hub) Detach(session *Session) {
	defer session.Close()
	hub.lockState()
	hub.scopeSession(session)
	defer hub.unlockState()
	if hub.Sessions[session.UID] != session {
		return
	}
	hub.leave(session, false)
	delete(hub.Sessions, session.UID)
	session.Close()
}
func (hub *Hub) profileReady(session *Session) error {
	channel := session.Channels[session.BootstrapChannel]
	if !session.TablesReady || channel == nil || channel.Phase != "bootstrap" {
		return nil
	}
	account, err := storage2_1(hub, hub.Store.RoleManager().Snapshot, session.UID)
	if err != nil {
		return err
	}
	if len(account.Profile) == 360 && bytes.Equal(account.Profile, make([]byte, 360)) {
		options, err := persistence.CharacterOptions(hub.Config.CharacterChoices)
		if err != nil {
			return fmt.Errorf("character creation choices are not configured")
		}
		channel.Phase = "character_create"
		session.send(channel.ID, protocol.Message{ID: protocol.MsgCharacterOptions, Payload: options})
		return nil
	}
	state, err := storage2_1(hub, hub.Store.RandomWeapon, session.UID)
	if err != nil {
		return err
	}
	session.RandomWeaponMode = protocol.RandomWeaponOff
	if randomWeaponsEnabled {
		session.RandomWeaponMode = state.Mode
	}
	channel.Phase = "profile_sent"
	session.rememberInventory(account.Inventory)
	session.send(channel.ID, protocol.Message{ID: protocol.MsgCharacterCreated, Payload: append(account.Profile, account.InventoryBytes()...)})
	session.send(channel.ID, protocol.RandomWeaponPreferences(session.UID, session.RandomWeaponMode))
	return nil
}

// Handle accepts authenticated tunnel operations. UID is bound by Attach;
// native packet fields can only confirm it, never select another account.
func (hub *Hub) Handle(session *Session, frame tunnel.Frame) error {
	started := time.Now()
	roomUnlock := hub.lockRoomFrame(session, frame)
	owner := false
	if roomUnlock == nil {
		owner = hub.lockFrame(session, frame)
	}
	var scope *storageScope
	if owner {
		scope = hub.ioScope
	}
	acquired := time.Now()
	defer func() {
		finished := time.Now()
		storageTime := time.Duration(0)
		if scope != nil {
			storageTime = scope.ioTime
		}
		held := finished.Sub(acquired) - storageTime
		hub.slowLogMutex.Lock()
		report := (acquired.Sub(started) > 100*time.Millisecond || held > 100*time.Millisecond || storageTime > 100*time.Millisecond) && finished.Sub(hub.lastSlowHandleLog) > 5*time.Second
		uid := session.UID
		if report {
			hub.lastSlowHandleLog = finished
		}
		hub.slowLogMutex.Unlock()
		if owner {
			hub.ioScope = nil
		}
		if roomUnlock != nil {
			roomUnlock()
		} else {
			hub.Mutex.Unlock()
		}
		if report {
			log.Printf("game_handle_slow uid=%d op=%s channel=%d wait_ms=%d hold_ms=%d storage_ms=%d", uid, frame.Op, frame.Channel, acquired.Sub(started).Milliseconds(), held.Milliseconds(), storageTime.Milliseconds())
		}
	}()
	if frame.Op == "data" || frame.Op == "logout" || frame.Op == "open" || frame.Op == "close" {
		session.refreshRevision++
	}
	if hub.Sessions[session.UID] != session {
		if session.LoggedOut {
			// A native 2060 already released this account. The unchanged bridge
			// may still request logout before authenticating the same process.
			if frame.Op == "logout" {
				session.emit(tunnel.Frame{Op: "logged_out"})
			}
			return nil
		}
		return persistence.ErrDenied
	}
	if frame.Op != "data" {
		session.traceFrame("C->S", frame)
	}
	switch frame.Op {
	case "logout":
		hub.leave(session, false)
		session.LoggedOut = true
		delete(hub.Sessions, session.UID)
		session.emit(tunnel.Frame{Op: "logged_out"})
		log.Printf("logout_complete uid=%d account=%q", session.UID, session.Account)
		return nil
	case "ping":
		session.emit(tunnel.Frame{Op: "pong"})
		return nil
	case "ready":
		session.TablesReady = true
		return hub.profileReady(session)
	case "open":
		if frame.Channel == 0 || len(session.Channels) >= 8 || session.Channels[frame.Channel] != nil || (frame.Kind != "sdk" && frame.Kind != "game") {
			return protocol.ErrFrame
		}
		session.Channels[frame.Channel] = &Channel{ID: frame.Channel, Kind: frame.Kind, Phase: "connected"}
	case "close":
		if frame.Channel == session.GameChannel {
			hub.leave(session, false)
			session.GameChannel = 0
			session.LobbyID = 0
			session.Bound = false
		}
		if frame.Channel == session.BootstrapChannel {
			session.BootstrapChannel = 0
		}
		delete(session.Channels, frame.Channel)
	case "udp":
		return hub.datagram(session, frame)
	case "data":
		channel := session.Channels[frame.Channel]
		if channel == nil || len(frame.Data) > 65536 {
			return protocol.ErrFrame
		}
		if channel.Phase == "closed" {
			return nil
		}
		if channel.Kind == "sdk" {
			return hub.sdk(session, channel, frame.Data)
		}
		messages, err := channel.Decoder.Feed(frame.Data)
		if err != nil {
			session.tracePacket("C->S", channel.ID, "invalid-game-frame", 0, frame.Data, false)
			return err
		}
		for _, message := range messages {
			session.tracePacket("C->S", channel.ID, "game", message.ID, message.Payload, false)
			channel.Sequence++
			if err = hub.route(session, channel, message); err != nil {
				var rejection *securityRejection
				if errors.As(err, &rejection) {
					hub.recordSecurityRejection(session, channel, message, err)
					continue
				}
				log.Printf("packet_rejected uid=%d channel=%d phase=%s message=%d reason=%v", session.UID, channel.ID, channel.Phase, message.ID, err)
				return err
			}
		}
	default:
		return protocol.ErrFrame
	}
	return nil
}

func (hub *Hub) sdk(session *Session, channel *Channel, data []byte) error {
	channel.LoginBuffer = append(channel.LoginBuffer, data...)
	if len(channel.LoginBuffer) > 8192 {
		return protocol.ErrFrame
	}
	for len(channel.LoginBuffer) >= 8 {
		buffer := channel.LoginBuffer
		length := int(protocol.ReadUint16(buffer, 4)) + int(buffer[7]) + 8
		if length > 4104 {
			return protocol.ErrFrame
		}
		if len(buffer) < length {
			return nil
		}
		flags, body, err := protocol.ReadLogin(bytes.NewReader(buffer[:length]))
		if err != nil {
			return err
		}
		session.tracePacket("C->S", channel.ID, "sdk", uint32(protocol.ReadUint16(body, 0)), body[2:], flags == 1)
		channel.LoginBuffer = buffer[length:]
		var reply protocol.Message
		if flags == 1 && (channel.Phase == "connected" || channel.Phase == "authenticated") {
			// Re-authentication belongs to this already authenticated tunnel UID.
			// Never retain the previous native channels or room across SDK login.
			hub.resetNativeSession(session, channel.ID)
			channel.Phase = "authenticated"
			session.GrantUntil = time.Now().Add(2 * time.Minute)
			reply = protocol.LoginAck(session.Account, session.UID)
		} else if flags == 0 && channel.Phase == "authenticated" && protocol.ReadUint16(body, 0) == 1011 {
			reply = protocol.LoginDirectory(session.Port)
		} else {
			return protocol.ErrFrame
		}
		session.emit(tunnel.Frame{Op: "data", Channel: channel.ID, Data: protocol.LoginEncode(reply)})
	}
	return nil
}

func (hub *Hub) route(session *Session, channel *Channel, message protocol.Message) error {
	payload := message.Payload
	if message.ID == 0 {
		if len(payload) != 0 {
			return protocol.ErrFrame
		}
		return nil
	}
	if message.ID == protocol.MsgGameLogin || message.ID == protocol.MsgEnterLobby {
		if channel.Phase != "connected" || len(payload) != 96 || protocol.ReadUint64(payload, 0) != session.UID || protocol.ReadUint32(payload, 49) != 594 {
			return protocol.ErrFrame
		}
		if message.ID == protocol.MsgGameLogin {
			if time.Now().After(session.GrantUntil) || session.BootstrapChannel != 0 || session.GameChannel != 0 {
				return persistence.ErrDenied
			}
			session.BootstrapChannel = channel.ID
			channel.Phase = "bootstrap"
			account, err := storage2_1(hub, hub.Store.RoleManager().Snapshot, session.UID)
			if err != nil {
				return err
			}
			catalog, err := hub.Config.lobbyCatalog(session.Port)
			if err != nil {
				return err
			}
			replies := protocol.Bootstrap(account.InventoryBytes(), session.Port, account.Gold, account.Tickets)
			session.VIPKind = account.VIPKind()
			session.VIPShopPercent = 0
			if session.VIPKind >= 2 {
				session.VIPShopPercent, err = storage2_1(hub, hub.Store.ShopManager().VIPShopPercent, session.UID)
				if err != nil {
					return err
				}
			}
			for i := range replies {
				if replies[i].ID == protocol.MsgLoginCore {
					protocol.WriteUint32(replies[i].Payload, 33, session.VIPKind)
				}
			}
			honour, err := hub.honourRules()
			if err != nil {
				return err
			}
			for i := range replies {
				if replies[i].ID == 1035 {
					protocol.WriteUint32(replies[i].Payload, 0, uint32(len(honour.Periods)))
				}
			}
			replies = append(replies[:len(replies)-2], catalog...)
			for _, reply := range replies {
				session.send(channel.ID, reply)
			}
			session.send(channel.ID, vipIdentityPacket(session.VIPKind, session.VIPShopPercent))
			return hub.profileReady(session)
		}
		if time.Now().After(session.HandoffUntil) || session.GameChannel != 0 {
			return persistence.ErrDenied
		}
		lobbyID, err := hub.admitLobby(protocol.ReadUint32(payload, 8))
		if err != nil {
			return err
		}
		session.LobbyID = lobbyID
		session.HandoffUntil = time.Time{}
		session.GrantUntil = time.Time{}
		session.GameChannel = channel.ID
		channel.Phase = "lobby"
		reply := protocol.Lobby(session.Port)
		protocol.WriteUint32(reply.Payload, 0, lobbyID)
		protocol.WriteUint32(reply.Payload, 26, lobbyID)
		session.send(channel.ID, reply)
		log.Printf("lobby uid=%d", session.UID)
		// The lobby acknowledgement precedes native PVE panel initialization.
		// Wait for its first room-directory request before seeding those panels.
		session.StageViewReady = false
		session.StageViewRequested = false
		return nil
	}
	if channel.Phase == "connected" {
		return protocol.ErrFrame
	}
	if handled, err := hub.characterMessage(session, channel, message); handled {
		return err
	}
	if message.ID == 3320 {
		if channel.ID != session.BootstrapChannel || (channel.Phase != "profile_sent" && channel.Phase != "handoff") || !bytes.Equal(payload, protocol.Uint32Bytes(1)) {
			return protocol.ErrFrame
		}
		session.send(channel.ID, protocol.Message{ID: 3330, Payload: payload})
		if channel.Phase != "handoff" {
			channel.Phase = "handoff"
			session.HandoffUntil = time.Now().Add(2 * time.Minute)
			session.send(channel.ID, protocol.Message{ID: 1201, Payload: protocol.Uint32Bytes(1)})
		}
		return nil
	}
	if message.ID == protocol.MsgPeerHeartbeat {
		if len(payload) != 0 {
			return protocol.ErrFrame
		}
		catalog, err := hub.Config.lobbyCatalog(session.Port)
		if err != nil {
			return err
		}
		for _, reply := range catalog {
			session.send(channel.ID, reply)
		}
		return nil
	}
	if channel.ID != session.GameChannel {
		return nil
	}
	if message.ID == protocol.MsgLogout {
		if len(payload) != 0 {
			return protocol.ErrFrame
		}
		// 2060 leaves the current GS, including native channel switches. Only
		// tunnel logout (before password reauthentication) releases the account.
		session.send(channel.ID, protocol.Message{ID: 2070})
		hub.leave(session, false)
		session.GameChannel, session.BootstrapChannel = 0, 0
		session.LobbyID = 0
		session.StageViewRequested = false
		session.StageViewReady = false
		session.StageViewDigest = [32]byte{}
		session.Bound = false
		session.UDPRelayed = 0
		session.ConsumeIntents, session.Inventory = nil, nil
		session.TalismanPending = nil
		session.GrantUntil = time.Now().Add(2 * time.Minute)
		session.HandoffUntil = session.GrantUntil
		for _, old := range session.Channels {
			if old.Kind == "game" {
				old.Phase = "closed"
				old.LoginBuffer = nil
			}
		}
		log.Printf("native_channel_left uid=%d channel=%d protocol=2070", session.UID, channel.ID)
		return nil
	}
	if message.ID == protocol.MsgPeerBind {
		if len(payload) != 12 || session.P2P == 0 || protocol.ReadUint64(payload, 0) != session.UID || protocol.ReadUint32(payload, 8) != session.P2P {
			return protocol.ErrFrame
		}
		session.P2PUntil = time.Now().Add(time.Minute)
		session.Bound = true
		return nil
	}
	if message.ID == protocol.MsgWeaponSwitchRequest {
		return hub.switchWeapon(session, channel, payload)
	}
	if session.Room != nil && session.Room.isObserver(session) && (message.ID == 4201 || message.ID == protocol.MsgBattleEvent) {
		return nil
	}
	if message.ID == 4201 || (message.ID == protocol.MsgBattleEvent && len(payload) >= 4 && (protocol.ReadUint32(payload, 0) == 8291 || protocol.ReadUint32(payload, 0) == 8292)) {
		return hub.useTalisman(session, channel, message)
	}
	if message.ID == 4200 || (message.ID == protocol.MsgBattleEvent && len(payload) >= 4 && protocol.ReadUint32(payload, 0) == 8289) {
		return hub.consume(session, channel, message)
	}
	if handled, err := hub.roomMessage(session, channel, message); handled {

		return err
	}
	if message.ID == msgFriends {
		return hub.friends(session, payload)
	}
	if message.ID == 5000 || message.ID == 5002 {
		if err := hub.chat(session, message); err != nil {
			session.sendGame(notice("消息格式不正确，请重新输入。"))
		}
		return nil
	}
	if channel.Phase != "lobby" && channel.Phase != "room" {
		return nil
	}
	switch message.ID {
	case protocol.MsgStageStateQuery:
		if len(payload) != 0 {
			session.sendGame(notice("关卡状态查询格式不正确。"))
			return nil
		}
		session.sendGame(protocol.Message{ID: protocol.MsgStageStateReply, Payload: protocol.EmptyStageState()})
		return nil
	case 21370:
		return hub.stageSelection(session, payload)
	case protocol.MsgClaimTitleReward:
		return hub.claimTitleReward(session, payload)
	case 6001, 6002:
		if len(payload) != 0 {
			return protocol.ErrFrame
		}
		return hub.extendedTaskList(session, message.ID+40)
	case 6000, 6050, 6080:
		return hub.tasks(session, message)
	case 6051, 6052, 6081, 6082, 6311, 6312:
		return hub.extendedTaskAction(session, message)
	case 4202, 4204:
		return hub.repairTalisman(session, channel, message)
	case 21410:
		if len(payload) != 0 {
			return protocol.ErrFrame
		}
		config, revision, err := hub.weaponConfig()
		if err != nil {
			return err
		}
		session.WeaponRevision = 0
		data, err := config.weaponLevelPayload()
		if err != nil {
			return err
		}
		if len(data) == 0 {
			session.sendGame(notice("尚未配置武器升级表。"))
			return nil
		}
		session.sendGame(protocol.Message{ID: 21411, Payload: data})
		session.WeaponRevision = revision
	case 21412:
		return hub.upgradeWeapon(session, channel, payload)
	case protocol.MsgPlayerListRequest:
		return hub.playerDirectory(session, payload)
	case protocol.MsgPlayerEquipmentRequest:
		return hub.inspectEquipment(session, payload)
	case 2420:
		if len(payload) != 8 {
			return protocol.ErrFrame
		}
		target := protocol.ReadUint64(payload, 0)
		account, err := storage2_1(hub, hub.Store.RoleManager().Snapshot, target)
		if err != nil {
			session.sendGame(notice("未找到该玩家的资料。"))
			return nil
		}
		reply, err := playerDetails(account)
		if err != nil {
			session.sendGame(notice("该玩家的资料暂时无法显示。"))
			return nil
		}
		session.sendGame(reply)
	case 2430:
		if len(payload) != 8 {
			return protocol.ErrFrame
		}
		target := protocol.ReadUint64(payload, 0)
		account, err := storage2_1(hub, hub.Store.RoleManager().Snapshot, target)
		if err != nil {
			session.sendGame(notice("未找到该玩家的武器资料。"))
			return nil
		}
		if target == session.UID {
			session.syncInventory(account.Inventory)
		}
		session.sendGame(weaponCollection(account))
	case 2540, 2560:
		if (message.ID == 2540 && len(payload) != 1) || (message.ID == 2560 && len(payload) != 9) {
			return protocol.ErrFrame
		}
		directory, own, err := storage3_2(hub, hub.Store.Rankings, session.UID, payload[0])
		if err != nil {
			return err
		}
		if message.ID == 2540 {
			session.sendGame(protocol.Message{ID: 2550, Payload: directory})
		} else {
			session.sendGame(protocol.Message{ID: 2570, Payload: own})
		}
	case 9006:
		if len(payload) != 29 {
			return protocol.ErrFrame
		}
		nameField := payload[8:]
		terminator := bytes.IndexByte(nameField, 0)
		if channel.Phase != "lobby" || protocol.ReadUint64(payload, 0) != session.UID || terminator < 1 || !bytes.Equal(nameField[terminator:], make([]byte, len(nameField)-terminator)) {
			return protocol.ErrFrame
		}
		nickname, err := persistence.DecodeGBK(nameField[:terminator])
		if err != nil {
			return err
		}
		oldName, err := storage2_2(hub, hub.Store.RoleManager().Rename, session.UID, nickname)
		if err != nil {
			rejected := make([]byte, 54)
			protocol.WriteUint32(rejected, 0, 130)
			session.sendGame(protocol.Message{ID: 9008, Payload: rejected})
			if errors.Is(err, persistence.ErrBannedWord) {
				session.sendGame(notice(moderationNotice(err)))
			}
			return nil
		}
		reply := make([]byte, 54)
		copy(reply[4:25], persistence.GBK(oldName))
		protocol.WriteUint64(reply, 25, session.UID)
		copy(reply[33:], nameField)
		session.Nickname = nickname
		session.sendGame(protocol.Message{ID: 9007, Payload: reply})
	case 20561:
		if len(payload) != 4 || protocol.ReadUint32(payload, 0) > 10 {
			return protocol.ErrFrame
		}
		reply := make([]byte, 878)
		copy(reply, payload)
		copy(reply[4:68], "Local wealth event not configured")
		session.sendGame(protocol.Message{ID: 20562, Payload: reply})
	case 20563, 20565:
		if len(payload) != 0 {
			return protocol.ErrFrame
		}
		if message.ID == 20563 {
			reply := make([]byte, 68)
			protocol.WriteUint32(reply, 0, 0xffffffff)
			session.sendGame(protocol.Message{ID: 20564, Payload: reply})
		} else {
			session.sendGame(protocol.Message{ID: 20566, Payload: make([]byte, 48)})
		}
	case 9070, 1540, 1500:
		if message.ID == 1500 && len(payload) == 9 && protocol.ReadUint32(payload, 5) == 1 {
			return hub.renewalPrices(session, payload)
		}
		if err := hub.refreshVIPShop(session); err != nil {
			session.sendGame(notice("VIP价格读取失败，请稍后重试。"))
			return nil
		}
		category, variant := -1, 0
		if message.ID == 9070 {
			if len(payload) != 2 {
				return protocol.ErrFrame
			}
			category, variant = int(payload[0]), int(payload[1])
		}
		if message.ID == 1540 && len(payload) != 0 {
			return protocol.ErrFrame
		}
		if message.ID == 1500 && len(payload) != 9 {
			return protocol.ErrFrame
		}
		offers, err := storage2_2(hub, hub.Store.ShopManager().Offers, category, variant)
		if err != nil {
			return err
		}
		var records []byte
		for _, offer := range offers {
			if message.ID == 1500 && (protocol.ReadUint32(payload, 5) != 0 || offer.Record[4] != payload[0] || protocol.ReadUint32(offer.Record, 5) != protocol.ReadUint32(payload, 1)) {
				continue
			}
			records = append(records, offer.Record...)
		}
		if message.ID == 9070 {
			header := append([]byte{byte(category), byte(variant)}, protocol.Uint32Bytes(uint32(len(records)/108))...)
			records = append(header, records...)
		}
		session.sendGame(protocol.Message{ID: message.ID + 10, Payload: records})
	case protocol.MsgRenewItem:
		if len(payload) == 173 {
			instance := protocol.ReadUint32(payload, 0)
			if record := session.Inventory[instance]; len(record) == protocol.InventoryRecordSize && record[protocol.InventoryKindOffset] == protocol.ItemSuit {
				return hub.openSuit(session, payload)
			}
		}
		return hub.renewItem(session, payload)
	case 9091:
		// Native ticket-only gift error; this alternative purchase path is not implemented.
		session.sendGame(protocol.Message{ID: 9110, Payload: []byte{56, 0}})
	case 9090:
		if err := hub.refreshVIPShop(session); err != nil {
			session.sendGame(notice("VIP价格读取失败，请稍后重试。"))
			return nil
		}
		gift, err := protocol.ParseGiftRequest(payload)
		if err != nil {
			session.sendGame(notice("赠送请求格式不正确。"))
			return nil
		}
		if gift.Currency != 109 {
			// Native A2A120 explicitly maps 56 to tickets-only gifting.
			session.sendGame(protocol.Message{ID: 9110, Payload: []byte{56, 0}})
			return nil
		}
		operationID := fmt.Sprintf("%s:%d:%d", session.Namespace, channel.ID, channel.Sequence)
		result, err := storage2_3(hub, hub.Store.MailManager().Gift, session.UID, operationID, payload)
		if err != nil {
			// Unknown native 9110 codes index a client string table directly.
			// Use the established notice path rather than inventing an error code.
			session.sendGame(notice("赠送未完成，请刷新余额及邮件后核对收件人、商品和价格。"))
			log.Printf("gift_failed uid=%d", session.UID)
			return nil
		}
		session.sendGame(protocol.Message{ID: 1230, Payload: protocol.Uint32Bytes(result.Balance)})
		// A2D550 requires nonempty data but reads no result fields.
		session.sendGame(protocol.Message{ID: 9100, Payload: []byte{1}})
		if result.Created {
			if recipient := hub.Sessions[result.Recipient]; recipient != nil {
				recipient.MailDirty = true
				recipient.mailRevision++
				if e := hub.refreshMail(recipient); e != nil {
					log.Printf("mail_refresh_failed uid=%d", recipient.UID)
				}
			}
		}
		log.Printf("gift_delivered uid=%d recipient=%d mail=%d", session.UID, result.Recipient, result.MailID)
	case 9040, 9041:
		if err := hub.refreshVIPShop(session); err != nil {
			session.sendGame(notice("VIP价格读取失败，请稍后重试。"))
			return nil
		}
		if len(payload) != 169 {
			return protocol.ErrFrame
		}
		if message.ID == 9041 {
			session.sendGame(protocol.Message{ID: 9060, Payload: []byte{130, 0}})
			return nil
		}
		operationID := fmt.Sprintf("%s:%d:%d", session.Namespace, channel.ID, channel.Sequence)
		balance, item, catalog, err := storage4_3(hub, hub.Store.ShopManager().Purchase, session.UID, operationID, payload)
		if err != nil {
			session.sendGame(protocol.Message{ID: 9060, Payload: []byte{130, 0}})
			log.Printf("purchase_rejected uid=%d", session.UID)
			return nil
		}
		balanceMessage := uint32(1230)
		if protocol.ReadUint32(catalog, 30) > 0 {
			balanceMessage = 1240
		}
		session.sendGame(protocol.Message{ID: balanceMessage, Payload: protocol.Uint32Bytes(balance)})
		if len(item) == 68 {
			messageID := uint32(protocol.MsgItemAdded)
			if _, exists := session.Inventory[protocol.ReadUint32(item, 0)]; exists {
				messageID = 2161 // Existing stack: replace its quantity, do not add a second row.
			}
			session.sendGame(protocol.Message{ID: messageID, Payload: item})
			if session.Inventory == nil {
				session.Inventory = map[uint32][]byte{}
			}
			session.Inventory[protocol.ReadUint32(item, 0)] = bytes.Clone(item)
		}
		session.sendGame(protocol.Message{ID: 9050, Payload: catalog})
		log.Printf("purchase uid=%d instance=%d", session.UID, protocol.ReadUint32(item, 0))
	case protocol.MsgExpiredItemsRequest:
		if len(payload) != 0 {
			return protocol.ErrFrame
		}
		return hub.expiredItems(session)
	case 2130:
		if len(payload) != 4 {
			return protocol.ErrFrame
		}
		if session.Room != nil {
			member := session.Room.Members[session.UID]
			if session.Room.Stage != "room" || member == nil || member.Ready {
				return nil
			}
		}
		instance := protocol.ReadUint32(payload, 0)
		if err := storage1_2(hub, hub.Store.InventoryManager().Discard, session.UID, instance); err != nil {
			log.Printf("discard_rejected uid=%d instance=%d error=%v", session.UID, instance, err)
			session.sendGame(notice("丢弃失败：道具不存在、已装备、状态不允许，或是最后一件必需装备。"))
			return nil
		}
		// Only acknowledge after commit. 2162 removes exactly one local instance
		// and refreshes warehouse UI, including after a lost/rebuilt local cache.
		session.sendGame(protocol.Message{ID: 2162, Payload: protocol.Uint32Bytes(instance)})
		delete(session.Inventory, instance)
		log.Printf("discard uid=%d instance=%d", session.UID, instance)
	case protocol.MsgRandomWeaponSet, protocol.MsgRandomWeaponQuery, protocol.MsgRandomWeaponEquipmentQuery, protocol.MsgRandomWeaponCancelAck:
		return hub.randomWeapon(session, message)
	case protocol.MsgEquipItem, protocol.MsgUnequipItem:
		if (message.ID == protocol.MsgEquipItem && len(payload) != 16) || (message.ID == protocol.MsgUnequipItem && len(payload) != 4) {
			return protocol.ErrFrame
		}
		if session.Room != nil && session.Room.Members[session.UID].Ready {
			return nil
		}
		slot := uint32(0)
		if message.ID == protocol.MsgEquipItem {
			slot = protocol.ReadUint32(payload, 4)
			if slot > 65535 {
				session.sendGame(protocol.Message{ID: protocol.MsgEquipError, Payload: []byte{38, 0}})
				return nil
			}
		}
		if record := session.Inventory[protocol.ReadUint32(payload, 0)]; message.ID == protocol.MsgEquipItem && len(record) == protocol.InventoryRecordSize && record[protocol.InventoryKindOffset] == protocol.ItemSuit {
			return hub.consumeSuit(session, protocol.ReadUint32(payload, 0), false)
		}
		equip := hub.Store.EquipmentManager().Equip
		if message.ID == protocol.MsgEquipItem {
			equip = hub.Store.EquipmentManager().EquipDefault
		}
		changed, err := storage2_3(hub, equip, session.UID, protocol.ReadUint32(payload, 0), uint16(slot))
		// Ownership and usability are checked against the authenticated owner's
		// inventory in the equipment transaction. Never acknowledge or broadcast
		// an item that was not admitted by that check.
		if errors.Is(err, persistence.ErrDenied) {
			return nil
		}
		if err != nil {
			if message.ID == protocol.MsgEquipItem {
				session.sendGame(protocol.Message{ID: protocol.MsgEquipError, Payload: []byte{38, 0}})
			} else {
				session.sendGame(notice("卸下失败，请检查道具及栏位。"))
			}
			return nil
		}
		if changed == nil {
			return nil
		}
		account, err := storage2_1(hub, hub.Store.RoleManager().Snapshot, session.UID)
		if err != nil {
			return err
		}
		session.syncEquipmentChange(message, changed, account.Inventory)
		if session.RandomWeaponMode != protocol.RandomWeaponOff {
			session.RandomWeaponMode = protocol.RandomWeaponOff
			session.sendRandomWeaponOff()
		}
		hub.broadcastEquipment(session, account)
	case 1232, 20546:
		if len(payload) != 0 {
			return protocol.ErrFrame
		}
		account, err := storage2_1(hub, hub.Store.RoleManager().Snapshot, session.UID)
		if err != nil {
			return err
		}
		if message.ID == 1232 {
			session.sendGame(protocol.Message{ID: 1230, Payload: protocol.Uint32Bytes(account.Tickets)})
		} else {
			session.sendGame(protocol.Message{ID: 20547, Payload: account.Profile[352:356]})
		}
	case 1300, 1320, 1340, 2171:
		return hub.mail(session, message)
	case 1400:
		return hub.renewalReminders(session, payload)
	case 1440:
		return hub.ignoreRenewalReminder(session, payload)
	case 20360:
		return hub.honourProfile(session, payload)
	case 21006:
		return hub.claimTraining(session, channel, payload)
	case 21000, 21002:
		if (message.ID == 21000 && len(payload) != 8) || (message.ID == 21002 && len(payload) != 0) {
			return protocol.ErrFrame
		}
		target := session.UID
		if message.ID == 21000 {
			// Viewing another player's 2421 automatically requests their
			// training status. This UID is a read target, not an auth identity.
			target = protocol.ReadUint64(payload, 0)
		}
		minutes, active, err := storage3_2(hub, hub.Store.TrainingManager().Training, target, message.ID == 21002)
		if err != nil {
			if message.ID == 21000 && err == sql.ErrNoRows {
				session.sendGame(notice("未找到该玩家的训练资料。"))
				return nil
			}
			return err
		}
		rank, err := storage2_1(hub, hub.Store.TrainingManager().TrainingRank, target)
		if err != nil {
			return err
		}
		rules, err := storage2_0(hub, hub.Store.TrainingManager().TrainingSettings)
		if err != nil {
			return err
		}
		status, err := trainingStatus(target, rank, minutes, active, rules)
		if err != nil {
			return err
		}
		replyID := uint32(21001)
		if message.ID == 21002 {
			replyID = 21005
		}
		session.sendGame(protocol.Message{ID: replyID, Payload: status})
	default:
		log.Printf("unhandled uid=%d phase=%s message=%d length=%d", session.UID, channel.Phase, message.ID, len(payload))
	}
	return nil
}

func (hub *Hub) datagram(session *Session, frame tunnel.Frame) error {
	payload := frame.Data
	if len(payload) < 24 || len(payload) > 32768 || protocol.ReadUint16(payload, 0) != 1 || 24+int(payload[23]) > len(payload) {
		return protocol.ErrFrame
	}
	messageID := protocol.ReadUint16(payload, 2)
	body := payload[24+int(payload[23]):]
	if messageID == 1008 {
		return hub.relayDatagram(session, frame)
	}
	if messageID == 1001 {
		if payload[23] != 0 || len(body) < 141 {
			return protocol.ErrFrame
		}
		nameLength := int(protocol.BigEndian.Uint16(body))
		if nameLength > 20 || len(body) != 141+nameLength {
			return protocol.ErrFrame
		}
		if session.P2P == 0 {
			if err := hub.initPeerKey(); err != nil {
				return err
			}
			session.P2P = hub.NextPlayer
			hub.NextPlayer++
		}
	} else if messageID == 1013 {
		if len(body) != 4 || session.P2P == 0 || protocol.ReadUint32(payload, 4) != session.P2P || protocol.ReadUint32(payload, 12) != session.P2P {
			return protocol.ErrFrame
		}
	} else {
		if time.Since(session.LastUDPNotice) >= 5*time.Second {
			log.Printf("udp_unhandled uid=%d id=%d bytes=%d extra=%d source=%d target=%d", session.UID, messageID, len(payload), payload[23], protocol.ReadUint32(payload, 12), protocol.ReadUint32(payload, 16))
			session.LastUDPNotice = time.Now()
		}
		return nil
	}
	if session.UDPPort != 0 && session.UDPPort != frame.Port {
		return protocol.ErrFrame
	}
	session.UDPPort = frame.Port
	session.P2PUntil = time.Now().Add(time.Minute)
	reply := make([]byte, 24)
	protocol.WriteUint16(reply, 0, 1)
	protocol.WriteUint16(reply, 2, messageID+1)
	protocol.WriteUint32(reply, 4, session.P2P)
	protocol.WriteUint32(reply, 16, session.P2P)
	if messageID == 1001 {
		body := make([]byte, 18)
		protocol.BigEndian.PutUint32(body[4:], session.P2P)
		protocol.BigEndian.PutUint32(body[8:], session.P2P)
		copy(body[12:], []byte{127, 0, 0, 1})
		protocol.BigEndian.PutUint16(body[16:], frame.Port)
		reply = append(reply, body...)
	} else {
		reply = append(reply, 0, 0, 0, 0)
	}
	receipt := ""
	if messageID == 1001 {
		receipt = hub.peerReceipt(session.P2P)
	}
	session.emit(tunnel.Frame{Op: "udp", PeerReceipt: receipt, Port: frame.Port, Data: reply})
	return nil
}

// SDP2P.dll sends 1008 to its relay server when a direct peer connection is
// unavailable (10003E70). Its peer receiver accepts 1009 with the recipient's
// session id and the original sender's P2P id (10003FC0/1000402F).
func (hub *Hub) relayDatagram(session *Session, frame tunnel.Frame) error {
	packet := frame.Data
	extra := int(packet[23])
	if session.P2P == 0 || !session.Bound || session.UDPPort != frame.Port || time.Now().After(session.P2PUntil) ||
		protocol.ReadUint32(packet, 4) != session.P2P || protocol.ReadUint32(packet, 12) != session.P2P ||
		extra%4 != 0 || extra > 128 || len(packet) <= 24+extra {
		return protocol.ErrFrame
	}
	// SDP2P can emit a valid relay envelope with no recipients while equipment
	// refresh rebuilds peer state. Nothing to forward; keep the session alive.
	// Authentication, lease, source port and envelope checks above still apply.
	if extra == 0 {
		return nil
	}
	room := session.Room
	if room == nil {
		return nil
	}
	// Observers may echo latency probes, never submit opaque combat traffic.
	if room.isObserver(session) && !observerProbe(packet[24+extra:], session.UID) {
		return nil
	}
	recipients := map[uint64]bool{}
	seen := map[uint32]bool{}
	for offset := 24; offset < 24+extra; offset += 4 {
		targetID := protocol.ReadUint32(packet, offset)
		if targetID == session.P2P || seen[targetID] {
			continue
		}
		seen[targetID] = true
		for _, member := range room.Members {
			peer := member.Session
			if peer.P2P != targetID || peer.UDPPort == 0 || !peer.Bound || time.Now().After(peer.P2PUntil) {
				continue
			}
			reply := make([]byte, 24, len(packet)-extra)
			copy(reply, packet[:24])
			protocol.WriteUint16(reply, 2, 1009)
			protocol.WriteUint32(reply, 4, peer.P2P)
			protocol.WriteUint32(reply, 16, peer.P2P)
			reply[23] = 0
			reply = append(reply, packet[24+extra:]...)
			if !peer.emit(tunnel.Frame{Op: "udp", Port: peer.UDPPort, Data: reply}) {
				continue
			}
			hub.observePeerProbe(session, peer, packet[24+extra:], time.Now())
			room.observeRelayedPairSelection(session, peer, packet[24+extra:])
			recipients[peer.UID] = true
			session.UDPRelayed++
		}
	}
	hub.observeRelayedBattle(session, packet[24+extra:], recipients)
	if time.Since(session.LastUDPNotice) >= 5*time.Second {
		log.Printf("udp_relay uid=%d room=%d stage=%s bytes=%d recipients=%d forwarded_total=%d", session.UID, room.ID, room.Stage, len(packet), extra/4, session.UDPRelayed)
		session.LastUDPNotice = time.Now()
	}
	return nil
}

func notice(text string) protocol.Message {
	encoded := persistence.GBK(text)
	if len(encoded) > 199 {
		encoded = []byte("Request unavailable.")
	}
	payload := make([]byte, 215)
	payload[12] = byte(len(encoded) + 1)
	copy(payload[13:], encoded)
	return protocol.Message{ID: 20150, Payload: payload}
}

func (hub *Hub) chat(session *Session, message protocol.Message) error {
	if time.Since(session.LastChat) < time.Second {
		session.sendGame(notice("发送太快，请稍后再试。"))
		return nil
	}
	payload := message.Payload
	lengthOffset, textOffset := 12, 13
	if message.ID == 5000 {
		lengthOffset, textOffset = 50, 55
	}
	if (message.ID == 5002 && len(payload) != 215) || (message.ID == 5000 && len(payload) != 256) {
		return protocol.ErrFrame
	}
	length := int(payload[lengthOffset])
	if length < 2 || length > 200 || textOffset+length > len(payload) || payload[textOffset+length-1] != 0 {
		return protocol.ErrFrame
	}
	text := payload[textOffset : textOffset+length-1]
	decoded, err := persistence.DecodeGBK(text)
	if err != nil {
		return protocol.ErrFrame
	}
	if bytes.IndexByte(text, 0) >= 0 {
		return protocol.ErrFrame
	}
	for _, character := range decoded {
		if unicode.IsControl(character) {
			return protocol.ErrFrame
		}
	}
	for _, padding := range payload[textOffset+length:] {
		if padding != 0 {
			return protocol.ErrFrame
		}
	}
	if hub.rejectText(session, decoded) {
		return nil
	}
	reply := make([]byte, 256)
	protocol.WriteUint64(reply, 0, session.UID)
	copy(reply[8:29], persistence.GBK(session.Nickname))
	reply[50] = byte(length)
	copy(reply[55:], text)
	session.LastChat = time.Now()
	if message.ID == 5000 {
		recipientField := payload[29:50]
		terminator := bytes.IndexByte(recipientField, 0)
		if terminator < 1 {
			return protocol.ErrFrame
		}
		for _, padding := range recipientField[terminator:] {
			if padding != 0 {
				return protocol.ErrFrame
			}
		}
		recipient := recipientField[:terminator]
		if _, err := persistence.DecodeGBK(recipient); err != nil {
			return protocol.ErrFrame
		}
		var target *Session
		for _, peer := range hub.Sessions {
			if peer != session && peer.game() != nil && bytes.Equal(persistence.GBK(peer.Nickname), recipient) {
				if target != nil {
					return nil
				}
				target = peer
			}
		}
		if target == nil {
			session.sendGame(notice("对方当前不在线。"))
			return nil
		}
		copy(reply[29:50], recipient)
		message = protocol.Message{ID: 5001, Payload: reply}
		target.sendGame(message)
		session.sendGame(message)
	} else {
		message = protocol.Message{ID: 5003, Payload: reply}
		if session.Room != nil {
			hub.broadcast(session.Room, message, 0)
		} else {
			for _, peer := range hub.Sessions {
				if peer.LobbyID == session.LobbyID && peer.game() != nil && peer.game().Phase == "lobby" {
					peer.sendGame(message)
				}
			}
		}
	}
	return nil
}
