package game

import (
	"bytes"
	"errors"
	"kungfu.local/server/internal/moderation"
	"kungfu.local/server/internal/persistence"
)

func moderationNotice(err error) string {
	if errors.Is(err, persistence.ErrBannedWord) {
		return moderation.Notice
	}
	return "文字审核暂不可用，请稍后重试。"
}
func (h *Hub) rejectText(s *Session, text string) bool {
	if err := storage1_1(h, h.Store.CheckText, text); err != nil {
		s.sendGame(notice(moderationNotice(err)))
		return true
	}
	return false
}

// Room create name is 21 GBK bytes at +0; room settings project back into it.
const roomNameFieldSize = 21

func (h *Hub) checkRoomName(request []byte) error {
	if len(request) < roomNameFieldSize {
		return persistence.ErrDenied
	}
	field := request[:roomNameFieldSize]
	end := bytes.IndexByte(field, 0)
	if end < 0 {
		return persistence.ErrDenied
	}
	text, err := persistence.DecodeGBK(field[:end])
	if err != nil {
		return err
	}
	return storage1_1(h, h.Store.CheckText, text)
}
func (h *Hub) rejectRoomName(s *Session, request []byte) bool {
	if err := h.checkRoomName(request); err != nil {
		s.sendGame(notice(moderationNotice(err)))
		return true
	}
	return false
}
