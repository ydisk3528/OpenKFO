package game

import (
	"bytes"
	"context"
	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
	"log"
	"time"
)

// Refresh only at a safe lobby/waiting-room boundary. Native battle equipment
// is a start-of-round snapshot; never replace its model mid-frame.
func (hub *Hub) canRefreshInventory(s *Session) bool {
	if hub.Sessions[s.UID] != s || s.LoggedOut || hub.Store == nil {
		return false
	}
	ch := s.game()
	return ch != nil && (ch.Phase == "lobby" || ch.Phase == "room") && (s.Room == nil || s.Room.Stage == "room")
}

func (hub *Hub) RefreshExpiredInventory(s *Session) error {
	hub.lockState()
	if !hub.canRefreshInventory(s) || s.refreshBusy {
		hub.unlockState()
		return nil
	}
	s.refreshBusy = true
	revision, mailRevision := s.refreshRevision, s.mailRevision
	room, ch, phase := s.Room, s.game(), s.game().Phase
	uid, store, config := s.UID, hub.Store, hub.Config
	ready, requested, mailDirty := s.StageViewReady, s.StageViewRequested, s.MailDirty
	inventory := make(map[uint32][]byte, len(s.Inventory))
	for id, record := range s.Inventory {
		inventory[id] = bytes.Clone(record)
	}
	hub.unlockState()
	defer func() { hub.lockState(); s.refreshBusy = false; hub.unlockState() }()
	// Queries and map-plan validation run without the game-wide mutex.
	var stagePayload, mailPayload []byte
	if ready {
		view, err := store.StagePlayerView(uid, config.ConfigHash)
		if err == nil && (view.Configured || requested) {
			stagePayload, err = stageSelectionPayload(config, view)
		}
		if err != nil {
			log.Printf("stage_refresh_failed uid=%d", uid)
		}
	}
	if mailDirty {
		var err error
		mailPayload, err = store.MailManager().Mailbox(uid)
		if err != nil {
			log.Printf("mail_refresh_failed uid=%d", uid)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err := store.DB.QueryContext(ctx, `SELECT instance FROM inventory_expirations WHERE uid=? AND expires_at<=? ORDER BY instance`, uid, time.Now().Unix())
	if err != nil {
		return err
	}
	refresh := false
	for rows.Next() {
		var id uint32
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		if old := inventory[id]; len(old) == 68 && protocol.ReadUint32(old, 19) != 2 {
			refresh = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var account persistence.Account
	if refresh {
		account, err = store.RoleManager().Snapshot(uid)
		if err != nil {
			return err
		}
	}
	hub.lockState()
	defer hub.unlockState()
	// A player may have changed room, equipment, or logged out during the read.
	if !hub.canRefreshInventory(s) || s.refreshRevision != revision || s.Room != room || s.game() != ch || ch.Phase != phase || hub.Config.ConfigHash != config.ConfigHash {
		return nil
	}
	if len(inventory) != len(s.Inventory) {
		return nil
	}
	for id, record := range inventory {
		if !bytes.Equal(record, s.Inventory[id]) {
			return nil
		}
	}
	if stagePayload != nil {
		if err = sendStageSelectionPayload(s, stagePayload, false); err != nil {
			return err
		}
	}
	if mailPayload != nil && s.MailDirty && s.mailRevision == mailRevision {
		s.sendGame(protocol.Message{ID: 1310, Payload: mailPayload})
		s.MailDirty = false
	}
	if refresh && s.syncUnequippedInventory(account.Inventory) && s.Room != nil {
		hub.refreshExpiredEquipment(s, account)
	}
	return nil
}

// An expired loadout invalidates only its owner's ready confirmation.
func (hub *Hub) refreshExpiredEquipment(s *Session, account persistence.Account) {
	r := s.Room
	if r == nil || r.Stage != "room" || r.Members[s.UID] == nil {
		return
	}
	hub.cancelNetworkProbe(r)
	hub.cancelSeatExchange(r)
	if r.Members[s.UID].Ready {
		r.Members[s.UID].Ready = false
		hub.broadcast(r, protocol.Message{ID: protocol.MsgPlayerNotReady, Payload: protocol.Uint64Bytes(s.UID)}, 0)
	}
	hub.broadcastEquipment(s, account)
}
