package game

import (
	"database/sql"
	"errors"
	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
)

func (h *Hub) mail(s *Session, m protocol.Message) error {
	if m.ID == 1300 {
		if len(m.Payload) != 0 {
			return protocol.ErrFrame
		}
		return h.mailboxSnapshot(s)
	}
	key, err := protocol.ParseMailAction(m.Payload, s.UID)
	if m.ID == 2171 {
		s.MailClaimFailed = true
	}
	if err != nil {
		s.sendGame(notice("邮件请求无效。"))
		return nil
	}
	if m.ID == 1320 {
		s.MailPreview, s.MailAttachment, s.MailClaimFailed = 0, 0, false
		p, err := storage2_2(h, h.Store.MailManager().ReadMail, s.UID, key)
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, persistence.ErrDenied) {
			s.sendGame(notice("邮件不存在或已经删除。"))
			return nil
		}
		if err != nil {
			return err
		}
		s.MailPreview, s.MailAttachment = key, protocol.ReadUint32(p, 8)
		s.sendGame(protocol.Message{ID: 1330, Payload: p})
		return nil
	}
	if m.ID == 2171 {
		if s.MailPreview == 0 || key != s.MailAttachment {
			s.sendGame(notice("请先打开对应邮件详情再领取。"))
			return nil
		}
		_, err := storage2_2(h, h.Store.MailManager().ClaimMailItem, s.UID, key)
		if errors.Is(err, persistence.ErrDenied) || errors.Is(err, sql.ErrNoRows) {
			s.sendGame(notice("附件领取失败，邮件已保留。"))
			return nil
		}
		if err != nil {
			return err
		}
		account, err := storage2_1(h, h.Store.RoleManager().Snapshot, s.UID)
		if err != nil {
			return err
		}
		s.syncInventory(account.Inventory)
		s.MailClaimFailed = false
		return nil
	}
	if key == s.MailPreview && s.MailClaimFailed {
		// Native 89EE80 sends 1340 immediately after 2171, before its result.
		// Consume that one automatic delete; a later explicit delete still works.
		s.MailClaimFailed = false
		p := make([]byte, 5)
		protocol.WriteUint32(p, 1, key)
		s.sendGame(protocol.Message{ID: 1350, Payload: p})
		return nil
	}
	err = storage1_2(h, h.Store.MailManager().DeleteMail, s.UID, key)
	if err != nil && !errors.Is(err, persistence.ErrDenied) {
		return err
	}
	p := make([]byte, 5)
	protocol.WriteUint32(p, 1, key)
	if err == nil {
		p[0] = 1
	}
	s.sendGame(protocol.Message{ID: 1350, Payload: p})
	return nil
}

// Caller holds Hub.Mutex. Refresh failures retain the pending flag and never
// roll back a committed gift or disconnect its sender.
func (h *Hub) refreshMail(s *Session) error {
	if !s.MailDirty || s.LoggedOut || h.Sessions[s.UID] != s {
		return nil
	}
	ch := s.game()
	if ch == nil || (ch.Phase != "lobby" && ch.Phase != "room") || (s.Room != nil && s.Room.Stage != "room") {
		return nil
	}
	return h.mailboxSnapshot(s)
}

func (h *Hub) mailboxSnapshot(s *Session) error {
	manager, uid, revision := h.Store.MailManager(), s.UID, s.mailRevision
	var p []byte
	var err error
	if !h.readSessionSnapshot(s, func() { p, err = manager.Mailbox(uid) }) || s.mailRevision != revision {
		return nil
	}
	if err != nil {
		return err
	}
	s.sendGame(protocol.Message{ID: 1310, Payload: p})
	s.MailDirty = false
	return nil
}
