package game

import (
	"bytes"
	"encoding/binary"
	"errors"
	"log"
	"time"

	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
)

// SDFriend.dll: outer/header LE, serialized integers and string lengths BE.
const (
	msgFriends         uint32 = 8020
	friendInitialize   uint16 = 203
	friendInitialized  uint16 = 204
	friendAdd          uint16 = 207
	friendAdded        uint16 = 212
	friendRemove       uint16 = 213
	friendRemoved      uint16 = 214
	friendQuery        uint16 = 225
	friendList         uint16 = 226
	friendSuccess      uint32 = 1
	friendFullSnapshot byte   = 11
	friendOffline      byte   = 1
	friendOnline       byte   = 2
)

type friendRequest struct {
	op   uint16
	name string
}

func parseFriendRequest(p []byte) (friendRequest, error) {
	var r friendRequest
	if len(p) < 4 || len(p) > 256 || p[2] != 0 || p[3] != 0 {
		return r, protocol.ErrFrame
	}
	r.op = binary.LittleEndian.Uint16(p)
	p = p[4:]
	readName := func(max int) (string, error) {
		if len(p) < 2 {
			return "", protocol.ErrFrame
		}
		n := int(binary.BigEndian.Uint16(p))
		p = p[2:]
		if n > max || n > len(p) {
			return "", protocol.ErrFrame
		}
		b := p[:n]
		p = p[n:]
		name, err := persistence.DecodeGBK(b)
		if err != nil || bytes.IndexByte(b, 0) >= 0 || !bytes.Equal(persistence.GBK(name), b) {
			return "", protocol.ErrFrame
		}
		return name, nil
	}
	switch r.op {
	case friendInitialize:
		if len(p) != 1 || p[0] > 1 {
			return r, protocol.ErrFrame
		}
		p = nil
	case friendQuery:
		// Native sends its last list version. Always send a full authoritative snapshot.
		if len(p) != 4 {
			return r, protocol.ErrFrame
		}
		p = nil
	case friendAdd, friendRemove:
		var err error
		r.name, err = readName(20)
		if err != nil || r.name == "" {
			return r, protocol.ErrFrame
		}
		if r.op == friendAdd {
			if _, err = readName(199); err != nil {
				return r, err
			}
		}
	default:
		return r, protocol.ErrFrame
	}
	if len(p) != 0 {
		return r, protocol.ErrFrame
	}
	return r, nil
}

func friendPacket(op uint16, body []byte) protocol.Message {
	p := make([]byte, 4, 4+len(body))
	binary.LittleEndian.PutUint16(p, op)
	return protocol.Message{ID: msgFriends, Payload: append(p, body...)}
}

func friendString(p []byte, s string) []byte {
	b := persistence.GBK(s)
	p = binary.BigEndian.AppendUint16(p, uint16(len(b)))
	return append(p, b...)
}

func friendResult(op uint16, name string) protocol.Message {
	return friendPacket(op, friendString(binary.BigEndian.AppendUint32(nil, friendSuccess), name))
}

// 226 decodes BOTH sections even when type=11: the trailing empty auxiliary
// list (u16 count) is mandatory. Per-person metadata is 34 wire bytes, not the
// 36-byte aligned native struct. See FriendProtocol.md for native evidence.
func buildFriendList(list []persistence.Friend, online func(uint64) bool) (protocol.Message, error) {
	if len(list) > persistence.FriendLimit {
		return protocol.Message{}, protocol.ErrFrame
	}
	p := []byte{friendFullSnapshot}
	p = binary.BigEndian.AppendUint32(p, 0) // no delta version; full snapshots only
	p = binary.BigEndian.AppendUint16(p, uint16(len(list)))
	p = append(p, 0) // custom group count
	for _, f := range list {
		b := persistence.GBK(f.Name)
		decoded, err := persistence.DecodeGBK(b)
		if f.UID == 0 || len(b) == 0 || len(b) > 20 || err != nil || decoded != f.Name || bytes.IndexByte(b, 0) >= 0 {
			return protocol.Message{}, protocol.ErrFrame
		}
		state := friendOffline
		if online(f.UID) {
			state = friendOnline
		}
		p = append(p, state, 0)
		p = friendString(p, f.Name)
	}
	for _, f := range list {
		p = binary.BigEndian.AppendUint32(p, uint32(f.UID>>32))
		p = binary.BigEndian.AppendUint32(p, uint32(f.UID))
		// Unknown optional location/appearance fields remain unset, rather than
		// mislabelling rank or leaking network addresses into the native record.
		p = append(p, make([]byte, 26)...)
	}
	p = binary.BigEndian.AppendUint16(p, 0)
	return friendPacket(friendList, p), nil
}

func (h *Hub) friendSnapshot(s *Session) error {
	manager, uid := h.Store.FriendManager(), s.UID
	var list []persistence.Friend
	var err error
	if !h.readSessionSnapshot(s, func() { list, err = manager.List(uid) }) {
		return nil
	}
	if err != nil {
		return err
	}
	p, err := buildFriendList(list, func(uid uint64) bool {
		other := h.Sessions[uid]
		if other == nil || other.LoggedOut || other.GameChannel == 0 {
			return false
		}
		ch := other.game()
		if ch == nil || (ch.Phase != "lobby" && ch.Phase != "room" && ch.Phase != "battle") {
			return false
		}
		select {
		case <-other.Done:
			return false
		default:
			return true
		}
	})
	if err != nil {
		return err
	}
	s.sendGame(p)
	return nil
}

func (h *Hub) friends(s *Session, p []byte) error {
	r, err := parseFriendRequest(p)
	if err != nil {
		s.sendGame(notice("好友请求格式无效或此操作尚不支持。"))
		return nil
	}
	if time.Since(s.LastFriendRequest) < 100*time.Millisecond {
		if r.op == friendAdd || r.op == friendRemove {
			s.sendGame(notice("好友操作过于频繁，请稍后再试。"))
		}
		return nil
	}
	s.LastFriendRequest = time.Now()
	if r.op == friendAdd || r.op == friendRemove {
		var f persistence.Friend
		f, err = storage2_3(h, h.Store.FriendManager().Change, s.UID, r.name, r.op == friendAdd)
		if errors.Is(err, persistence.ErrDenied) {
			s.sendGame(notice("好友操作失败：请检查角色名、是否添加自己或好友数量已达100人。"))
			return nil
		}
		if err == nil {
			op := friendAdded
			if r.op == friendRemove {
				op = friendRemoved
			}
			s.sendGame(friendResult(op, f.Name))
		}
	}
	if err == nil && r.op == friendInitialize {
		s.sendGame(friendPacket(friendInitialized, binary.BigEndian.AppendUint32(nil, friendSuccess)))
	}
	if err == nil {
		err = h.friendSnapshot(s)
	}
	if err != nil {
		log.Printf("friends_failed uid=%d operation=%d error=%v", s.UID, r.op, err)
		s.sendGame(notice("好友数据暂时无法读取，请稍后刷新好友列表。"))
	}
	return nil
}
