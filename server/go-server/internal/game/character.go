package game

import (
	"bytes"
	"errors"
	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
)

func (hub *Hub) characterMessage(s *Session, ch *Channel, m protocol.Message) (bool, error) {
	if m.ID != protocol.MsgCreateCharacter && !(m.ID == 3320 && ch.Phase == "character_created") {
		return false, nil
	}
	if ch.ID != s.BootstrapChannel || (ch.Phase != "character_create" && ch.Phase != "character_created") {
		return true, protocol.ErrFrame
	}
	if m.ID == protocol.MsgCreateCharacter {
		account, err := storage2_3(hub, hub.Store.RoleManager().CreateCharacter, s.UID, m.Payload, hub.Config.CharacterChoices)
		if err != nil {
			// Current 822BC0 uses WORD >= 0x82 for its generic failure path.
			// Do not index an undocumented message table or expose DB errors.
			s.send(ch.ID, protocol.Message{ID: protocol.MsgCharacterCreateError, Payload: []byte{0x82, 0}})
			if errors.Is(err, persistence.ErrBannedWord) {
				s.send(ch.ID, notice(moderationNotice(err)))
			}
			return true, nil
		}
		s.Nickname = account.Nickname
		s.rememberInventory(account.Inventory)
		ch.Phase = "character_created"
		s.send(ch.ID, protocol.Message{ID: protocol.MsgCharacterCreated, Payload: append(bytes.Clone(account.Profile), account.InventoryBytes()...)})
		return true, nil
	}
	if len(m.Payload) != 4 {
		return true, protocol.ErrFrame
	}
	account, err := storage2_1(hub, hub.Store.RoleManager().Snapshot, s.UID)
	if err != nil {
		return true, err
	}
	// The first native 3320 after creation is a UI acknowledgement; its value
	// is not always the role ID. Respond with the actual saved role, not its input.
	ch.Phase = "profile_sent"
	s.send(ch.ID, protocol.Message{ID: 3330, Payload: bytes.Clone(account.Profile[:4])})
	s.send(ch.ID, protocol.Message{ID: protocol.MsgCharacterList, Payload: bytes.Clone(account.Profile)})
	return true, nil
}
